package botlimits

import (
	"context"
	"testing"

	"simpleAI/internal/plan"
)

type fakeReader struct {
	snap plan.Snapshot
	got  []plan.Scope
}

func (f *fakeReader) Read(_ context.Context, sc plan.Scope) (plan.Snapshot, error) {
	f.got = append(f.got, sc)
	return f.snap, nil
}

func d(y, m, day int) plan.Date { return plan.Date{Year: y, Month: m, Day: day} }

// Окно 05.03–18.03, сегодня 10.03: доход 100 000 ₽, аренда 45 000, еда 5 000 (лимит 20 000 на «Еду»).
func fixture() plan.Snapshot {
	dt := d(2026, 3, 7)
	ahead := d(2026, 3, 12)
	in := d(2026, 3, 5)
	m := func(u int64) *plan.Money { return &plan.Money{Minor: u * 100, Cur: plan.CurRUB} }
	return plan.Snapshot{
		Incomes: []plan.Event{{Key: "transaction:in", Kind: plan.KindIncome, State: plan.StateDone, Name: "зп", Amount: m(100000), Date: &in, Source: plan.SourceTransaction}},
		Payments: []plan.Event{{Key: "transaction:food", Kind: plan.KindPayment, State: plan.StateDone, Name: "еда", Amount: m(5000), Date: &dt,
			Category: "Еда", Source: plan.SourceTransaction},
			{Key: "planned:p", Kind: plan.KindPayment, State: plan.StatePlanned, Name: "ящик", Amount: m(2000), Date: &ahead, Category: "Еда", Source: plan.SourcePlanned}},
		Rate:   &plan.Rate{RubPerThbMicro: 2_500_000},
		Limits: []plan.Limit{{ID: "L1", Category: "Еда", Amount: *m(20000), WindowStart: d(2026, 3, 5)}},
	}
}

func newPlan(f *fakeReader, chats ...int64) *Plan {
	svc := plan.NewService(f, nil, func() plan.Date { return d(2026, 3, 10) }, plan.Scope{ChatIDs: []int64{1}})
	return NewPlan(svc, ScopeOfChats(chats))
}

func TestSnapshot_MapsPlanViewNumbers(t *testing.T) {
	f := &fakeReader{snap: fixture()}
	snap, ok, err := newPlan(f, 42).Snapshot(context.Background(), 42, "rub")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	// Свободно = 100 000 − 5 000 потрачено − 2 000 плановый − 13 000 остаток лимита; на день = /9 (10..18 марта)
	if snap.Cur != "RUB" || snap.CanSpend != 80000 || snap.LimitHold != 13000 || snap.DaysLeft != 9 || snap.PerDay == nil || *snap.PerDay != 8888 {
		t.Fatalf("snap=%+v perDay=%v", snap, snap.PerDay)
	}
	if !snap.WindowStart.Equal(day(d(2026, 3, 5))) || !snap.WindowEnd.Equal(day(d(2026, 3, 18))) {
		t.Fatalf("окно %v–%v", snap.WindowStart, snap.WindowEnd)
	}
	l, found := snap.ForCategory("  еда ")
	if !found || l.Amount != 20000 || l.Spent != 5000 || l.Planned != 2000 || l.Left != 13000 || l.Over != 0 {
		t.Fatalf("лимит %+v found=%v", l, found)
	}
	if _, found := snap.ForCategory("Такси"); found {
		t.Error("нашёлся лимит несуществующей категории")
	}
}

// Область запроса доходит до читателя плана; чат вне области — старый путь, плана не трогаем.
func TestSnapshot_ScopePerRequest(t *testing.T) {
	f := &fakeReader{snap: fixture()}
	p := newPlan(f, 42, 43)
	if _, ok, err := p.Snapshot(context.Background(), 42, "RUB"); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(f.got) != 1 || len(f.got[0].ChatIDs) != 2 || f.got[0].ChatIDs[0] != 42 || f.got[0].ChatIDs[1] != 43 {
		t.Fatalf("область в читателе: %+v (ждали [42 43], а не область сервиса [1])", f.got)
	}
	if _, ok, err := p.Snapshot(context.Background(), 99, "RUB"); err != nil || ok {
		t.Fatalf("чат вне области: ok=%v err=%v", ok, err)
	}
	if len(f.got) != 1 {
		t.Fatalf("чужой чат дошёл до читателя плана: %+v", f.got)
	}
}

func TestSnapshot_NoWindowAndBadCurrency(t *testing.T) {
	f := &fakeReader{snap: plan.Snapshot{Rate: &plan.Rate{RubPerThbMicro: 2_500_000}}}
	if _, ok, err := newPlan(f, 42).Snapshot(context.Background(), 42, "RUB"); err != nil || ok {
		t.Fatalf("нет окна: ok=%v err=%v, ждали ok=false", ok, err)
	}
	if _, _, err := newPlan(&fakeReader{snap: fixture()}, 42).Snapshot(context.Background(), 42, "USD"); err == nil {
		t.Fatal("валюта USD принята")
	}
}
