package budget

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Правка записей плана окна с экрана (issue #81). Каждая правка — один UPDATE, то
// есть одна транзакция БД; область чатов, «не закрыта» и направление стоят в WHERE,
// поэтому чужую, закрытую или подменённую запись правка не заденет.
//
// Имя категории переводится в category_id до SQL тем же модулем категорий, что и в
// AddPlanItem (имя или алиас, ADR-012 §1); SQL получает готовый UUID.

// keepCategorySQL — новая category_id записи ($7 — UUID из expenseCategoryID, NULL —
// без категории). Поступлению категория не меняется.
func keepCategorySQL(table, dirCol string) string {
	return `CASE
		    WHEN ` + table + `.` + dirCol + ` = 'income' THEN ` + table + `.category_id
		    ELSE $7::uuid
		END`
}

// editCategoryID — category_id для правки: у поступления имя не разрешается (категория
// не меняется), у расхода непустое неизвестное имя — ErrUnknownCategory.
func (s *Store) editCategoryID(ctx context.Context, direction, name string) (uuid.NullUUID, error) {
	if direction == "income" {
		return uuid.NullUUID{}, nil
	}
	return s.expenseCategoryID(ctx, name)
}

// PlanItemEdit — новые значения плановой записи. SetPlace=false — дата и окно
// остаются как в базе: правка названия не должна откатывать перенос, сделанный
// между чтением формы и сохранением.
type PlanItemEdit struct {
	Direction   string // "expense" | "income": должен совпасть с записью
	Description string
	AmountMinor *int64 // nil — сумма неизвестна (только у расхода)
	Currency    string
	Category    string // имя категории расходов; пусто — без категории; у поступления не меняется
	SetPlace    bool
	DueDate     *time.Time
	WindowStart *time.Time
	// OpensWindow — вид прихода (ADR-019): nil — не менять. У расхода не используется.
	OpensWindow *bool
}

// UpdatePlanItem меняет незакрытую плановую запись из перечисленных чатов. false —
// записи нет в этих чатах, она закрыта или у неё другое направление.
func (s *Store) UpdatePlanItem(ctx context.Context, chatIDs []int64, id uuid.UUID, e PlanItemEdit) (bool, error) {
	if e.SetPlace && e.DueDate != nil && e.WindowStart != nil {
		return false, fmt.Errorf("update plan item: дата и окно вместе")
	}
	catID, err := s.editCategoryID(ctx, e.Direction, e.Category)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE budget_planned_expense SET
		    description  = $4,
		    amount       = CASE WHEN $5::bigint IS NULL THEN NULL ELSE $5::bigint::numeric / 100 END,
		    currency     = $6,
		    category_id  = `+keepCategorySQL("budget_planned_expense", "direction")+`,
		    due_date     = CASE WHEN $8::boolean THEN $9::date ELSE due_date END,
		    window_start = CASE WHEN $8::boolean THEN $10::date ELSE window_start END,
		    opens_window = COALESCE($11::boolean, opens_window)
		WHERE id = $1 AND chat_id = ANY($2) AND NOT settled AND direction = $3`,
		id, chatIDs, e.Direction, e.Description, e.AmountMinor, e.Currency, catID, e.SetPlace, e.DueDate, e.WindowStart, e.OpensWindow)
	if err != nil {
		return false, fmt.Errorf("update plan item: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RecurringEdit — новые название, сумма, валюта и категория правила повтора.
// Расписания здесь нет намеренно: его старые колонки (recurrence_type, day_of_month,
// next_date) читают бот и воркер, новые (anchor_date, interval_*) — план, и правка
// только одной группы развела бы их.
type RecurringEdit struct {
	Type        string // "expense" | "income": должен совпасть с правилом
	Name        string
	AmountMinor int64
	Currency    string
	Category    string // только у расхода; у поступления категория не меняется
}

// UpdateRecurringRule меняет включённое правило из перечисленных чатов. false —
// правила нет в этих чатах, оно выключено или у него другой тип.
func (s *Store) UpdateRecurringRule(ctx context.Context, chatIDs []int64, id uuid.UUID, e RecurringEdit) (bool, error) {
	catID, err := s.editCategoryID(ctx, e.Type, e.Category)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE budget_recurring SET
		    name        = $4,
		    amount      = $5::bigint::numeric / 100,
		    currency    = $6,
		    category_id = `+keepCategorySQL("budget_recurring", "type")+`,
		    updated_at  = NOW()
		WHERE id = $1 AND chat_id = ANY($2) AND enabled AND type = $3`,
		id, chatIDs, e.Type, e.Name, e.AmountMinor, e.Currency, catID)
	if err != nil {
		return false, fmt.Errorf("update recurring rule: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
