package plan

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// fakeLengths хранит правки прямо в снимке читателя: следующее чтение видит их, как с настоящей базой.
type fakeLengths struct {
	r      *fakeReader
	scopes []Scope
	sets   []WindowLength
	resets []Date
	err    error
}

func (f *fakeLengths) SetWindowLength(_ context.Context, sc Scope, l WindowLength) error {
	if f.err != nil {
		return f.err
	}
	f.scopes = append(f.scopes, sc)
	f.sets = append(f.sets, l)
	f.drop(l.Start)
	f.r.snap.WindowLengths = append(f.r.snap.WindowLengths, l)
	return nil
}

func (f *fakeLengths) ResetWindowLength(_ context.Context, sc Scope, start Date) error {
	if f.err != nil {
		return f.err
	}
	f.scopes = append(f.scopes, sc)
	f.resets = append(f.resets, start)
	f.drop(start)
	return nil
}

func (f *fakeLengths) drop(start Date) {
	var keep []WindowLength
	for _, l := range f.r.snap.WindowLengths {
		if l.Start != start {
			keep = append(keep, l)
		}
	}
	f.r.snap.WindowLengths = keep
}

func lsvc(today Date, incomes ...Event) (*Service, *fakeLengths) {
	r := &fakeReader{snap: Snapshot{Incomes: incomes}}
	l := &fakeLengths{r: r}
	return svc(r, today).WithWindowLengths(l), l
}

func TestBuildWindows_LengthMovesEnd(t *testing.T) {
	a := inc("a", d(2026, 10, 5), StateDone, 1)
	ws := BuildWindows([]Event{a}, WindowLength{Start: d(2026, 10, 5), Days: 16})
	if ws[0].End() != d(2026, 10, 20) || ws[0].Days() != 16 || !ws[0].Adjusted() {
		t.Fatalf("окно 16 дней с 05.10 кончается 20.10: %+v end=%v", ws[0], ws[0].End())
	}
}

// Продлённое окно забирает открывающее поступление внутри новой границы; сокращённое — отпускает его в новое окно.
func TestBuildWindows_LengthChangesChain(t *testing.T) {
	a := inc("a", d(2026, 10, 5), StateDone, 1)
	b := inc("b", d(2026, 10, 19), StateDone, 1)
	if ws := BuildWindows([]Event{a, b}); len(ws) != 2 {
		t.Fatalf("без правки 19.10 открывает второе окно: %+v", ws)
	}
	ext := BuildWindows([]Event{a, b}, WindowLength{Start: d(2026, 10, 5), Days: 15})
	if len(ext) != 1 || len(ext[0].Incomes) != 2 {
		t.Fatalf("окно до 19.10 включает поступление 19.10: %+v", ext)
	}
	c := inc("c", d(2026, 10, 12), StateDone, 1)
	short := BuildWindows([]Event{a, c}, WindowLength{Start: d(2026, 10, 5), Days: 7})
	if len(short) != 2 || short[1].Start != d(2026, 10, 12) || short[0].End() != d(2026, 10, 11) {
		t.Fatalf("окно 05–11.10 отпускает поступление 12.10 в новое окно: %+v", short)
	}
}

func TestBuildWindows_LengthForOtherDateIgnored(t *testing.T) {
	a := inc("a", d(2026, 10, 5), StateDone, 1)
	ws := BuildWindows([]Event{a}, WindowLength{Start: d(2026, 10, 6), Days: 30})
	if ws[0].Days() != windowDays || ws[0].Adjusted() {
		t.Fatalf("правка не для даты начала окна ничего не меняет: %+v", ws[0])
	}
}

// Без правок результат тот же, что был до ADR-020, при любом наборе поступлений.
func TestBuildWindows_NoLengthsSameAsDefault(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 8).Draw(rt, "n")
		evs := make([]Event, 0, n)
		for i := range n {
			day := rapid.IntRange(1, 120).Draw(rt, "day")
			evs = append(evs, inc(string(rune('a'+i)), d(2026, 1, 1).AddDays(day), StateDone, 1))
		}
		got := BuildWindows(evs)
		for _, w := range got {
			if w.Days() != windowDays || w.Length != 0 {
				rt.Fatalf("окно без правки обязано быть 14 дней: %+v", w)
			}
		}
		if !reflect.DeepEqual(got, BuildWindows(evs, []WindowLength{}...)) {
			rt.Fatal("пустой список правок меняет результат")
		}
	})
}

func TestLocate_UpcomingUsesWindowLength(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", d(2026, 10, 5), StatePlanned, 1)}, WindowLength{Start: d(2026, 10, 5), Days: 20})
	if st := Locate(ws, d(2026, 10, 1)); st.Kind != StateUpcoming || st.DaysLeft != 20 {
		t.Fatalf("будущее окно 20 дней: %+v", st)
	}
}

func TestWindowLength_Validate(t *testing.T) {
	ok := WindowLength{Start: d(2026, 10, 5), Days: 1}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{0, -3, MaxWindowDays + 1} {
		if err := (WindowLength{Start: d(2026, 10, 5), Days: days}).Validate(); !errors.Is(err, ErrBadWindowLength) {
			t.Errorf("days=%d: %v", days, err)
		}
	}
	if err := (WindowLength{Start: Date{2026, 2, 30}, Days: 5}).Validate(); !errors.Is(err, ErrBadDate) {
		t.Errorf("кривая дата: %v", err)
	}
}

func TestService_ResizeWindow_ExtendAndShrink(t *testing.T) {
	ctx := context.Background()
	s, l := lsvc(d(2026, 10, 10), incomeFact("transaction:a", d(2026, 10, 5), rub(1000)))

	f, err := s.ResizeWindow(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := WindowFrame{Start: d(2026, 10, 5), End: d(2026, 10, 19), Days: 15, DaysLeft: 10, Adjusted: true}
	if f != want {
		t.Fatalf("+1 день: %+v, ждали %+v", f, want)
	}
	if !reflect.DeepEqual(l.sets, []WindowLength{{Start: d(2026, 10, 5), Days: 15}}) {
		t.Fatalf("в хранилище ушло %+v", l.sets)
	}
	if !reflect.DeepEqual(l.scopes[0], Scope{ChatIDs: []int64{42}}) {
		t.Fatalf("область не дошла до хранилища: %+v", l.scopes)
	}

	f, err = s.ResizeWindow(ctx, -3)
	if err != nil {
		t.Fatal(err)
	}
	if f.Days != 12 || f.End != d(2026, 10, 16) || f.DaysLeft != 7 {
		t.Fatalf("−3 дня от 15: %+v", f)
	}
}

// Возврат к 14 дням стирает правку, а не пишет «14».
func TestService_ResizeWindow_BackToDefaultResets(t *testing.T) {
	ctx := context.Background()
	s, l := lsvc(d(2026, 10, 10), incomeFact("transaction:a", d(2026, 10, 5), rub(1000)))
	if _, err := s.ResizeWindow(ctx, 2); err != nil {
		t.Fatal(err)
	}
	f, err := s.ResizeWindow(ctx, -2)
	if err != nil {
		t.Fatal(err)
	}
	if f.Adjusted || f.Days != windowDays || len(l.sets) != 1 || !reflect.DeepEqual(l.resets, []Date{d(2026, 10, 5)}) {
		t.Fatalf("frame=%+v sets=%+v resets=%+v", f, l.sets, l.resets)
	}
}

// Конец датой; возврат к 14 дням — тем же «до даты», правка при этом стирается.
func TestService_SetWindowEnd(t *testing.T) {
	ctx := context.Background()
	s, l := lsvc(d(2026, 10, 10), incomeFact("transaction:a", d(2026, 10, 5), rub(1000)))
	f, err := s.SetWindowEnd(ctx, d(2026, 10, 25))
	if err != nil {
		t.Fatal(err)
	}
	if f.End != d(2026, 10, 25) || f.Days != 21 {
		t.Fatalf("конец 25.10: %+v", f)
	}
	f, err = s.SetWindowEnd(ctx, d(2026, 10, 18))
	if err != nil {
		t.Fatal(err)
	}
	if f.End != d(2026, 10, 18) || f.Adjusted || len(l.resets) != 1 {
		t.Fatalf("сброс: %+v resets=%v", f, l.resets)
	}
	cur, err := s.CurrentWindow(ctx)
	if err != nil || cur != f {
		t.Fatalf("CurrentWindow=%+v err=%v, ждали %+v", cur, err, f)
	}
}

func TestService_EditWindow_Rejects(t *testing.T) {
	ctx := context.Background()
	s, l := lsvc(d(2026, 10, 10), incomeFact("transaction:a", d(2026, 10, 5), rub(1000)))
	cases := []struct {
		name string
		run  func() (WindowFrame, error)
		want error
	}{
		{"ноль дней", func() (WindowFrame, error) { return s.ResizeWindow(ctx, 0) }, ErrBadWindowLength},
		{"короче дня", func() (WindowFrame, error) { return s.ResizeWindow(ctx, -14) }, ErrBadWindowLength},
		{"длиннее предела", func() (WindowFrame, error) { return s.ResizeWindow(ctx, MaxWindowDays) }, ErrBadWindowLength},
		{"конец раньше начала", func() (WindowFrame, error) { return s.SetWindowEnd(ctx, d(2026, 10, 4)) }, ErrBadWindowLength},
		{"кривая дата", func() (WindowFrame, error) { return s.SetWindowEnd(ctx, Date{2026, 13, 1}) }, ErrBadDate},
	}
	for _, c := range cases {
		if _, err := c.run(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, ждали %v", c.name, err, c.want)
		}
	}
	if len(l.sets)+len(l.resets) != 0 {
		t.Fatalf("отказ не должен ничего писать: sets=%v resets=%v", l.sets, l.resets)
	}
}

// Конец раньше сегодня запрещён: иначе окно пропало бы с экрана и следующая команда правила бы другое окно.
//
// Мутация: убрать проверку ErrWindowEndPast — после сокращения правка «вернуть» отвечает ErrNoWindow.
func TestService_EditWindow_EndNotBeforeToday(t *testing.T) {
	ctx := context.Background()
	s, l := lsvc(d(2026, 10, 10), incomeFact("transaction:a", d(2026, 10, 5), rub(1000)))
	if _, err := s.ResizeWindow(ctx, -9); !errors.Is(err, ErrWindowEndPast) { // 05–09.10, сегодня 10.10
		t.Fatalf("сокращение до вчера: %v", err)
	}
	if _, err := s.SetWindowEnd(ctx, d(2026, 10, 9)); !errors.Is(err, ErrWindowEndPast) {
		t.Fatalf("конец вчера: %v", err)
	}
	if len(l.sets)+len(l.resets) != 0 {
		t.Fatal("отказ не должен ничего писать")
	}
	f, err := s.SetWindowEnd(ctx, d(2026, 10, 10)) // последний день — сегодня: можно
	if err != nil || f.DaysLeft != 1 {
		t.Fatalf("конец сегодня: %+v err=%v", f, err)
	}
	if f, err = s.ResizeWindow(ctx, 4); err != nil || f.Days != 10 {
		t.Fatalf("продлить после сокращения: %+v err=%v", f, err)
	}
}

// На 15-й день продлённого окна вернуть его к 14 дням нельзя (конец встал бы на вчера), а сократить до сегодня — можно.
func TestService_EditWindow_Day15OfExtendedWindow(t *testing.T) {
	ctx := context.Background()
	r := &fakeReader{snap: Snapshot{
		Incomes:       []Event{incomeFact("transaction:a", d(2026, 10, 5), rub(1000))},
		WindowLengths: []WindowLength{{Start: d(2026, 10, 5), Days: 20}},
	}}
	s := svc(r, d(2026, 10, 19)).WithWindowLengths(&fakeLengths{r: r})
	if _, err := s.SetWindowEnd(ctx, d(2026, 10, 18)); !errors.Is(err, ErrWindowEndPast) {
		t.Fatalf("14 дней на 15-й день: %v", err)
	}
	if f, err := s.ResizeWindow(ctx, -5); err != nil || f.End != d(2026, 10, 19) || f.DaysLeft != 1 {
		t.Fatalf("сократить до сегодня: %+v err=%v", f, err)
	}
}

func TestService_EditWindow_ReadOnlyAndNoWindow(t *testing.T) {
	ctx := context.Background()
	r := &fakeReader{snap: Snapshot{Incomes: []Event{incomeFact("transaction:a", d(2026, 10, 5), rub(1000))}}}
	if _, err := svc(r, d(2026, 10, 10)).ResizeWindow(ctx, 1); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("без хранилища правки: %v", err)
	}
	ended, _ := lsvc(d(2026, 11, 30), incomeFact("transaction:a", d(2026, 10, 5), rub(1000)))
	if _, err := ended.ResizeWindow(ctx, 1); !errors.Is(err, ErrNoWindow) {
		t.Fatalf("последнее окно закончилось: %v", err)
	}
	none, _ := lsvc(d(2026, 10, 10))
	if _, err := none.CurrentWindow(ctx); !errors.Is(err, ErrNoWindow) {
		t.Fatalf("окон нет: %v", err)
	}
}

// В зазоре экран показывает следующее окно — его и правим.
func TestService_EditWindow_BetweenTargetsNext(t *testing.T) {
	ctx := context.Background()
	s, l := lsvc(d(2026, 10, 20),
		incomeFact("transaction:a", d(2026, 10, 1), rub(1000)),
		incomeFact("transaction:b", d(2026, 10, 25), rub(1000)))
	if _, err := s.ResizeWindow(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if l.sets[0].Start != d(2026, 10, 25) {
		t.Fatalf("правится окно на экране (следующее): %+v", l.sets)
	}
}

func TestService_EditWindow_StoreErrorPropagates(t *testing.T) {
	s, l := lsvc(d(2026, 10, 10), incomeFact("transaction:a", d(2026, 10, 5), rub(1000)))
	l.err = errors.New("база недоступна")
	if _, err := s.ResizeWindow(context.Background(), 1); !errors.Is(err, l.err) {
		t.Fatalf("ошибка хранилища: %v", err)
	}
}

func TestService_View_ShowsWindowLength(t *testing.T) {
	s, _ := lsvc(d(2026, 10, 10), incomeFact("transaction:a", d(2026, 10, 5), rub(1000)))
	if _, err := s.ResizeWindow(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	res, err := s.View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	w := res.View.Window
	if w == nil || w.Days != 16 || !w.Adjusted || w.End != d(2026, 10, 20) || w.DaysLeft != 11 {
		t.Fatalf("экран видит поправленное окно: %+v", w)
	}
}
