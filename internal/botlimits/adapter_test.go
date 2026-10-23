package botlimits

import (
	"context"
	"errors"
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

type fakeWriter struct {
	added   []plan.NewLimit
	edited  map[string]plan.LimitEdit
	deleted []string
	scopes  []plan.Scope
	err     error
}

func (f *fakeWriter) AddLimit(_ context.Context, sc plan.Scope, n plan.NewLimit) (string, error) {
	f.added = append(f.added, n)
	f.scopes = append(f.scopes, sc)
	return "new-1", f.err
}

func (f *fakeWriter) UpdateLimit(_ context.Context, sc plan.Scope, id string, e plan.LimitEdit) error {
	if f.edited == nil {
		f.edited = map[string]plan.LimitEdit{}
	}
	f.edited[id] = e
	f.scopes = append(f.scopes, sc)
	return f.err
}

func (f *fakeWriter) DeleteLimit(_ context.Context, sc plan.Scope, id string) error {
	f.deleted = append(f.deleted, id)
	f.scopes = append(f.scopes, sc)
	return f.err
}

func newWritable(f *fakeReader, w *fakeWriter, chats ...int64) *Plan {
	svc := plan.NewService(f, nil, func() plan.Date { return d(2026, 3, 10) }, plan.Scope{ChatIDs: []int64{1}}).WithLimitWriter(w)
	return NewPlan(svc, ScopeOfChats(chats))
}

// SetLimit: существующий лимит меняется (повтор сохраняется), новый создаётся повторяющимся на окно плана;
// сумма в минимальных единицах, валюта по умолчанию RUB, область запроса доходит до писателя.
//
// Мутации: создавать разовый вместо повторяющегося; терять Repeat при правке; брать область сервиса; минор без округления.
func TestSetLimit_EditsExistingAddsRepeatingNew(t *testing.T) {
	snap := fixture()
	snap.Limits[0].Repeat = true // «Еда» повторяется, «Кино» — разовый: признак сохраняется у каждого
	snap.Limits = append(snap.Limits, plan.Limit{ID: "L2", Category: "Кино", Amount: plan.Money{Minor: 100000, Cur: plan.CurRUB}, WindowStart: d(2026, 3, 5)})
	f := &fakeReader{snap: snap}
	w := &fakeWriter{}
	p := newWritable(f, w, 42, 43)
	ctx := context.Background()
	ok, err := p.SetLimit(ctx, 42, " еда ", 15000.005, "")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	e, edited := w.edited["L1"]
	if !edited || e.Amount != (plan.Money{Minor: 1500001, Cur: plan.CurRUB}) || !e.Repeat {
		t.Fatalf("правка существующего: %+v (ждали 15000,01 ₽ и Repeat=true как у лимита)", w.edited)
	}
	if ok, err := p.SetLimit(ctx, 42, "кино", 500, "RUB"); err != nil || !ok || w.edited["L2"].Repeat {
		t.Fatalf("разовый лимит стал повторяющимся: ok=%v err=%v %+v", ok, err, w.edited)
	}
	ok, err = p.SetLimit(ctx, 42, "Такси", 3000, "thb")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := plan.NewLimit{Category: "Такси", Amount: plan.Money{Minor: 300000, Cur: plan.CurTHB}, Repeat: true, WindowStart: d(2026, 3, 5)}
	if len(w.added) != 1 || w.added[0] != want {
		t.Fatalf("новый лимит %+v, ждали %+v", w.added, want)
	}
	if len(w.scopes) != 3 || len(w.scopes[2].ChatIDs) != 2 || w.scopes[2].ChatIDs[0] != 42 {
		t.Fatalf("область записи: %+v", w.scopes)
	}
}

func TestSetLimit_OutOfScopeNoWindowBadCurrency(t *testing.T) {
	w := &fakeWriter{}
	ctx := context.Background()
	if ok, err := newWritable(&fakeReader{snap: fixture()}, w, 42).SetLimit(ctx, 99, "Еда", 1, "RUB"); ok || err != nil {
		t.Fatalf("чат вне области: ok=%v err=%v", ok, err)
	}
	noWin := &fakeReader{snap: plan.Snapshot{Rate: &plan.Rate{RubPerThbMicro: 2_500_000}}}
	if ok, err := newWritable(noWin, w, 42).SetLimit(ctx, 42, "Еда", 1, "RUB"); ok || err != nil {
		t.Fatalf("нет окна: ok=%v err=%v, ждали старый путь", ok, err)
	}
	if _, err := newWritable(&fakeReader{snap: fixture()}, w, 42).SetLimit(ctx, 42, "Еда", 1, "USD"); err == nil {
		t.Fatal("валюта USD принята")
	}
	if len(w.added)+len(w.edited) != 0 {
		t.Fatalf("писали при отказе: %+v %+v", w.added, w.edited)
	}
}

func TestClearLimit_DeletesFoundOnly(t *testing.T) {
	w := &fakeWriter{}
	p := newWritable(&fakeReader{snap: fixture()}, w, 42)
	ctx := context.Background()
	ok, found, err := p.ClearLimit(ctx, 42, " ЕДА")
	if err != nil || !ok || !found || len(w.deleted) != 1 || w.deleted[0] != "L1" {
		t.Fatalf("ok=%v found=%v err=%v deleted=%v", ok, found, err, w.deleted)
	}
	ok, found, err = p.ClearLimit(ctx, 42, "Такси")
	if err != nil || !ok || found || len(w.deleted) != 1 {
		t.Fatalf("нет лимита: ok=%v found=%v err=%v deleted=%v", ok, found, err, w.deleted)
	}
	if ok, _, err := p.ClearLimit(ctx, 99, "Еда"); ok || err != nil {
		t.Fatalf("чат вне области обслужен планом: ok=%v err=%v", ok, err)
	}
}

// Отказ плана про категорию доходит до скилла как ErrUnknownCategory (скилл не импортирует plan); прочее — как есть.
func TestWriter_MapsCategoryErrors(t *testing.T) {
	ctx := context.Background()
	w := &fakeWriter{err: plan.ErrLimitCategory}
	p := newWritable(&fakeReader{snap: fixture()}, w, 42)
	if _, err := p.SetLimit(ctx, 42, "Такси", 1, "RUB"); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("Add: %v", err)
	}
	w.err = plan.ErrUnknownCategory
	if _, err := p.SetLimit(ctx, 42, "Еда", 1, "RUB"); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("Edit: %v", err)
	}
	if _, _, err := p.ClearLimit(ctx, 42, "Еда"); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("Delete: %v", err)
	}
	w.err = errors.New("boom")
	if _, err := p.SetLimit(ctx, 42, "Еда", 1, "RUB"); err == nil || errors.Is(err, ErrUnknownCategory) {
		t.Errorf("прочая ошибка: %v", err)
	}
}
