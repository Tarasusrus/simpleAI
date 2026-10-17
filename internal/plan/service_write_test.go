package plan

import (
	"context"
	"errors"
	"testing"
)

type fakeWriter struct {
	added   []NewItem
	scopes  []Scope
	deleted []string
	settled []settleCall
	skipped []string
	moved   []moveCall
	rates   []int64
	err     error
}

type moveCall struct {
	id          string
	date, start *Date
}

type settleCall struct {
	id     string
	actual *Money
	date   Date
}

func (f *fakeWriter) AddPlanned(_ context.Context, sc Scope, n NewItem) (string, error) {
	f.added = append(f.added, n)
	f.scopes = append(f.scopes, sc)
	return "id-1", f.err
}

func (f *fakeWriter) DeletePlanned(_ context.Context, _ Scope, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

func (f *fakeWriter) SettlePlanned(_ context.Context, _ Scope, id string, actual *Money, date Date) error {
	f.settled = append(f.settled, settleCall{id, actual, date})
	return f.err
}

func (f *fakeWriter) MovePlanned(_ context.Context, _ Scope, id string, date, start *Date) error {
	f.moved = append(f.moved, moveCall{id, date, start})
	return f.err
}

func (f *fakeWriter) SkipTerm(_ context.Context, _ Scope, ruleID string, term Date) error {
	f.skipped = append(f.skipped, ruleID+"@"+term.String())
	return f.err
}

func (f *fakeWriter) SetRate(_ context.Context, micro int64) error {
	f.rates = append(f.rates, micro)
	return f.err
}

func (f *fakeWriter) ExpenseCategories(context.Context) ([]string, error) {
	return []string{"Еда"}, nil
}

func wsvc(r *fakeReader, w *fakeWriter, today Date) *Service {
	return NewService(r, w, func() Date { return today }, Scope{ChatIDs: []int64{42}})
}

func TestService_ReadOnly(t *testing.T) {
	s := svc(&fakeReader{snap: baseSnap()}, d(2026, 3, 10))
	if _, err := s.AddIncome(context.Background(), AddIncomeInput{Name: "x", Amount: *rub(1), Date: d(2026, 3, 20)}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("AddIncome: %v", err)
	}
	if _, err := s.AddPayment(context.Background(), AddPaymentInput{Name: "x"}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("AddPayment: %v", err)
	}
	if err := s.Delete(context.Background(), "planned:1"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Delete: %v", err)
	}
}

func TestService_AddIncome(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 3, 10))
	id, err := s.AddIncome(context.Background(), AddIncomeInput{Name: "  аванс ", Amount: *rub(30000), Date: d(2026, 3, 20)})
	if err != nil || id != "id-1" {
		t.Fatalf("AddIncome: %v %q", err, id)
	}
	got := w.added[0]
	if got.Kind != KindIncome || got.Name != "аванс" || got.Amount.Minor != 3_000_000 || *got.Date != d(2026, 3, 20) || got.WindowStart != nil {
		t.Errorf("записано %+v", got)
	}
	if len(w.scopes[0].ChatIDs) != 1 || w.scopes[0].ChatIDs[0] != 42 {
		t.Errorf("область не передана писателю: %+v", w.scopes)
	}
	bad := []struct {
		name string
		in   AddIncomeInput
		want error
	}{
		{"без названия", AddIncomeInput{Amount: *rub(1), Date: d(2026, 3, 20)}, ErrNameRequired},
		{"нулевая сумма", AddIncomeInput{Name: "x", Amount: Money{0, CurRUB}, Date: d(2026, 3, 20)}, ErrAmountNonPositive},
		{"чужая валюта", AddIncomeInput{Name: "x", Amount: Money{100, "USD"}, Date: d(2026, 3, 20)}, ErrUnsupportedCurrency},
		{"битая дата", AddIncomeInput{Name: "x", Amount: *rub(1), Date: d(2026, 2, 30)}, ErrBadDate},
		{"лишние цифры", AddIncomeInput{Name: "x", Amount: Money{Minor: maxAmountMinor + 1, Cur: CurRUB}, Date: d(2026, 3, 20)}, ErrAmountTooLarge},
	}
	for _, c := range bad {
		before := len(w.added)
		if _, err := s.AddIncome(context.Background(), c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, ждали %v", c.name, err, c.want)
		}
		if len(w.added) != before {
			t.Errorf("%s: невалидная запись дошла до писателя", c.name)
		}
	}
}

// Окно «в это» — день начала окна, которое сейчас на экране; «в следующее» — старт
// следующего, а если его ещё нет, то день после конца текущего.
//
// Мутация: вернуть для WindowNext Start текущего окна — тест краснеет.
func TestService_AddPayment_WindowChoice(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 3, 10)) // окно 05.03–18.03
	ctx := context.Background()
	if _, err := s.AddPayment(ctx, AddPaymentInput{Name: "тоник", Window: WindowCurrent}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPayment(ctx, AddPaymentInput{Name: "корм", Amount: rub(500), Window: WindowNext, Category: "Еда"}); err != nil {
		t.Fatal(err)
	}
	if ws := w.added[0].WindowStart; ws == nil || *ws != d(2026, 3, 5) {
		t.Errorf("«в это окно»: %v, ждали 05.03", ws)
	}
	if ws := w.added[1].WindowStart; ws == nil || *ws != d(2026, 3, 19) {
		t.Errorf("«в следующее» без окна: %v, ждали 19.03 (день после конца текущего)", ws)
	}
	if w.added[1].Category != "Еда" || w.added[1].Amount.Minor != 50000 {
		t.Errorf("запись %+v", w.added[1])
	}
	// Дата и окно вместе — отказ.
	if _, err := s.AddPayment(ctx, AddPaymentInput{Name: "x", Date: dp(2026, 3, 12), Window: WindowCurrent}); !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("дата+окно: %v", err)
	}
	// Платёж с датой и без суммы — можно; без даты и окна — тоже («пока без даты»).
	if _, err := s.AddPayment(ctx, AddPaymentInput{Name: "права", Date: dp(2026, 3, 12)}); err != nil {
		t.Errorf("платёж с датой без суммы: %v", err)
	}
	if _, err := s.AddPayment(ctx, AddPaymentInput{Name: "уборка"}); err != nil {
		t.Errorf("платёж без даты: %v", err)
	}
	if _, err := s.AddPayment(ctx, AddPaymentInput{Name: "x", Window: "weird"}); !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("неизвестное окно: %v", err)
	}
}

// Нет открытого окна (последнее закончилось) — «в окно» невозможно.
func TestService_AddPayment_NoWindow(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 6, 1)) // окно закончилось 18.03
	if _, err := s.AddPayment(context.Background(), AddPaymentInput{Name: "x", Window: WindowCurrent}); !errors.Is(err, ErrNoWindow) {
		t.Errorf("ждали ErrNoWindow: %v", err)
	}
	snap := baseSnap()
	snap.Incomes = nil
	s = wsvc(&fakeReader{snap: snap}, w, d(2026, 3, 10))
	if _, err := s.AddPayment(context.Background(), AddPaymentInput{Name: "x", Window: WindowNext}); !errors.Is(err, ErrNoWindow) {
		t.Errorf("без поступлений ждали ErrNoWindow: %v", err)
	}
	if len(w.added) != 0 {
		t.Error("при отказе запись всё равно ушла писателю")
	}
}

// Сценарий владельца: окно закончилось, он вносит ожидаемое поступление на будущую
// дату — экран переходит на это окно, и расходы можно планировать в него.
//
// Мутация: убрать ветку StateBetween из View — тест краснеет.
func TestService_View_BetweenFocusesNextWindow(t *testing.T) {
	snap := baseSnap()
	snap.Incomes = append(snap.Incomes, Event{
		Key: "planned:p1", Kind: KindIncome, State: StatePlanned, Name: "зарплата", Amount: rub(120000),
		Date: dp(2026, 3, 25), Source: SourcePlanned,
	})
	snap.Payments = append(snap.Payments, Event{
		Key: "planned:r1", Kind: KindPayment, State: StatePlanned, Name: "тоник", Amount: rub(1000),
		WindowStart: dp(2026, 3, 25), Source: SourcePlanned,
	})
	res, err := svc(&fakeReader{snap: snap}, d(2026, 3, 22)).View(context.Background(), CurRUB) // окно 05–18.03 закончилось
	if err != nil {
		t.Fatal(err)
	}
	v := res.View
	if v.State != StateUpcoming || v.Window == nil || v.Window.Start != d(2026, 3, 25) || !v.Window.AnchorPlanned {
		t.Fatalf("экран должен показывать будущее окно от 25.03: %+v %+v", v.State, v.Window)
	}
	if v.Income.Expected != 120000 || v.Payments < 1000 {
		t.Errorf("ожидаемое %d (ждали 120000), платежи %d (ждали ≥1000, тоник в этом окне)", v.Income.Expected, v.Payments)
	}
	if res.Meta.Today != d(2026, 3, 22) {
		t.Errorf("Meta.Today должен остаться настоящим: %v", res.Meta.Today)
	}
	// «В это окно» теперь — это окно от 25.03.
	w := &fakeWriter{}
	if _, err := wsvc(&fakeReader{snap: snap}, w, d(2026, 3, 22)).AddPayment(context.Background(), AddPaymentInput{Name: "ещё", Window: WindowCurrent}); err != nil {
		t.Fatal(err)
	}
	if ws := w.added[0].WindowStart; ws == nil || *ws != d(2026, 3, 25) {
		t.Errorf("«в это окно» в зазоре: %v, ждали 25.03", ws)
	}
}

func TestService_Delete(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 3, 10))
	if err := s.Delete(context.Background(), "planned:abc"); err != nil {
		t.Fatal(err)
	}
	if len(w.deleted) != 1 || w.deleted[0] != "abc" {
		t.Errorf("удалено %v, ждали abc без префикса", w.deleted)
	}
	w.err = ErrNotFound
	if err := s.Delete(context.Background(), "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ждали ErrNotFound: %v", err)
	}
}

// Следующее окно уже есть (поступление открыто) — «в следующее» привязывается к его старту.
//
// Мутация: брать Start текущего окна вместо следующего — тест краснеет.
func TestService_AddPayment_NextWindowExists(t *testing.T) {
	snap := baseSnap()
	snap.Incomes = append(snap.Incomes, Event{
		Key: "planned:p1", Kind: KindIncome, State: StatePlanned, Name: "зарплата", Amount: rub(120000),
		Date: dp(2026, 3, 25), Source: SourcePlanned,
	})
	w := &fakeWriter{}
	if _, err := wsvc(&fakeReader{snap: snap}, w, d(2026, 3, 10)).AddPayment(context.Background(), AddPaymentInput{Name: "x", Window: WindowNext}); err != nil {
		t.Fatal(err)
	}
	if ws := w.added[0].WindowStart; ws == nil || *ws != d(2026, 3, 25) {
		t.Errorf("«в следующее»: %v, ждали 25.03", ws)
	}
}

// Предельная сумма принимается, а лишняя цифра — нет; принятая не ломает расчёт.
//
// Мутация: убрать проверку maxAmountMinor — тест краснеет.
func TestService_AmountCap_NoOverflowDownstream(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 3, 10))
	if _, err := s.AddIncome(context.Background(), AddIncomeInput{Name: "max", Amount: Money{Minor: maxAmountMinor, Cur: CurRUB}, Date: d(2026, 3, 20)}); err != nil {
		t.Fatalf("предельная сумма отвергнута: %v", err)
	}
	snap := baseSnap()
	snap.Incomes = append(snap.Incomes, incomeFact("transaction:big", d(2026, 3, 6), &Money{Minor: maxAmountMinor, Cur: CurRUB}))
	snap.ReservePct = 100
	if _, err := svc(&fakeReader{snap: snap}, d(2026, 3, 10)).View(context.Background(), CurTHB); err != nil {
		t.Errorf("предельная сумма ломает расчёт: %v", err)
	}
}

func TestService_Settle(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 3, 10))
	ctx := context.Background()
	if err := s.Settle(ctx, "planned:abc", nil); err != nil {
		t.Fatal(err)
	}
	if got := w.settled[0]; got.id != "abc" || got.actual != nil || got.date != d(2026, 3, 10) {
		t.Errorf("закрыто как %+v", got)
	}
	if err := s.Settle(ctx, "planned:abc", rub(500)); err != nil || w.settled[1].actual.Minor != 50000 {
		t.Errorf("сумма не дошла: %v %+v", err, w.settled)
	}
	n := len(w.settled)
	for name, tc := range map[string]struct {
		key    string
		actual *Money
		want   error
	}{
		"повтор нельзя закрыть вручную": {"recurring:r@2026-03-10", nil, ErrInvalidEvent},
		"факт нельзя закрыть":           {"transaction:1", nil, ErrInvalidEvent},
		"нулевая сумма":                 {"planned:abc", &Money{Minor: 0, Cur: CurRUB}, ErrAmountNonPositive},
		"чужая валюта":                  {"planned:abc", &Money{Minor: 1, Cur: "USD"}, ErrUnsupportedCurrency},
		"слишком большая":               {"planned:abc", &Money{Minor: maxAmountMinor + 1, Cur: CurRUB}, ErrAmountTooLarge},
	} {
		if err := s.Settle(ctx, tc.key, tc.actual); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, ждали %v", name, err, tc.want)
		}
	}
	if len(w.settled) != n {
		t.Errorf("невалидные команды дошли до писателя")
	}
	if err := svc(&fakeReader{snap: baseSnap()}, d(2026, 3, 10)).Settle(ctx, "planned:abc", nil); !errors.Is(err, ErrReadOnly) {
		t.Errorf("без писателя: %v", err)
	}
}

func TestService_Skip(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 3, 10))
	ctx := context.Background()
	if err := s.Skip(ctx, "recurring:rule-1@2026-03-15"); err != nil || w.skipped[0] != "rule-1@2026-03-15" {
		t.Fatalf("%v %v", err, w.skipped)
	}
	for _, key := range []string{"planned:abc", "recurring:@2026-03-15", "recurring:r", "recurring:r@2026-13-01", "recurring:r@15.03.2026"} {
		if err := s.Skip(ctx, key); err == nil {
			t.Errorf("%q принят", key)
		}
	}
	if len(w.skipped) != 1 {
		t.Errorf("невалидные ключи дошли до писателя: %v", w.skipped)
	}
}

func TestParseDate(t *testing.T) {
	if got, err := ParseDate(" 2026-03-05 "); err != nil || got != d(2026, 3, 5) {
		t.Errorf("%v %v", got, err)
	}
	for _, in := range []string{"", "2026-3-5", "2026-02-30", "26-03-05", "2026-03-05x", "2026/03/05"} {
		if _, err := ParseDate(in); !errors.Is(err, ErrBadDate) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestService_SetRate(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 3, 10))
	ctx := context.Background()
	if err := s.SetRate(ctx, 2_500_000); err != nil || w.rates[0] != 2_500_000 {
		t.Fatalf("%v %v", err, w.rates)
	}
	for _, bad := range []int64{0, -1, maxRateMicro + 1} {
		if err := s.SetRate(ctx, bad); !errors.Is(err, ErrBadRate) {
			t.Errorf("%d: %v", bad, err)
		}
	}
	if len(w.rates) != 1 {
		t.Errorf("плохой курс дошёл до писателя: %v", w.rates)
	}
	if err := svc(&fakeReader{snap: baseSnap()}, d(2026, 3, 10)).SetRate(ctx, 2_500_000); !errors.Is(err, ErrReadOnly) {
		t.Errorf("без писателя: %v", err)
	}
}

func TestService_ViewReportsRate(t *testing.T) {
	snap := baseSnap()
	snap.Rate = &Rate{RubPerThbMicro: 2_650_000}
	res, err := svc(&fakeReader{snap: snap}, d(2026, 3, 10)).View(context.Background(), CurRUB)
	if err != nil || res.Meta.RubPerThbMicro != 2_650_000 {
		t.Errorf("%v %d", err, res.Meta.RubPerThbMicro)
	}
}

func TestService_Move(t *testing.T) {
	w := &fakeWriter{}
	s := wsvc(&fakeReader{snap: baseSnap()}, w, d(2026, 3, 10)) // окно 5–18 марта
	ctx := context.Background()
	day := d(2026, 4, 2)

	if err := s.Move(ctx, "planned:a", MoveInput{Window: WindowCurrent}); err != nil {
		t.Fatal(err)
	}
	if m := w.moved[0]; m.id != "a" || m.date != nil || m.start == nil || *m.start != d(2026, 3, 5) {
		t.Errorf("в текущее окно: %+v", m)
	}
	if err := s.Move(ctx, "planned:b", MoveInput{Window: WindowNext}); err != nil {
		t.Fatal(err)
	}
	if m := w.moved[1]; m.start == nil || *m.start != d(2026, 3, 19) {
		t.Errorf("в следующее окно: %+v", m)
	}
	if err := s.Move(ctx, "planned:c", MoveInput{Date: &day}); err != nil {
		t.Fatal(err)
	}
	if m := w.moved[2]; m.date == nil || *m.date != day || m.start != nil {
		t.Errorf("на дату: %+v", m)
	}
	if err := s.Move(ctx, "planned:d", MoveInput{}); err != nil {
		t.Fatal(err)
	}
	if m := w.moved[3]; m.date != nil || m.start != nil {
		t.Errorf("в «без даты» должно очистить оба: %+v", m)
	}

	n := len(w.moved)
	bad := d(2026, 2, 30)
	for name, tc := range map[string]struct {
		key  string
		in   MoveInput
		want error
	}{
		"повтор нельзя":  {"recurring:r@2026-03-15", MoveInput{Window: WindowCurrent}, ErrInvalidEvent},
		"факт нельзя":    {"transaction:1", MoveInput{}, ErrInvalidEvent},
		"дата и окно":    {"planned:a", MoveInput{Date: &day, Window: WindowNext}, ErrInvalidEvent},
		"странное окно":  {"planned:a", MoveInput{Window: "later"}, ErrInvalidEvent},
		"несуществующая": {"planned:a", MoveInput{Date: &bad}, ErrBadDate},
	} {
		if err := s.Move(ctx, tc.key, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, ждали %v", name, err, tc.want)
		}
	}
	if len(w.moved) != n {
		t.Errorf("невалидные переносы дошли до писателя")
	}
	// окон нет совсем: «в окно» невозможно, а «на дату» и «без даты» можно
	empty := wsvc(&fakeReader{snap: Snapshot{Rate: &Rate{RubPerThbMicro: 2_500_000}}}, w, d(2026, 3, 10))
	if err := empty.Move(ctx, "planned:a", MoveInput{Window: WindowCurrent}); !errors.Is(err, ErrNoWindow) {
		t.Errorf("без окон: %v", err)
	}
	if err := empty.Move(ctx, "planned:a", MoveInput{}); err != nil {
		t.Errorf("«без даты» без окон: %v", err)
	}
	if err := svc(&fakeReader{snap: baseSnap()}, d(2026, 3, 10)).Move(ctx, "planned:a", MoveInput{}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("без писателя: %v", err)
	}
}
