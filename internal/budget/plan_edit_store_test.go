package budget

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type plannedRow struct {
	desc, cur, cat string
	amount         *int64
	due, ws        *time.Time
	settled        bool
}

func readPlanned(t *testing.T, s *Store, ctx context.Context, id uuid.UUID) plannedRow {
	t.Helper()
	var r plannedRow
	if err := s.pool.QueryRow(ctx, `
		SELECT p.description, p.currency, COALESCE(c.name, ''), ROUND(p.amount * 100)::bigint, p.due_date, p.window_start, p.settled
		FROM budget_planned_expense p LEFT JOIN budget_category c ON c.id = p.category_id WHERE p.id = $1`, id).
		Scan(&r.desc, &r.cur, &r.cat, &r.amount, &r.due, &r.ws, &r.settled); err != nil {
		t.Fatalf("чтение %s: %v", id, err)
	}
	return r
}

func sameDay(a *time.Time, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Format("2006-01-02") == b.Format("2006-01-02"))
}

// Правка плановой записи: одна команда меняет название, сумму, валюту и категорию;
// без SetPlace дата и окно остаются как в базе, даже если запись перенесли после
// того, как форма прочитала её. Чужой чат, закрытая запись и другое направление не
// меняются.
//
// Мутации: убрать CASE WHEN $8 у due_date/window_start, NOT settled, chat_id = ANY
// или direction = $3 — тест краснеет.
func TestUpdatePlanItem(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	own := []int64{planStoreChat}
	amt := int64(10000)
	due := d(2026, 3, 15)
	add := func(chat int64, dir string) uuid.UUID {
		t.Helper()
		id, err := s.AddPlanItem(ctx, PlanItemInput{ChatID: chat, Direction: dir, Description: "до", AmountMinor: &amt, Currency: "RUB", Category: "Еда", DueDate: &due})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	item, income, foreign, closed := add(planStoreChat, "expense"), add(planStoreChat, "income"), add(planStoreChat-1, "expense"), add(planStoreChat, "expense")
	if _, err := s.pool.Exec(ctx, `UPDATE budget_planned_expense SET settled = true WHERE id = $1`, closed); err != nil {
		t.Fatal(err)
	}

	// Форма прочитала запись с датой, а её тем временем перенесли в окно.
	ws := d(2026, 3, 19)
	if ok, err := s.MovePlanItem(ctx, own, item, nil, &ws); err != nil || !ok {
		t.Fatalf("перенос: %v %v", ok, err)
	}
	newAmt := int64(123456)
	ok, err := s.UpdatePlanItem(ctx, own, item, PlanItemEdit{
		Direction: "expense", Description: "после", AmountMinor: &newAmt, Currency: "THB", Category: "здоровье", DueDate: &due,
	})
	if err != nil || !ok {
		t.Fatalf("правка: %v %v", ok, err)
	}
	got := readPlanned(t, s, ctx, item)
	if got.desc != "после" || got.cur != "THB" || got.amount == nil || *got.amount != 123456 || got.cat != "Здоровье" {
		t.Errorf("содержимое: %+v", got)
	}
	if got.due != nil || !sameDay(got.ws, &ws) {
		t.Errorf("правка без SetPlace откатила перенос: дата %v окно %v", got.due, got.ws)
	}

	// С SetPlace — дата ставится, окно снимается; сумму можно стереть.
	ok, err = s.UpdatePlanItem(ctx, own, item, PlanItemEdit{Direction: "expense", Description: "после", Currency: "RUB", Category: "нет такой", SetPlace: true, DueDate: &due})
	if err != nil || !ok {
		t.Fatalf("правка с местом: %v %v", ok, err)
	}
	got = readPlanned(t, s, ctx, item)
	if !sameDay(got.due, &due) || got.ws != nil || got.amount != nil || got.cat != "" {
		t.Errorf("с местом, без суммы, неизвестная категория: %+v", got)
	}

	for name, c := range map[string]struct {
		id   uuid.UUID
		dir  string
		want plannedRow
	}{
		"чужой чат":          {foreign, "expense", plannedRow{desc: "до"}},
		"закрытая":           {closed, "expense", plannedRow{desc: "до"}},
		"другое направление": {income, "expense", plannedRow{desc: "до"}},
	} {
		ok, err := s.UpdatePlanItem(ctx, own, c.id, PlanItemEdit{Direction: c.dir, Description: "взлом", AmountMinor: &newAmt, Currency: "THB", SetPlace: true})
		if err != nil || ok {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
		if r := readPlanned(t, s, ctx, c.id); r.desc != c.want.desc || *r.amount != amt || r.cur != "RUB" || !sameDay(r.due, &due) {
			t.Errorf("%s: запись изменилась: %+v", name, r)
		}
	}
	if _, err := s.UpdatePlanItem(ctx, own, item, PlanItemEdit{Direction: "expense", SetPlace: true, DueDate: &due, WindowStart: &ws}); err == nil {
		t.Error("дата и окно вместе приняты")
	}
}

type ruleRow struct {
	name, cur, cat, sched string
	amount                int64
	updated               time.Time
}

func readRule(t *testing.T, s *Store, ctx context.Context, id uuid.UUID) ruleRow {
	t.Helper()
	var r ruleRow
	if err := s.pool.QueryRow(ctx, `
		SELECT r.name, r.currency, COALESCE(c.name, ''), ROUND(r.amount * 100)::bigint, r.updated_at,
		       concat_ws('|', r.recurrence_type, r.day_of_month, r.next_date, r.interval_n, r.interval_unit,
		                 r.anchor_date, r.until, r.skipped_dates::text, r.enabled, r.type, r.chat_id)
		FROM budget_recurring r LEFT JOIN budget_category c ON c.id = r.category_id WHERE r.id = $1`, id).
		Scan(&r.name, &r.cur, &r.cat, &r.amount, &r.updated, &r.sched); err != nil {
		t.Fatalf("чтение правила %s: %v", id, err)
	}
	return r
}

// Правка правила меняет название, сумму, валюту и категорию, но не расписание (обе
// группы колонок — байт в байт); категория поступления не трогается; факты прошлых
// сроков остаются прежними (R3c). Выключенное, чужое и другого типа не меняются.
//
// Мутации: убрать AND enabled, chat_id = ANY, type = $3 или CASE у category_id — тест краснеет.
func TestUpdateRecurringRule(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	own := []int64{planStoreChat}
	insert := func(chat int64, typ, cat string, enabled bool) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO budget_recurring (id, chat_id, name, type, amount, category_id, currency, recurrence_type, day_of_month,
			                              next_date, enabled, interval_n, interval_unit, anchor_date, until, skipped_dates)
			VALUES ($1, $2, 'до', $3, 180.00, (SELECT id FROM budget_category WHERE name = $4 AND type = $3), 'THB', 'monthly', 15,
			        '2026-03-15', $5, 1, 'month', '2026-02-15', '2026-12-15', '{2026-04-15}')`, id, chat, typ, cat, enabled); err != nil {
			t.Fatal(err)
		}
		return id
	}
	rent := insert(planStoreChat, "expense", "Жильё", true)
	salary := insert(planStoreChat, "income", "Зарплата", true)
	off := insert(planStoreChat, "expense", "Жильё", false)
	foreign := insert(planStoreChat-1, "expense", "Жильё", true)
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, description, transaction_date, recurring_id, scheduled_for)
		VALUES ($1, 'expense', 180.00, 'THB', 'plan-store-test факт', '2026-02-15', $2, '2026-02-15')`, uuid.New(), rent); err != nil {
		t.Fatal(err)
	}
	before := readRule(t, s, ctx, rent)

	ok, err := s.UpdateRecurringRule(ctx, own, rent, RecurringEdit{Type: "expense", Name: "аренда", AmountMinor: 2_000_050, Currency: "RUB", Category: "транспорт"})
	if err != nil || !ok {
		t.Fatalf("правка: %v %v", ok, err)
	}
	got := readRule(t, s, ctx, rent)
	if got.name != "аренда" || got.amount != 2_000_050 || got.cur != "RUB" || got.cat != "Транспорт" || !got.updated.After(before.updated) {
		t.Errorf("правило: %+v", got)
	}
	if got.sched != before.sched {
		t.Errorf("расписание изменилось:\nдо    %s\nпосле %s", before.sched, got.sched)
	}
	var factAmount int64
	if err := s.pool.QueryRow(ctx, `SELECT ROUND(amount * 100)::bigint FROM budget_transaction WHERE recurring_id = $1`, rent).Scan(&factAmount); err != nil || factAmount != 18000 {
		t.Errorf("факт прошлого срока: %d %v", factAmount, err)
	}

	ok, err = s.UpdateRecurringRule(ctx, own, salary, RecurringEdit{Type: "income", Name: "зарплата", AmountMinor: 100, Currency: "RUB", Category: "Еда"})
	if err != nil || !ok || readRule(t, s, ctx, salary).cat != "Зарплата" {
		t.Errorf("категория поступления: ok=%v err=%v %+v", ok, err, readRule(t, s, ctx, salary))
	}

	for name, c := range map[string]struct {
		id  uuid.UUID
		typ string
	}{"выключенное": {off, "expense"}, "чужой чат": {foreign, "expense"}, "другой тип": {rent, "income"}} {
		was := readRule(t, s, ctx, c.id)
		ok, err := s.UpdateRecurringRule(ctx, own, c.id, RecurringEdit{Type: c.typ, Name: "взлом", AmountMinor: 1, Currency: "RUB"})
		if err != nil || ok {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
		if now := readRule(t, s, ctx, c.id); now != was {
			t.Errorf("%s: правило изменилось: %+v → %+v", name, was, now)
		}
	}
}

// Правка не теряет категорию записи: у поступления категория не меняется (форма шлёт
// пусто), категория другого типа и дубль по регистру остаются, если имя в форме то же.
//
// Мутации: убрать ветку direction = 'income' или ветку EXISTS у category_id — тест краснеет.
func TestUpdatePlanItem_KeepsCategory(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	own := []int64{planStoreChat}
	amt := int64(10000)
	due := d(2026, 3, 15)
	add := func(dir, catSQL string, args ...any) uuid.UUID {
		t.Helper()
		id, err := s.AddPlanItem(ctx, PlanItemInput{ChatID: planStoreChat, Direction: dir, Description: "до", AmountMinor: &amt, Currency: "RUB", DueDate: &due})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE budget_planned_expense SET category_id = (`+catSQL+`) WHERE id = $1`, append([]any{id}, args...)...); err != nil {
			t.Fatal(err)
		}
		return id
	}
	edit := func(id uuid.UUID, dir, cat string) string {
		t.Helper()
		if ok, err := s.UpdatePlanItem(ctx, own, id, PlanItemEdit{Direction: dir, Description: "после", AmountMinor: &amt, Currency: "RUB", Category: cat}); err != nil || !ok {
			t.Fatalf("правка: %v %v", ok, err)
		}
		return readPlanned(t, s, ctx, id).cat
	}

	salary := add("income", `SELECT id FROM budget_category WHERE name = 'Зарплата' AND type = 'income'`)
	if got := edit(salary, "income", ""); got != "Зарплата" {
		t.Errorf("категория поступления: %q", got)
	}
	odd := add("expense", `SELECT id FROM budget_category WHERE name = 'Зарплата' AND type = 'income'`)
	if got := edit(odd, "expense", "Зарплата"); got != "Зарплата" {
		t.Errorf("категория другого типа с тем же именем: %q", got)
	}

	// Дубли по регистру запрещает уникальный индекс из ADR-010; пока его нет, они бывают.
	lower, upper := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM budget_planned_expense WHERE category_id = ANY($1)`, []uuid.UUID{lower, upper})
		_, _ = s.pool.Exec(ctx, `DELETE FROM budget_category WHERE id = ANY($1)`, []uuid.UUID{lower, upper})
	})
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_category (id, name, type) VALUES ($1, 'planeditdup', 'expense'), ($2, 'PlanEditDup', 'expense')`, lower, upper); err != nil {
		t.Skipf("дубль по регистру не вставить (уникальный индекс): %v", err)
	}
	dup := add("expense", `SELECT $2::uuid`, lower)
	if got := edit(dup, "expense", "PlanEditDup"); got != "planeditdup" {
		t.Errorf("дубль по регистру сменил категорию: %q", got)
	}
}
