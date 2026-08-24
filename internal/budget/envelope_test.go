package budget

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestEnvelope_Integration проверяет CreateEnvelope/GetActiveEnvelope, правило
// «один активный на chat» и chat-scope изоляцию (ADR-007 T4). Требует WRITE —
// BOTCLIENT_DATABASE_URL_RW (owner-роль на реплике); иначе пропуск.
func TestEnvelope_Integration(t *testing.T) {
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

	const chatA, chatB = int64(-70001), int64(-70002)
	cleanup := func() {
		if _, err := pool.Exec(ctx, "DELETE FROM budget_envelope WHERE chat_id IN ($1,$2)", chatA, chatB); err != nil {
			t.Logf("cleanup budget_envelope: %v", err)
		}
	}
	cleanup()
	defer cleanup()

	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 14)
	if _, err := s.CreateEnvelope(ctx, chatA, 127000, "RUB", from, to); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Правило одного активного: второй create деактивирует первый.
	if _, err := s.CreateEnvelope(ctx, chatA, 50000, "RUB", from, to); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	env, ok, err := s.GetActiveEnvelope(ctx, chatA)
	if err != nil || !ok {
		t.Fatalf("get active: ok=%v err=%v", ok, err)
	}
	if env.IncomeAmount != 50000 {
		t.Errorf("активным должен быть последний конверт (50000), got %.0f", env.IncomeAmount)
	}

	// chat-scope: у chatB конверта нет.
	if _, ok, err := s.GetActiveEnvelope(ctx, chatB); err != nil || ok {
		t.Errorf("chat-scope нарушен: у chatB найден конверт (ok=%v)", ok)
	}
}

// Правило «один активный конверт на чат» держится СХЕМОЙ (частичный уникальный
// индекс idx_budget_envelope_active_chat, 00016), а не только кодом
// insertEnvelopeTx. Теста на это не было, и наблюдение «два active=true» на
// реплике (simpleAI-faeq.10, баг 2) читалось как поломка закрытия — хотя на
// деле конверты принадлежали РАЗНЫМ чатам, что по ADR-004 норма. Проверяем
// прямой вставкой мимо стора: второй активный в ОДНОМ чате обязан быть отвергнут.
func TestEnvelope_SecondActiveRejectedBySchema(t *testing.T) {
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

	const chatID = int64(-70003)
	cleanup := func() {
		if _, err := pool.Exec(ctx, "DELETE FROM budget_envelope WHERE chat_id = $1", chatID); err != nil {
			t.Logf("cleanup: %v", err)
		}
	}
	cleanup()
	defer cleanup()

	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 13)
	insert := func() error {
		_, err := pool.Exec(ctx, `
			INSERT INTO budget_envelope (chat_id, income_amount, income_currency, period_start, period_end)
			VALUES ($1, 1000, 'RUB', $2, $3)
		`, chatID, from, to)
		return err
	}
	if err := insert(); err != nil {
		t.Fatalf("первая вставка: %v", err)
	}
	if err := insert(); err == nil {
		t.Fatal("второй active=true конверт в том же чате вставился — схема не держит правило «один активный»")
	}
}
