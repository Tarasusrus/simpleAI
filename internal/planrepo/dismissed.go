package planrepo

import (
	"context"
	"errors"
	"fmt"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// DismissedSource — то, что читателю нужно от budget.Store для скрытых категорий (ISP).
type DismissedSource interface {
	PlanDismissed(ctx context.Context, chatIDs []int64) ([]string, error)
}

// WithDismissed подключает чтение скрытых категорий (ADR-015 §3).
func (r *Reader) WithDismissed(src DismissedSource) *Reader {
	r.dism = src
	return r
}

func (r *Reader) readDismissed(ctx context.Context, sc plan.Scope) ([]string, error) {
	if r.dism == nil {
		return nil, nil
	}
	return r.dism.PlanDismissed(ctx, sc.ChatIDs)
}

// DismissSink — то, что писателю скрытия нужно от budget.Store.
type DismissSink interface {
	DismissPlanCategory(ctx context.Context, chatID int64, category string) error
	RestorePlanCategory(ctx context.Context, chatIDs []int64, category string) error
}

// CategoryHider реализует plan.CategoryHider поверх DismissSink.
type CategoryHider struct{ sink DismissSink }

func NewCategoryHider(sink DismissSink) *CategoryHider { return &CategoryHider{sink: sink} }

var _ plan.CategoryHider = (*CategoryHider)(nil)

func mapHideErr(err error) error {
	if errors.Is(err, budget.ErrPlanLimitCategory) {
		return plan.ErrLimitCategory
	}
	return err
}

// Dismiss пишет скрытие в чат владельца — первый в области.
func (h *CategoryHider) Dismiss(ctx context.Context, sc plan.Scope, category string) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	return mapHideErr(h.sink.DismissPlanCategory(ctx, sc.ChatIDs[0], category))
}

// Restore снимает скрытие во всей области.
func (h *CategoryHider) Restore(ctx context.Context, sc plan.Scope, category string) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	return mapHideErr(h.sink.RestorePlanCategory(ctx, sc.ChatIDs, category))
}
