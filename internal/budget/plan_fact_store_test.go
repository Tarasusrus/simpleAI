package budget

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Правка факта: меняются название, сумма и дата; валюта, тип и категория остаются.
// Не от экрана (бот), факт правила и несуществующий id ничего не меняют.
//
// Мутации: убрать added_by = 'web' или recurring_id IS NULL из WHERE — краснеет
// кейс «чужой факт» / «факт правила»; поменять поле SET (например, не писать
// transaction_date) — краснеет проверка даты.
func TestUpdateWebFact(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	newDay := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
	rule := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_recurring (id, chat_id, name, amount, recurrence_type, day_of_month, next_date)
		VALUES ($1, $2, 'plan-store-test rule', 1, 'monthly', 1, '2026-03-01')`, rule, planStoreChat); err != nil {
		t.Fatal(err)
	}
	web, bot, fromRule := uuid.New(), uuid.New(), uuid.New()
	for _, r := range []struct {
		id    uuid.UUID
		by    string
		rule  *uuid.UUID
		sched *time.Time
	}{{web, "web", nil, nil}, {bot, "bot", nil, nil}, {fromRule, "web", &rule, &day}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, description, transaction_date, added_by, recurring_id, scheduled_for)
			VALUES ($1, 'expense', 10, 'RUB', 'plan-store-test fact', $2, $3, $4, $5)`, r.id, day, r.by, r.rule, r.sched); err != nil {
			t.Fatal(err)
		}
	}
	edit := FactEdit{Description: "plan-store-test edited", AmountMinor: 90000, Date: newDay}

	ok, err := s.UpdateWebFact(ctx, web, edit)
	if err != nil || !ok {
		t.Fatalf("правка факта экрана: %v %v", ok, err)
	}
	var desc, cur string
	var minor int64
	var date time.Time
	if err := s.pool.QueryRow(ctx, `SELECT description, ROUND(amount*100)::bigint, currency, transaction_date FROM budget_transaction WHERE id = $1`, web).
		Scan(&desc, &minor, &cur, &date); err != nil {
		t.Fatal(err)
	}
	if desc != edit.Description || minor != 90000 || cur != "RUB" || date.Format("2006-01-02") != "2026-03-12" {
		t.Errorf("факт после правки: %q %d %s %s", desc, minor, cur, date.Format("2006-01-02"))
	}
	for name, id := range map[string]uuid.UUID{"факт бота": bot, "факт правила": fromRule, "нет факта": uuid.New()} {
		if ok, err := s.UpdateWebFact(ctx, id, edit); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v, ждали false без ошибки", name, ok, err)
		}
	}
	var untouched int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM budget_transaction WHERE id = ANY($1) AND description = 'plan-store-test fact'`,
		[]uuid.UUID{bot, fromRule}).Scan(&untouched); err != nil || untouched != 2 {
		t.Errorf("факты бота и правила изменены: %d %v", untouched, err)
	}
}

// Возврат в план: факт удаляется, плановая запись снова открыта — одной транзакцией.
// Старый факт без planned_id, факт бота, факт правила, чужой чат плановой записи и
// повторный вызов не меняют ничего.
//
// Мутации: убрать planned_id IS NOT NULL / added_by / recurring_id из SELECT, фильтр
// chat_id или AND settled из UPDATE — краснеет соответствующий кейс; убрать DELETE —
// краснеет проверка «факта нет»; вернуть DELETE до UPDATE без отката — краснеет «чужой чат».
func TestUnsettlePlanItem(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	amt := int64(80600)
	own, foreignPlan, legacy, botPlan := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, r := range []struct {
		id   uuid.UUID
		chat int64
	}{{own, planStoreChat}, {foreignPlan, planStoreChat - 1}, {legacy, planStoreChat}, {botPlan, planStoreChat}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_planned_expense (id, chat_id, direction, description, amount, currency, settled)
			VALUES ($1, $2, 'expense', 'plan-store-test', $3::bigint::numeric/100, 'THB', true)`, r.id, r.chat, amt); err != nil {
			t.Fatal(err)
		}
	}
	rule := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_recurring (id, chat_id, name, amount, recurrence_type, day_of_month, next_date)
		VALUES ($1, $2, 'plan-store-test rule', 1, 'monthly', 1, '2026-03-01')`, rule, planStoreChat); err != nil {
		t.Fatal(err)
	}
	facts := map[string]uuid.UUID{"own": uuid.New(), "foreign": uuid.New(), "legacy": uuid.New(), "bot": uuid.New(), "rule": uuid.New()}
	insert := func(id uuid.UUID, by string, planned *uuid.UUID, ruleID *uuid.UUID, sched *time.Time) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, description, transaction_date, added_by, planned_id, recurring_id, scheduled_for)
			VALUES ($1, 'expense', 806, 'THB', 'plan-store-test fact', $2, $3, $4, $5, $6)`, id, day, by, planned, ruleID, sched); err != nil {
			t.Fatal(err)
		}
	}
	insert(facts["own"], "web", &own, nil, nil)
	insert(facts["foreign"], "web", &foreignPlan, nil, nil)
	insert(facts["legacy"], "web", nil, nil, nil)
	insert(facts["bot"], "bot", &botPlan, nil, nil)
	insert(facts["rule"], "web", &own, &rule, &day)
	scope := []int64{planStoreChat}

	for _, name := range []string{"foreign", "legacy", "bot", "rule"} {
		if ok, err := s.UnsettlePlanItem(ctx, scope, facts[name]); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v, ждали false без ошибки", name, ok, err)
		}
	}
	var left int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM budget_transaction WHERE id = ANY($1)`,
		[]uuid.UUID{facts["foreign"], facts["legacy"], facts["bot"], facts["rule"]}).Scan(&left); err != nil || left != 4 {
		t.Errorf("отказ удалил факты: осталось %d из 4 (%v)", left, err)
	}
	var stillSettled bool
	if err := s.pool.QueryRow(ctx, `SELECT settled FROM budget_planned_expense WHERE id = $1`, foreignPlan).Scan(&stillSettled); err != nil || !stillSettled {
		t.Errorf("запись чужого чата открыта: %v %v", stillSettled, err)
	}

	ok, err := s.UnsettlePlanItem(ctx, scope, facts["own"])
	if err != nil || !ok {
		t.Fatalf("возврат своего факта: %v %v", ok, err)
	}
	var settled bool
	if err := s.pool.QueryRow(ctx, `SELECT settled FROM budget_planned_expense WHERE id = $1`, own).Scan(&settled); err != nil || settled {
		t.Errorf("запись не открыта: settled=%v %v", settled, err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM budget_transaction WHERE id = $1`, facts["own"]).Scan(&n); err != nil || n != 0 {
		t.Errorf("факт не удалён: %d %v", n, err)
	}
	if ok, err := s.UnsettlePlanItem(ctx, scope, facts["own"]); ok || err != nil {
		t.Errorf("повторный возврат: ok=%v err=%v, ждали false без ошибки", ok, err)
	}
	// Закрыть заново можно, и факт снова привязан к той же записи.
	if ok, err := s.SettlePlanItem(ctx, scope, own, nil, day); !ok || err != nil {
		t.Fatalf("повторное закрытие: %v %v", ok, err)
	}
}
