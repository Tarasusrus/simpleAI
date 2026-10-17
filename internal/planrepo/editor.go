package planrepo

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

func direction(k plan.Kind) string {
	if k == plan.KindIncome {
		return "income"
	}
	return "expense"
}

// UpdatePlanned меняет плановую запись из области; чужой, закрытый, другого вида
// или несуществующий id — ErrNotFound. Запись проверяется ещё раз: адаптер не
// пишет того, что домен не принял бы.
func (w *Writer) UpdatePlanned(ctx context.Context, sc plan.Scope, id string, e plan.PlannedEdit) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	n := e.Item
	if err := n.Validate(); err != nil {
		return err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return plan.ErrNotFound
	}
	in := budget.PlanItemEdit{
		Direction:   direction(n.Kind),
		Description: n.Name,
		Currency:    string(plan.CurRUB),
		Category:    n.Category,
		SetPlace:    e.SetPlace,
		DueDate:     dbDate(n.Date),
		WindowStart: dbDate(n.WindowStart),
	}
	if n.Amount != nil {
		minor := n.Amount.Minor
		in.AmountMinor, in.Currency = &minor, string(n.Amount.Cur)
	}
	ok, err := w.sink.UpdatePlanItem(ctx, sc.ChatIDs, u, in)
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}

// UpdateRule меняет правило повтора из области; чужое, выключенное, другого вида или
// несуществующее — ErrNotFound.
func (w *Writer) UpdateRule(ctx context.Context, sc plan.Scope, ruleID string, e plan.RuleEdit) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	if e.Amount.Minor <= 0 {
		return plan.ErrAmountNonPositive
	}
	if !e.Amount.Cur.Supported() {
		return plan.ErrUnsupportedCurrency
	}
	u, err := uuid.Parse(ruleID)
	if err != nil {
		return plan.ErrNotFound
	}
	ok, err := w.sink.UpdateRecurringRule(ctx, sc.ChatIDs, u, budget.RecurringEdit{
		Type: direction(e.Kind), Name: e.Name, AmountMinor: e.Amount.Minor, Currency: string(e.Amount.Cur), Category: e.Category,
	})
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}
