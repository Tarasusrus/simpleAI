package budget

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func closeTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — write-доступ к реплике недоступен")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func closeTestShares() []EnvelopeShare {
	return []EnvelopeShare{
		{Name: "Еда", Kind: ShareKindSpend, Allocated: 10000, Source: ShareSourceAuto, Position: 0,
			Categories: []EnvelopeShareCategory{{CategoryName: "еда"}}},
		{Name: "Накопления", Kind: ShareKindSave, Allocated: 5000, Source: ShareSourceAuto, Position: 1},
	}
}

// TestEnvelopeClose_SwitchDayCountedOnce — гейт ADR-008 §10.
//
// Трата дня переключения обязана попасть РОВНО в один конверт. Все даты в схеме
// DATE, а фильтры границ включительные, поэтому проверка идёт не по полю
// period_end как таковому, а по факту: сумма за период закрытого конверта плюс
// сумма за период нового должны дать одну трату, а не две.
//
// Мутация: вернуть `period_end := now` (убрать «− 1») в insertEnvelopeTx — трата
// попадёт в оба периода, sum станет 2000 вместо 1000, тест краснеет.
func TestEnvelopeClose_SwitchDayCountedOnce(t *testing.T) {
	pool, ctx := closeTestPool(t)
	s := NewStore(pool)

	const chatID = int64(-70051)
	// Окно в прошлом — чтобы не пересечься с данными дампа.
	switchDay := time.Date(2002, 5, 20, 0, 0, 0, 0, time.UTC)
	oldFrom := switchDay.AddDate(0, 0, -10)

	var catID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM budget_category WHERE type = 'expense' AND lower(name) = 'еда' LIMIT 1`).Scan(&catID); err != nil {
		if err := pool.QueryRow(ctx,
			`SELECT id FROM budget_category WHERE type = 'expense' ORDER BY sort_order LIMIT 1`).Scan(&catID); err != nil {
			t.Fatalf("нет ни одной расходной категории: %v", err)
		}
	}

	txID := uuid.New()
	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM budget_envelope WHERE chat_id = $1`, chatID); err != nil {
			t.Logf("cleanup envelope: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM budget_transaction WHERE id = $1`, txID); err != nil {
			t.Logf("cleanup tx: %v", err)
		}
	}
	cleanup()
	defer cleanup()

	oldID, err := s.CreateEnvelopeWithShares(ctx, chatID, 100000, "RUB",
		oldFrom, switchDay.AddDate(0, 0, 4), closeTestShares(), oldFrom)
	if err != nil {
		t.Fatalf("create old envelope: %v", err)
	}

	// Трата ровно в день переключения.
	if _, err := pool.Exec(ctx, `
		INSERT INTO budget_transaction (id, type, amount, currency, category_id, description, transaction_date)
		VALUES ($1, 'expense', 1000, 'THB', $2, 'тест: трата дня переключения', $3)
	`, txID, catID, switchDay); err != nil {
		t.Fatalf("insert tx: %v", err)
	}

	newFrom := switchDay
	if _, err := s.CreateEnvelopeWithShares(ctx, chatID, 120000, "RUB",
		newFrom, switchDay.AddDate(0, 0, 14), closeTestShares(), switchDay); err != nil {
		t.Fatalf("create new envelope: %v", err)
	}

	var oldEnd time.Time
	var oldActive bool
	if err := pool.QueryRow(ctx,
		`SELECT period_end, active FROM budget_envelope WHERE id = $1`, oldID).Scan(&oldEnd, &oldActive); err != nil {
		t.Fatalf("read old envelope: %v", err)
	}
	if oldActive {
		t.Errorf("прошлый конверт остался активным")
	}
	want := switchDay.AddDate(0, 0, -1)
	if !oldEnd.Equal(want) {
		t.Errorf("period_end закрытого конверта = %s, ожидалось %s (now − 1 день, ADR-008 §10)",
			oldEnd.Format("2006-01-02"), want.Format("2006-01-02"))
	}

	sumFor := func(from, to time.Time) float64 {
		if to.Before(from) {
			return 0
		}
		rows, err := s.SpentByCategoryExcludingRecurring(ctx, from, to)
		if err != nil {
			t.Fatalf("spent %s..%s: %v", from.Format("2006-01-02"), to.Format("2006-01-02"), err)
		}
		var total float64
		for _, r := range rows {
			if r.CategoryID != nil && *r.CategoryID == catID {
				total += r.Amount
			}
		}
		return total
	}

	inOld := sumFor(oldFrom, oldEnd)
	inNew := sumFor(newFrom, switchDay.AddDate(0, 0, 14))
	if inOld+inNew != 1000 {
		t.Fatalf("трата дня переключения учтена %.0f раз(а): старый конверт %.0f + новый %.0f, ожидалось ровно 1000 суммарно",
			(inOld+inNew)/1000, inOld, inNew)
	}
	if inNew != 1000 {
		t.Errorf("трата дня переключения должна достаться НОВОМУ конверту, got %.0f", inNew)
	}
}

// Конверт, заведённый и закрытый в один день: period_end := now − 1 ушёл бы
// раньше period_start и дал период отрицательной длины, который
// GetPeriodSnapshot не принимает. Обрезка до period_start (ADR-008 §10).
func TestEnvelopeClose_SameDayClampedToStart(t *testing.T) {
	pool, ctx := closeTestPool(t)
	s := NewStore(pool)

	const chatID = int64(-70052)
	day := time.Date(2002, 6, 10, 0, 0, 0, 0, time.UTC)
	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM budget_envelope WHERE chat_id = $1`, chatID); err != nil {
			t.Logf("cleanup envelope: %v", err)
		}
	}
	cleanup()
	defer cleanup()

	oldID, err := s.CreateEnvelopeWithShares(ctx, chatID, 100000, "RUB",
		day, day.AddDate(0, 0, 14), closeTestShares(), day)
	if err != nil {
		t.Fatalf("create old envelope: %v", err)
	}
	if _, err := s.CreateEnvelopeWithShares(ctx, chatID, 120000, "RUB",
		day, day.AddDate(0, 0, 14), closeTestShares(), day); err != nil {
		t.Fatalf("create new envelope: %v", err)
	}

	var start, end time.Time
	if err := pool.QueryRow(ctx,
		`SELECT period_start, period_end FROM budget_envelope WHERE id = $1`, oldID).Scan(&start, &end); err != nil {
		t.Fatalf("read old envelope: %v", err)
	}
	if end.Before(start) {
		t.Fatalf("период отрицательной длины: %s..%s", start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
	if !end.Equal(start) {
		t.Errorf("period_end = %s, ожидалась обрезка до period_start %s",
			end.Format("2006-01-02"), start.Format("2006-01-02"))
	}
}

// Уже истёкший конверт закрытием НЕ удлиняется: его period_end в прошлом, и
// присвоение now − 1 задним числом втянуло бы в него чужие траты.
func TestEnvelopeClose_ExpiredNotExtended(t *testing.T) {
	pool, ctx := closeTestPool(t)
	s := NewStore(pool)

	const chatID = int64(-70053)
	oldFrom := time.Date(2002, 7, 1, 0, 0, 0, 0, time.UTC)
	oldTo := oldFrom.AddDate(0, 0, 14)
	now := oldTo.AddDate(0, 0, 30) // конверт давно истёк
	cleanup := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM budget_envelope WHERE chat_id = $1`, chatID); err != nil {
			t.Logf("cleanup envelope: %v", err)
		}
	}
	cleanup()
	defer cleanup()

	oldID, err := s.CreateEnvelopeWithShares(ctx, chatID, 100000, "RUB", oldFrom, oldTo, closeTestShares(), oldFrom)
	if err != nil {
		t.Fatalf("create old envelope: %v", err)
	}
	if _, err := s.CreateEnvelopeWithShares(ctx, chatID, 120000, "RUB",
		now, now.AddDate(0, 0, 14), closeTestShares(), now); err != nil {
		t.Fatalf("create new envelope: %v", err)
	}

	var end time.Time
	if err := pool.QueryRow(ctx, `SELECT period_end FROM budget_envelope WHERE id = $1`, oldID).Scan(&end); err != nil {
		t.Fatalf("read old envelope: %v", err)
	}
	if !end.Equal(oldTo) {
		t.Errorf("истёкший конверт удлинён: period_end = %s, ожидалось %s",
			end.Format("2006-01-02"), oldTo.Format("2006-01-02"))
	}
}
