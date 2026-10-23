package botlimits

import (
	"context"
	"fmt"
	"strings"
	"time"

	"simpleAI/internal/plan"
)

// PlanService — то, что адаптеру нужно от plan.Service. ForScope возвращает сервис для области запроса.
type PlanService interface {
	ForScope(sc plan.Scope) *plan.Service
}

// ScopeFor выбирает область плана для чата бота. ok=false — чат вне области плана: старый путь.
type ScopeFor func(chatID int64) (plan.Scope, bool)

// ScopeOfChats — область как у веба (PLAN_CHAT_IDS): чат бота обслуживается планом, если входит в список
// (ADR-016, решение владельца 4). План читает транзакции без chat_id, поэтому область — это чаты,
// чьи плановые записи, правила и лимиты видны.
func ScopeOfChats(chats []int64) ScopeFor {
	in := map[int64]bool{}
	for _, c := range chats {
		in[c] = true
	}
	scope := plan.Scope{ChatIDs: append([]int64(nil), chats...)}
	return func(chatID int64) (plan.Scope, bool) { return scope, in[chatID] }
}

// Plan реализует Reader поверх plan.Service.
type Plan struct {
	svc   PlanService
	scope ScopeFor
}

func NewPlan(svc PlanService, scope ScopeFor) *Plan { return &Plan{svc: svc, scope: scope} }

var _ Reader = (*Plan)(nil)

func parseCur(cur string) (plan.Currency, error) {
	c := plan.Currency(strings.ToUpper(strings.TrimSpace(cur)))
	if !c.Supported() {
		return "", plan.ErrUnsupportedCurrency
	}
	return c, nil
}

func day(d plan.Date) time.Time {
	return time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
}

// Snapshot читает окно плана для чата. Окна с приходом нет (View.State none/ended) — ok=false.
func (p *Plan) Snapshot(ctx context.Context, chatID int64, cur string) (Snapshot, bool, error) {
	sc, in := p.scope(chatID)
	if !in {
		return Snapshot{}, false, nil
	}
	c, err := parseCur(cur)
	if err != nil {
		return Snapshot{}, false, err
	}
	res, err := p.svc.ForScope(sc).View(ctx, c)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("botlimits: окно плана: %w", err)
	}
	v := res.View
	if v.Window == nil || v.State == plan.StateNone || v.State == plan.StateEnded {
		return Snapshot{}, false, nil
	}
	s := Snapshot{
		Cur: string(c), WindowStart: day(v.Window.Start), WindowEnd: day(v.Window.End), DaysLeft: v.Window.DaysLeft,
		CanSpend: v.CanSpend, PerDay: v.PerDay, LimitHold: v.LimitHold,
	}
	for _, l := range v.Limits {
		s.Limits = append(s.Limits, Limit{Category: l.Category, Repeat: l.Repeat, Amount: l.Amount, Spent: l.Spent, Planned: l.Planned, Left: l.Left, Over: l.Over})
	}
	return s, true, nil
}
