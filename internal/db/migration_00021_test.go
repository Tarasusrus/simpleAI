package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pressly/goose/v3"
)

// scratchSeq даёт каждой временной базе своё латинское имя: имя теста может быть
// кириллическим и не влезть в 63 байта идентификатора Postgres.
var scratchSeq atomic.Int64

// scratchDB создаёт чистую базу, накатывает миграции до version и отдаёт
// подключение. Нужен суперпользовательский URL (BOTCLIENT_DATABASE_URL_RW):
// без него тест пропускается, как и остальные DB-тесты проекта (C26).
func scratchDB(t *testing.T, version int64) *sql.DB {
	t.Helper()
	adminURL := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if adminURL == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — DB-тест миграций пропущен (это НЕ зелёный результат)")
	}
	admin, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() }) //nolint:errcheck // очистка в конце теста

	name := fmt.Sprintf("mig21_%d_%d", os.Getpid(), scratchSeq.Add(1))
	if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + name); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`) }) //nolint:errcheck // очистка в конце теста

	// Та же строка подключения, но к новой базе: меняем имя базы в пути URL.
	idx := strings.LastIndex(adminURL, "/")
	rest := adminURL[idx+1:]
	query := ""
	if q := strings.Index(rest, "?"); q >= 0 {
		query = rest[q:]
	}
	dsn := adminURL[:idx+1] + name + query
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open scratch: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() }) //nolint:errcheck // очистка в конце теста

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.UpToContext(context.Background(), conn, "migrations", version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	scratchDSNs.Store(conn, dsn)
	return conn
}

// scratchDSNs помнит строку подключения временной базы: тестам модуля категорий нужен pgxpool.
var scratchDSNs sync.Map

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// Бэкфилл старых правил и повторный прогон тела миграции.
func TestMigration00021_BackfillAndRerun(t *testing.T) {
	conn := scratchDB(t, 20)
	mustExec(t, conn, `INSERT INTO budget_recurring (id, chat_id, name, amount, recurrence_type, day_of_month, next_date) VALUES
		('00000000-0000-0000-0000-000000000001', 1, 'аренда 31', 100, 'monthly', 31, '2026-02-15'),
		('00000000-0000-0000-0000-000000000002', 1, 'месяц без дня', 100, 'monthly', NULL, '2026-03-10'),
		('00000000-0000-0000-0000-000000000003', 1, 'неделя', 100, 'weekly', NULL, '2026-03-12'),
		('00000000-0000-0000-0000-000000000004', 1, 'день', 100, 'daily', NULL, '2026-03-13')`)

	if err := goose.UpToContext(context.Background(), conn, "migrations", 21); err != nil {
		t.Fatalf("up 21: %v", err)
	}

	want := map[string][2]string{
		"аренда 31":     {"month", "2026-02-28"}, // 31 прижато к концу февраля; день хранит day_of_month
		"месяц без дня": {"month", "2026-03-10"},
		"неделя":        {"week", "2026-03-12"},
		"день":          {"day", "2026-03-13"},
	}
	rows, err := conn.Query(`SELECT name, interval_unit, anchor_date::text, interval_n, day_of_month, next_date::text FROM budget_recurring`)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	defer rows.Close() //nolint:errcheck // чтение в тесте
	seen := 0
	for rows.Next() {
		var name, unit, anchor, next string
		var n int
		var dom sql.NullInt64
		if err := rows.Scan(&name, &unit, &anchor, &n, &dom, &next); err != nil {
			t.Fatalf("scan: %v", err)
		}
		w := want[name]
		if unit != w[0] || anchor != w[1] || n != 1 {
			t.Errorf("%s: unit=%s anchor=%s n=%d, ждали %v шаг 1", name, unit, anchor, n, w)
		}
		if name == "аренда 31" && (!dom.Valid || dom.Int64 != 31) {
			t.Errorf("day_of_month потерян: %v", dom)
		}
		seen++
	}
	if seen != len(want) {
		t.Fatalf("строк %d, ждали %d", seen, len(want))
	}

	// Повторный прогон тела Up (как после частично упавшей миграции) безопасен.
	body := upBody(t)
	if _, err := conn.Exec(body); err != nil {
		t.Fatalf("повторный прогон Up: %v", err)
	}
}

// upBody достаёт SQL секции Up из файла миграции.
func upBody(t *testing.T) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/00021_plan_window.sql")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(b)
	up := strings.Index(s, "-- +goose Up")
	down := strings.Index(s, "-- +goose Down")
	if up < 0 || down < 0 {
		t.Fatal("нет секций Up/Down")
	}
	body := s[up:down]
	body = strings.ReplaceAll(body, "-- +goose Up", "")
	body = strings.ReplaceAll(body, "-- +goose StatementBegin", "")
	body = strings.ReplaceAll(body, "-- +goose StatementEnd", "")
	return "BEGIN;\n" + body + "\nCOMMIT;"
}

// Ограничения: идемпотентность срока, дата/окно, сумма.
func TestMigration00021_Constraints(t *testing.T) {
	conn := scratchDB(t, 21)
	mustExec(t, conn, `INSERT INTO budget_recurring (id, chat_id, name, amount, recurrence_type, next_date)
		VALUES ('00000000-0000-0000-0000-0000000000a1', 1, 'r', 10, 'daily', '2026-03-13')`)

	ins := `INSERT INTO budget_transaction (id, type, amount, recurring_id, scheduled_for)
		VALUES (gen_random_uuid(), 'expense', 10, '00000000-0000-0000-0000-0000000000a1', $1)`
	mustExec(t, conn, ins, "2026-03-13")
	if _, err := conn.Exec(ins, "2026-03-13"); err == nil {
		t.Error("второй факт того же срока принят: уникальность (recurring_id, scheduled_for) не работает")
	}
	mustExec(t, conn, ins, "2026-03-14") // другой срок — можно
	// Старые строки с NULL индексу не мешают.
	for i := 0; i < 2; i++ {
		mustExec(t, conn, `INSERT INTO budget_transaction (id, type, amount, recurring_id)
			VALUES (gen_random_uuid(), 'expense', 10, '00000000-0000-0000-0000-0000000000a1')`)
	}

	if _, err := conn.Exec(`INSERT INTO budget_planned_expense (chat_id, description, due_date, window_start)
		VALUES (1, 'x', '2026-03-13', '2026-03-13')`); err == nil {
		t.Error("принята и дата, и окно одновременно")
	}
	if _, err := conn.Exec(`INSERT INTO budget_planned_expense (chat_id, description, amount) VALUES (1, 'x', 0)`); err == nil {
		t.Error("принята нулевая сумма")
	}
	mustExec(t, conn, `INSERT INTO budget_planned_expense (chat_id, description) VALUES (1, 'Права')`) // без суммы можно
	if _, err := conn.Exec(`INSERT INTO budget_planned_expense (chat_id, description, direction) VALUES (1, 'x', 'bogus')`); err == nil {
		t.Error("принято неизвестное направление")
	}
	if _, err := conn.Exec(`UPDATE plan_settings SET reserve_pct = 101`); err == nil {
		t.Error("принят резерв 101%")
	}
}

// Down отказывается при данных, которые старая схема не хранит, и откатывает иначе.
func TestMigration00021_DownRefusesThenRollsBack(t *testing.T) {
	for name, seed := range map[string]string{
		"сумма NULL":  `INSERT INTO budget_planned_expense (chat_id, description) VALUES (1, 'Права')`,
		"поступление": `INSERT INTO budget_planned_expense (chat_id, description, amount, direction) VALUES (1, 'аванс', 10, 'income')`,
	} {
		t.Run(name, func(t *testing.T) {
			conn := scratchDB(t, 21)
			mustExec(t, conn, seed)
			err := goose.DownToContext(context.Background(), conn, "migrations", 20)
			if err == nil {
				t.Fatal("откат прошёл при данных, которые старая схема не хранит")
			}
			if !strings.Contains(err.Error(), "откат 00021 невозможен") {
				t.Errorf("непонятная ошибка: %v", err)
			}
			// Данные целы.
			var n int
			if err := conn.QueryRow(`SELECT count(*) FROM budget_planned_expense`).Scan(&n); err != nil || n != 1 {
				t.Errorf("строки потеряны: n=%d err=%v", n, err)
			}
		})
	}

	t.Run("чистый откат", func(t *testing.T) {
		conn := scratchDB(t, 21)
		mustExec(t, conn, `INSERT INTO budget_planned_expense (chat_id, description, amount) VALUES (1, 'обычный', 10)`)
		if err := goose.DownToContext(context.Background(), conn, "migrations", 20); err != nil {
			t.Fatalf("откат: %v", err)
		}
		var cols int
		if err := conn.QueryRow(`SELECT count(*) FROM information_schema.columns
			WHERE table_name IN ('budget_recurring','budget_transaction','budget_planned_expense')
			AND column_name IN ('anchor_date','skipped_dates','scheduled_for','direction','window_start','interval_n')`).Scan(&cols); err != nil || cols != 0 {
			t.Errorf("колонки не откатились: %d err=%v", cols, err)
		}
		// И снова вперёд.
		if err := goose.UpToContext(context.Background(), conn, "migrations", 21); err != nil {
			t.Fatalf("повторный накат: %v", err)
		}
	})
}
