package budget

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestGetPeriodSnapshot_InvalidRange — чистый юнит без БД: to раньше from →
// ошибка ДО запроса (pool не трогается).
func TestGetPeriodSnapshot_InvalidRange(t *testing.T) {
	s := &Store{} // pool не используется на этом пути
	from := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.GetPeriodSnapshot(context.Background(), 0, from, to, map[string]float64{"THB": 2.6}); err == nil {
		t.Fatal("ожидалась ошибка при to < from")
	}
}

// TestGetPeriodSnapshot_Integration проверяет границы периода и chat-scope на
// реальной реплике. Пропускается, если BOTCLIENT_DATABASE_URL не задан.
//
//	set -a; . ~/.simpleai-replica/botclient.env; set +a
//	go test ./internal/budget/ -run TestGetPeriodSnapshot_Integration -v
func TestGetPeriodSnapshot_Integration(t *testing.T) {
	url := os.Getenv("BOTCLIENT_DATABASE_URL")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL не задан — реплика не поднята")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	s := NewStore(pool)
	rates := map[string]float64{"RUB": 1.0, "THB": 2.6, "USD": 90, "EUR": 100}

	const chatID = 420229961 // владелец (есть recurring)
	day := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	next := day.AddDate(0, 0, 1)

	// Плотный день 2026-03-09: транзакции есть.
	onDay, err := s.GetPeriodSnapshot(ctx, chatID, day, day, rates)
	if err != nil {
		t.Fatalf("snapshot on-day: %v", err)
	}
	if onDay.TxCount == 0 {
		t.Fatalf("ожидались транзакции на 2026-03-09, TxCount=0")
	}

	// Граница: тот же день НЕ попадает в период [след.день, след.день].
	nextDay, err := s.GetPeriodSnapshot(ctx, chatID, next, next, rates)
	if err != nil {
		t.Fatalf("snapshot next-day: %v", err)
	}
	if nextDay.TxCount >= onDay.TxCount {
		t.Errorf("граница периода: TxCount след.дня (%d) не должен включать транзакции 03-09 (%d)", nextDay.TxCount, onDay.TxCount)
	}

	// chat-scope: recurring фильтруется по chatID. У владельца recurring есть,
	// у несуществующего chat — нет.
	wide := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	far := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	owner, err := s.GetPeriodSnapshot(ctx, chatID, wide, far, rates)
	if err != nil {
		t.Fatalf("snapshot owner: %v", err)
	}
	stranger, err := s.GetPeriodSnapshot(ctx, 999999999, wide, far, rates)
	if err != nil {
		t.Fatalf("snapshot stranger: %v", err)
	}
	if owner.UpcomingRecurring == 0 {
		t.Errorf("у владельца ожидались recurring-обязательства, получено 0")
	}
	if stranger.UpcomingRecurring != 0 {
		t.Errorf("chat-scope нарушен: у чужого chatID recurring=%.2f, ожидался 0", stranger.UpcomingRecurring)
	}
}
