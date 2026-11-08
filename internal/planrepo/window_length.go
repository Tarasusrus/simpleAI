package planrepo

import (
	"context"
	"fmt"
	"time"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// WindowLengthSource — то, что читателю нужно от budget.Store для правок окна (ISP, ADR-020).
type WindowLengthSource interface {
	PlanWindowLengths(ctx context.Context, chatIDs []int64) ([]budget.PlanWindowLength, error)
}

// WithWindowLengths подключает чтение правок длины окна.
func (r *Reader) WithWindowLengths(src WindowLengthSource) *Reader {
	r.lengths = src
	return r
}

func (r *Reader) readWindowLengths(ctx context.Context, sc plan.Scope) ([]plan.WindowLength, error) {
	if r.lengths == nil {
		return nil, nil
	}
	rows, err := r.lengths.PlanWindowLengths(ctx, sc.ChatIDs)
	if err != nil {
		return nil, err
	}
	out := make([]plan.WindowLength, 0, len(rows))
	for _, x := range rows {
		out = append(out, plan.WindowLength{Start: date(x.WindowStart), Days: x.Days})
	}
	return out, nil
}

// WindowLengthSink — то, что писателю правок окна нужно от budget.Store.
type WindowLengthSink interface {
	SetPlanWindowLength(ctx context.Context, chatID int64, windowStart time.Time, days int) error
	ResetPlanWindowLength(ctx context.Context, chatIDs []int64, windowStart time.Time) error
}

// WindowLengthWriter реализует plan.WindowLengthWriter поверх WindowLengthSink.
type WindowLengthWriter struct{ sink WindowLengthSink }

func NewWindowLengthWriter(sink WindowLengthSink) *WindowLengthWriter {
	return &WindowLengthWriter{sink: sink}
}

var _ plan.WindowLengthWriter = (*WindowLengthWriter)(nil)

// SetWindowLength пишет правку в чат владельца — первый в области (как лимиты и скрытие категорий).
func (w *WindowLengthWriter) SetWindowLength(ctx context.Context, sc plan.Scope, l plan.WindowLength) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	return w.sink.SetPlanWindowLength(ctx, sc.ChatIDs[0], *dbDate(&l.Start), l.Days)
}

// ResetWindowLength снимает правку во всей области.
func (w *WindowLengthWriter) ResetWindowLength(ctx context.Context, sc plan.Scope, start plan.Date) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	return w.sink.ResetPlanWindowLength(ctx, sc.ChatIDs, *dbDate(&start))
}
