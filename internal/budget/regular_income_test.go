package budget

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestGetRegularMonthlyIncomeAvg_Integration (ADR-007 §8, баг №3): чистый якорь
// дохода исключает разовые поступления и потому МЕНЬШЕ загрязнённого среднего.
// Пропускается без BOTCLIENT_DATABASE_URL.
func TestGetRegularMonthlyIncomeAvg_Integration(t *testing.T) {
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

	polluted, err := s.GetMonthlyIncomeAvg(ctx, rates)
	if err != nil {
		t.Fatalf("polluted avg: %v", err)
	}
	regular, err := s.GetRegularMonthlyIncomeAvg(ctx, rates)
	if err != nil {
		t.Fatalf("regular avg: %v", err)
	}
	if regular <= 0 {
		t.Fatalf("чистый якорь должен быть > 0, got %.2f", regular)
	}
	// Разовые поступления (Март «Прочее» 126000, Апрель без категории 152126)
	// раздувают загрязнённое среднее → чистое строго меньше.
	if !(regular < polluted) {
		t.Errorf("чистый якорь (%.0f THB) должен быть меньше загрязнённого (%.0f THB)", regular, polluted)
	}
}
