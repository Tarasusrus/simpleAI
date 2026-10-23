package botlimits

import (
	"context"
	"testing"

	"simpleAI/internal/plan"
)

// Паритет бот ↔ веб (ADR-016 §7): бот получает от ядра ровно те числа, что экран. Сравниваются числа до
// форматирования: то, что адаптер передаёт форматировщику бота (Snapshot), против plan.View, по которому веб
// строит DTO. Общая фикстура: несколько категорий, факт повторяющегося правила, плановый платёж без суммы,
// RUB и THB, граница окна, перерасход.
//
// Мутации: подставить в адаптер собственный расчёт остатка (например, Amount−Spent); убрать фильтр RuleID в
// plan.limits — красный и этот тест, и тест plan.
func parityPlan() (*plan.Service, *fakeReader) {
	m := func(minor int64, cur plan.Currency) *plan.Money { return &plan.Money{Minor: minor, Cur: cur} }
	in := d(2026, 3, 5)
	fact := func(key, cat string, amt *plan.Money, day plan.Date, rule string) plan.Event {
		dd := day
		e := plan.Event{Key: key, Kind: plan.KindPayment, State: plan.StateDone, Name: key, Amount: amt, Date: &dd, Category: cat, Source: plan.SourceTransaction}
		if rule != "" {
			e.RuleID, e.ScheduledFor, e.Source = rule, &dd, plan.SourceRecurring
		}
		return e
	}
	planned := func(key, cat string, amt *plan.Money, day plan.Date) plan.Event {
		dd := day
		return plan.Event{Key: key, Kind: plan.KindPayment, State: plan.StatePlanned, Name: key, Amount: amt, Date: &dd, Category: cat, Source: plan.SourcePlanned}
	}
	snap := plan.Snapshot{
		Incomes: []plan.Event{{Key: "transaction:in", Kind: plan.KindIncome, State: plan.StateDone, Name: "зп", Amount: m(10000000, plan.CurRUB), Date: &in, Source: plan.SourceTransaction}},
		Payments: []plan.Event{
			fact("transaction:f1", "Еда", m(500000, plan.CurRUB), d(2026, 3, 7), ""),
			fact("transaction:f2", "Еда", m(120000, plan.CurTHB), d(2026, 3, 18), ""),  // последний день окна, ฿1200 = 3000 ₽
			fact("transaction:f3", "Еда", m(300000, plan.CurRUB), d(2026, 3, 8), "r1"), // повторяющееся правило лимит не ест
			fact("transaction:f4", "Такси", m(130000, plan.CurRUB), d(2026, 3, 9), ""), // перерасход лимита 1000
			fact("transaction:f5", "Еда", m(70000, plan.CurRUB), d(2026, 3, 4), ""),    // день до окна — в окно не входит
			planned("planned:p1", "Еда", m(200000, plan.CurRUB), d(2026, 3, 12)),       // плановый ест лимит
			planned("planned:p2", "Еда", nil, d(2026, 3, 13)),                          // без суммы — в расчёт не входит
		},
		Rate:       &plan.Rate{RubPerThbMicro: 2_500_000},
		ReservePct: 10,
		Limits: []plan.Limit{
			{ID: "L1", Category: "Еда", Amount: *m(2000000, plan.CurRUB), Repeat: true, WindowStart: d(2026, 3, 5)},
			{ID: "L2", Category: "Такси", Amount: *m(100000, plan.CurRUB), WindowStart: d(2026, 3, 5)},
		},
	}
	f := &fakeReader{snap: snap}
	return plan.NewService(f, nil, func() plan.Date { return d(2026, 3, 10) }, plan.Scope{ChatIDs: []int64{42}}), f
}

func TestParity_BotSnapshotEqualsWebView(t *testing.T) {
	svc, _ := parityPlan()
	bot := NewPlan(svc, ScopeOfChats([]int64{42}))
	for _, cur := range []string{"RUB", "THB"} {
		web, err := svc.View(context.Background(), plan.Currency(cur))
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := bot.Snapshot(context.Background(), 42, cur)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", cur, ok, err)
		}
		v := web.View
		if got.CanSpend != v.CanSpend || got.LimitHold != v.LimitHold || got.DaysLeft != v.Window.DaysLeft {
			t.Fatalf("%s: бот CanSpend/LimitHold/DaysLeft = %d/%d/%d, экран %d/%d/%d", cur, got.CanSpend, got.LimitHold, got.DaysLeft, v.CanSpend, v.LimitHold, v.Window.DaysLeft)
		}
		if (got.PerDay == nil) != (v.PerDay == nil) || (got.PerDay != nil && *got.PerDay != *v.PerDay) {
			t.Fatalf("%s: PerDay бот %v, экран %v", cur, got.PerDay, v.PerDay)
		}
		if len(got.Limits) != len(v.Limits) || len(got.Limits) != 2 {
			t.Fatalf("%s: лимитов бот %d, экран %d (ждали 2)", cur, len(got.Limits), len(v.Limits))
		}
		for i, l := range v.Limits {
			g := got.Limits[i]
			if g.Category != l.Category || g.Repeat != l.Repeat || g.Amount != l.Amount || g.Spent != l.Spent || g.Planned != l.Planned || g.Left != l.Left || g.Over != l.Over {
				t.Fatalf("%s: лимит %q: бот %+v, экран %+v", cur, l.Category, g, l)
			}
		}
	}
	// опорные числа фикстуры: повторяющийся факт лимит не ест, граница окна входит, перерасход отдельно (в ₽)
	rub, _, err := bot.Snapshot(context.Background(), 42, "RUB")
	if err != nil {
		t.Fatal(err)
	}
	eda, _ := rub.ForCategory("Еда")
	taxi, _ := rub.ForCategory("Такси")
	if eda.Spent != 8000 || eda.Planned != 2000 || eda.Left != 10000 || eda.Over != 0 {
		t.Errorf("Еда: %+v, ждали Spent 8000 (5000 + 3000 ₽, без правила и без дня до окна), Planned 2000, Left 10000", eda)
	}
	if taxi.Spent != 1300 || taxi.Left != 0 || taxi.Over != 300 {
		t.Errorf("Такси: %+v, ждали Spent 1300, Left 0, Over 300", taxi)
	}
}
