package botlimits

import (
	"context"
	"errors"
	"fmt"
	"math"
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

// Plan реализует Reader и Writer поверх plan.Service.
type Plan struct {
	svc   PlanService
	scope ScopeFor
}

func NewPlan(svc PlanService, scope ScopeFor) *Plan { return &Plan{svc: svc, scope: scope} }

var (
	_ Reader = (*Plan)(nil)
	_ Writer = (*Plan)(nil)
)

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

// mapWriteErr переводит отказ плана про категорию в ErrUnknownCategory; прочее отдаёт как есть.
func mapWriteErr(err error) error {
	if errors.Is(err, plan.ErrLimitCategory) || errors.Is(err, plan.ErrUnknownCategory) {
		return fmt.Errorf("%w: %w", ErrUnknownCategory, err)
	}
	return err
}

// windowOf читает окно плана для поиска лимита. Валюта показа здесь не важна (нужны id и имена), поэтому
// сначала рубли, а при ошибке перевода (нет курса) — баты: хотя бы одна из валют считается без курса.
func windowOf(ctx context.Context, svc *plan.Service) (plan.Result, error) {
	res, err := svc.View(ctx, plan.CurRUB)
	if err != nil {
		var err2 error
		if res, err2 = svc.View(ctx, plan.CurTHB); err2 != nil {
			return plan.Result{}, fmt.Errorf("botlimits: окно плана: %w", err)
		}
	}
	return res, nil
}

// minor — сумма в минимальных единицах валюты (копейки, сатанги) с округлением до ближайшей.
func minor(amount float64) int64 { return int64(math.Round(amount * 100)) }

// SetLimit ставит лимит категории на окно плана через plan.Service. Существующий лимит меняется (его признак
// «повторяется» сохраняется), новый создаётся повторяющимся: прежний set_share_limit тоже жил между приходами,
// пока его не снимут. Валюта по умолчанию — RUB, как у прежнего set_share_limit. Окна с приходом нет — ok=false.
func (p *Plan) SetLimit(ctx context.Context, chatID int64, category string, amount float64, currency string) (bool, error) {
	sc, in := p.scope(chatID)
	if !in {
		return false, nil
	}
	if strings.TrimSpace(currency) == "" {
		currency = string(plan.CurRUB)
	}
	c, err := parseCur(currency)
	if err != nil {
		return true, err
	}
	money := plan.Money{Minor: minor(amount), Cur: c}
	svc := p.svc.ForScope(sc)
	res, err := windowOf(ctx, svc)
	if err != nil {
		return true, err
	}
	if res.View.Window == nil || res.View.State == plan.StateNone || res.View.State == plan.StateEnded {
		return false, nil
	}
	for _, l := range res.View.Limits {
		if plan.CategoryKey(l.Category) == plan.CategoryKey(category) {
			return true, mapWriteErr(svc.EditLimit(ctx, l.ID, plan.LimitEdit{Amount: money, Repeat: l.Repeat}))
		}
	}
	_, err = svc.AddLimit(ctx, plan.AddLimitInput{Category: category, Amount: money, Repeat: true})
	return true, mapWriteErr(err)
}

// ClearLimit снимает лимит категории, действующий на окно плана.
func (p *Plan) ClearLimit(ctx context.Context, chatID int64, category string) (ok, found bool, err error) {
	sc, in := p.scope(chatID)
	if !in {
		return false, false, nil
	}
	svc := p.svc.ForScope(sc)
	res, err := windowOf(ctx, svc)
	if err != nil {
		return true, false, err
	}
	if res.View.Window == nil || res.View.State == plan.StateNone || res.View.State == plan.StateEnded {
		return false, false, nil
	}
	for _, l := range res.View.Limits {
		if plan.CategoryKey(l.Category) == plan.CategoryKey(category) {
			return true, true, mapWriteErr(svc.DeleteLimit(ctx, l.ID))
		}
	}
	return true, false, nil
}
