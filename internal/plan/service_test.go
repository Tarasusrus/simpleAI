package plan

import (
	"context"
	"errors"
	"testing"
)

// fakeReader отдаёт заготовленный снимок и запоминает область.
type fakeReader struct {
	snap Snapshot
	err  error
	got  []Scope
}

func (f *fakeReader) Read(_ context.Context, sc Scope) (Snapshot, error) {
	f.got = append(f.got, sc)
	return f.snap, f.err
}

func d(y, m, day int) Date   { return Date{y, m, day} }
func rub(units int64) *Money { return &Money{Minor: units * 100, Cur: CurRUB} }
func thb(units int64) *Money { return &Money{Minor: units * 100, Cur: CurTHB} }

func incomeFact(key string, at Date, m *Money) Event {
	return Event{Key: key, Kind: KindIncome, State: StateDone, Name: key, Amount: m, Date: &at, Source: SourceTransaction}
}

func rentRule() RuleWithExceptions {
	return RuleWithExceptions{
		ID: "rent", Name: "аренда", Kind: KindPayment,
		Rule: Rule{Anchor: d(2026, 2, 15), N: 1, Unit: UnitMonth, Amount: *thb(18000)},
	}
}

func baseSnap() Snapshot {
	return Snapshot{
		Incomes:    []Event{incomeFact("transaction:in", d(2026, 3, 5), rub(100000))},
		Payments:   []Event{{Key: "transaction:food", Kind: KindPayment, State: StateDone, Name: "еда", Amount: rub(5000), Date: dp(2026, 3, 7), Source: SourceTransaction}},
		Rules:      []RuleWithExceptions{rentRule()},
		Rate:       &Rate{RubPerThbMicro: 2_500_000},
		ReservePct: 10,
	}
}

func svc(f *fakeReader, today Date) *Service {
	return NewService(f, func() Date { return today }, Scope{ChatIDs: []int64{42}})
}

// Поступление 100 000, аренда 18 000 ฿ по 2,5 = 45 000, потрачено 5 000, резерв 10 %.
func TestService_View_Numbers(t *testing.T) {
	f := &fakeReader{snap: baseSnap()}
	res, err := svc(f, d(2026, 3, 10)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	v := res.View
	if v.State != StateCurrent || v.Window == nil || v.Window.Start != d(2026, 3, 5) {
		t.Fatalf("окно: %+v", v)
	}
	if v.Payments != 45000 || v.Spent != 5000 || v.Reserve != 10000 || v.CanSpend != 40000 {
		t.Errorf("платежи=%d потрачено=%d резерв=%d можно=%d; ждали 45000/5000/10000/40000", v.Payments, v.Spent, v.Reserve, v.CanSpend)
	}
	if res.Meta.Today != d(2026, 3, 10) || !res.Meta.HasRate {
		t.Errorf("meta: %+v", res.Meta)
	}
	if len(f.got) != 1 || len(f.got[0].ChatIDs) != 1 || f.got[0].ChatIDs[0] != 42 {
		t.Errorf("область не дошла до Reader: %+v", f.got)
	}
}

// «Сегодня» берётся из часов сервиса, а не из системных.
func TestService_View_TodayFromClock(t *testing.T) {
	f := &fakeReader{snap: baseSnap()}
	a, err := svc(f, d(2026, 3, 10)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc(f, d(2026, 3, 17)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if a.View.Window.DaysLeft == b.View.Window.DaysLeft {
		t.Errorf("дней до конца окна одинаково (%d) при разных «сегодня»", a.View.Window.DaysLeft)
	}
}

// Срок правила, по которому уже есть факт, считается один раз: фактом.
// Без вычитания плана Compute вернул бы ErrDuplicateTerm.
//
// Мутация: убрать dropCoveredTerms из View — тест краснеет.
func TestService_View_TermWithFactCountedOnce(t *testing.T) {
	snap := baseSnap()
	snap.Payments = append(snap.Payments, Event{
		Key: "transaction:rent", Kind: KindPayment, State: StateDone, Name: "аренда", Amount: rub(45000),
		Date: dp(2026, 3, 9), Source: SourceTransaction, RuleID: "rent", ScheduledFor: dp(2026, 3, 15),
	})
	res, err := svc(&fakeReader{snap: snap}, d(2026, 3, 10)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if res.View.Payments != 0 || res.View.Spent != 50000 {
		t.Errorf("платежи=%d потрачено=%d; ждали 0 и 50000 (аренда только фактом)", res.View.Payments, res.View.Spent)
	}
}

// Повторяющееся поступление без факта — ожидаемое; расчёт включает его в окно.
func TestService_View_RecurringIncomeExpected(t *testing.T) {
	snap := baseSnap()
	snap.Rules = append(snap.Rules, RuleWithExceptions{
		ID: "avans", Name: "аванс", Kind: KindIncome,
		Rule: Rule{Anchor: d(2026, 3, 12), N: 1, Unit: UnitMonth, Amount: *rub(30000)},
	})
	res, err := svc(&fakeReader{snap: snap}, d(2026, 3, 10)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.Income.Received != 100000 || res.View.Income.Expected != 30000 {
		t.Errorf("поступления %+v, ждали получено 100000 и ожидается 30000", res.View.Income)
	}
}

func TestService_View_NoIncomeIsStateNone(t *testing.T) {
	snap := baseSnap()
	snap.Incomes = nil
	res, err := svc(&fakeReader{snap: snap}, d(2026, 3, 10)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.State != StateNone || res.View.Window != nil {
		t.Errorf("без поступлений ждали состояние «нет окна»: %+v", res.View.State)
	}
}

func TestService_View_Errors(t *testing.T) {
	t.Run("чтение", func(t *testing.T) {
		boom := errors.New("boom")
		if _, err := svc(&fakeReader{err: boom}, d(2026, 3, 10)).View(context.Background(), CurRUB); !errors.Is(err, boom) {
			t.Errorf("ошибка Reader потеряна: %v", err)
		}
	})
	t.Run("нет курса при бате", func(t *testing.T) {
		snap := baseSnap()
		snap.Rate = nil
		if _, err := svc(&fakeReader{snap: snap}, d(2026, 3, 10)).View(context.Background(), CurRUB); !errors.Is(err, ErrNoRate) {
			t.Errorf("ждали ErrNoRate: %v", err)
		}
	})
	t.Run("битое сегодня", func(t *testing.T) {
		if _, err := svc(&fakeReader{snap: baseSnap()}, d(2026, 2, 30)).View(context.Background(), CurRUB); !errors.Is(err, ErrBadDate) {
			t.Errorf("ждали ErrBadDate: %v", err)
		}
	})
}

func TestCheckInvariant(t *testing.T) {
	per := int64(5)
	ok := View{State: StateCurrent, Window: &WindowInfo{}, Income: IncomeSum{Received: 100}, Payments: 30, Spent: 20, Reserve: 10, CanSpend: 40, PerDay: &per}
	if err := checkInvariant(ok); err != nil {
		t.Errorf("верный итог отвергнут: %v", err)
	}
	bad := ok
	bad.CanSpend = 41
	if err := checkInvariant(bad); !errors.Is(err, ErrInvariant) {
		t.Errorf("неверный итог принят: %v", err)
	}
}

// Срок правила уже наступил в этом окне, а факта нет (воркер не успел): платёж
// остаётся в расчёте, а не пропадает из-за того, что дата раньше «сегодня».
//
// Мутация: начинать разворачивание повторов с today — тест краснеет.
func TestService_View_PastTermInWindowWithoutFactStillCounts(t *testing.T) {
	snap := baseSnap()
	snap.Rules = []RuleWithExceptions{{
		ID: "rent", Name: "аренда", Kind: KindPayment,
		Rule: Rule{Anchor: d(2026, 2, 8), N: 1, Unit: UnitMonth, Amount: *thb(18000)}, // срок 08.03 < сегодня 10.03
	}}
	res, err := svc(&fakeReader{snap: snap}, d(2026, 3, 10)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.Payments != 45000 {
		t.Errorf("платежи %d, ждали 45000 (аренда 08.03 без факта)", res.View.Payments)
	}
}
