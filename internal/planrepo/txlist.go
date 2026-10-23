package planrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// TxSource — то, что списку трат нужно от budget.Store.
type TxSource interface {
	PlanCategoryTransactions(ctx context.Context, q budget.PlanCategoryTxQuery) ([]budget.PlanTx, error)
}

// TxLister реализует plan.TxLister поверх TxSource.
type TxLister struct{ src TxSource }

func NewTxLister(src TxSource) *TxLister { return &TxLister{src: src} }

var _ plan.TxLister = (*TxLister)(nil)

func dbDay(d plan.Date) time.Time {
	return time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
}

// CategoryTransactions переводит запрос в запрос базы; траты в чужой валюте (не бат и не рубль)
// пропускаются, как в расчёте Spent.
func (l *TxLister) CategoryTransactions(ctx context.Context, q plan.TxQuery) ([]plan.TxItem, error) {
	dq := budget.PlanCategoryTxQuery{Category: q.Category, From: dbDay(q.From), To: dbDay(q.To), Limit: q.Limit}
	if q.After != nil {
		id, err := uuid.Parse(q.After.ID)
		if err != nil {
			return nil, plan.ErrBadCursor
		}
		d := dbDay(q.After.Date)
		dq.AfterDate, dq.AfterID = &d, &id
	}
	rows, err := l.src.PlanCategoryTransactions(ctx, dq)
	if err != nil {
		return nil, fmt.Errorf("planrepo: траты категории: %w", err)
	}
	out := make([]plan.TxItem, 0, len(rows))
	for _, t := range rows {
		m, ok := money(t.AmountMinor, t.Currency)
		if !ok {
			continue
		}
		out = append(out, plan.TxItem{ID: t.ID.String(), Date: date(t.Date), Name: firstNonEmpty(t.Description, t.Category, plan.UnnamedName), Amount: m})
	}
	return out, nil
}
