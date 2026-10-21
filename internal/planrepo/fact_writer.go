package planrepo

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// FactSink — то, что писателю закрытых записей нужно от budget.Store (узкий
// интерфейс, ISP): плановому писателю эти методы не нужны, а ему не нужны его.
type FactSink interface {
	UpdateWebFact(ctx context.Context, id uuid.UUID, e budget.FactEdit) (bool, error)
	UnsettlePlanItem(ctx context.Context, chatIDs []int64, txID uuid.UUID) (bool, error)
}

// FactWriter реализует plan.FactWriter поверх FactSink.
type FactWriter struct{ sink FactSink }

func NewFactWriter(sink FactSink) *FactWriter { return &FactWriter{sink: sink} }

var _ plan.FactWriter = (*FactWriter)(nil)

// UpdateFact меняет факт экрана; чужой факт, факт правила или нет факта — ErrNotFound.
func (w *FactWriter) UpdateFact(ctx context.Context, id string, e plan.FactEdit) error {
	u, err := uuid.Parse(id)
	if err != nil {
		return plan.ErrNotFound
	}
	ok, err := w.sink.UpdateWebFact(ctx, u, budget.FactEdit{
		Description: e.Name,
		AmountMinor: e.Amount.Minor,
		Date:        *dbDate(&e.Date),
	})
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}

// UnsettlePlanned возвращает факт в план; область чатов проверяется по плановой
// записи, потому что у транзакций chat_id нет. Пустая область — ошибка: иначе
// проверка границы молча отвалилась бы.
func (w *FactWriter) UnsettlePlanned(ctx context.Context, sc plan.Scope, factID string) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	u, err := uuid.Parse(factID)
	if err != nil {
		return plan.ErrNotFound
	}
	ok, err := w.sink.UnsettlePlanItem(ctx, sc.ChatIDs, u)
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}
