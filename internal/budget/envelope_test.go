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
