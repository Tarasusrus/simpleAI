package seenstore

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Claim атомарен и отделяет чаты; Seen видит занятый номер; Release снимает отметку.
//
// Мутации: ON CONFLICT DO NOTHING → DO UPDATE (Claim всегда true), убрать chat_id из ключа запроса Seen — краснеют.
func TestStore(t *testing.T) {
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — DB-тест пропущен (это НЕ зелёный результат)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	const chat, other, ref = int64(-990001), int64(-990002), "SEENSTORE-TEST-REF"
	s := New(pool)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM receipt_seen WHERE reference = $1`, ref) //nolint:errcheck // очистка после теста
	})
	if ok, err := s.Claim(ctx, chat, ref); err != nil || !ok {
		t.Fatalf("первый Claim: %v %v", ok, err)
	}
	if ok, err := s.Claim(ctx, chat, ref); err != nil || ok {
		t.Errorf("второй Claim: ok=%v err=%v, ждали false", ok, err)
	}
	if seen, err := s.Seen(ctx, chat, ref); err != nil || !seen {
		t.Errorf("Seen после Claim: %v %v", seen, err)
	}
	if seen, err := s.Seen(ctx, other, ref); err != nil || seen {
		t.Errorf("Seen в другом чате: %v %v", seen, err)
	}
	if err := s.Release(ctx, chat, ref); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Claim(ctx, chat, ref); err != nil || !ok {
		t.Errorf("Claim после Release: %v %v", ok, err)
	}
}
