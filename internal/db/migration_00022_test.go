package db

import (
	"context"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func upBody22(t *testing.T) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/00022_category_dedup.sql")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(b)
	up, down := strings.Index(s, "-- +goose Up"), strings.Index(s, "-- +goose Down")
	if up < 0 || down < 0 {
		t.Fatal("нет секций Up/Down")
	}
	body := s[up:down]
	for _, m := range []string{"-- +goose Up", "-- +goose StatementBegin", "-- +goose StatementEnd"} {
		body = strings.ReplaceAll(body, m, "")
	}
	return "BEGIN;\n" + body + "\nCOMMIT;"
}

const (
	catCoffeeOld = "00000000-0000-0000-0000-0000000000c1"
	catCoffeeDup = "00000000-0000-0000-0000-0000000000c2"
)

// Прод мог получить category_merge_log руками (6 колонок, без merge_id): миграция
// расширяет её, дубли по регистру сливает с журналом и ставит уникальный индекс.
func TestMigration00022_MergesCaseDuplicatesAndExtendsLog(t *testing.T) {
	conn := scratchDB(t, 21)
	mustExec(t, conn, `CREATE TABLE category_merge_log (
		at timestamptz NOT NULL DEFAULT now(), table_name text NOT NULL, row_id text NOT NULL,
		old_category_id uuid NOT NULL, new_category_id uuid NOT NULL, old_category_name text NOT NULL)`)
	mustExec(t, conn, `INSERT INTO category_merge_log (table_name,row_id,old_category_id,new_category_id,old_category_name)
		VALUES ('budget_transaction','x',$1,$2,'старое')`, catCoffeeOld, catCoffeeDup)
	mustExec(t, conn, `INSERT INTO budget_category (id,name,type,created_at) VALUES
		($1,'Кофе','expense','2026-01-01'), ($2,'кофе','expense','2026-02-01'),
		('00000000-0000-0000-0000-0000000000c3','Кофе','income','2026-01-01')`, catCoffeeOld, catCoffeeDup)
	mustExec(t, conn, `INSERT INTO budget_transaction (id,type,amount,category_id) VALUES
		(gen_random_uuid(),'expense',10,$1), (gen_random_uuid(),'expense',20,$2)`, catCoffeeOld, catCoffeeDup)
	mustExec(t, conn, `INSERT INTO budget_envelope (id,chat_id,income_amount,period_start,period_end)
		VALUES ('00000000-0000-0000-0000-0000000000e1',1,100,'2026-01-01','2026-01-31')`)
	mustExec(t, conn, `INSERT INTO budget_envelope_share (id,envelope_id,name,kind,allocated,source,position)
		VALUES ('00000000-0000-0000-0000-0000000000f1','00000000-0000-0000-0000-0000000000e1','Еда','spend',50,'auto',0)`)
	mustExec(t, conn, `INSERT INTO budget_envelope_share_category (share_id,category_id,category_name)
		VALUES ('00000000-0000-0000-0000-0000000000f1',$1,'кофе')`, catCoffeeDup)

	if err := goose.UpToContext(context.Background(), conn, "migrations", 22); err != nil {
		t.Fatalf("up 22: %v", err)
	}

	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM budget_category WHERE lower(name)='кофе' AND type='expense'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("осталось категорий «кофе» расходов: %d (err %v), ждали 1", n, err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM budget_category WHERE id=$1`, catCoffeeOld).Scan(&n); err != nil || n != 1 {
		t.Errorf("самая старая категория должна остаться")
	}
	if err := conn.QueryRow(`SELECT count(*) FROM budget_category WHERE type='income' AND name='Кофе'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("категория другого типа не должна сливаться")
	}
	if err := conn.QueryRow(`SELECT count(*) FROM budget_transaction WHERE category_id=$1`, catCoffeeOld).Scan(&n); err != nil || n != 2 {
		t.Errorf("транзакций у оставшейся: %d, ждали 2", n)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM budget_envelope_share_category WHERE category_id=$1`, catCoffeeOld).Scan(&n); err != nil || n != 1 {
		t.Errorf("доля не переехала на оставшуюся категорию: %d", n)
	}
	// Журнал: старая строка цела (merge_id NULL), новые строки с merge_id и снимком.
	if err := conn.QueryRow(`SELECT count(*) FROM category_merge_log WHERE merge_id IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Errorf("строка ручного прогона потеряна или получила merge_id: %d", n)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM category_merge_log WHERE merge_id IS NOT NULL AND old_category_id=$1 AND source_category_type='expense'`, catCoffeeDup).Scan(&n); err != nil || n != 2 {
		t.Errorf("записей слияния в журнале: %d, ждали 2 (транзакция и доля)", n)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM category_decision_log WHERE action='merge' AND decider='rule'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("решений в журнале: %d, ждали 1", n)
	}
	// Регистронезависимый индекс работает.
	if _, err := conn.Exec(`INSERT INTO budget_category (id,name,type) VALUES (gen_random_uuid(),'КОФЕ','expense')`); err == nil {
		t.Error("дубль по регистру принят: уникальный индекс lower(name),type не создан")
	}
	// Повторный прогон безопасен и ничего не меняет.
	if _, err := conn.Exec(upBody22(t)); err != nil {
		t.Fatalf("повторный прогон Up: %v", err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM category_merge_log`).Scan(&n); err != nil || n != 3 {
		t.Errorf("повторный прогон дописал журнал: %d строк, ждали 3", n)
	}
}

// На чистой базе (журнала ещё нет) миграция создаёт всё сама.
func TestMigration00022_CleanDB(t *testing.T) {
	conn := scratchDB(t, 22)
	for _, tbl := range []string{"category_merge_log", "budget_category_alias", "category_review_queue", "category_decision_log"} {
		var n int
		if err := conn.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name=$1`, tbl).Scan(&n); err != nil || n != 1 {
			t.Errorf("таблицы %s нет", tbl)
		}
	}
}
