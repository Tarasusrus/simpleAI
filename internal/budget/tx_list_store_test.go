package budget

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedTxs кладёт расходы с описанием «plan-store-test N» и датой от 1 до n октября; возвращает id в порядке списка
// (дата по убыванию, при равных датах id по возрастанию).
func seedTxs(t *testing.T, s *Store, cat uuid.UUID, days []int) []uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var ids []uuid.UUID
	for i, d := range days {
		id := uuid.New()
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_transaction (id, type, amount, currency, category_id, description, transaction_date, added_by)
			VALUES ($1, 'expense', 10, 'THB', $2, $3, $4, 'bot')`, id, cat, fmt.Sprintf("plan-store-test Шлем %d", i), time.Date(2099, 10, d, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func txIDs(rows []TxRow) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// Список: фильтр по подстроке без учёта регистра, по категории и датам; keyset-продолжение не теряет и не дублирует
// строки, в том числе при равных датах.
//
// Мутации: убрать lower() из фильтра текста, заменить < на <= в курсоре (дубль на границе), убрать условие по id при
// равных датах (потеря строк), убрать фильтр категории — краснеют соответствующие проверки.
func TestListTx(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	cat, other := uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{cat: "plan-store-test A", other: "plan-store-test B"} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO budget_category (id, name, type) VALUES ($1, $2, 'expense')`, id, name); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		s.pool.Exec(ctx, `DELETE FROM budget_transaction WHERE category_id = ANY($1)`, []uuid.UUID{cat, other}) //nolint:errcheck // очистка после теста
		s.pool.Exec(ctx, `DELETE FROM budget_category WHERE id = ANY($1)`, []uuid.UUID{cat, other})             //nolint:errcheck // очистка после теста
	})
	seedTxs(t, s, cat, []int{5, 5, 5, 3, 1}) // три строки в один день — граница курсора
	seedTxs(t, s, other, []int{4})

	all, err := s.ListTx(ctx, TxListQuery{Category: " PLAN-store-test a ", Limit: 100})
	if err != nil || len(all) != 5 {
		t.Fatalf("список категории: %d %v", len(all), err)
	}
	if all[0].CategoryName != "plan-store-test A" {
		t.Errorf("имя категории не подставлено: %q", all[0].CategoryName)
	}
	// Постраничный обход по 2 строки собирает ровно тот же список.
	var got []TxRow
	q := TxListQuery{Category: "plan-store-test A", Limit: 2}
	for page := 0; page < 10; page++ {
		rows, err := s.ListTx(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, rows...)
		if len(rows) < q.Limit {
			break
		}
		last := rows[len(rows)-1]
		q.AfterDate, q.AfterID = &last.Date, &last.ID
	}
	if fmt.Sprint(txIDs(got)) != fmt.Sprint(txIDs(all)) {
		t.Errorf("постраничный обход разошёлся с полным списком:\n%v\n%v", txIDs(got), txIDs(all))
	}
	if rows, _ := s.ListTx(ctx, TxListQuery{Text: "ШЛЕМ 3", Limit: 100}); len(rows) != 1 {
		t.Errorf("поиск «ШЛЕМ 3» без учёта регистра: %d", len(rows))
	}
	from, to := time.Date(2099, 10, 3, 0, 0, 0, 0, time.UTC), time.Date(2099, 10, 4, 0, 0, 0, 0, time.UTC)
	if rows, _ := s.ListTx(ctx, TxListQuery{Text: "plan-store-test", From: &from, To: &to, Limit: 100}); len(rows) != 2 {
		t.Errorf("фильтр дат 3–4 октября: %d, ждали 2 (одна в A, одна в B)", len(rows))
	}
}

// Окно вокруг строки: до before более новых, сама строка в центре, до after более старых; порядок как в списке.
// Строка на краю списка и несуществующий id не ломают запрос.
//
// Мутации: убрать reverse у «новых» — порядок нарушен; заменить id < на id <= в «новых» — строка задвоена.
func TestAroundTx(t *testing.T) {
	s, ctx := rateTestStore(t)
	cleanPlanStore(t, s, ctx)
	cat := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO budget_category (id, name, type) VALUES ($1, 'plan-store-test C', 'expense')`, cat); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.pool.Exec(ctx, `DELETE FROM budget_transaction WHERE category_id = $1`, cat) //nolint:errcheck // очистка после теста
		s.pool.Exec(ctx, `DELETE FROM budget_category WHERE id = $1`, cat)             //nolint:errcheck // очистка после теста
	})
	ids := seedTxs(t, s, cat, []int{9, 8, 7, 7, 7, 5, 4, 3}) // порядок списка: 9, 8, три за 7 (по id), 5, 4, 3
	all, err := s.ListTx(ctx, TxListQuery{Category: "plan-store-test C", Limit: 100})
	if err != nil || len(all) != len(ids) {
		t.Fatalf("список: %d %v", len(all), err)
	}
	for i := range all { // центр — каждая строка по очереди, включая края и середину группы за один день
		rows, err := s.AroundTx(ctx, all[i].ID, 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		lo, hi := max(0, i-2), min(len(all), i+3)
		// За краем набора окно захватывает чужие, более старые строки базы: сравниваем только засеянные.
		seeded := map[uuid.UUID]bool{}
		for _, id := range ids {
			seeded[id] = true
		}
		var mine []TxRow
		for _, r := range rows {
			if seeded[r.ID] {
				mine = append(mine, r)
			}
		}
		if fmt.Sprint(txIDs(mine)) != fmt.Sprint(txIDs(all[lo:hi])) {
			t.Errorf("окно вокруг строки %d:\n%v\nждали %v", i, txIDs(mine), txIDs(all[lo:hi]))
		}
	}
	if _, err := s.AroundTx(ctx, uuid.New(), 2, 2); err == nil {
		t.Error("несуществующий id принят")
	}
}
