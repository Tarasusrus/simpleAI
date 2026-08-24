package budget

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSpentByCategoryExcludingRecurring_Integration — инвариант ADR-008 §5 на
// реальном SQL: транзакция с recurring_id НЕ попадает в факт доли, обычная —
// попадает. Требует write-доступ (BOTCLIENT_DATABASE_URL_RW), иначе пропуск.
//
// Тест непустой по построению: обе транзакции лежат в одном периоде и в одной
// категории, отличаются ТОЛЬКО recurring_id. Убрать фильтр из запроса — и сумма
// станет 3300 вместо 1000; тест краснеет.
func TestSpentByCategoryExcludingRecurring_Integration(t *testing.T) {
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — write-доступ к реплике недоступен")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	s := NewStore(pool)

	// Окно в далёком прошлом — чтобы не пересечься с данными дампа.
	from := time.Date(2001, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2001, 3, 28, 0, 0, 0, 0, time.UTC)
	const chatID = int64(-70021)

	var catID uuid.UUID
	var catName string
	if err := pool.QueryRow(ctx,
		`SELECT id, name FROM budget_category WHERE type = 'expense' ORDER BY sort_order LIMIT 1`).
		Scan(&catID, &catName); err != nil {
		t.Fatalf("нет ни одной расходной категории: %v", err)
	}

	recID := uuid.New()
	plainTx, recurringTx := uuid.New(), uuid.New()
	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM budget_transaction WHERE id = ANY($1)`,
			[]uuid.UUID{plainTx, recurringTx}); err != nil {
			t.Logf("cleanup tx: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM budget_recurring WHERE id = $1`, recID); err != nil {
			t.Logf("cleanup recurring: %v", err)
		}
	}
	cleanup()
	defer cleanup()

	if _, err := pool.Exec(ctx, `
		INSERT INTO budget_recurring (id, chat_id, name, type, amount, category_id, currency, recurrence_type, next_date)
		VALUES ($1, $2, 'тест-подписка', 'expense', 2300, $3, 'THB', 'monthly', $4)
	`, recID, chatID, catID, from); err != nil {
		t.Fatalf("insert recurring: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO budget_transaction (id, recurring_id, type, amount, currency, category_id, description, transaction_date)
		VALUES ($1, NULL, 'expense', 1000, 'THB', $2, 'тест: обычная трата', $3),
		       ($4, $5,   'expense', 2300, 'THB', $2, 'тест: платёж по подписке', $3)
	`, plainTx, catID, from.AddDate(0, 0, 3), recurringTx, recID); err != nil {
		t.Fatalf("insert transactions: %v", err)
	}

	rows, err := s.SpentByCategoryExcludingRecurring(ctx, from, to)
	if err != nil {
		t.Fatalf("SpentByCategoryExcludingRecurring: %v", err)
	}
	var total float64
	for _, r := range rows {
		if r.CategoryID != nil && *r.CategoryID == catID {
			total += r.Amount
		}
	}
	if total != 1000 {
		t.Fatalf("факт по категории %q = %.2f, ожидалось 1000: recurring-платёж 2300 не должен прожигать долю (ADR-008 §5)",
			catName, total)
	}
}
