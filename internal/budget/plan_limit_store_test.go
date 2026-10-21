package budget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	limitChat       int64 = -88101
	limitCatName          = "plan-limit-test-еда"
	limitCatAnother       = "plan-limit-test-такси"
)

func limitFixture(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, ctx := rateTestStore(t)
	del := func() {
		if _, err := s.pool.Exec(ctx, `DELETE FROM plan_category_limit WHERE chat_id IN ($1, $2)`, limitChat, limitChat-1); err != nil {
			t.Errorf("очистка лимитов: %v", err)
		}
		if _, err := s.pool.Exec(ctx, `DELETE FROM budget_category WHERE name IN ($1, $2)`, limitCatName, limitCatAnother); err != nil {
			t.Errorf("очистка категорий: %v", err)
		}
	}
	del()
	t.Cleanup(del)
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_category (id, name, type) VALUES (gen_random_uuid(), $1, 'expense'), (gen_random_uuid(), $2, 'expense')`, limitCatName, limitCatAnother); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func limitIn(cat string, repeat bool) PlanLimitInput {
	return PlanLimitInput{ChatID: limitChat, Category: cat, AmountMinor: 150_000, Currency: "THB", Repeat: repeat,
		WindowStart: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}
}

func TestPlanLimits_AddReadScope(t *testing.T) {
	s, ctx := limitFixture(t)
	id, err := s.AddPlanLimit(ctx, limitIn("PLAN-LIMIT-TEST-ЕДА", true)) // регистр не важен
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.PlanLimits(ctx, []int64{limitChat})
	if err != nil || len(got) != 1 {
		t.Fatalf("limits=%v err=%v", got, err)
	}
	x := got[0]
	if x.ID != id || x.Category != limitCatName || x.AmountMinor != 150_000 || x.Currency != "THB" || !x.Repeat || x.WindowStart.Day() != 5 {
		t.Fatalf("limit=%+v", x)
	}
	if other := mustLimits(t, s, ctx, []int64{limitChat - 1}); len(other) != 0 {
		t.Fatalf("чужой чат видит лимиты: %v", other)
	}
}

func TestPlanLimits_DuplicateAndUnknownCategory(t *testing.T) {
	s, ctx := limitFixture(t)
	if _, err := s.AddPlanLimit(ctx, limitIn(limitCatName, true)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlanLimit(ctx, limitIn(limitCatName, true)); !errors.Is(err, ErrPlanLimitDuplicate) {
		t.Fatalf("err=%v, ждали ErrPlanLimitDuplicate", err)
	}
	if _, err := s.AddPlanLimit(ctx, limitIn("plan-limit-test-нет-такой", false)); !errors.Is(err, ErrPlanLimitCategory) {
		t.Fatalf("err=%v, ждали ErrPlanLimitCategory", err)
	}
}

// Правка и удаление действуют только в своей области чатов.
func TestPlanLimits_UpdateDeleteScope(t *testing.T) {
	s, ctx := limitFixture(t)
	id, err := s.AddPlanLimit(ctx, limitIn(limitCatName, false))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdatePlanLimit(ctx, []int64{limitChat - 1}, id, 1, "RUB", true, nil); err != nil || ok {
		t.Fatalf("чужой чат изменил лимит: ok=%v err=%v", ok, err)
	}
	if ok, err := s.DeletePlanLimit(ctx, []int64{limitChat - 1}, id); err != nil || ok {
		t.Fatalf("чужой чат удалил лимит: ok=%v err=%v", ok, err)
	}
	if ok, err := s.UpdatePlanLimit(ctx, []int64{limitChat}, id, 200_000, "RUB", true, nil); err != nil || !ok {
		t.Fatalf("update ok=%v err=%v", ok, err)
	}
	if got := mustLimits(t, s, ctx, []int64{limitChat}); len(got) != 1 || got[0].WindowStart.Day() != 5 {
		t.Fatalf("nil-окно изменило window_start: %+v", got)
	}
	ws := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if ok, err := s.UpdatePlanLimit(ctx, []int64{limitChat}, id, 200_000, "RUB", true, &ws); err != nil || !ok {
		t.Fatalf("update с окном ok=%v err=%v", ok, err)
	}
	if got := mustLimits(t, s, ctx, []int64{limitChat}); len(got) != 1 || got[0].WindowStart.Day() != 2 {
		t.Fatalf("окно не сменилось: %+v", got)
	}
	got := mustLimits(t, s, ctx, []int64{limitChat})
	if len(got) != 1 || got[0].AmountMinor != 200_000 || got[0].Currency != "RUB" || !got[0].Repeat || got[0].WindowStart.Day() != 2 {
		t.Fatalf("после правки: %+v", got)
	}
	if ok, err := s.DeletePlanLimit(ctx, []int64{limitChat}, id); err != nil || !ok {
		t.Fatalf("delete ok=%v err=%v", ok, err)
	}
	if ok, err := s.DeletePlanLimit(ctx, []int64{limitChat}, uuid.New()); err != nil || ok {
		t.Fatal("удалён несуществующий")
	}
}

func TestPlanLimits_UpdateToRepeatConflicts(t *testing.T) {
	s, ctx := limitFixture(t)
	if _, err := s.AddPlanLimit(ctx, limitIn(limitCatName, true)); err != nil {
		t.Fatal(err)
	}
	id, err := s.AddPlanLimit(ctx, limitIn(limitCatName, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePlanLimit(ctx, []int64{limitChat}, id, 1, "THB", true, nil); !errors.Is(err, ErrPlanLimitDuplicate) {
		t.Fatalf("err=%v, ждали ErrPlanLimitDuplicate", err)
	}
}

// История подсказки: повторяющиеся траты и текущий месяц в неё не входят.
//
// Мутация: убрать `t.recurring_id IS NULL` либо условие на текущий месяц — тест краснеет.
func TestPlanLimitHistory_ExcludesRecurringAndCurrentMonth(t *testing.T) {
	s, ctx := limitFixture(t)
	var catID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM budget_category WHERE name=$1`, limitCatName).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	rule := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_recurring (id, chat_id, name, amount, recurrence_type, next_date)
		VALUES ($1, $2, 'plan-limit-test-правило', 1, 'weekly', '2026-03-12')`, rule, limitChat); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []struct {
			sql string
			arg []any
		}{
			{`DELETE FROM budget_transaction WHERE description LIKE 'plan-limit-test%'`, nil},
			{`DELETE FROM budget_recurring WHERE id=$1`, []any{rule}},
		} {
			if _, err := s.pool.Exec(ctx, q.sql, q.arg...); err != nil {
				t.Errorf("очистка: %v", err)
			}
		}
	})
	ins := func(amount float64, date string, rec *uuid.UUID) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, category_id, description, transaction_date, recurring_id)
			VALUES (gen_random_uuid(), 'expense', $1, 'THB', $2, 'plan-limit-test', $3::date, $4)`, amount, catID, date, rec); err != nil {
			t.Fatal(err)
		}
	}
	ins(100, "2025-01-10", nil)
	ins(50, "2025-01-20", nil)    // тот же месяц — суммируется
	ins(999, "2025-02-10", &rule) // повторяющаяся — исключена
	ins(300, "2025-03-05", nil)
	ins(777, time.Now().UTC().Format("2006-01-02"), nil) // текущий месяц — исключён

	rows, err := s.PlanLimitHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range rows {
		if r.CategoryName == limitCatName {
			got[r.Month.Format("2006-01")] += r.Total
		}
	}
	if len(got) != 2 || got["2025-01"] != 150 || got["2025-03"] != 300 {
		t.Fatalf("история=%v, ждали {2025-01:150 2025-03:300}", got)
	}
}

func mustLimits(t *testing.T, s *Store, ctx context.Context, ids []int64) []PlanLimit {
	t.Helper()
	got, err := s.PlanLimits(ctx, ids)
	if err != nil {
		t.Fatalf("PlanLimits: %v", err)
	}
	return got
}
