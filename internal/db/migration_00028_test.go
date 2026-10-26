package db

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// 00028 (ADR-018): журнал правок tx_edit_log и updated_at у транзакций.
//
// Мутации: убрать CHECK по action, убрать CHECK (action/after), поставить внешний ключ на tx_id (удаление
// транзакции съест журнал), не удалять таблицу в Down — проверки краснеют.
func TestMigration00028_TxEditLog(t *testing.T) {
	conn := scratchDB(t, 27)
	ctx := context.Background()
	if err := goose.UpToContext(ctx, conn, "migrations", 28); err != nil {
		t.Fatalf("up 28: %v", err)
	}
	cat := mkCat(t, conn, "тест-журнал", "expense")
	mustExec(t, conn, `INSERT INTO budget_transaction (id, type, amount, category_id) VALUES ('11111111-1111-1111-1111-111111111111','expense',10,$1)`, cat)
	if n := count(t, conn, `SELECT count(*) FROM budget_transaction WHERE updated_at IS NOT NULL`); n != 1 {
		t.Errorf("updated_at не заполнен по умолчанию: %d", n)
	}

	// Версия растёт при любом UPDATE, в том числе чужом (бот, воркер), а не только из кода страницы.
	var before, after string
	q := `SELECT updated_at::text FROM budget_transaction WHERE id='11111111-1111-1111-1111-111111111111'`
	if err := conn.QueryRow(q).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, conn, `UPDATE budget_transaction SET description='x' WHERE id='11111111-1111-1111-1111-111111111111'`)
	if err := conn.QueryRow(q).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("UPDATE не изменил updated_at: оптимистичная блокировка не увидит чужую правку")
	}

	ins := `INSERT INTO tx_edit_log (id, tx_id, action, before, after, source) VALUES (gen_random_uuid(), '11111111-1111-1111-1111-111111111111', $1, '{"amount":10}', $2::jsonb, 'web')`
	mustExec(t, conn, ins, "edit", `{"amount":20}`)
	if _, err := conn.Exec(ins, "rename", `{"amount":20}`); err == nil {
		t.Error("принято неизвестное действие")
	}
	if _, err := conn.Exec(ins, "edit", nil); err == nil {
		t.Error("правка без значений «после» принята")
	}
	if _, err := conn.Exec(ins, "delete", `{"amount":20}`); err == nil {
		t.Error("удаление со значениями «после» принято")
	}
	mustExec(t, conn, ins, "delete", nil)

	mustExec(t, conn, `DELETE FROM budget_transaction WHERE id='11111111-1111-1111-1111-111111111111'`)
	if n := count(t, conn, `SELECT count(*) FROM tx_edit_log`); n != 2 {
		t.Errorf("удаление транзакции затронуло журнал: осталось %d из 2", n)
	}

	if err := goose.DownToContext(ctx, conn, "migrations", 27); err != nil {
		t.Fatalf("down 28: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM information_schema.tables WHERE table_name='tx_edit_log'`); n != 0 {
		t.Error("Down оставил таблицу")
	}
	if n := count(t, conn, `SELECT count(*) FROM pg_trigger WHERE tgname='trg_budget_transaction_touch'`); n != 0 {
		t.Error("Down оставил триггер")
	}
	if n := count(t, conn, `SELECT count(*) FROM information_schema.columns WHERE table_name='budget_transaction' AND column_name='updated_at'`); n != 0 {
		t.Error("Down оставил updated_at")
	}
	if err := goose.UpToContext(ctx, conn, "migrations", 28); err != nil {
		t.Fatalf("повторный up 28: %v", err)
	}
}
