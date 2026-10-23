package budget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	aliasChat   int64 = -88201
	aliasTarget       = "plan-alias-test-цель"
	aliasOld          = "plan-alias-test-старое"
)

// aliasFixture: категория-цель и алиас на неё (как после слияния дубля).
func aliasFixture(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, ctx := rateTestStore(t)
	del := func() {
		for _, q := range []string{
			`DELETE FROM plan_category_limit WHERE chat_id = $1`,
			`DELETE FROM budget_recurring WHERE chat_id = $1`,
			`DELETE FROM budget_planned_expense WHERE chat_id = $1`,
		} {
			if _, err := s.pool.Exec(ctx, q, aliasChat); err != nil {
				t.Errorf("очистка: %v", err)
			}
		}
		if _, err := s.pool.Exec(ctx, `DELETE FROM budget_category_alias WHERE alias_lower = $1`, aliasOld); err != nil {
			t.Errorf("очистка алиаса: %v", err)
		}
		if _, err := s.pool.Exec(ctx, `DELETE FROM budget_category WHERE name = $1`, aliasTarget); err != nil {
			t.Errorf("очистка категории: %v", err)
		}
	}
	del()
	t.Cleanup(del)
	if _, err := s.pool.Exec(ctx, `
		WITH c AS (INSERT INTO budget_category (id, name, type) VALUES (gen_random_uuid(), $1, 'expense') RETURNING id)
		INSERT INTO budget_category_alias (alias_lower, type, category_id, source) SELECT $2, 'expense', id, 'merge' FROM c`,
		aliasTarget, aliasOld); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

// Имя слитой категории (алиас) в форме плана ведёт на категорию-цель, а не в NULL; имя,
// которого нет ни среди категорий, ни среди алиасов, — ErrUnknownCategory, запись не создаётся.
//
// Мутация: вернуть в AddPlanItem подзапрос LOWER(c.name) = LOWER($8) — алиас даст NULL, тест краснеет.
func TestAddPlanItem_ResolvesAliasAndRefusesUnknown(t *testing.T) {
	s, ctx := aliasFixture(t)
	amt := int64(10000)
	due := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	in := func(cat string) PlanItemInput {
		return PlanItemInput{ChatID: aliasChat, Direction: "expense", Description: "до", AmountMinor: &amt, Currency: "RUB", DueDate: &due, Category: cat}
	}
	id, err := s.AddPlanItem(ctx, in(aliasOld))
	if err != nil {
		t.Fatal(err)
	}
	if got := readPlanned(t, s, ctx, id).cat; got != aliasTarget {
		t.Errorf("категория по алиасу = %q, ждали %q", got, aliasTarget)
	}
	if _, err := s.AddPlanItem(ctx, in("plan-alias-test-нет-такой")); !errors.Is(err, ErrUnknownCategory) {
		t.Fatalf("err=%v, ждали ErrUnknownCategory", err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM budget_planned_expense WHERE chat_id = $1`, aliasChat).Scan(&n); err != nil || n != 1 {
		t.Errorf("записей %d (err %v), ждали 1: отказанная запись не должна создаваться", n, err)
	}
}

// Мутация: вернуть в keepCategorySQL подзапрос LOWER(c.name) = LOWER($7) — алиас даст NULL, тест краснеет.
func TestUpdatePlanItem_ResolvesAliasAndRefusesUnknown(t *testing.T) {
	s, ctx := aliasFixture(t)
	amt := int64(10000)
	due := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	id, err := s.AddPlanItem(ctx, PlanItemInput{ChatID: aliasChat, Direction: "expense", Description: "до", AmountMinor: &amt, Currency: "RUB", DueDate: &due})
	if err != nil {
		t.Fatal(err)
	}
	edit := func(cat string) error {
		_, err := s.UpdatePlanItem(ctx, []int64{aliasChat}, id, PlanItemEdit{Direction: "expense", Description: "после", AmountMinor: &amt, Currency: "RUB", Category: cat})
		return err
	}
	if err := edit(aliasOld); err != nil {
		t.Fatal(err)
	}
	if got := readPlanned(t, s, ctx, id); got.cat != aliasTarget || got.desc != "после" {
		t.Errorf("после правки по алиасу: %+v", got)
	}
	if err := edit("plan-alias-test-нет-такой"); !errors.Is(err, ErrUnknownCategory) {
		t.Fatalf("err=%v, ждали ErrUnknownCategory", err)
	}
	if got := readPlanned(t, s, ctx, id); got.cat != aliasTarget {
		t.Errorf("отказанная правка сбросила категорию: %+v", got)
	}
}

func TestUpdateRecurringRule_ResolvesAliasAndRefusesUnknown(t *testing.T) {
	s, ctx := aliasFixture(t)
	id := uuid.New()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO budget_recurring (id, chat_id, name, type, amount, currency, recurrence_type, day_of_month,
		                              next_date, enabled, interval_n, interval_unit, anchor_date)
		VALUES ($1, $2, 'до', 'expense', 180.00, 'THB', 'monthly', 15, '2026-03-15', true, 1, 'month', '2026-02-15')`, id, aliasChat); err != nil {
		t.Fatal(err)
	}
	edit := func(cat string) error {
		_, err := s.UpdateRecurringRule(ctx, []int64{aliasChat}, id, RecurringEdit{Type: "expense", Name: "после", AmountMinor: 18000, Currency: "THB", Category: cat})
		return err
	}
	cat := func() string {
		var c string
		if err := s.pool.QueryRow(ctx, `SELECT COALESCE(c.name, '') FROM budget_recurring r LEFT JOIN budget_category c ON c.id = r.category_id WHERE r.id = $1`, id).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if err := edit(aliasOld); err != nil {
		t.Fatal(err)
	}
	if got := cat(); got != aliasTarget {
		t.Errorf("категория правила по алиасу = %q, ждали %q", got, aliasTarget)
	}
	if err := edit("plan-alias-test-нет-такой"); !errors.Is(err, ErrUnknownCategory) {
		t.Fatalf("err=%v, ждали ErrUnknownCategory", err)
	}
	if got := cat(); got != aliasTarget {
		t.Errorf("отказанная правка сбросила категорию правила: %q", got)
	}
}

// Мутация: вернуть в AddPlanLimit подзапрос LOWER(c.name) = LOWER($2) — алиас даст ErrPlanLimitCategory, тест краснеет.
func TestAddPlanLimit_ResolvesAlias(t *testing.T) {
	s, ctx := aliasFixture(t)
	in := PlanLimitInput{ChatID: aliasChat, Category: aliasOld, AmountMinor: 100_000, Currency: "RUB", Repeat: false,
		WindowStart: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}
	if _, err := s.AddPlanLimit(ctx, in); err != nil {
		t.Fatalf("лимит по алиасу: %v", err)
	}
	got, err := s.PlanLimits(ctx, []int64{aliasChat})
	if err != nil || len(got) != 1 || got[0].Category != aliasTarget {
		t.Fatalf("лимиты=%+v err=%v, ждали один на %q", got, err, aliasTarget)
	}
	in.Category = ""
	if _, err := s.AddPlanLimit(ctx, in); !errors.Is(err, ErrPlanLimitCategory) {
		t.Fatalf("пустая категория: err=%v, ждали ErrPlanLimitCategory", err)
	}
}
