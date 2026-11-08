package plan

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// memWindows — хранилище окон в снимке читателя: следующее чтение видит записи, как с настоящей базой.
type memWindows struct {
	r       *fakeReader
	creates []StoredWindow
	updates []StoredWindow
	deletes []string
	scopes  []Scope
	err     error
	n       int
}

func (m *memWindows) CreateWindow(_ context.Context, sc Scope, start, end Date, fixed bool) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	m.scopes = append(m.scopes, sc)
	for _, w := range m.r.snap.Windows {
		if !w.Deleted && w.Start == start {
			return w.ID, nil
		}
	}
	m.n++
	w := StoredWindow{ID: fmt.Sprintf("w%d", m.n), Start: start, End: end, FixedEnd: fixed}
	m.creates = append(m.creates, w)
	m.r.snap.Windows = append(m.r.snap.Windows, w)
	return w.ID, nil
}

func (m *memWindows) UpdateWindow(_ context.Context, sc Scope, id string, start, end Date, fixed bool) error {
	if m.err != nil {
		return m.err
	}
	m.scopes = append(m.scopes, sc)
	for i, w := range m.r.snap.Windows {
		if w.ID == id && !w.Deleted {
			m.r.snap.Windows[i].Start, m.r.snap.Windows[i].End, m.r.snap.Windows[i].FixedEnd = start, end, fixed
			m.updates = append(m.updates, m.r.snap.Windows[i])
			return nil
		}
	}
	return ErrNotFound
}

func (m *memWindows) DeleteWindow(_ context.Context, sc Scope, id string) error {
	m.scopes = append(m.scopes, sc)
	for i, w := range m.r.snap.Windows {
		if w.ID == id && !w.Deleted {
			m.r.snap.Windows[i].Deleted = true
			m.deletes = append(m.deletes, id)
			return nil
		}
	}
	return ErrNotFound
}

func wsvc2(today Date, incomes ...Event) (*Service, *memWindows) {
	r := &fakeReader{snap: Snapshot{Incomes: incomes}}
	m := &memWindows{r: r}
	return svc(r, today).WithWindowStore(m), m
}

func salary(key string, at Date) Event { return incomeFact("transaction:"+key, at, rub(1000)) }

func expected(key string, at Date) Event {
	return Event{Key: "planned:" + key, Kind: KindIncome, State: StatePlanned, Name: key, Amount: rub(1000), Date: &at, Source: SourcePlanned}
}

// --- BuildWindows с сохранёнными окнами ---

func TestBuildWindows_StoredWindowIndependentOfIncomes(t *testing.T) {
	sw := StoredWindow{ID: "w1", Start: d(2026, 10, 3), End: d(2026, 10, 20)}
	ws := BuildWindows([]Event{inc("a", d(2026, 10, 5), StateDone, 1)}, sw)
	if len(ws) != 1 || ws[0].ID != "w1" || ws[0].Start != d(2026, 10, 3) || ws[0].End() != d(2026, 10, 20) || len(ws[0].Incomes) != 1 {
		t.Fatalf("поступление внутри сохранённого окна идёт в него, даты окна не меняются: %+v", ws)
	}
	if empty := BuildWindows(nil, sw); len(empty) != 1 || empty[0].ID != "w1" {
		t.Fatalf("окно без поступлений остаётся (решение владельца): %+v", empty)
	}
}

// Удалённое окно: на его датах окно само не появляется, поступление уходит в соседнее по дате.
func TestBuildWindows_DeletedZone(t *testing.T) {
	gone := StoredWindow{ID: "w0", Start: d(2026, 10, 1), End: d(2026, 10, 14), Deleted: true}
	next := StoredWindow{ID: "w1", Start: d(2026, 10, 15), End: d(2026, 10, 28)}
	ws := BuildWindows([]Event{inc("a", d(2026, 10, 1), StateDone, 1)}, gone, next)
	if len(ws) != 1 || ws[0].ID != "w1" || len(ws[0].Incomes) != 1 {
		t.Fatalf("поступление удалённого окна — в следующее: %+v", ws)
	}
}

// Без сохранённых окон результат тот же, что до ADR-021, а сохранив все окна, получаем те же даты.
func TestBuildWindows_StoringChangesNothing(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 8).Draw(rt, "n")
		evs := make([]Event, 0, n)
		for i := range n {
			day := rapid.IntRange(1, 120).Draw(rt, "day")
			evs = append(evs, inc(string(rune('a'+i)), d(2026, 1, 1).AddDays(day), StateDone, 1))
		}
		plain := BuildWindows(evs)
		stored := make([]StoredWindow, 0, len(plain))
		for i, w := range plain {
			if w.Days() != windowDays {
				rt.Fatalf("без сохранённых окон все по 14 дней: %+v", w)
			}
			stored = append(stored, StoredWindow{ID: fmt.Sprint(i), Start: w.Start, End: w.End()})
		}
		again := BuildWindows(evs, stored...)
		if len(again) != len(plain) {
			rt.Fatalf("сохранение изменило число окон: %d → %d", len(plain), len(again))
		}
		for i := range plain {
			if again[i].Start != plain[i].Start || again[i].End() != plain[i].End() || len(again[i].Incomes) != len(plain[i].Incomes) {
				rt.Fatalf("окно %d: %+v → %+v", i, plain[i], again[i])
			}
		}
	})
}

func TestLocate_UpcomingUsesWindowLength(t *testing.T) {
	ws := BuildWindows(nil, StoredWindow{ID: "w", Start: d(2026, 10, 5), End: d(2026, 10, 24)})
	if st := Locate(ws, d(2026, 10, 1)); st.Kind != StateUpcoming || st.DaysLeft != 20 {
		t.Fatalf("будущее окно 20 дней: %+v", st)
	}
}

// --- Сохранение окон ---

// Начавшееся окно сохраняется само; будущее (открытое ожидаемой зарплатой) — нет.
func TestService_MaterializesStartedWindowsOnly(t *testing.T) {
	s, m := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)), expected("b", d(2026, 10, 25)))
	if _, err := s.View(context.Background(), CurRUB); err != nil {
		t.Fatal(err)
	}
	want := []StoredWindow{{ID: "w1", Start: d(2026, 10, 5), End: d(2026, 10, 18)}}
	if !reflect.DeepEqual(m.creates, want) {
		t.Fatalf("сохранено %+v, ждали %+v", m.creates, want)
	}
	if !reflect.DeepEqual(m.scopes[0], Scope{ChatIDs: []int64{42}}) {
		t.Fatalf("область не дошла до хранилища: %+v", m.scopes)
	}
	if _, err := s.View(context.Background(), CurRUB); err != nil || len(m.creates) != 1 {
		t.Fatalf("повторное чтение не создаёт окно второй раз: %+v err=%v", m.creates, err)
	}
}

// Сохранённое окно остаётся, даже если его зарплату удалили или перенесли.
func TestService_StoredWindowSurvivesIncomeChange(t *testing.T) {
	s, m := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)))
	if _, err := s.View(context.Background(), CurRUB); err != nil {
		t.Fatal(err)
	}
	m.r.snap.Incomes = []Event{salary("a", d(2026, 10, 7))}
	f, err := s.CurrentWindow(context.Background())
	if err != nil || f.Start != d(2026, 10, 5) || f.End != d(2026, 10, 18) {
		t.Fatalf("окно после переноса зарплаты: %+v err=%v", f, err)
	}
}

func TestService_NoStore_ComputesOnly(t *testing.T) {
	r := &fakeReader{snap: Snapshot{Incomes: []Event{salary("a", d(2026, 10, 5))}}}
	s := svc(r, d(2026, 10, 10))
	if _, err := s.View(context.Background(), CurRUB); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResizeWindow(context.Background(), 1); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("без хранилища правка отключена: %v", err)
	}
}

func TestService_MaterializeErrorSurfaces(t *testing.T) {
	s, m := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)))
	m.err = errors.New("база недоступна")
	if _, err := s.View(context.Background(), CurRUB); !errors.Is(err, m.err) {
		t.Fatalf("ошибка сохранения окна: %v", err)
	}
}

// --- Правка ---

func TestService_ResizeAndSetEnd(t *testing.T) {
	ctx := context.Background()
	s, m := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)))
	f, err := s.ResizeWindow(ctx, 1)
	want := WindowFrame{Start: d(2026, 10, 5), End: d(2026, 10, 19), Days: 15, DaysLeft: 10, Adjusted: true}
	if err != nil || f != want {
		t.Fatalf("+1 день: %+v err=%v, ждали %+v", f, err, want)
	}
	if f, err = s.ResizeWindow(ctx, -3); err != nil || f.Days != 12 || f.End != d(2026, 10, 16) {
		t.Fatalf("−3: %+v err=%v", f, err)
	}
	if f, err = s.SetWindowEnd(ctx, d(2026, 10, 25)); err != nil || f.Days != 21 {
		t.Fatalf("конец 25.10: %+v err=%v", f, err)
	}
	if len(m.creates) != 1 || len(m.updates) != 3 {
		t.Fatalf("одно окно, три правки: creates=%v updates=%v", m.creates, m.updates)
	}
	if cur, err := s.CurrentWindow(ctx); err != nil || cur != f {
		t.Fatalf("CurrentWindow=%+v err=%v", cur, err)
	}
}

// Начало двигается: поступление, оказавшееся до начала, уходит в соседнее окно по дате, а «сегодня» может
// оказаться до окна.
func TestService_SetWindowStart(t *testing.T) {
	ctx := context.Background()
	s, _ := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)))
	f, err := s.SetWindowStart(ctx, d(2026, 10, 3))
	if err != nil || f.Start != d(2026, 10, 3) || f.End != d(2026, 10, 18) || f.Days != 16 {
		t.Fatalf("начало 03.10: %+v err=%v", f, err)
	}
	res, err := s.View(ctx, CurRUB)
	if err != nil || res.View.Window == nil || res.View.Window.Start != d(2026, 10, 3) || res.View.Income.Received != 1000 {
		t.Fatalf("экран после сдвига начала: %+v err=%v", res.View.Window, err)
	}
}

// Будущее окно (ожидаемая зарплата) при правке сохраняется сразу.
func TestService_EditProvisionalStoresIt(t *testing.T) {
	s, m := wsvc2(d(2026, 10, 1), expected("b", d(2026, 10, 5)))
	f, err := s.ResizeWindow(context.Background(), 2)
	if err != nil || f.Days != 16 || len(m.creates) != 1 || m.creates[0].Start != d(2026, 10, 5) {
		t.Fatalf("frame=%+v creates=%+v err=%v", f, m.creates, err)
	}
}

func TestService_EditRejects(t *testing.T) {
	ctx := context.Background()
	s, m := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)), salary("b", d(2026, 10, 1)))
	_ = m
	s2, m2 := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)), expected("next", d(2026, 10, 25)))
	if _, err := s2.SetWindowEnd(ctx, d(2026, 10, 30)); err != nil {
		t.Fatalf("продлить через будущее расчётное окно можно (оно пересчитается): %v", err)
	}
	cases := []struct {
		name string
		run  func() (WindowFrame, error)
		want error
	}{
		{"ноль дней", func() (WindowFrame, error) { return s.ResizeWindow(ctx, 0) }, ErrBadWindowLength},
		{"длиннее предела", func() (WindowFrame, error) { return s.ResizeWindow(ctx, MaxWindowDays) }, ErrBadWindowLength},
		{"конец раньше начала", func() (WindowFrame, error) { return s.SetWindowStart(ctx, d(2026, 10, 20)) }, ErrBadWindowLength},
		{"конец вчера", func() (WindowFrame, error) { return s.SetWindowEnd(ctx, d(2026, 10, 9)) }, ErrWindowEndPast},
		{"кривая дата", func() (WindowFrame, error) { return s.SetWindowEnd(ctx, Date{2026, 13, 1}) }, ErrBadDate},
		{"кривое начало", func() (WindowFrame, error) { return s.SetWindowStart(ctx, Date{2026, 2, 30}) }, ErrBadDate},
	}
	for _, c := range cases {
		if _, err := c.run(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, ждали %v", c.name, err, c.want)
		}
	}
	_ = m2
}

// Сохранённые окна не пересекаются.
func TestService_EditOverlapRejected(t *testing.T) {
	ctx := context.Background()
	s, m := wsvc2(d(2026, 10, 20), salary("a", d(2026, 10, 1)), salary("b", d(2026, 10, 15)))
	if _, err := s.View(ctx, CurRUB); err != nil || len(m.creates) != 2 {
		t.Fatalf("оба начавшихся окна сохранены: %+v err=%v", m.creates, err)
	}
	if _, err := s.SetWindowStart(ctx, d(2026, 10, 14)); !errors.Is(err, ErrWindowOverlap) {
		t.Fatalf("начало внутри прошлого окна: %v", err)
	}
	if len(m.updates) != 0 {
		t.Fatal("отказ не должен ничего писать")
	}
}

func TestService_DeleteWindow(t *testing.T) {
	ctx := context.Background()
	// Сегодня 10.10 в окне 05.10; следующее окно откроет ожидаемая зарплата 25.10. После удаления на экране будущее окно.
	s, m := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)), expected("b", d(2026, 10, 25)))
	f, err := s.DeleteWindow(ctx)
	if err != nil || len(m.deletes) != 1 || f.Start != d(2026, 10, 25) {
		t.Fatalf("удалено %v, на экране %+v, err=%v", m.deletes, f, err)
	}
	if _, err := s.View(ctx, CurRUB); err != nil {
		t.Fatal(err)
	}
	for _, w := range m.r.snap.Windows {
		if !w.Deleted && w.Start == d(2026, 10, 5) {
			t.Fatalf("удалённое окно появилось снова: %+v", m.r.snap.Windows)
		}
	}
	// Остальные окна закончились: после удаления прошлое окно само продолжится до сегодня (ADR-022).
	ended, me := wsvc2(d(2026, 10, 20), salary("a", d(2026, 10, 1)), salary("b", d(2026, 10, 15)))
	f, err = ended.DeleteWindow(ctx)
	if err != nil || len(me.deletes) != 1 || f.Start != d(2026, 10, 1) || !f.Ongoing || f.End != d(2026, 10, 20) {
		t.Fatalf("прошлое окно продолжилось: %+v err=%v deletes=%v", f, err, me.deletes)
	}
	only, _ := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)))
	if _, err := only.DeleteWindow(ctx); !errors.Is(err, ErrLastWindow) {
		t.Fatalf("единственное окно: %v", err)
	}
}

// Сразу после миграции сохранено только поправленное текущее окно: прошлые зарплаты дают свои окна до него и
// сохраняются при чтении, а не сливаются с ним.
func TestService_HistoryBeforeFirstStoredWindow(t *testing.T) {
	r := &fakeReader{snap: Snapshot{
		Incomes: []Event{salary("s1", d(2026, 9, 7)), salary("s2", d(2026, 9, 21)), salary("s3", d(2026, 10, 5))},
		Windows: []StoredWindow{{ID: "migrated", Start: d(2026, 10, 5), End: d(2026, 10, 20)}},
	}}
	m := &memWindows{r: r}
	s := svc(r, d(2026, 10, 10)).WithWindowStore(m)
	res, err := s.View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.Income.Received != 1000 {
		t.Fatalf("в текущем окне одна зарплата, а не три: %+v", res.View.Income)
	}
	want := []StoredWindow{{ID: "w1", Start: d(2026, 9, 7), End: d(2026, 9, 20)}, {ID: "w2", Start: d(2026, 9, 21), End: d(2026, 10, 4)}}
	if !reflect.DeepEqual(m.creates, want) {
		t.Fatalf("прошлые окна сохранены %+v, ждали %+v", m.creates, want)
	}
}

// Прошлая зарплата открывает окно до истории, только если окно целиком помещается до первого сохранённого.
func TestBuildWindows_PreHistoryOnlyWholeWindows(t *testing.T) {
	sw := StoredWindow{ID: "w", Start: d(2026, 10, 20), End: d(2026, 11, 2)}
	ws := BuildWindows([]Event{inc("a", d(2026, 10, 1), StateDone, 1), inc("b", d(2026, 10, 15), StateDone, 1)}, sw)
	if len(ws) != 2 || ws[0].Start != d(2026, 10, 1) || ws[0].End() != d(2026, 10, 14) || ws[1].ID != "w" || len(ws[1].Incomes) != 1 {
		t.Fatalf("01.10 — своё окно, 15.10 (не помещается) — в сохранённое: %+v", ws)
	}
	if err := checkWindows(ws); err != nil {
		t.Fatal(err)
	}
}

// Последнее окно закончилось, зарплаты нет: окно идёт до сегодня, и его можно править.
func TestService_EditOngoingWindow(t *testing.T) {
	ongoing, m := wsvc2(d(2026, 10, 25), salary("a", d(2026, 10, 5)))
	cur, err := ongoing.CurrentWindow(context.Background())
	if err != nil || !cur.Ongoing || cur.End != d(2026, 10, 25) || cur.DaysLeft != 1 {
		t.Fatalf("идущее окно: %+v err=%v", cur, err)
	}
	f, err := ongoing.ResizeWindow(context.Background(), 3)
	if err != nil || f.Ongoing || f.End != d(2026, 10, 28) || !m.updates[0].FixedEnd {
		t.Fatalf("+3 к идущему окну: %+v err=%v updates=%+v", f, err, m.updates)
	}
	none, _ := wsvc2(d(2026, 10, 10))
	if _, err := none.CurrentWindow(context.Background()); !errors.Is(err, ErrNoWindow) {
		t.Fatalf("окон нет: %v", err)
	}
}

func TestService_View_ShowsWindowLength(t *testing.T) {
	s, _ := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)))
	if _, err := s.ResizeWindow(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	res, err := s.View(context.Background(), CurRUB)
	if w := res.View.Window; err != nil || w == nil || w.Days != 16 || !w.Adjusted || w.DaysLeft != 11 {
		t.Fatalf("экран видит поправленное окно: %+v err=%v", w, err)
	}
}

// Сдвиг начала окна позже зарплаты не плодит рядом крошечное окно: зарплата остаётся в окне по дате.
func TestBuildWindows_IncomeBeforeMovedStartJoins(t *testing.T) {
	sw := StoredWindow{ID: "w1", Start: d(2026, 10, 7), End: d(2026, 10, 18)}
	ws := BuildWindows([]Event{inc("a", d(2026, 10, 5), StateDone, 1)}, sw)
	if len(ws) != 1 || ws[0].ID != "w1" || len(ws[0].Incomes) != 1 {
		t.Fatalf("зарплата до сдвинутого начала — в это же окно, новых окон нет: %+v", ws)
	}
	later := BuildWindows([]Event{inc("a", d(2026, 10, 5), StateDone, 1), inc("b", d(2026, 10, 19), StateDone, 1)}, sw)
	if len(later) != 2 || later[1].Stored() || later[1].Start != d(2026, 10, 19) {
		t.Fatalf("зарплата после истории открывает новое окно: %+v", later)
	}
}

// Пересекающиеся сохранённые окна (разные чаты области) не роняют расчёт: остаётся раннее.
func TestBuildWindows_OverlappingStoredKeepsEarliest(t *testing.T) {
	ws := BuildWindows(nil,
		StoredWindow{ID: "a", Start: d(2026, 10, 1), End: d(2026, 10, 14)},
		StoredWindow{ID: "b", Start: d(2026, 10, 10), End: d(2026, 10, 23)})
	if len(ws) != 1 || ws[0].ID != "a" {
		t.Fatalf("окна: %+v", ws)
	}
	if err := checkWindows(ws); err != nil {
		t.Fatal(err)
	}
}

// Удалённое последнее окно остаётся историей: его зарплата (даже лежащая до сдвинутого начала) окно не открывает.
func TestBuildWindows_DeletedLastWindowStaysHistory(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", d(2026, 10, 1), StateDone, 1), inc("b", d(2026, 10, 15), StateDone, 1)},
		StoredWindow{ID: "w1", Start: d(2026, 10, 1), End: d(2026, 10, 14)},
		StoredWindow{ID: "w2", Start: d(2026, 10, 16), End: d(2026, 10, 30), Deleted: true})
	if len(ws) != 1 || ws[0].ID != "w1" || len(ws[0].Incomes) != 2 {
		t.Fatalf("окна: %+v", ws)
	}
}

// Все окна удалены: расчёт не падает, окон нет.
func TestBuildWindows_AllDeletedNoPanic(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", d(2026, 10, 5), StateDone, 1)}, StoredWindow{ID: "w", Start: d(2026, 10, 5), End: d(2026, 10, 18), Deleted: true})
	if len(ws) != 0 {
		t.Fatalf("окна: %+v", ws)
	}
}

// Зарплата пришла позже конца окна (конец на выходных, деньги в понедельник): окно дотягивается до дня перед ней,
// приход в бывшем зазоре — в выросшее окно. Окно с концом, поставленным владельцем, не растёт.
func TestBuildGrowingWindows_FillsGapUnlessFixed(t *testing.T) {
	a := inc("a", d(2026, 10, 1), StateDone, 1)  // окно 01–14.10, 14.10 — суббота
	b := inc("b", d(2026, 10, 16), StateDone, 1) // зарплата в понедельник
	sale := Event{Key: "sale", Kind: KindIncome, State: StateDone, Name: "байк", Amount: &Money{100, CurRUB}, Date: dp(2026, 10, 15), JoinsWindow: true}
	ws := BuildGrowingWindows([]Event{a, b, sale})
	if len(ws) != 2 || ws[0].End() != d(2026, 10, 15) || ws[0].Extra != 1 || ws[0].BaseEnd() != d(2026, 10, 14) || len(ws[0].Incomes) != 2 {
		t.Fatalf("окно до 15.10 с продажей внутри: %+v", ws)
	}
	if err := checkWindows(ws); err != nil {
		t.Fatal(err)
	}
	fixed := BuildGrowingWindows([]Event{a, b}, StoredWindow{ID: "w", Start: d(2026, 10, 1), End: d(2026, 10, 12), FixedEnd: true})
	if fixed[0].End() != d(2026, 10, 12) || fixed[0].Extra != 0 {
		t.Fatalf("конец владельца не растёт: %+v", fixed[0])
	}
	if plain := BuildWindows([]Event{a, b}); plain[0].End() != d(2026, 10, 14) {
		t.Fatalf("BuildWindows без роста: %+v", plain[0])
	}
}

func TestExtendLast(t *testing.T) {
	ws := BuildGrowingWindows([]Event{inc("a", d(2026, 10, 1), StateDone, 1)})
	ExtendLast(ws, d(2026, 10, 10))
	if ws[0].Ongoing || ws[0].Extra != 0 {
		t.Fatalf("окно ещё идёт само: %+v", ws[0])
	}
	ExtendLast(ws, d(2026, 10, 20))
	if !ws[0].Ongoing || ws[0].End() != d(2026, 10, 20) || ws[0].BaseEnd() != d(2026, 10, 14) {
		t.Fatalf("закончившееся окно идёт до сегодня: %+v", ws[0])
	}
	ExtendLast(nil, d(2026, 10, 20)) // без окон — без паники
}

// Сохраняется заданный конец, а не выросший: рост пересчитывается каждый раз.
func TestService_MaterializeStoresBaseEnd(t *testing.T) {
	s, m := wsvc2(d(2026, 10, 20), salary("a", d(2026, 10, 1)))
	if _, err := s.View(context.Background(), CurRUB); err != nil {
		t.Fatal(err)
	}
	if len(m.creates) != 1 || m.creates[0].End != d(2026, 10, 14) || m.creates[0].FixedEnd {
		t.Fatalf("сохранено %+v", m.creates)
	}
}

// Начало сдвигается у растущего окна — оно растёт и дальше; правка конца делает конец «владельца».
func TestService_SetStartKeepsGrowth(t *testing.T) {
	ctx := context.Background()
	s, m := wsvc2(d(2026, 10, 10), salary("a", d(2026, 10, 5)), expected("b", d(2026, 10, 25)))
	f, err := s.SetWindowStart(ctx, d(2026, 10, 4))
	if err != nil || f.End != d(2026, 10, 24) || m.updates[0].FixedEnd || m.updates[0].End != d(2026, 10, 18) {
		t.Fatalf("frame=%+v updates=%+v err=%v", f, m.updates, err)
	}
	if f, err = s.SetWindowEnd(ctx, d(2026, 10, 20)); err != nil || f.End != d(2026, 10, 20) || !m.updates[1].FixedEnd {
		t.Fatalf("конец владельца: frame=%+v updates=%+v err=%v", f, m.updates, err)
	}
}

// Правка выросшего окна (ADR-022): сдвиг начала у идущего окна работает, сокращение окна длиннее предела — тоже,
// а удлинение сверх предела — нет.
func TestService_EditGrownWindow(t *testing.T) {
	ctx := context.Background()
	s, m := wsvc2(d(2026, 10, 25), salary("a", d(2026, 10, 5))) // окно 05–18.10 идёт до 25.10
	f, err := s.SetWindowStart(ctx, d(2026, 10, 3))
	if err != nil || f.Start != d(2026, 10, 3) || f.End != d(2026, 10, 25) || !f.Ongoing || m.updates[0].FixedEnd ||
		m.updates[0].End != d(2026, 10, 18) {
		t.Fatalf("начало у идущего окна: %+v err=%v updates=%+v", f, err, m.updates)
	}
	long, _ := wsvc2(d(2026, 12, 31), salary("a", d(2026, 10, 1))) // идёт 92 дня
	if f, err := long.ResizeWindow(ctx, -10); err != nil || f.End != d(2026, 12, 21) {
		// конец 21.12 раньше сегодня — честный отказ про конец, а не про длину
		if !errors.Is(err, ErrWindowEndPast) {
			t.Fatalf("сокращение длинного окна упёрлось в длину: %+v err=%v", f, err)
		}
	}
	if _, err := long.ResizeWindow(ctx, 1); !errors.Is(err, ErrBadWindowLength) {
		t.Fatalf("удлинение сверх предела: %v", err)
	}
	if _, err := long.SetWindowStart(ctx, d(2026, 11, 15)); err != nil {
		t.Fatalf("сокращение длинного окна сдвигом начала: %v", err)
	}
}

// Самовыросшее окно не «изменено»; изменено — только поправленное владельцем.
func TestWindow_AdjustedOnlyByOwner(t *testing.T) {
	grown := BuildGrowingWindows([]Event{inc("a", d(2026, 10, 1), StateDone, 1), inc("b", d(2026, 10, 18), StateDone, 1)})
	if grown[0].Days() != 17 || grown[0].Adjusted() {
		t.Fatalf("выросшее окно: %+v", grown[0])
	}
}

// Долгое идущее окно видит сроки повторов с самого начала, а не только за 62 дня.
func TestService_LongOngoingWindowKeepsEarlyTerms(t *testing.T) {
	r := &fakeReader{snap: Snapshot{
		Incomes: []Event{salary("a", d(2026, 1, 1))},
		Rules: []RuleWithExceptions{{ID: "r", Name: "связь", Kind: KindPayment,
			Rule: Rule{Anchor: d(2026, 1, 3), N: 1, Unit: UnitMonth, Amount: *rub(100)}}},
	}}
	s := svc(r, d(2026, 4, 1)).WithWindowStore(&memWindows{r: r})
	if _, err := s.View(context.Background(), CurRUB); err != nil { // сохраняет окно 01.01
		t.Fatal(err)
	}
	res, err := s.View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.Payments != 300 { // сроки 03.01, 03.02, 03.03
		t.Fatalf("платежи окна %d, ждали 300 (сроки с начала окна)", res.View.Payments)
	}
}
