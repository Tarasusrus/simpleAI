package db

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// 00029: таблица отметок записанных чеков — уникальность (чат, номер), другой чат можно, откат и повторный накат.
//
// Мутации: убрать PRIMARY KEY — принимается дубль; не удалять таблицу в Down — краснеет проверка отката.
func TestMigration00029_ReceiptSeen(t *testing.T) {
	conn := scratchDB(t, 28)
	ctx := context.Background()
	if err := goose.UpToContext(ctx, conn, "migrations", 29); err != nil {
		t.Fatalf("up 29: %v", err)
	}
	ins := `INSERT INTO receipt_seen (chat_id, reference) VALUES ($1, 'REF1')`
	mustExec(t, conn, ins, 1)
	if _, err := conn.Exec(ins, 1); err == nil {
		t.Error("дубль (чат, номер) принят")
	}
	mustExec(t, conn, ins, 2) // тот же номер в другом чате — можно
	if err := goose.DownToContext(ctx, conn, "migrations", 28); err != nil {
		t.Fatalf("down 29: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM information_schema.tables WHERE table_name='receipt_seen'`); n != 0 {
		t.Error("Down оставил таблицу")
	}
	if err := goose.UpToContext(ctx, conn, "migrations", 29); err != nil {
		t.Fatalf("повторный up 29: %v", err)
	}
}
