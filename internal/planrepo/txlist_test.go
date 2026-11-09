package planrepo

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

type fakeTxSource struct {
	rows []budget.PlanTx
	got  []budget.PlanCategoryTxQuery
}

func (f *fakeTxSource) PlanCategoryTransactions(_ context.Context, q budget.PlanCategoryTxQuery) ([]budget.PlanTx, error) {
	f.got = append(f.got, q)
	return f.rows, nil
}

func TestTxLister_MapsQueryCursorAndRows(t *testing.T) {
	id := uuid.New()
	f := &fakeTxSource{rows: []budget.PlanTx{
		{ID: id, AmountMinor: 12300, Currency: "THB", Description: "", Category: "Такси", Date: day(2026, 3, 9)},
		{ID: uuid.New(), AmountMinor: 1, Currency: "USD", Category: "Такси", Date: day(2026, 3, 9)}, // не бат и не рубль — пропуск
	}}
	cur := uuid.New()
	items, err := NewTxLister(f).CategoryTransactions(context.Background(), plan.TxQuery{
		Category: "Такси", From: plan.Date{Year: 2026, Month: 3, Day: 5}, To: plan.Date{Year: 2026, Month: 3, Day: 18},
		After: &plan.TxCursor{Date: plan.Date{Year: 2026, Month: 3, Day: 9}, ID: cur.String()}, Limit: 21})
	if err != nil {
		t.Fatal(err)
	}
	q := f.got[0]
	if q.Category != "Такси" || q.Limit != 21 || !q.From.Equal(day(2026, 3, 5)) || !q.To.Equal(day(2026, 3, 18)) ||
		q.AfterDate == nil || !q.AfterDate.Equal(day(2026, 3, 9)) || q.AfterID == nil || *q.AfterID != cur {
		t.Fatalf("запрос %+v", q)
	}
	if len(items) != 1 || items[0].ID != id.String() || items[0].Name != "Такси" || items[0].Amount != (plan.Money{Minor: 12300, Cur: plan.CurTHB}) ||
		items[0].Date != (plan.Date{Year: 2026, Month: 3, Day: 9}) {
		t.Fatalf("items=%+v", items)
	}
	// первая страница — без курсора
	if _, err := NewTxLister(f).CategoryTransactions(context.Background(), plan.TxQuery{Category: "Такси", Limit: 21}); err != nil {
		t.Fatal(err)
	}
	if q := f.got[1]; q.AfterDate != nil || q.AfterID != nil {
		t.Fatalf("курсор на первой странице: %+v", q)
	}
	// id курсора — не uuid
	_, err = NewTxLister(f).CategoryTransactions(context.Background(), plan.TxQuery{Category: "Такси", After: &plan.TxCursor{ID: "не-uuid"}})
	if !errors.Is(err, plan.ErrBadCursor) {
		t.Fatalf("плохой id курсора: %v", err)
	}
}
