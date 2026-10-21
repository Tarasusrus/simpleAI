package db

import (
	"context"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// Миграция 00023 добавляет связь факта с плановой записью. Старые факты остаются без
// неё, повторный прогон безопасен, удаление плановой записи только обнуляет ссылку, а
// откат убирает колонку и не трогает сами транзакции.
//
// Мутации: убрать ON DELETE SET NULL (удаление записи упадёт на FK), убрать
// IF NOT EXISTS (повторный прогон упадёт), убрать DROP COLUMN из Down — тест краснеет.
func TestMigration00023_PlannedLink(t *testing.T) {
	conn := scratchDB(t, 22)
	mustExec(t, conn, `INSERT INTO budget_transaction (id, type, amount, currency, description, transaction_date)
		VALUES ('00000000-0000-0000-0000-0000000000a1', 'expense', 10, 'THB', 'старый факт', '2026-03-01')`)

	if err := goose.UpToContext(context.Background(), conn, "migrations", 23); err != nil {
		t.Fatalf("up 23: %v", err)
	}
	var planned *string
	if err := conn.QueryRow(`SELECT planned_id::text FROM budget_transaction WHERE description = 'старый факт'`).Scan(&planned); err != nil || planned != nil {
		t.Fatalf("старый факт должен остаться без связи: %v %v", planned, err)
	}

	b, err := migrationsFS.ReadFile("migrations/00023_transaction_planned_id.sql")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(b)
	body := s[strings.Index(s, "-- +goose Up"):strings.Index(s, "-- +goose Down")]
	if _, err := conn.Exec(body); err != nil {
		t.Fatalf("повторный прогон Up: %v", err)
	}

	mustExec(t, conn, `INSERT INTO budget_planned_expense (id, chat_id, description, amount, currency)
		VALUES ('00000000-0000-0000-0000-0000000000b1', 1, 'план', 10, 'THB')`)
	mustExec(t, conn, `INSERT INTO budget_transaction (id, type, amount, currency, description, transaction_date, planned_id)
		VALUES ('00000000-0000-0000-0000-0000000000a2', 'expense', 10, 'THB', 'новый факт', '2026-03-02', '00000000-0000-0000-0000-0000000000b1')`)
	mustExec(t, conn, `DELETE FROM budget_planned_expense WHERE id = '00000000-0000-0000-0000-0000000000b1'`)
	if err := conn.QueryRow(`SELECT planned_id::text FROM budget_transaction WHERE description = 'новый факт'`).Scan(&planned); err != nil || planned != nil {
		t.Fatalf("после удаления записи факт должен жить без ссылки: %v %v", planned, err)
	}

	if err := goose.DownToContext(context.Background(), conn, "migrations", 22); err != nil {
		t.Fatalf("down 23: %v", err)
	}
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'budget_transaction' AND column_name = 'planned_id'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("колонка planned_id осталась после отката: %d %v", n, err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM budget_transaction`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("откат затронул транзакции: %d %v", n, err)
	}
}
