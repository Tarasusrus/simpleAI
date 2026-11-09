package planrepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// LimitSource — то, что читателю нужно от budget.Store для лимитов (ISP).
type LimitSource interface {
	PlanLimits(ctx context.Context, chatIDs []int64) ([]budget.PlanLimit, error)
}

// WithLimits подключает чтение лимитов категорий (ADR-011) и возвращает читателя.
// Без него Snapshot.Limits пуст: лимиты — отдельная сущность, а не часть Source.
func (r *Reader) WithLimits(src LimitSource) *Reader {
	r.limits = src
	return r
}

func (r *Reader) readLimits(ctx context.Context, sc plan.Scope) ([]plan.Limit, error) {
	if r.limits == nil {
		return nil, nil
	}
	rows, err := r.limits.PlanLimits(ctx, sc.ChatIDs)
	if err != nil {
		return nil, err
	}
	out := make([]plan.Limit, 0, len(rows))
	for _, x := range rows {
		m, ok := money(x.AmountMinor, x.Currency)
		if !ok {
			r.skip("лимит категории", x.ID.String(), x.Currency)
			continue
		}
		out = append(out, plan.Limit{
			ID: x.ID.String(), Category: x.Category, Amount: m, Repeat: x.Repeat, WindowStart: date(x.WindowStart),
		})
	}
	return out, nil
}

// LimitSink — то, что писателю лимитов нужно от budget.Store.
type LimitSink interface {
	AddPlanLimit(ctx context.Context, in budget.PlanLimitInput) (uuid.UUID, error)
	UpdatePlanLimit(ctx context.Context, chatIDs []int64, id uuid.UUID, amountMinor int64, currency string, repeat bool, windowStart *time.Time) (bool, error)
	DeletePlanLimit(ctx context.Context, chatIDs []int64, id uuid.UUID) (bool, error)
}

// LimitWriter реализует plan.LimitWriter поверх LimitSink.
type LimitWriter struct{ sink LimitSink }

func NewLimitWriter(sink LimitSink) *LimitWriter { return &LimitWriter{sink: sink} }

var _ plan.LimitWriter = (*LimitWriter)(nil)

func mapLimitErr(err error) error {
	switch {
	case errors.Is(err, budget.ErrPlanLimitDuplicate):
		return plan.ErrLimitExists
	case errors.Is(err, budget.ErrPlanLimitCategory):
		return plan.ErrLimitCategory
	}
	return err
}

// AddLimit пишет лимит в чат владельца — первый в области.
func (w *LimitWriter) AddLimit(ctx context.Context, sc plan.Scope, n plan.NewLimit) (string, error) {
	if len(sc.ChatIDs) == 0 {
		return "", fmt.Errorf("planrepo: пустая область чатов")
	}
	if err := n.Validate(); err != nil {
		return "", err
	}
	start := time.Date(n.WindowStart.Year, time.Month(n.WindowStart.Month), n.WindowStart.Day, 0, 0, 0, 0, time.UTC)
	id, err := w.sink.AddPlanLimit(ctx, budget.PlanLimitInput{
		ChatID: sc.ChatIDs[0], Category: n.Category, AmountMinor: n.Amount.Minor,
		Currency: string(n.Amount.Cur), Repeat: n.Repeat, WindowStart: start,
	})
	if err != nil {
		return "", mapLimitErr(err)
	}
	return id.String(), nil
}

// UpdateLimit меняет лимит из области; чужой или несуществующий — ErrNotFound.
func (w *LimitWriter) UpdateLimit(ctx context.Context, sc plan.Scope, id string, e plan.LimitEdit) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	if err := e.Validate(); err != nil {
		return err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return plan.ErrNotFound
	}
	ok, err := w.sink.UpdatePlanLimit(ctx, sc.ChatIDs, u, e.Amount.Minor, string(e.Amount.Cur), e.Repeat, dbDate(e.WindowStart))
	if err != nil {
		return mapLimitErr(err)
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}

// DeleteLimit удаляет лимит из области; чужой или несуществующий — ErrNotFound.
func (w *LimitWriter) DeleteLimit(ctx context.Context, sc plan.Scope, id string) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return plan.ErrNotFound
	}
	ok, err := w.sink.DeletePlanLimit(ctx, sc.ChatIDs, u)
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}
