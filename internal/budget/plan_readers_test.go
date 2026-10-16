package budget

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

const planTestChat int64 = -88001 // чат, которого нет в боевых данных

func cleanPlanTestRows(t *testing.T, s *Store, ctx context.Context) {
	t.Helper()
	del := func() {
		for _, q := range []string{
			`DELETE FROM budget_recurring WHERE chat_id = $1`,
			`DELETE FROM budget_planned_expense WHERE chat_id = $1`,
		} {
			if _, err := s.pool.Exec(ctx, q, planTestChat); err != nil {
				t.Errorf("очистка тестовых строк: %v", err)
			}
		}
	}
	del()
	t.Cleanup(del)
}

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

// Правило бота обязано попасть и в старые, и в новые колонки (C15).
//
// Мутация: убрать interval_unit/anchor_date из INSERT AddRecurring — тест краснеет.
func TestAddRecurring_WritesBothColumnGroups(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanTestRows(t, s, ctx)

	day31, day10 := 31, 10
	cases := []struct {
		name       string
		r          RecurringPayment
		wantUnit   string
		wantAnchor time.Time
	}{
		{"месяц, день 31 в феврале", RecurringPayment{RecurrenceType: "monthly", DayOfMonth: &day31, NextDate: d(2026, 2, 15)}, "month", d(2026, 2, 28)},
		{"месяц, день 10", RecurringPayment{RecurrenceType: "monthly", DayOfMonth: &day10, NextDate: d(2026, 3, 10)}, "month", d(2026, 3, 10)},
		{"месяц без дня", RecurringPayment{RecurrenceType: "monthly", NextDate: d(2026, 3, 11)}, "month", d(2026, 3, 11)},
		{"неделя", RecurringPayment{RecurrenceType: "weekly", NextDate: d(2026, 3, 12)}, "week", d(2026, 3, 12)},
		{"день", RecurringPayment{RecurrenceType: "daily", NextDate: d(2026, 3, 13)}, "day", d(2026, 3, 13)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.r.ID, tc.r.ChatID, tc.r.Name, tc.r.Amount = uuid.New(), planTestChat, tc.name, 100
			if err := s.AddRecurring(ctx, tc.r); err != nil {
				t.Fatalf("AddRecurring: %v", err)
			}
			var (
				unit   string
				n      int
				anchor time.Time
				recT   string
				next   time.Time
			)
			if err := s.pool.QueryRow(ctx,
				`SELECT interval_unit, interval_n, anchor_date, recurrence_type, next_date FROM budget_recurring WHERE id = $1`,
				tc.r.ID).Scan(&unit, &n, &anchor, &recT, &next); err != nil {
				t.Fatalf("select: %v", err)
			}
			if unit != tc.wantUnit || n != 1 || !anchor.Equal(tc.wantAnchor) {
				t.Errorf("новые колонки: unit=%s n=%d anchor=%v, ждали %s/1/%v", unit, n, anchor, tc.wantUnit, tc.wantAnchor)
			}
			if recT != tc.r.RecurrenceType || !next.Equal(tc.r.NextDate) {
				t.Errorf("старые колонки: %s %v, ждали %s %v", recT, next, tc.r.RecurrenceType, tc.r.NextDate)
			}
		})
	}
}

// Воркер исполняет только расходы: повторяющееся поступление создаёт факт кнопка
// «пришло», а не старый воркер (C14).
//
// Мутация: убрать r.type = 'expense' из GetDueRecurring — тест краснеет.
func TestGetDueRecurring_OnlyExpenses(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanTestRows(t, s, ctx)

	exp, inc := uuid.New(), uuid.New()
	for _, r := range []RecurringPayment{
		{ID: exp, Type: "expense", Name: "аренда"},
		{ID: inc, Type: "income", Name: "аванс"},
	} {
		r.ChatID, r.Amount, r.RecurrenceType, r.NextDate = planTestChat, 100, "monthly", d(2026, 3, 1)
		if err := s.AddRecurring(ctx, r); err != nil {
			t.Fatalf("AddRecurring: %v", err)
		}
	}
	due, err := s.GetDueRecurring(ctx, d(2026, 3, 2))
	if err != nil {
		t.Fatalf("GetDueRecurring: %v", err)
	}
	var gotExp, gotInc bool
	for _, r := range due {
		gotExp = gotExp || r.ID == exp
		gotInc = gotInc || r.ID == inc
	}
	if !gotExp {
		t.Error("расход не попал в список к исполнению")
	}
	if gotInc {
		t.Error("поступление попало к исполнению воркером")
	}
}

// Читатели бота не видят плановые поступления и платежи без суммы: на «свободно»
// они не влияют (AC-8.5).
//
// Мутация: убрать direction/amount-фильтр из любого из двух запросов — тест краснеет.
func TestPlannedReaders_IgnoreIncomeAndUnpriced(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanTestRows(t, s, ctx)

	for _, q := range []string{
		`INSERT INTO budget_planned_expense (chat_id, amount, currency, description) VALUES ($1, 1000, 'THB', 'обычный')`,
		`INSERT INTO budget_planned_expense (chat_id, amount, currency, description, direction) VALUES ($1, 5000, 'THB', 'аванс', 'income')`,
		`INSERT INTO budget_planned_expense (chat_id, currency, description) VALUES ($1, 'THB', 'Права')`,
	} {
		if _, err := s.pool.Exec(ctx, q, planTestChat); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	items, err := s.ListPlannedExpenses(ctx, planTestChat)
	if err != nil {
		t.Fatalf("ListPlannedExpenses: %v", err)
	}
	if len(items) != 1 || items[0].Description != "обычный" {
		t.Errorf("список %+v, ждали одну запись «обычный»", items)
	}
	sum, cnt, err := s.PlannedExpensesTHB(ctx, planTestChat, map[string]float64{"THB": 2.5, "RUB": 1})
	if err != nil {
		t.Fatalf("PlannedExpensesTHB: %v", err)
	}
	if sum != 1000 || cnt != 1 {
		t.Errorf("сумма %v, число %d; ждали 1000 и 1", sum, cnt)
	}
}
