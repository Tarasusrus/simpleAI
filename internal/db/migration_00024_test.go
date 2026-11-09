package db

import (
	"context"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	limCatFood = "00000000-0000-0000-0000-0000000000a1"
	limCatTaxi = "00000000-0000-0000-0000-0000000000a2"
)

// Ограничения таблицы лимитов (ADR-011): сумма > 0, валюта THB|RUB, один повторяющийся
// лимит на категорию, один разовый на категорию и окно; категорию с лимитом не удалить.
func TestMigration00024_Constraints(t *testing.T) {
	conn := scratchDB(t, 24)
	mustExec(t, conn, `INSERT INTO budget_category (id,name,type) VALUES ($1,'Лимит-еда','expense'), ($2,'Лимит-такси','expense')`, limCatFood, limCatTaxi)
	ins := `INSERT INTO plan_category_limit (chat_id,category_id,amount_minor,currency,repeat,window_start) VALUES ($1,$2,$3,$4,$5,$6)`
	mustExec(t, conn, ins, 1, limCatFood, 100, "THB", true, "2026-10-05")

	bad := map[string][]any{
		"сумма 0": {1, limCatTaxi, 0, "THB", false, "2026-10-05"},
		"валюта":  {1, limCatTaxi, 10, "USD", false, "2026-10-05"},
		"второй повторяющийся": {1, limCatFood, 50, "THB", true, "2026-10-19"},
		"нет категории":        {1, "00000000-0000-0000-0000-0000000000ff", 10, "THB", false, "2026-10-05"},
	}
	for name, args := range bad {
		if _, err := conn.Exec(ins, args...); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	// разовые: разные окна — можно, то же окно — нельзя; другой чат — можно
	mustExec(t, conn, ins, 1, limCatTaxi, 10, "RUB", false, "2026-10-05")
	mustExec(t, conn, ins, 1, limCatTaxi, 10, "RUB", false, "2026-10-19")
	mustExec(t, conn, ins, 2, limCatTaxi, 10, "RUB", false, "2026-10-05")
	if _, err := conn.Exec(ins, 1, limCatTaxi, 20, "RUB", false, "2026-10-05"); err == nil {
		t.Error("второй разовый на то же окно принят")
	}
	if _, err := conn.Exec(`DELETE FROM budget_category WHERE id=$1`, limCatFood); err == nil {
		t.Error("категория с лимитом удалена (ON DELETE RESTRICT)")
	}
}

func upBody24(t *testing.T) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/00024_plan_category_limit.sql")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(b)
	up, down := strings.Index(s, "-- +goose Up"), strings.Index(s, "-- +goose Down")
	body := s[up:down]
	for _, m := range []string{"-- +goose Up", "-- +goose StatementBegin", "-- +goose StatementEnd"} {
		body = strings.ReplaceAll(body, m, "")
	}
	return "BEGIN;\n" + body + "\nCOMMIT;"
}

// Повторный прогон Up безопасен, откат с данными отказывает, откат пустой таблицы работает.
func TestMigration00024_RerunAndDown(t *testing.T) {
	conn := scratchDB(t, 24)
	mustExec(t, conn, `INSERT INTO budget_category (id,name,type) VALUES ($1,'Лимит-еда','expense')`, limCatFood)
	mustExec(t, conn, `INSERT INTO plan_category_limit (chat_id,category_id,amount_minor,currency,repeat,window_start)
		VALUES (1,$1,100,'THB',true,'2026-10-05')`, limCatFood)
	if _, err := conn.Exec(upBody24(t)); err != nil {
		t.Fatalf("повторный прогон Up: %v", err)
	}
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM plan_category_limit`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("повторный прогон изменил данные: %d (err %v)", n, err)
	}
	if err := goose.DownToContext(context.Background(), conn, "migrations", 23); err == nil {
		t.Fatal("откат с лимитами в таблице прошёл — данные владельца пропали бы")
	}
	mustExec(t, conn, `DELETE FROM plan_category_limit`)
	if err := goose.DownToContext(context.Background(), conn, "migrations", 23); err != nil {
		t.Fatalf("откат пустой таблицы: %v", err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name='plan_category_limit'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("таблица осталась после отката: %d", n)
	}
}

// Откат отказывает и тогда, когда в журнале слияний остались снимки лимитов, нужные для отката.
//
// Мутация: убрать вторую проверку в Down — тест краснеет.
func TestMigration00024_DownRefusesWithMergeSnapshots(t *testing.T) {
	conn := scratchDB(t, 24)
	mustExec(t, conn, `INSERT INTO category_merge_log (table_name,row_id,old_category_id,new_category_id,old_category_name,merge_id,row_snapshot)
		VALUES ('plan_category_limit:dropped','x',$1,$2,'а',gen_random_uuid(),'{"id":"x"}')`, limCatFood, limCatTaxi)
	if err := goose.DownToContext(context.Background(), conn, "migrations", 23); err == nil {
		t.Fatal("откат прошёл, хотя журнал хранит снимок конфликтного лимита")
	}
	mustExec(t, conn, `UPDATE category_merge_log SET rolled_back_at = now()`)
	if err := goose.DownToContext(context.Background(), conn, "migrations", 23); err != nil {
		t.Fatalf("откат после отката слияния: %v", err)
	}
}
