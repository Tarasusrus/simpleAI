package planrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// Sink — то, что писателю нужно от budget.Store.
type Sink interface {
	AddPlanItem(ctx context.Context, in budget.PlanItemInput) (uuid.UUID, error)
	DeletePlanItem(ctx context.Context, chatIDs []int64, id uuid.UUID) (bool, error)
	MovePlanItem(ctx context.Context, chatIDs []int64, id uuid.UUID, due, windowStart *time.Time) (bool, error)
	SettlePlanItem(ctx context.Context, chatIDs []int64, id uuid.UUID, actual *budget.PlanAmount, date time.Time) (bool, error)
	SkipRecurringTerm(ctx context.Context, chatIDs []int64, ruleID uuid.UUID, term time.Time) (bool, error)
	SetManualRate(ctx context.Context, currency string, rateToRUB float64) error
	ExpenseCategoryNames(ctx context.Context) ([]string, error)
	UpdatePlanItem(ctx context.Context, chatIDs []int64, id uuid.UUID, e budget.PlanItemEdit) (bool, error)
	UpdateRecurringRule(ctx context.Context, chatIDs []int64, id uuid.UUID, e budget.RecurringEdit) (bool, error)
}

// Writer реализует plan.Writer поверх Sink.
type Writer struct{ sink Sink }

func NewWriter(sink Sink) *Writer { return &Writer{sink: sink} }

var _ plan.Writer = (*Writer)(nil)

func dbDate(d *plan.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
	return &t
}

// AddPlanned пишет запись в чат владельца — первый в области. Запись без области
// запрещена: иначе она осела бы «ни у кого» и экран её не увидел.
func (w *Writer) AddPlanned(ctx context.Context, sc plan.Scope, n plan.NewItem) (string, error) {
	if len(sc.ChatIDs) == 0 {
		return "", fmt.Errorf("planrepo: пустая область чатов")
	}
	if err := n.Validate(); err != nil {
		return "", err
	}
	in := budget.PlanItemInput{
		ChatID:      sc.ChatIDs[0],
		Direction:   "expense",
		Description: n.Name,
		Currency:    string(plan.CurRUB),
		Category:    n.Category,
		DueDate:     dbDate(n.Date),
		WindowStart: dbDate(n.WindowStart),
	}
	if n.Kind == plan.KindIncome {
		in.Direction = "income"
	}
	if n.Amount != nil {
		minor := n.Amount.Minor
		in.AmountMinor, in.Currency = &minor, string(n.Amount.Cur)
	}
	id, err := w.sink.AddPlanItem(ctx, in)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// DeletePlanned удаляет запись из области; чужой или несуществующий id — ErrNotFound.
func (w *Writer) DeletePlanned(ctx context.Context, sc plan.Scope, id string) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return plan.ErrNotFound
	}
	ok, err := w.sink.DeletePlanItem(ctx, sc.ChatIDs, u)
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}

// MovePlanned переносит плановый платёж; чужой, закрытый или несуществующий — ErrNotFound.
func (w *Writer) MovePlanned(ctx context.Context, sc plan.Scope, id string, date, windowStart *plan.Date) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return plan.ErrNotFound
	}
	ok, err := w.sink.MovePlanItem(ctx, sc.ChatIDs, u, dbDate(date), dbDate(windowStart))
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}

// SettlePlanned закрывает плановую запись фактом; чужой, закрытый или несуществующий
// id — ErrNotFound. Сумма нужна, если записи её не задавали.
func (w *Writer) SettlePlanned(ctx context.Context, sc plan.Scope, id string, actual *plan.Money, date plan.Date) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return plan.ErrNotFound
	}
	var amt *budget.PlanAmount
	if actual != nil {
		amt = &budget.PlanAmount{Minor: actual.Minor, Currency: string(actual.Cur)}
	}
	ok, err := w.sink.SettlePlanItem(ctx, sc.ChatIDs, u, amt, *dbDate(&date))
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}

// SkipTerm пропускает срок правила; чужое или несуществующее правило — ErrNotFound.
func (w *Writer) SkipTerm(ctx context.Context, sc plan.Scope, ruleID string, term plan.Date) error {
	if len(sc.ChatIDs) == 0 {
		return fmt.Errorf("planrepo: пустая область чатов")
	}
	u, err := uuid.Parse(ruleID)
	if err != nil {
		return plan.ErrNotFound
	}
	ok, err := w.sink.SkipRecurringTerm(ctx, sc.ChatIDs, u, *dbDate(&term))
	if err != nil {
		return err
	}
	if !ok {
		return plan.ErrNotFound
	}
	return nil
}

// SetRate пишет ручной курс бата тем же способом, что команда бота «курс»: один
// источник курса для бота и экрана.
func (w *Writer) SetRate(ctx context.Context, rubPerThbMicro int64) error {
	if rubPerThbMicro <= 0 {
		return plan.ErrBadRate
	}
	return w.sink.SetManualRate(ctx, string(plan.CurTHB), float64(rubPerThbMicro)/1_000_000)
}

func (w *Writer) ExpenseCategories(ctx context.Context) ([]string, error) {
	return w.sink.ExpenseCategoryNames(ctx)
}
