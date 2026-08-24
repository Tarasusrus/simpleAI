package budget

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Факт трат для остатка долей конверта (ADR-008 §8). Отдельный файл от
// store.go: у этого запроса своя семантика — он отдаёт СЫРЫЕ строки (категория
// + валюта + сумма), не сворачивая их в THB и не фильтруя по виду категории.
// Классификация и конвертация живут в чистой функции computeShareRemaining,
// иначе инвариант «трата в „Переводы“ не трогает ни одну долю» проверялся бы
// только интеграционным тестом с БД.

// CategorySpentRow — одна строка факта: сколько потрачено по категории в одной
// валюте за период. CategoryID может быть nil (budget_transaction.category_id
// nullable, ADR-008 §6), поэтому имя категории обязательно.
type CategorySpentRow struct {
	CategoryID   *uuid.UUID
	CategoryName string
	Currency     string
	Amount       float64
}

// SpentByCategoryExcludingRecurring — факт расходов за период БЕЗ транзакций,
// порождённых регулярными платежами (`recurring_id IS NOT NULL`).
//
// Исключение recurring — не оптимизация запроса, а инвариант недвойного учёта
// (ADR-007 §4, ADR-008 §5): такие траты уже вычтены как обязательства на этапе
// computeSafeToSpend, из которого и получена сумма к раскладке. Учесть их ещё
// и в факте доли значит списать одни и те же деньги дважды. Механизм тот же,
// что у дохода в GetRegularMonthlyIncomeAvg — фильтр прямо в SQL-агрегате.
//
// Оба ключа категории (id и имя) отдаются наверх, потому что матчинг траты к
// доле двухступенчатый — ResolveShare.
func (s *Store) SpentByCategoryExcludingRecurring(ctx context.Context, from, to time.Time) ([]CategorySpentRow, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("SpentByCategoryExcludingRecurring: to (%s) раньше from (%s)",
			to.Format("2006-01-02"), from.Format("2006-01-02"))
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.category_id,
		       COALESCE(c.name, '') AS category_name,
		       t.currency,
		       SUM(t.amount)::float8 AS total
		FROM budget_transaction t
		LEFT JOIN budget_category c ON c.id = t.category_id
		WHERE t.type = 'expense'
		  AND t.recurring_id IS NULL
		  AND t.transaction_date >= $1::date
		  AND t.transaction_date <= $2::date
		GROUP BY t.category_id, category_name, t.currency
	`, from, to)
	if err != nil {
		return nil, fmt.Errorf("SpentByCategoryExcludingRecurring: %w", err)
	}
	defer rows.Close()

	var out []CategorySpentRow
	for rows.Next() {
		var r CategorySpentRow
		if err := rows.Scan(&r.CategoryID, &r.CategoryName, &r.Currency, &r.Amount); err != nil {
			return nil, fmt.Errorf("SpentByCategoryExcludingRecurring scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
