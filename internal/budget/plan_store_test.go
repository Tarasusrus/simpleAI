package budget

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

const planStoreChat int64 = -88002

func cleanPlanStore(t *testing.T, s *Store, ctx context.Context) {
	t.Helper()
	del := func() {
		if _, err := s.pool.Exec(ctx, `DELETE FROM budget_transaction WHERE description LIKE 'plan-store-test%'`); err != nil {
			t.Errorf("очистка транзакций: %v", err)
		}
		for _, q := range []string{
			`DELETE FROM budget_recurring WHERE chat_id IN ($1, $2)`,
			`DELETE FROM budget_planned_expense WHERE chat_id IN ($1, $2)`,
		} {
			if _, err := s.pool.Exec(ctx, q, planStoreChat, planStoreChat-1); err != nil {
				t.Errorf("очистка: %v", err)
			}
		}
	}
	del()
	t.Cleanup(del)
}

// Деньги уходят целыми минимальными единицами без потерь: 1234.56 → 123456.
//
// Мутация: заменить ROUND(t.amount * 100) на t.amount::bigint — тест краснеет.
func TestPlanTransactions_ExactMinorUnits(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	id := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, description, transaction_date)
		VALUES ($1, 'expense', 1234.56, 'THB', 'plan-store-test', '2026-03-02')`, id); err != nil {
		t.Fatal(err)
	}
	txs, err := s.PlanTransactions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range txs {
		if x.ID == id {
			if x.AmountMinor != 123456 || x.Currency != "THB" {
				t.Errorf("сумма %d %s, ждали 123456 THB", x.AmountMinor, x.Currency)
			}
			return
		}
	}
	t.Fatal("транзакция не прочитана")
}

// Правило со старыми колонками (NULL в новых, как после отката бинаря) читается
// по выводимым значениям; чужой чат и выключенное правило не видны.
//
// Мутация: убрать COALESCE у interval_unit/anchor_date или фильтр chat_id — тест краснеет.
func TestPlanRecurringRules_FallbackAndScope(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	own, foreign, off := uuid.New(), uuid.New(), uuid.New()
	for _, r := range []struct {
		id      uuid.UUID
		chat    int64
		enabled bool
	}{{own, planStoreChat, true}, {foreign, planStoreChat - 1, true}, {off, planStoreChat, false}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_recurring (id, chat_id, name, amount, recurrence_type, day_of_month, next_date, enabled)
			VALUES ($1, $2, 'правило', 100.50, 'weekly', NULL, '2026-03-12', $3)`, r.id, r.chat, r.enabled); err != nil {
			t.Fatal(err)
		}
	}
	rules, err := s.PlanRecurringRules(ctx, []int64{planStoreChat})
	if err != nil {
		t.Fatal(err)
	}
	var got *PlanRecurring
	for i := range rules {
		switch rules[i].ID {
		case own:
			got = &rules[i]
		case foreign, off:
			t.Errorf("увидели чужое или выключенное правило %s", rules[i].ID)
		}
	}
	if got == nil {
		t.Fatal("своё правило не прочитано")
	}
	if got.IntervalUnit != "week" || got.Anchor.Format("2006-01-02") != "2026-03-12" || got.AmountMinor != 10050 || got.IntervalN != 1 {
		t.Errorf("правило %+v: ждали week / 2026-03-12 / 10050 / 1", *got)
	}
}

// Плановые: оба направления и без суммы, но без закрытых и без чужого чата.
//
// Мутация: убрать NOT p.settled или фильтр chat_id — тест краснеет.
func TestPlanPlannedItems_AllDirectionsScopedAndOpen(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	for _, q := range []struct {
		chat    int64
		desc    string
		dir     string
		amt     any
		settled bool
	}{
		{planStoreChat, "Права", "expense", nil, false},
		{planStoreChat, "премия", "income", 5000.00, false},
		{planStoreChat, "закрытый", "expense", 10.0, true},
		{planStoreChat - 1, "чужой", "expense", 10.0, false},
	} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_planned_expense (chat_id, description, direction, amount, settled)
			VALUES ($1, $2, $3, $4, $5)`, q.chat, q.desc, q.dir, q.amt, q.settled); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.PlanPlannedItems(ctx, []int64{planStoreChat})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]PlanPlanned{}
	for _, x := range items {
		names[x.Description] = x
	}
	if len(names) != 2 || names["Права"].AmountMinor != nil || names["премия"].Direction != "income" || *names["премия"].AmountMinor != 500000 {
		t.Errorf("прочитано: %+v", names)
	}
}

func TestPlanReservePct_Default(t *testing.T) {
	s, ctx := rateTestStore(t)
	pct, err := s.PlanReservePct(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("резерв вне 0..100: %d", pct)
	}
}

// Запись плановой записи: сумма в минимальных единицах, категория по имени без
// регистра, свой чат; удаление — только из своих чатов.
//
// Мутация: убрать chat_id = ANY($2) из DeletePlanItem — тест краснеет.
func TestPlanItems_AddReadDeleteScope(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)

	amt := int64(123456)
	ws := d(2026, 3, 19)
	id, err := s.AddPlanItem(ctx, PlanItemInput{
		ChatID: planStoreChat, Direction: "expense", Description: "корм", AmountMinor: &amt, Currency: "THB",
		Category: "еда", WindowStart: &ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	noAmt, err := s.AddPlanItem(ctx, PlanItemInput{ChatID: planStoreChat, Direction: "income", Description: "премия", Currency: "RUB", DueDate: &ws})
	if err != nil {
		t.Fatal(err)
	}

	items, err := s.PlanPlannedItems(ctx, []int64{planStoreChat})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uuid.UUID]PlanPlanned{}
	for _, x := range items {
		byID[x.ID] = x
	}
	got := byID[id]
	if got.AmountMinor == nil || *got.AmountMinor != 123456 || got.Currency != "THB" || got.WindowStart == nil || got.DueDate != nil {
		t.Errorf("запись: %+v", got)
	}
	if got.Category == "" {
		t.Errorf("категория «еда» не нашлась без учёта регистра (нужна в сид-данных): %+v", got)
	}
	if byID[noAmt].Direction != "income" || byID[noAmt].AmountMinor != nil {
		t.Errorf("поступление без суммы: %+v", byID[noAmt])
	}

	// Чужая область удалить не может; своя — может.
	ok, err := s.DeletePlanItem(ctx, []int64{planStoreChat - 1}, id)
	if err != nil || ok {
		t.Errorf("чужой чат удалил запись: ok=%v err=%v", ok, err)
	}
	ok, err = s.DeletePlanItem(ctx, []int64{planStoreChat}, id)
	if err != nil || !ok {
		t.Errorf("своя область не смогла удалить: ok=%v err=%v", ok, err)
	}
	ok, err = s.DeletePlanItem(ctx, []int64{planStoreChat}, id)
	if err != nil || ok {
		t.Errorf("повторное удаление: ok=%v err=%v", ok, err)
	}
}

// Закрытие плановой записи: факт с суммой из записи (или из запроса, если её не было),
// запись закрыта; повтор, чужой чат и запись без суммы без ввода ничего не пишут.
//
// Мутация: убрать UPDATE ... settled, фильтр chat_id или NOT settled — тест краснеет.
func TestSettlePlanItem(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	amt := int64(123456)
	withSum, noSum, foreign := uuid.New(), uuid.New(), uuid.New()
	for _, r := range []struct {
		id   uuid.UUID
		chat int64
		sum  *int64
		dir  string
	}{{withSum, planStoreChat, &amt, "income"}, {noSum, planStoreChat, nil, "expense"}, {foreign, planStoreChat - 1, &amt, "expense"}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_planned_expense (id, chat_id, direction, description, amount, currency)
			VALUES ($1, $2, $3, 'plan-store-test', CASE WHEN $4::bigint IS NULL THEN NULL ELSE $4::bigint::numeric/100 END, 'THB')`,
			r.id, r.chat, r.dir, r.sum); err != nil {
			t.Fatal(err)
		}
	}
	scope := []int64{planStoreChat}

	ok, err := s.SettlePlanItem(ctx, scope, withSum, nil, day)
	if err != nil || !ok {
		t.Fatalf("закрытие: %v %v", ok, err)
	}
	var typ, cur string
	var minor int64
	var added *string
	if err := s.pool.QueryRow(ctx, `SELECT type, ROUND(amount*100)::bigint, currency, added_by FROM budget_transaction
		WHERE description = 'plan-store-test' AND transaction_date = $1`, day).Scan(&typ, &minor, &cur, &added); err != nil {
		t.Fatalf("факт не записан: %v", err)
	}
	if typ != "income" || minor != 123456 || cur != "THB" || added == nil || *added != "web" {
		t.Errorf("факт: %s %d %s %v", typ, minor, cur, added)
	}
	if ok, err := s.SettlePlanItem(ctx, scope, withSum, nil, day); ok || err != nil {
		t.Errorf("повторное закрытие: %v %v", ok, err)
	}
	if ok, err := s.SettlePlanItem(ctx, scope, foreign, nil, day); ok || err != nil {
		t.Errorf("запись чужого чата: %v %v", ok, err)
	}
	if ok, err := s.SettlePlanItem(ctx, scope, noSum, nil, day); ok || err == nil {
		t.Errorf("запись без суммы закрыта без ввода: %v %v", ok, err)
	}
	var open bool
	if err := s.pool.QueryRow(ctx, `SELECT NOT settled FROM budget_planned_expense WHERE id = $1`, noSum).Scan(&open); err != nil || !open {
		t.Errorf("неудачное закрытие изменило запись: %v %v", open, err)
	}
	ok, err = s.SettlePlanItem(ctx, scope, noSum, &PlanAmount{Minor: 5000, Currency: "RUB"}, day)
	if err != nil || !ok {
		t.Fatalf("закрытие с суммой: %v %v", ok, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT ROUND(amount*100)::bigint, currency FROM budget_transaction
		WHERE description = 'plan-store-test' AND type = 'expense'`).Scan(&minor, &cur); err != nil || minor != 5000 || cur != "RUB" {
		t.Errorf("введённая сумма: %d %s %v", minor, cur, err)
	}
}

// «Пропустить этот раз»: срок попадает в skipped_dates один раз; чужое правило не трогаем.
//
// Мутация: убрать проверку ANY(skipped_dates) или фильтр chat_id — тест краснеет.
func TestSkipRecurringTerm(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	own, foreign := uuid.New(), uuid.New()
	for _, r := range []struct {
		id   uuid.UUID
		chat int64
	}{{own, planStoreChat}, {foreign, planStoreChat - 1}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_recurring (id, chat_id, name, amount, recurrence_type, next_date)
			VALUES ($1, $2, 'правило', 100, 'weekly', '2026-03-12')`, r.id, r.chat); err != nil {
			t.Fatal(err)
		}
	}
	term := time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if ok, err := s.SkipRecurringTerm(ctx, []int64{planStoreChat}, own, term); err != nil || !ok {
			t.Fatalf("пропуск #%d: %v %v", i, ok, err)
		}
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT cardinality(skipped_dates) FROM budget_recurring WHERE id = $1`, own).Scan(&n); err != nil || n != 1 {
		t.Errorf("в skipped_dates %d срок(ов), ждали 1: %v", n, err)
	}
	if ok, err := s.SkipRecurringTerm(ctx, []int64{planStoreChat}, foreign, term); ok || err != nil {
		t.Errorf("чужое правило: %v %v", ok, err)
	}
}

// Перенос планового расхода: в окно, на дату, обратно «без даты»; чужой чат,
// закрытая запись и поступление не переносятся; дата и окно вместе отвергаются.
//
// Мутация: убрать NOT settled, direction = 'expense' или фильтр chat_id — тест краснеет.
func TestMovePlanItem(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	own, foreign, settled, income := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, r := range []struct {
		id      uuid.UUID
		chat    int64
		dir     string
		settled bool
	}{{own, planStoreChat, "expense", false}, {foreign, planStoreChat - 1, "expense", false},
		{settled, planStoreChat, "expense", true}, {income, planStoreChat, "income", false}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_planned_expense (id, chat_id, direction, description, amount, currency, settled)
			VALUES ($1, $2, $3, 'plan-store-test', 100, 'THB', $4)`, r.id, r.chat, r.dir, r.settled); err != nil {
			t.Fatal(err)
		}
	}
	scope := []int64{planStoreChat}
	day := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	read := func(id uuid.UUID) (due, win *time.Time) {
		if err := s.pool.QueryRow(ctx, `SELECT due_date, window_start FROM budget_planned_expense WHERE id = $1`, id).Scan(&due, &win); err != nil {
			t.Fatal(err)
		}
		return
	}
	if ok, err := s.MovePlanItem(ctx, scope, own, nil, &day); err != nil || !ok {
		t.Fatalf("в окно: %v %v", ok, err)
	}
	if due, win := read(own); due != nil || win == nil || !win.Equal(day) {
		t.Errorf("в окно: due=%v window=%v", due, win)
	}
	if ok, err := s.MovePlanItem(ctx, scope, own, &day, nil); err != nil || !ok {
		t.Fatalf("на дату: %v %v", ok, err)
	}
	if due, win := read(own); win != nil || due == nil || !due.Equal(day) {
		t.Errorf("на дату должно очистить окно: due=%v window=%v", due, win)
	}
	if ok, err := s.MovePlanItem(ctx, scope, own, nil, nil); err != nil || !ok {
		t.Fatalf("без даты: %v %v", ok, err)
	}
	if due, win := read(own); due != nil || win != nil {
		t.Errorf("без даты: due=%v window=%v", due, win)
	}
	for name, id := range map[string]uuid.UUID{"чужой чат": foreign, "закрытая": settled, "поступление": income} {
		if ok, err := s.MovePlanItem(ctx, scope, id, &day, nil); ok || err != nil {
			t.Errorf("%s перенесена: %v %v", name, ok, err)
		}
	}
	if _, err := s.MovePlanItem(ctx, scope, own, &day, &day); err == nil {
		t.Error("дата и окно вместе приняты")
	}
}
