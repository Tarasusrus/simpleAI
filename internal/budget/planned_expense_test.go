package budget

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPlannedExpensesTHB_Integration проверяет сумму и chat-scope изоляцию
// плановых трат на реплике (сид: 6 разовых для chat 420229961 = 71200₽).
// Пропускается без BOTCLIENT_DATABASE_URL.
func TestPlannedExpensesTHB_Integration(t *testing.T) {
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
	rates := map[string]float64{"RUB": 1.0, "THB": 2.6}

	// Владелец: плановые есть (71200₽ / 2.6 в THB).
	sumTHB, cnt, err := s.PlannedExpensesTHB(ctx, 420229961, rates)
	if err != nil {
		t.Fatalf("owner planned: %v", err)
	}
	if cnt == 0 || sumTHB <= 0 {
		t.Fatalf("у владельца ожидались плановые траты, got sum=%.2f cnt=%d", sumTHB, cnt)
	}

	// chat-scope: у чужого chatID плановых нет.
	strangerTHB, strangerCnt, err := s.PlannedExpensesTHB(ctx, 999999999, rates)
	if err != nil {
		t.Fatalf("stranger planned: %v", err)
	}
	if strangerCnt != 0 || strangerTHB != 0 {
		t.Errorf("chat-scope нарушен: у чужого chatID plannedTHB=%.2f cnt=%d", strangerTHB, strangerCnt)
	}
}
