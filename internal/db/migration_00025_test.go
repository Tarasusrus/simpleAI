package db

import (
	"context"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	splitSrc = "00000000-0000-0000-0000-0000000000b1"
	splitA   = "00000000-0000-0000-0000-0000000000b2"
	splitB   = "00000000-0000-0000-0000-0000000000b3"
)

// Ограничения таблицы разделения (ADR-013 §2): источник не равен цели, пара уникальна, не больше
// одной цели по умолчанию на источник; удаление категории убирает её строки.
//
// Мутация: убрать uq_category_split_default или CHECK (source_id <> target_id) — тест краснеет.
func TestMigration00025_Constraints(t *testing.T) {
	conn := scratchDB(t, 25)
	mustExec(t, conn, `INSERT INTO budget_category (id,name,type) VALUES ($1,'Сплит-источник','expense'), ($2,'Сплит-А','expense'), ($3,'Сплит-Б','expense')`, splitSrc, splitA, splitB)
	ins := `INSERT INTO category_split_target (source_id,target_id,is_default) VALUES ($1,$2,$3)`
	mustExec(t, conn, ins, splitSrc, splitA, true)
	mustExec(t, conn, ins, splitSrc, splitB, false)

	bad := map[string][]any{
		"источник равен цели":      {splitSrc, splitSrc, false},
		"дубль пары":               {splitSrc, splitA, false},
		"вторая цель по умолчанию": {splitA, splitB, true},
		"нет категории":            {splitSrc, "00000000-0000-0000-0000-0000000000ff", false},
	}
	// «вторая цель по умолчанию» проверяется на том же источнике:
	bad["вторая цель по умолчанию"] = []any{splitSrc, splitB, true}
	for name, args := range bad {
		if name == "вторая цель по умолчанию" {
			mustExec(t, conn, `DELETE FROM category_split_target WHERE source_id=$1 AND target_id=$2`, splitSrc, splitB)
		}
		if _, err := conn.Exec(ins, args...); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	mustExec(t, conn, `DELETE FROM budget_category WHERE id=$1`, splitA)
	if n := count(t, conn, `SELECT count(*) FROM category_split_target WHERE target_id=$1`, splitA); n != 0 {
		t.Errorf("строки удалённой цели остались: %d", n)
	}
}

func upBody25(t *testing.T) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/00025_category_split_target.sql")
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

// Повторный прогон Up безопасен, откат со строками отказывает, откат пустой таблицы работает.
//
// Мутация: убрать проверку EXISTS в Down — тест краснеет.
func TestMigration00025_RerunAndDown(t *testing.T) {
	conn := scratchDB(t, 25)
	mustExec(t, conn, `INSERT INTO budget_category (id,name,type) VALUES ($1,'Сплит-источник','expense'), ($2,'Сплит-А','expense')`, splitSrc, splitA)
	mustExec(t, conn, `INSERT INTO category_split_target (source_id,target_id,is_default) VALUES ($1,$2,true)`, splitSrc, splitA)
	if _, err := conn.Exec(upBody25(t)); err != nil {
		t.Fatalf("повторный прогон Up: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM category_split_target`); n != 1 {
		t.Fatalf("повторный прогон изменил данные: %d", n)
	}
	if err := goose.DownToContext(context.Background(), conn, "migrations", 24); err == nil {
		t.Fatal("откат со строками разделения прошёл — решение владельца пропало бы")
	}
	mustExec(t, conn, `DELETE FROM category_split_target`)
	if err := goose.DownToContext(context.Background(), conn, "migrations", 24); err != nil {
		t.Fatalf("откат пустой таблицы: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM information_schema.tables WHERE table_name='category_split_target'`); n != 0 {
		t.Fatalf("таблица осталась после отката: %d", n)
	}
}
