package budget

import (
	"context"
	"errors"
	"fmt"
)

// «Не планировать» (ADR-015 §3): скрытые из блока «Не запланировано» категории чата.

// DismissPlanCategory скрывает категорию расходов (по имени или алиасу, без учёта регистра) для
// чата. Повтор безопасен. Нет такой категории — ErrPlanLimitCategory.
func (s *Store) DismissPlanCategory(ctx context.Context, chatID int64, category string) error {
	catID, err := s.expenseCategoryID(ctx, category)
	if errors.Is(err, ErrUnknownCategory) || (err == nil && !catID.Valid) {
		return ErrPlanLimitCategory
	}
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO plan_category_dismissed (chat_id, category_id) VALUES ($1, $2)
		ON CONFLICT (chat_id, category_id) DO NOTHING`, chatID, catID.UUID); err != nil {
		return fmt.Errorf("dismiss plan category: %w", err)
	}
	return nil
}

// RestorePlanCategory возвращает скрытую категорию в блок. Если она не была скрыта — не ошибка:
// ErrPlanLimitCategory только когда категории нет вовсе.
func (s *Store) RestorePlanCategory(ctx context.Context, chatIDs []int64, category string) error {
	catID, err := s.expenseCategoryID(ctx, category)
	if errors.Is(err, ErrUnknownCategory) || (err == nil && !catID.Valid) {
		return ErrPlanLimitCategory
	}
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM plan_category_dismissed WHERE chat_id = ANY($1) AND category_id = $2`, chatIDs, catID.UUID); err != nil {
		return fmt.Errorf("restore plan category: %w", err)
	}
	return nil
}

// PlanDismissed возвращает имена скрытых категорий указанных чатов.
func (s *Store) PlanDismissed(ctx context.Context, chatIDs []int64) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT c.name FROM plan_category_dismissed d
		JOIN budget_category c ON c.id = d.category_id
		WHERE d.chat_id = ANY($1) ORDER BY c.name`, chatIDs)
	if err != nil {
		return nil, fmt.Errorf("plan dismissed: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("plan dismissed scan: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
