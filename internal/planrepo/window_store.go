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

// WindowSource — то, что читателю нужно от budget.Store для окон (ISP, ADR-021).
type WindowSource interface {
	PlanWindows(ctx context.Context, chatIDs []int64) ([]budget.PlanWindow, error)
}

// WithWindows подключает чтение сохранённых окон.
func (r *Reader) WithWindows(src WindowSource) *Reader {
	r.windows = src
	return r
}

func (r *Reader) readWindows(ctx context.Context, sc plan.Scope) ([]plan.StoredWindow, error) {
	if r.windows == nil {
		return nil, nil
	}
	rows, err := r.windows.PlanWindows(ctx, sc.ChatIDs)
	if err != nil {
		return nil, err
	}
	out := make([]plan.StoredWindow, 0, len(rows))
	for _, x := range rows {
		out = append(out, plan.StoredWindow{ID: x.ID.String(), Start: date(x.Start), End: date(x.End), Deleted: x.Deleted})
	}
	return out, nil
}

// WindowSink — то, что хранилищу окон нужно от budget.Store.
type WindowSink interface {
	CreatePlanWindow(ctx context.Context, chatID int64, start, end time.Time) (uuid.UUID, error)
	UpdatePlanWindow(ctx context.Context, chatIDs []int64, id uuid.UUID, start, end time.Time) error
	DeletePlanWindow(ctx context.Context, chatIDs []int64, id uuid.UUID) error
}

// WindowStore реализует plan.WindowStore поверх WindowSink.
type WindowStore struct{ sink WindowSink }

func NewWindowStore(sink WindowSink) *WindowStore { return &WindowStore{sink: sink} }

var _ plan.WindowStore = (*WindowStore)(nil)

var errEmptyScope = errors.New("planrepo: пустая область чатов")

// CreateWindow пишет окно в чат владельца — первый в области (как лимиты и скрытие категорий).
func (w *WindowStore) CreateWindow(ctx context.Context, sc plan.Scope, start, end plan.Date) (string, error) {
	if len(sc.ChatIDs) == 0 {
		return "", errEmptyScope
	}
	id, err := w.sink.CreatePlanWindow(ctx, sc.ChatIDs[0], *dbDate(&start), *dbDate(&end))
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// UpdateWindow меняет даты окна в области.
func (w *WindowStore) UpdateWindow(ctx context.Context, sc plan.Scope, id string, start, end plan.Date) error {
	u, err := windowID(sc, id)
	if err != nil {
		return err
	}
	return mapWindowErr(w.sink.UpdatePlanWindow(ctx, sc.ChatIDs, u, *dbDate(&start), *dbDate(&end)))
}

// DeleteWindow помечает окно удалённым.
func (w *WindowStore) DeleteWindow(ctx context.Context, sc plan.Scope, id string) error {
	u, err := windowID(sc, id)
	if err != nil {
		return err
	}
	return mapWindowErr(w.sink.DeletePlanWindow(ctx, sc.ChatIDs, u))
}

func windowID(sc plan.Scope, id string) (uuid.UUID, error) {
	if len(sc.ChatIDs) == 0 {
		return uuid.Nil, errEmptyScope
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: окно %q", plan.ErrNotFound, id)
	}
	return u, nil
}

func mapWindowErr(err error) error {
	if errors.Is(err, budget.ErrPlanWindowNotFound) {
		return fmt.Errorf("%w: %w", plan.ErrNotFound, err)
	}
	return err
}
