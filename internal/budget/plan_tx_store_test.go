package budget

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Список трат категории: те же факты, что дают Spent, паритет lower(btrim()) с plan.CategoryKey на кириллице,
// исключения (повторяющиеся, другой тип, другая категория, вне окна), keyset без пропусков и повторов.
//
// Мутации: убрать recurring_id IS NULL; убрать фильтр type; убрать границы окна; сравнивать имя без lower;
// убрать условие продолжения keyset; поменять порядок id — проверки краснеют.
func TestPlanCategoryTransactions_ParityWindowKeyset(t *testing.T) {
	s, ctx := limitFixture(t)
	var catID, otherID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM budget_category WHERE name=$1`, limitCatName).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT id FROM budget_category WHERE name=$1`, limitCatAnother).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	rule := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_recurring (id, chat_id, name, amount, recurrence_type, next_date)
		VALUES ($1, $2, 'plan-tx-test-правило', 1, 'weekly', '2026-03-12')`, rule, limitChat); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM budget_transaction WHERE description LIKE 'plan-tx-test%'`} {
			if _, err := s.pool.Exec(ctx, q); err != nil {
				t.Errorf("очистка: %v", err)
			}
		}
		if _, err := s.pool.Exec(ctx, `DELETE FROM budget_recurring WHERE id=$1`, rule); err != nil {
			t.Errorf("очистка правила: %v", err)
		}
	})
	ids := map[string]uuid.UUID{}
	ins := func(name string, typ string, cat uuid.UUID, date string, rec *uuid.UUID, cur string) {
		t.Helper()
		id := uuid.New()
		ids[name] = id
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, category_id, description, transaction_date, recurring_id)
			VALUES ($1, $2, 10.5, $3, $4, $5, $6::date, $7)`, id, typ, cur, cat, "plan-tx-test-"+name, date, rec); err != nil {
			t.Fatal(err)
		}
	}
	ins("a", "expense", catID, "2026-03-09", nil, "THB")
	ins("b", "expense", catID, "2026-03-09", nil, "RUB") // та же дата — порядок по id
	ins("c", "expense", catID, "2026-03-08", nil, "THB")
	ins("d", "expense", catID, "2026-03-05", nil, "THB") // первый день окна — входит
	ins("e", "expense", catID, "2026-03-18", nil, "THB") // последний день — входит
	ins("x-before", "expense", catID, "2026-03-04", nil, "THB")
	ins("x-after", "expense", catID, "2026-03-19", nil, "THB")
	ins("x-rule", "expense", catID, "2026-03-10", &rule, "THB")
	ins("x-income", "income", catID, "2026-03-10", nil, "THB")
	ins("x-other", "expense", otherID, "2026-03-10", nil, "THB")
	ins("x-usd", "expense", catID, "2026-03-10", nil, "USD")

	from, to := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC)
	// имя в другом регистре и с пробелами — тот же ключ
	q := PlanCategoryTxQuery{Category: "  " + strings.ToUpper(limitCatName) + " ", From: from, To: to, Limit: 100}
	all, err := s.PlanCategoryTransactions(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(all))
	for _, r := range all {
		got = append(got, r.Description)
	}
	// дата по убыванию, при равенстве — id по возрастанию
	first, second := "plan-tx-test-a", "plan-tx-test-b"
	if ids["b"].String() < ids["a"].String() {
		first, second = second, first
	}
	want := []string{"plan-tx-test-e", first, second, "plan-tx-test-c", "plan-tx-test-d"}
	if len(got) != len(want) {
		t.Fatalf("строки %v, ждали %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("строки %v, ждали %v", got, want)
		}
	}

	// keyset по одной строке: те же пять в том же порядке, без пропусков и повторов
	var paged []string
	q.Limit = 1
	for i := 0; i < 10; i++ {
		page, err := s.PlanCategoryTransactions(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		paged = append(paged, page[0].Description)
		d := page[0].Date
		id := page[0].ID
		q.AfterDate, q.AfterID = &d, &id
	}
	if len(paged) != len(want) {
		t.Fatalf("постранично %v, ждали %v", paged, want)
	}
	for i := range want {
		if paged[i] != want[i] {
			t.Fatalf("постранично %v, ждали %v", paged, want)
		}
	}

	// неизвестная категория — пусто
	q = PlanCategoryTxQuery{Category: "нет-такой-xyz", From: from, To: to, Limit: 10}
	if rows, err := s.PlanCategoryTransactions(ctx, q); err != nil || len(rows) != 0 {
		t.Fatalf("неизвестная категория: %v %v", rows, err)
	}
}
