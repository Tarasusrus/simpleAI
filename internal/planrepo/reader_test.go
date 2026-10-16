package planrepo

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// fakeSource запоминает аргументы вызовов: AC-8.4 проверяется по тому, что
// ушло в источник, а не по результату на пустой базе.
type fakeSource struct {
	txs      []budget.PlanTx
	rules    []budget.PlanRecurring
	planned  []budget.PlanPlanned
	reserve  int
	rates    map[string]float64
	manual   bool
	gotRules [][]int64
	gotPlan  [][]int64
	calls    int
}

func (f *fakeSource) PlanTransactions(context.Context) ([]budget.PlanTx, error) {
	f.calls++
	return f.txs, nil
}

func (f *fakeSource) PlanRecurringRules(_ context.Context, ids []int64) ([]budget.PlanRecurring, error) {
	f.calls++
	f.gotRules = append(f.gotRules, ids)
	return f.rules, nil
}

func (f *fakeSource) PlanPlannedItems(_ context.Context, ids []int64) ([]budget.PlanPlanned, error) {
	f.calls++
	f.gotPlan = append(f.gotPlan, ids)
	return f.planned, nil
}

func (f *fakeSource) PlanReservePct(context.Context) (int, error) { return f.reserve, nil }

func (f *fakeSource) GetExchangeRates(context.Context) (map[string]float64, error) {
	return f.rates, nil
}

func (f *fakeSource) GetRateSource(context.Context, string) (budget.RateSource, bool, error) {
	return budget.RateSource{Manual: f.manual}, true, nil
}

func newReader(f *fakeSource) *Reader {
	return NewReader(f, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestRead_ScopeReachesSource(t *testing.T) {
	f := &fakeSource{rates: map[string]float64{"THB": 2.5}}
	if _, err := newReader(f).Read(context.Background(), plan.Scope{ChatIDs: []int64{7, 9}}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := [][]int64{{7, 9}}
	if !reflect.DeepEqual(f.gotRules, want) || !reflect.DeepEqual(f.gotPlan, want) {
		t.Errorf("область не дошла до источника: правила %v, плановые %v, ждали %v", f.gotRules, f.gotPlan, want)
	}
}

func TestRead_EmptyScopeIsErrorAndReadsNothing(t *testing.T) {
	f := &fakeSource{}
	if _, err := newReader(f).Read(context.Background(), plan.Scope{}); err == nil {
		t.Fatal("пустая область принята")
	}
	if f.calls != 0 {
		t.Errorf("при пустой области источник вызван %d раз", f.calls)
	}
}

func TestRead_RateMicroAndManual(t *testing.T) {
	f := &fakeSource{rates: map[string]float64{"THB": 2.5018019}, manual: true}
	snap, err := newReader(f).Read(context.Background(), plan.Scope{ChatIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Rate == nil || snap.Rate.RubPerThbMicro != 2501802 || !snap.RateManual {
		t.Errorf("курс %+v ручной=%v, ждали 2501802 ручной", snap.Rate, snap.RateManual)
	}
	f.rates = map[string]float64{"USD": 80}
	snap, err = newReader(f).Read(context.Background(), plan.Scope{ChatIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Rate != nil {
		t.Errorf("без курса бата ждали nil, получили %+v", snap.Rate)
	}
}

func TestRead_TransactionsAndTerms(t *testing.T) {
	rule := uuid.New()
	sched := day(2026, 3, 5)
	f := &fakeSource{
		rates: map[string]float64{"THB": 2.5},
		txs: []budget.PlanTx{
			{ID: uuid.New(), Type: "income", AmountMinor: 4554200, Currency: "RUB", Description: "аванс", Date: day(2026, 3, 1)},
			{ID: uuid.New(), Type: "expense", AmountMinor: 1500, Currency: "THB", Category: "Еда", Date: day(2026, 3, 2)},
			// факт правила со старой строкой: scheduled_for NULL → срок = дата транзакции
			{ID: uuid.New(), Type: "expense", AmountMinor: 100, Currency: "RUB", Description: "аренда", Date: day(2026, 3, 10), RecurringID: &rule},
			// факт правила с явным сроком
			{ID: uuid.New(), Type: "expense", AmountMinor: 100, Currency: "RUB", Description: "аренда", Date: day(2026, 3, 6), RecurringID: &rule, ScheduledFor: &sched},
			// не бат и не рубль — в план не входит, но и молча не теряется
			{ID: uuid.New(), Type: "expense", AmountMinor: 100, Currency: "USD", Date: day(2026, 3, 3)},
		},
	}
	snap, err := newReader(f).Read(context.Background(), plan.Scope{ChatIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Incomes) != 1 || snap.Incomes[0].Amount.Minor != 4554200 || snap.Incomes[0].State != plan.StateDone {
		t.Errorf("поступления: %+v", snap.Incomes)
	}
	if len(snap.Payments) != 3 || snap.Skipped != 1 {
		t.Fatalf("платежей %d (ждали 3), пропущено %d (ждали 1)", len(snap.Payments), snap.Skipped)
	}
	legacy, explicit := snap.Payments[1], snap.Payments[2]
	if legacy.ScheduledFor == nil || *legacy.ScheduledFor != (plan.Date{Year: 2026, Month: 3, Day: 10}) {
		t.Errorf("старый факт: срок %v, ждали дату транзакции 10.03", legacy.ScheduledFor)
	}
	if explicit.ScheduledFor == nil || *explicit.ScheduledFor != (plan.Date{Year: 2026, Month: 3, Day: 5}) {
		t.Errorf("факт с явным сроком: %v, ждали 05.03", explicit.ScheduledFor)
	}
	for _, e := range snap.Payments {
		if err := e.Validate(); err != nil {
			t.Errorf("событие %s не проходит Validate: %v", e.Key, err)
		}
	}
}

func TestRead_PlannedAndRules(t *testing.T) {
	amt := int64(250000)
	due := day(2026, 3, 20)
	dom := 31
	until := day(2026, 12, 1)
	f := &fakeSource{
		rates:   map[string]float64{"THB": 2.5},
		reserve: 10,
		planned: []budget.PlanPlanned{
			{ID: uuid.New(), Direction: "expense", Description: "Права", Currency: "RUB"}, // без суммы и даты
			{ID: uuid.New(), Direction: "expense", Description: "телефон", Currency: "RUB", AmountMinor: &amt, DueDate: &due},
			{ID: uuid.New(), Direction: "income", Description: "премия", Currency: "RUB", AmountMinor: &amt, DueDate: &due},
			{ID: uuid.New(), Direction: "income", Description: "без даты", Currency: "RUB", AmountMinor: &amt}, // окно открыть нечем
		},
		rules: []budget.PlanRecurring{
			{ID: uuid.New(), Name: "аренда", Type: "expense", Currency: "THB", AmountMinor: 1800000, IntervalN: 1, IntervalUnit: "month",
				Anchor: day(2026, 2, 28), DayOfMonth: &dom, Until: &until, Skipped: []time.Time{day(2026, 5, 31)}},
			{ID: uuid.New(), Name: "неделя", Type: "expense", Currency: "RUB", AmountMinor: 100, IntervalN: 2, IntervalUnit: "week",
				Anchor: day(2026, 3, 1), DayOfMonth: &dom}, // день месяца у недельного правила не нужен
		},
	}
	snap, err := newReader(f).Read(context.Background(), plan.Scope{ChatIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if snap.ReservePct != 10 {
		t.Errorf("резерв %d", snap.ReservePct)
	}
	if len(snap.Payments) != 2 || snap.Payments[0].Amount != nil || snap.Payments[0].Date != nil {
		t.Errorf("платежи: %+v", snap.Payments)
	}
	if len(snap.Incomes) != 1 || snap.Skipped != 1 {
		t.Errorf("поступлений %d (ждали 1), пропущено %d (ждали 1)", len(snap.Incomes), snap.Skipped)
	}
	if len(snap.Rules) != 2 {
		t.Fatalf("правил %d", len(snap.Rules))
	}
	m := snap.Rules[0]
	if m.Rule.DayOfMonth == nil || *m.Rule.DayOfMonth != 31 || m.Rule.Until == nil || len(m.Exceptions) != 1 || m.Kind != plan.KindPayment {
		t.Errorf("месячное правило: %+v", m)
	}
	if w := snap.Rules[1]; w.Rule.DayOfMonth != nil || w.Rule.Unit != plan.UnitWeek || w.Rule.N != 2 {
		t.Errorf("недельное правило: %+v", w.Rule)
	}
	// Правила должны разворачиваться доменом без ошибок.
	if _, err := plan.Expand(snap.Rules, plan.Date{Year: 2026, Month: 3, Day: 1}, plan.Date{Year: 2026, Month: 6, Day: 30}); err != nil {
		t.Errorf("Expand: %v", err)
	}
}
