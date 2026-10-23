package budgetskill

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"simpleAI/internal/botlimits"
	"simpleAI/internal/budget"
)

type fakePlanLimits struct {
	snap  botlimits.Snapshot
	ok    bool
	err   error
	calls []string
}

func (f *fakePlanLimits) Snapshot(_ context.Context, _ int64, cur string) (botlimits.Snapshot, bool, error) {
	f.calls = append(f.calls, cur)
	return f.snap, f.ok, f.err
}

func planWarnSkill(st *fakeShareStore, l *fakePlanLimits, fromPlan bool) *BudgetSkill {
	return &BudgetSkill{buckets: defaultBuckets(), shareStore: st, limits: l, limitsFromPlan: fromPlan}
}

// Старый путь пробит (Еда: 6000 ฿ при лимите 5000 ฿) — план его не должен «видеть».
func overspentOldStore() *fakeShareStore {
	return &fakeShareStore{env: warnEnvelope(), shares: warnShares(), rates: warnRates(),
		spent: []budget.CategorySpentRow{{CategoryName: "Еда", Currency: "RUB", Amount: 12000}}}
}

func planSnapWith(ls ...botlimits.Limit) botlimits.Snapshot {
	return botlimits.Snapshot{Cur: "THB", Limits: ls}
}

// Предупреждение из плана: перерасход лимита категории, числа ядра.
//
// Мутации: не вызывать planOverspendWarning; печатать Left вместо Over; игнорировать флаг; проверять не ту категорию.
func TestPlanWarning_OverspentLimit(t *testing.T) {
	l := &fakePlanLimits{ok: true, snap: planSnapWith(botlimits.Limit{Category: "Еда", Amount: 5000, Spent: 4800, Planned: 700, Left: 0, Over: 500})}
	got := planWarnSkill(overspentOldStore(), l, true).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "  еда "}, budgetInput{})
	want := "Лимит «Еда» пробит на 500 ฿ (лимит 5 000 ฿, потрачено 4 800 ฿, запланировано 700 ฿)."
	if !strings.Contains(got, want) {
		t.Fatalf("предупреждение %q, ждали %q", got, want)
	}
	if len(l.calls) != 1 || l.calls[0] != "THB" {
		t.Fatalf("валюта запроса: %v", l.calls)
	}
	// в рублях по просьбе
	l2 := &fakePlanLimits{ok: true, snap: planSnapWith(botlimits.Limit{Category: "Еда", Amount: 5000, Spent: 6000, Over: 1000})}
	got = planWarnSkill(overspentOldStore(), l2, true).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Еда"}, budgetInput{DisplayCurrency: "RUB"})
	if !strings.Contains(got, "пробит на 1 000 ₽") || l2.calls[0] != "RUB" {
		t.Fatalf("RUB: %q calls=%v", got, l2.calls)
	}
}

// Без лимита у категории предупреждения нет — даже если по старой модели доля «прочее» пробита.
func TestPlanWarning_NoLimitOrWithinLimit_NoWarningAndNoOldPath(t *testing.T) {
	for name, snap := range map[string]botlimits.Snapshot{
		"нет лимита":     planSnapWith(botlimits.Limit{Category: "Такси", Amount: 1000, Over: 100}),
		"в пределах":     planSnapWith(botlimits.Limit{Category: "Еда", Amount: 5000, Spent: 3000, Left: 2000}),
		"лимитов вообще": planSnapWith(),
	} {
		l := &fakePlanLimits{ok: true, snap: snap}
		if got := planWarnSkill(overspentOldStore(), l, true).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Еда"}, budgetInput{}); got != "" {
			t.Errorf("%s: предупреждение %q, ждали пусто (старый путь не должен срабатывать)", name, got)
		}
	}
}

// Повторяющийся платёж лимит не ест — предупреждения нет.
func TestPlanWarning_RecurringIgnored(t *testing.T) {
	rid := uuid.New()
	l := &fakePlanLimits{ok: true, snap: planSnapWith(botlimits.Limit{Category: "Еда", Amount: 5000, Spent: 6000, Over: 1000})}
	if got := planWarnSkill(overspentOldStore(), l, true).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Еда", RecurringID: &rid}, budgetInput{}); got != "" {
		t.Fatalf("предупреждение для recurring: %q", got)
	}
}

// Флаг выключен, чат вне области плана или ошибка плана — прежнее предупреждение по долям конверта.
func TestPlanWarning_FallsBackToOldPath(t *testing.T) {
	cases := map[string]struct {
		l        *fakePlanLimits
		fromPlan bool
		wantCall bool
	}{
		"флаг выключен": {&fakePlanLimits{ok: true, snap: planSnapWith()}, false, false},
		"вне области":   {&fakePlanLimits{ok: false}, true, true},
		"ошибка плана":  {&fakePlanLimits{ok: true, err: errors.New("boom")}, true, true},
	}
	for name, c := range cases {
		got := planWarnSkill(overspentOldStore(), c.l, c.fromPlan).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Еда"}, budgetInput{})
		if !strings.Contains(got, "Конверт «Еда» пробит на 1 000 ฿") {
			t.Errorf("%s: ждали старое предупреждение, получили %q", name, got)
		}
		if (len(c.l.calls) > 0) != c.wantCall {
			t.Errorf("%s: вызовов плана %d", name, len(c.l.calls))
		}
	}
}

// Предупреждение относится к категории траты, а не к первому пробитому лимиту окна.
func TestPlanWarning_UsesTransactionCategory(t *testing.T) {
	l := &fakePlanLimits{ok: true, snap: planSnapWith(
		botlimits.Limit{Category: "Еда", Amount: 5000, Spent: 6000, Over: 1000},
		botlimits.Limit{Category: "Такси", Amount: 1000, Spent: 100, Left: 900})}
	if got := planWarnSkill(overspentOldStore(), l, true).shareOverspendWarning(warnCtx(), budget.Transaction{CategoryName: "Такси"}, budgetInput{}); got != "" {
		t.Fatalf("чужое предупреждение: %q", got)
	}
}
