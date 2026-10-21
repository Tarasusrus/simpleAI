package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"simpleAI/internal/categories"
)

const edaID = "b0000001-0000-0000-0000-000000000001" // «Еда», засеяна миграцией 00007

// stage1Fixture воспроизводит форму прода на 2026-10-21: «Еда», 4 алиаса слитых дублей, две доли
// с «едой», повторяющийся лимит на «Еду».
func stage1Fixture(t *testing.T) *sql.DB {
	t.Helper()
	conn := scratchDB(t, 25)
	for _, a := range []string{"кафе / ресторан", "продукт", "продукты", "ресторан"} {
		mustExec(t, conn, `INSERT INTO budget_category_alias (alias_lower,type,category_id,source) VALUES ($1,'expense',$2,'merge')`, a, edaID)
	}
	mustExec(t, conn, `INSERT INTO budget_envelope (id,chat_id,income_amount,income_currency,period_start,period_end,active)
		VALUES ('00000000-0000-0000-0000-0000000000e1', 1, 1000, 'RUB', '2026-10-01', '2026-10-31', true)`)
	mustExec(t, conn, `INSERT INTO budget_envelope_share (id,envelope_id,name,kind,allocated,source,position) VALUES
		('00000000-0000-0000-0000-0000000000f1','00000000-0000-0000-0000-0000000000e1','еда','spend',100,'auto',1),
		('00000000-0000-0000-0000-0000000000f2','00000000-0000-0000-0000-0000000000e1','дом','spend',100,'auto',2)`)
	mustExec(t, conn, `INSERT INTO budget_envelope_share_category (share_id,category_id,category_name) VALUES
		('00000000-0000-0000-0000-0000000000f1',$1,'еда'), ('00000000-0000-0000-0000-0000000000f2',$1,'еда')`, edaID)
	mustExec(t, conn, `INSERT INTO plan_category_limit (chat_id,category_id,amount_minor,currency,repeat,window_start)
		VALUES (7,$1,1406700,'RUB',true,'2026-10-21')`, edaID)
	return conn
}

func up26(t *testing.T, conn *sql.DB) {
	t.Helper()
	if err := goose.UpToContext(context.Background(), conn, "migrations", 26); err != nil {
		t.Fatalf("up 26: %v", err)
	}
}

func catID(t *testing.T, conn *sql.DB, name string) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(`SELECT id FROM budget_category WHERE lower(name)=lower($1) AND type='expense'`, name).Scan(&id); err != nil {
		t.Fatalf("нет категории %q: %v", name, err)
	}
	return id
}

// Стадия 1 на данных формы прода: категории, разделение, алиасы, доли, лимит.
//
// Мутации: убрать удаление алиаса «продукты», перенос лимита, добавление долей, строки разделения —
// соответствующая проверка краснеет.
func TestMigration00026_Stage1OnProdShapedData(t *testing.T) {
	conn := stage1Fixture(t)
	up26(t, conn)
	prod, rest := catID(t, conn, "Продукты"), catID(t, conn, "Рестораны")

	if n := count(t, conn, `SELECT count(*) FROM category_split_target WHERE source_id=$1`, edaID); n != 2 {
		t.Fatalf("строк разделения %d, ждали 2", n)
	}
	if n := count(t, conn, `SELECT count(*) FROM category_split_target WHERE source_id=$1 AND target_id=$2 AND is_default`, edaID, prod); n != 1 {
		t.Error("по умолчанию должна быть цель «Продукты»")
	}

	want := map[string]string{"продукт": prod, "ресторан": rest, "кафе / ресторан": rest, "кафе": rest, "кофейня": rest, "доставка еды": rest}
	for alias, target := range want {
		if n := count(t, conn, `SELECT count(*) FROM budget_category_alias WHERE alias_lower=$1 AND type='expense' AND category_id=$2`, alias, target); n != 1 {
			t.Errorf("алиас %q не ведёт на нужную цель", alias)
		}
	}
	if n := count(t, conn, `SELECT count(*) FROM budget_category_alias WHERE alias_lower='продукты'`); n != 0 {
		t.Error("алиас «продукты» остался, хотя это имя живой категории")
	}
	if n := count(t, conn, `SELECT count(*) FROM budget_category_alias a JOIN budget_category c ON lower(c.name)=a.alias_lower AND c.type=a.type`); n != 0 {
		t.Errorf("алиасов, совпадающих с именем живой категории: %d", n)
	}

	for _, share := range []string{"00000000-0000-0000-0000-0000000000f1", "00000000-0000-0000-0000-0000000000f2"} {
		for name, id := range map[string]string{"еда": edaID, "продукты": prod, "рестораны": rest} {
			if n := count(t, conn, `SELECT count(*) FROM budget_envelope_share_category WHERE share_id=$1 AND category_name=$2 AND category_id=$3`, share, name, id); n != 1 {
				t.Errorf("доля %s: нет строки %q", share[len(share)-2:], name)
			}
		}
	}

	var cat string
	var amt int64
	if err := conn.QueryRow(`SELECT category_id, amount_minor FROM plan_category_limit`).Scan(&cat, &amt); err != nil || cat != prod || amt != 1406700 {
		t.Errorf("лимит: category=%s amount=%d err=%v, ждали Продукты 1406700", cat, amt, err)
	}
	if n := count(t, conn, `SELECT count(*) FROM plan_category_limit WHERE category_id=$1`, rest); n != 0 {
		t.Error("на «Рестораны» появился лимит")
	}
}

// Повторный прогон Up ничего не дублирует и не ломает; конфликт ключа лимита оставляет лимит «Еды».
//
// Мутация: убрать NOT EXISTS в переносе лимита — тест падает на нарушении уникального индекса.
func TestMigration00026_RerunAndLimitConflict(t *testing.T) {
	conn := stage1Fixture(t)
	// У «Продуктов» уже есть повторяющийся лимит в том же чате: перенос конфликтует.
	mustExec(t, conn, `INSERT INTO budget_category (id,name,type) VALUES ('00000000-0000-0000-0000-0000000000c1','Продукты','expense')`)
	mustExec(t, conn, `INSERT INTO plan_category_limit (chat_id,category_id,amount_minor,currency,repeat,window_start)
		VALUES (7,'00000000-0000-0000-0000-0000000000c1',500,'RUB',true,'2026-10-01')`)
	up26(t, conn)
	if n := count(t, conn, `SELECT count(*) FROM plan_category_limit WHERE category_id=$1 AND amount_minor=1406700`, edaID); n != 1 {
		t.Fatal("лимит «Еды» пропал при конфликте ключа")
	}
	if n := count(t, conn, `SELECT count(*) FROM plan_category_limit WHERE amount_minor=500`); n != 1 {
		t.Fatal("лимит «Продуктов» изменён")
	}

	if _, err := conn.Exec(upBody26(t)); err != nil {
		t.Fatalf("повторный прогон Up: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM category_split_target`); n != 2 {
		t.Errorf("после повторного прогона строк разделения %d", n)
	}
	if n := count(t, conn, `SELECT count(*) FROM budget_envelope_share_category WHERE category_name IN ('продукты','рестораны')`); n != 4 {
		t.Errorf("после повторного прогона строк долей %d, ждали 4", n)
	}
}

// Чистая база без «Еды»: миграция ничего не создаёт и не падает.
func TestMigration00026_NoFoodCategory(t *testing.T) {
	conn := scratchDB(t, 25)
	mustExec(t, conn, `DELETE FROM budget_category WHERE id=$1`, edaID)
	up26(t, conn)
	if n := count(t, conn, `SELECT count(*) FROM budget_category WHERE lower(name) IN ('продукты','рестораны')`); n != 0 {
		t.Errorf("созданы категории при отсутствии «Еды»: %d", n)
	}
}

func upBody26(t *testing.T) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/00026_food_taxonomy_stage1.sql")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(b)
	up, down := strings.Index(s, "-- +goose Up"), strings.Index(s, "-- +goose Down")
	body := s[up:down]
	for _, m := range []string{"-- +goose Up", "-- +goose StatementBegin", "-- +goose StatementEnd"} {
		body = strings.ReplaceAll(body, m, "")
	}
	return "BEGIN;\n" + body + "\nCOMMIT;"
}

// Откат возвращает лимит и алиасы на «Еду» и убирает разделение; с тратами в «Продуктах» отказывает.
//
// Мутация: убрать проверку трат в Down — тест краснеет.
func TestMigration00026_Down(t *testing.T) {
	conn := stage1Fixture(t)
	up26(t, conn)
	prod := catID(t, conn, "Продукты")
	mustExec(t, conn, `INSERT INTO budget_transaction (id,type,amount,currency,description,category_id,transaction_date)
		VALUES (gen_random_uuid(),'expense',100,'THB','x',$1,'2026-10-21')`, prod)
	if err := goose.DownToContext(context.Background(), conn, "migrations", 25); err == nil {
		t.Fatal("откат с тратами в «Продуктах» прошёл")
	}
	mustExec(t, conn, `DELETE FROM budget_transaction WHERE category_id=$1`, prod)
	if err := goose.DownToContext(context.Background(), conn, "migrations", 25); err != nil {
		t.Fatalf("откат: %v", err)
	}
	if n := count(t, conn, `SELECT count(*) FROM plan_category_limit WHERE category_id=$1`, edaID); n != 1 {
		t.Error("лимит не вернулся на «Еду»")
	}
	if n := count(t, conn, `SELECT count(*) FROM budget_category_alias WHERE category_id=$1`, edaID); n != 4 {
		t.Errorf("на «Еду» вернулось алиасов %d, ждали 4", n)
	}
	if n := count(t, conn, `SELECT count(*) FROM category_split_target`); n != 0 {
		t.Error("разделение осталось")
	}
}

// После миграции бот видит новую таксономию через настоящий Store: «Еда» — источник разделения с
// «Продуктами» по умолчанию, она не назначаема, а «кафе» и «продукт» ведут на свои цели.
//
// Мутация: убрать вставку строк разделения или алиасы — тест краснеет.
func TestMigration00026_StoreSeesNewTaxonomy(t *testing.T) {
	conn := stage1Fixture(t)
	up26(t, conn)
	dsn, _ := scratchDSNs.Load(conn)
	dsnStr, ok := dsn.(string)
	if !ok {
		t.Fatal("нет DSN для временной базы")
	}
	pool, err := pgxpool.New(context.Background(), dsnStr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := categories.NewStore(pool)
	ctx := context.Background()

	eda, found, err := st.Resolve(ctx, "еда", "expense")
	if err != nil || !found {
		t.Fatalf("Resolve(еда): found=%v err=%v", found, err)
	}
	targets, err := st.SplitTargets(ctx, eda.ID)
	if err != nil || len(targets) != 2 || targets[0].Name != "Продукты" || !targets[0].IsDefault || targets[1].Name != "Рестораны" {
		t.Fatalf("цели разделения: %+v err=%v", targets, err)
	}
	list, err := st.ListAssignable(ctx, "expense")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, c := range list {
		have[c.Name] = true
	}
	if have["Еда"] || !have["Продукты"] || !have["Рестораны"] {
		t.Errorf("назначаемые категории: %v", have)
	}
	for alias, want := range map[string]string{"кафе": "Рестораны", "Продукт": "Продукты", "кофейня": "Рестораны"} {
		c, found, err := st.Resolve(ctx, alias, "expense")
		if err != nil || !found || c.Name != want {
			t.Errorf("Resolve(%q) = %q found=%v err=%v, ждали %q", alias, c.Name, found, err, want)
		}
	}
}
