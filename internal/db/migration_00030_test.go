package db

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// 00030 (ADR-019): opens_window у фактов, плановых записей и правил повтора. Старые строки получают true; новая
// строка без флага тоже true (бот и воркер пишут как раньше); false сохраняется; откат убирает колонки, повторный
// накат работает.
//
// Мутации: DEFAULT true → DEFAULT false (старые строки «обычный приход»), не добавить колонку в одну из трёх таблиц,
// не удалять колонку в Down — краснеют проверки.
func TestMigration00030_OpensWindow(t *testing.T) {
	conn := scratchDB(t, 29)
	ctx := context.Background()
	mustExec(t, conn, `INSERT INTO budget_transaction (id, type, amount) VALUES ('11111111-1111-1111-1111-111111111111','income',10)`)
	mustExec(t, conn, `INSERT INTO budget_planned_expense (id, chat_id, description, amount, direction) VALUES ('22222222-2222-2222-2222-222222222222', 1, 'до миграции', 10, 'income')`)
	if err := goose.UpToContext(ctx, conn, "migrations", 30); err != nil {
		t.Fatalf("up 30: %v", err)
	}
	for _, tbl := range []string{"budget_transaction", "budget_planned_expense"} {
		if n := count(t, conn, `SELECT count(*) FROM `+tbl+` WHERE opens_window IS TRUE`); n != 1 {
			t.Errorf("%s: старая строка после миграции opens_window=true: %d из 1", tbl, n)
		}
	}
	mustExec(t, conn, `INSERT INTO budget_recurring (id, chat_id, name, type, amount, recurrence_type, next_date, opens_window)
		VALUES ('33333333-3333-3333-3333-333333333333', 1, 'без флага', 'income', 5, 'monthly', '2026-11-01', false)`)
	mustExec(t, conn, `INSERT INTO budget_recurring (id, chat_id, name, type, amount, recurrence_type, next_date)
		VALUES ('44444444-4444-4444-4444-444444444444', 1, 'по умолчанию', 'income', 5, 'monthly', '2026-11-01')`)
	if n := count(t, conn, `SELECT count(*) FROM budget_recurring WHERE opens_window IS FALSE`); n != 1 {
		t.Errorf("явный false не сохранился: %d", n)
	}
	if n := count(t, conn, `SELECT count(*) FROM budget_recurring WHERE name='по умолчанию' AND opens_window IS TRUE`); n != 1 {
		t.Error("новая строка без флага не получила true")
	}

	if err := goose.DownToContext(ctx, conn, "migrations", 29); err != nil {
		t.Fatalf("down 30: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM information_schema.columns WHERE column_name='opens_window'`); n != 0 {
		t.Errorf("Down оставил колонки: %d", n)
	}
	if err := goose.UpToContext(ctx, conn, "migrations", 30); err != nil {
		t.Fatalf("повторный up 30: %v", err)
	}
}
