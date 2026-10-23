package db

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"

	"simpleAI/internal/categories"
)

// 00027: таблица скрытых категорий — уникальность, каскад при удалении категории, откат и повторный накат.
//
// Мутации: убрать UNIQUE, заменить ON DELETE CASCADE на RESTRICT, не удалять таблицу в Down — проверки краснеют.
func TestMigration00027_DismissedTable(t *testing.T) {
	conn := scratchDB(t, 26)
	ctx := context.Background()
	if err := goose.UpToContext(ctx, conn, "migrations", 27); err != nil {
		t.Fatalf("up 27: %v", err)
	}
	cat := mkCat(t, conn, "тест-скрытая", "expense")
	mustExec(t, conn, `INSERT INTO plan_category_dismissed (chat_id, category_id) VALUES (7, $1)`, cat)
	if _, err := conn.Exec(`INSERT INTO plan_category_dismissed (chat_id, category_id) VALUES (7, $1)`, cat); err == nil {
		t.Error("дубль (chat_id, category_id) принят")
	}
	mustExec(t, conn, `INSERT INTO plan_category_dismissed (chat_id, category_id) VALUES (8, $1)`, cat) // другой чат — можно
	mustExec(t, conn, `DELETE FROM budget_category WHERE id = $1`, cat)
	if n := count(t, conn, `SELECT count(*) FROM plan_category_dismissed`); n != 0 {
		t.Errorf("после удаления категории осталось %d флагов (ждали каскад)", n)
	}

	if err := goose.DownToContext(ctx, conn, "migrations", 26); err != nil {
		t.Fatalf("down 27: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM information_schema.tables WHERE table_name='plan_category_dismissed'`); n != 0 {
		t.Error("Down оставил таблицу")
	}
	if err := goose.UpToContext(ctx, conn, "migrations", 27); err != nil {
		t.Fatalf("повторный up 27: %v", err)
	}
}

// Слияние (ADR-015 §3): флаг скрытия источника уходит вместе с источником, флаг цели остаётся;
// слияние не падает из-за внешнего ключа.
//
// Мутация: ON DELETE CASCADE → RESTRICT в 00027 — слияние падает.
func TestCategories_MergeDropsSourceDismissedKeepsTarget(t *testing.T) {
	st, conn := catStoreAt(t, 27)
	src, dst := mkCat(t, conn, "тест-ист-скрыт", "expense"), mkCat(t, conn, "ТестЦельСкрыт", "expense")
	ins := `INSERT INTO plan_category_dismissed (chat_id, category_id) VALUES ($1, $2)`
	mustExec(t, conn, ins, 1, src)
	mustExec(t, conn, ins, 1, dst)
	if _, err := st.Merge(context.Background(), categories.MergeRequest{Source: src, Target: dst}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM plan_category_dismissed WHERE category_id=$1`, src); n != 0 {
		t.Errorf("флаг источника остался: %d", n)
	}
	if n := count(t, conn, `SELECT count(*) FROM plan_category_dismissed WHERE category_id=$1`, dst); n != 1 {
		t.Errorf("флаг цели пропал: %d", n)
	}
}
