package db

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/internal/categories"
)

func catStore(t *testing.T) (*categories.Store, *sql.DB) {
	t.Helper()
	conn := scratchDB(t, 22)
	dsn, _ := scratchDSNs.Load(conn)
	pool, err := pgxpool.New(context.Background(), dsn.(string))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return categories.NewStore(pool), conn
}

func mkCat(t *testing.T, conn *sql.DB, name, typ string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(t, conn, `INSERT INTO budget_category (id,name,type) VALUES ($1,$2,$3)`, id, name, typ)
	return id
}

func count(t *testing.T, conn *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func TestCategories_GetOrCreate_ConcurrentOneCategory(t *testing.T) {
	st, conn := catStore(t)
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "Кофе"
			if i%2 == 1 {
				name = "  кофе "
			}
			c, _, err := st.GetOrCreate(context.Background(), name, "expense")
			if err != nil {
				t.Errorf("GetOrCreate: %v", err)
				return
			}
			ids <- c.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[uuid.UUID]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 || count(t, conn, `SELECT count(*) FROM budget_category WHERE lower(name)='кофе'`) != 1 {
		t.Fatalf("создано разных категорий: %d, ждали 1", len(seen))
	}
}

func TestCategories_MergeMovesEverythingAndAliases(t *testing.T) {
	st, conn := catStore(t)
	ctx := context.Background()
	src, dst := mkCat(t, conn, "тест-ист", "expense"), mkCat(t, conn, "ТестЦель", "expense")
	mustExec(t, conn, `INSERT INTO budget_transaction (id,type,amount,category_id) VALUES (gen_random_uuid(),'expense',10,$1),(gen_random_uuid(),'expense',20,$1)`, src)
	mustExec(t, conn, `INSERT INTO budget_recurring (id,chat_id,name,amount,recurrence_type,next_date,category_id) VALUES (gen_random_uuid(),1,'r',5,'daily','2026-01-01',$1)`, src)
	mustExec(t, conn, `INSERT INTO budget_planned_expense (id,chat_id,description,amount,category_id,due_date) VALUES (gen_random_uuid(),1,'p',7,$1,'2026-01-01')`, src)
	mustExec(t, conn, `INSERT INTO budget_envelope (id,chat_id,income_amount,period_start,period_end) VALUES ('00000000-0000-0000-0000-0000000000e1',1,100,'2026-01-01','2026-01-31')`)
	for _, sh := range []string{"f1", "f2"} {
		mustExec(t, conn, `INSERT INTO budget_envelope_share (id,envelope_id,name,kind,allocated,source,position)
			VALUES ('00000000-0000-0000-0000-0000000000`+sh+`','00000000-0000-0000-0000-0000000000e1',$1,'spend',50,'auto',0)`, sh)
	}
	// f1: только источник (переедет с новым именем); f2: уже есть и цель (строка источника схлопнется).
	mustExec(t, conn, `INSERT INTO budget_envelope_share_category VALUES ('00000000-0000-0000-0000-0000000000f1',$1,'тест-ист')`, src)
	mustExec(t, conn, `INSERT INTO budget_envelope_share_category VALUES ('00000000-0000-0000-0000-0000000000f2',$1,'тест-ист'),('00000000-0000-0000-0000-0000000000f2',$2,'тестцель')`, src, dst)
	// Алиас, смотревший на источник, должен перенаправиться на цель.
	mustExec(t, conn, `INSERT INTO budget_category_alias (alias_lower,type,category_id,source) VALUES ('тест-ист2','expense',$1,'manual')`, src)

	res, err := st.Merge(ctx, categories.MergeRequest{Source: src, Target: dst, Decider: "rule", Reason: "тест"})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Moved.Transactions != 2 || res.Moved.Recurring != 1 || res.Moved.Planned != 1 || res.Moved.Shares != 1 {
		t.Errorf("moved = %+v", res.Moved)
	}
	if count(t, conn, `SELECT count(*) FROM budget_category WHERE id=$1`, src) != 0 {
		t.Error("источник не удалён")
	}
	if count(t, conn, `SELECT count(*) FROM budget_transaction WHERE category_id=$1`, dst) != 2 {
		t.Error("транзакции не переехали")
	}
	if count(t, conn, `SELECT count(*) FROM budget_envelope_share_category WHERE category_id=$1 AND category_name='тестцель'`, dst) != 2 {
		t.Error("доли: category_id и category_name должны быть целевыми, дубль схлопнут")
	}
	if count(t, conn, `SELECT count(*) FROM budget_category_alias WHERE category_id=$1 AND alias_lower IN ('тест-ист','тест-ист2')`, dst) != 2 {
		t.Error("алиасы не ведут на цель")
	}
	// Имя-алиас теперь резолвится в цель, а не создаёт новую категорию.
	c, created, err := st.GetOrCreate(ctx, "Тест-Ист", "expense")
	if err != nil || created || c.ID != dst {
		t.Errorf("GetOrCreate по алиасу: %+v created=%v err=%v", c, created, err)
	}
	if count(t, conn, `SELECT count(*) FROM category_decision_log WHERE merge_id=$1 AND decider='rule'`, res.MergeID) != 1 {
		t.Error("решение не записано в журнал")
	}
}

func TestCategories_RollbackRestoresExactly(t *testing.T) {
	st, conn := catStore(t)
	ctx := context.Background()
	src, dst := mkCat(t, conn, "тест-ист", "expense"), mkCat(t, conn, "ТестЦель", "expense")
	mustExec(t, conn, `INSERT INTO budget_transaction (id,type,amount,category_id) VALUES (gen_random_uuid(),'expense',10,$1)`, src)
	mustExec(t, conn, `INSERT INTO budget_envelope (id,chat_id,income_amount,period_start,period_end) VALUES ('00000000-0000-0000-0000-0000000000e1',1,100,'2026-01-01','2026-01-31')`)
	mustExec(t, conn, `INSERT INTO budget_envelope_share (id,envelope_id,name,kind,allocated,source,position) VALUES
		('00000000-0000-0000-0000-0000000000f2','00000000-0000-0000-0000-0000000000e1','x','spend',50,'auto',0)`)
	mustExec(t, conn, `INSERT INTO budget_envelope_share_category VALUES ('00000000-0000-0000-0000-0000000000f2',$1,'тест-ист'),('00000000-0000-0000-0000-0000000000f2',$2,'тестцель')`, src, dst)
	// Транзакция, добавленная ПОСЛЕ слияния в целевую категорию, откатом не трогается.
	res, err := st.Merge(ctx, categories.MergeRequest{Source: src, Target: dst})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	mustExec(t, conn, `INSERT INTO budget_transaction (id,type,amount,category_id) VALUES (gen_random_uuid(),'expense',99,$1)`, dst)

	if err := st.Rollback(ctx, res.MergeID); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if count(t, conn, `SELECT count(*) FROM budget_category WHERE id=$1 AND name='тест-ист'`, src) != 1 {
		t.Error("категория не восстановлена с прежним id")
	}
	if count(t, conn, `SELECT count(*) FROM budget_transaction WHERE category_id=$1 AND amount=10`, src) != 1 {
		t.Error("транзакция не вернулась")
	}
	if count(t, conn, `SELECT count(*) FROM budget_transaction WHERE category_id=$1 AND amount=99`, dst) != 1 {
		t.Error("новая транзакция цели ошибочно переехала")
	}
	if count(t, conn, `SELECT count(*) FROM budget_envelope_share_category WHERE category_id=$1 AND category_name='тест-ист'`, src) != 1 ||
		count(t, conn, `SELECT count(*) FROM budget_envelope_share_category WHERE category_id=$1 AND category_name='тестцель'`, dst) != 1 {
		t.Error("доли не восстановлены (схлопнутая строка должна вернуться)")
	}
	if count(t, conn, `SELECT count(*) FROM budget_category_alias`) != 0 {
		t.Error("алиас слияния остался после отката")
	}
	if err := st.Rollback(ctx, res.MergeID); err == nil {
		t.Error("повторный откат принят")
	}
}

func TestCategories_MergeIsAtomic(t *testing.T) {
	st, conn := catStore(t)
	src, dst := mkCat(t, conn, "тест-ист", "expense"), mkCat(t, conn, "ТестЦель", "expense")
	mustExec(t, conn, `INSERT INTO budget_transaction (id,type,amount,category_id) VALUES (gen_random_uuid(),'expense',10,$1)`, src)
	// Ломаем последний шаг (журнал решений): всё до него должно откатиться.
	mustExec(t, conn, `ALTER TABLE category_decision_log ADD CONSTRAINT boom CHECK (false)`)
	if _, err := st.Merge(context.Background(), categories.MergeRequest{Source: src, Target: dst}); err == nil {
		t.Fatal("слияние прошло при сбое журнала")
	}
	if count(t, conn, `SELECT count(*) FROM budget_transaction WHERE category_id=$1`, src) != 1 ||
		count(t, conn, `SELECT count(*) FROM budget_category WHERE id=$1`, src) != 1 ||
		count(t, conn, `SELECT count(*) FROM category_merge_log`) != 0 ||
		count(t, conn, `SELECT count(*) FROM budget_category_alias`) != 0 {
		t.Error("слияние применилось частично")
	}
}

func TestCategories_MergeRejectsTypeMismatchAndChains(t *testing.T) {
	st, conn := catStore(t)
	ctx := context.Background()
	a, b, c := mkCat(t, conn, "ta", "expense"), mkCat(t, conn, "tb", "expense"), mkCat(t, conn, "tc", "expense")
	inc := mkCat(t, conn, "тзарплата", "income")
	if _, err := st.Merge(ctx, categories.MergeRequest{Source: a, Target: inc}); err == nil {
		t.Error("слияние разных типов принято")
	}
	if _, err := st.Merge(ctx, categories.MergeRequest{Source: a, Target: b}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Merge(ctx, categories.MergeRequest{Source: b, Target: c}); err != nil {
		t.Fatal(err)
	}
	if count(t, conn, `SELECT count(*) FROM budget_category_alias WHERE category_id=$1 AND alias_lower IN ('ta','tb')`, c) != 2 {
		t.Error("цепочка алиасов: a и b должны вести прямо на c")
	}
}

func TestCategories_ReconcileShareNames(t *testing.T) {
	st, conn := catStore(t)
	dst := mkCat(t, conn, "ТестЦель", "expense")
	mustExec(t, conn, `INSERT INTO budget_envelope (id,chat_id,income_amount,period_start,period_end) VALUES ('00000000-0000-0000-0000-0000000000e1',1,100,'2026-01-01','2026-01-31')`)
	mustExec(t, conn, `INSERT INTO budget_envelope_share (id,envelope_id,name,kind,allocated,source,position) VALUES
		('00000000-0000-0000-0000-0000000000f2','00000000-0000-0000-0000-0000000000e1','x','spend',50,'auto',0)`)
	// Старое имя после ручного слияния: category_id уже цель, имя прежнее.
	mustExec(t, conn, `INSERT INTO budget_envelope_share_category VALUES ('00000000-0000-0000-0000-0000000000f2',$1,'тест-ист')`, dst)
	n, err := st.ReconcileShareNames(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("reconcile: n=%d err=%v", n, err)
	}
	if count(t, conn, `SELECT count(*) FROM budget_envelope_share_category WHERE category_name='тестцель'`) != 1 {
		t.Error("имя доли не исправлено")
	}
	if n, _ := st.ReconcileShareNames(context.Background()); n != 0 {
		t.Errorf("повторный запуск изменил %d строк", n)
	}
}

func TestCategories_DeduplicatorFirstRunCollapsesSecondRunNoop(t *testing.T) {
	st, conn := catStore(t)
	ctx := context.Background()
	food, products := mkCat(t, conn, "ТестЕда", "expense"), mkCat(t, conn, "тестпродукты", "expense")
	mustExec(t, conn, `INSERT INTO budget_transaction (id,type,amount,category_id) VALUES (gen_random_uuid(),'expense',10,$1)`, products)
	before := count(t, conn, `SELECT count(*) FROM budget_category WHERE type='expense'`)
	d := &categories.Deduplicator{
		Source: st, Finder: categories.SynonymFinder{Synonyms: map[string]string{"тестпродукты": "ТестЕда"}},
		Decider: categories.RuleDecider{}, Merger: st, Usage: st, Reconciler: st,
	}
	if _, err := d.Run(ctx, false); err != nil || count(t, conn, `SELECT count(*) FROM budget_category WHERE type='expense'`) != before {
		t.Fatalf("dry-run изменил данные: err=%v", err)
	}
	rep, err := d.Run(ctx, true)
	if err != nil || len(rep.Items) != 1 || rep.Items[0].Result == nil {
		t.Fatalf("первый запуск: %+v err=%v", rep, err)
	}
	if count(t, conn, `SELECT count(*) FROM budget_category WHERE type='expense'`) != before-1 ||
		count(t, conn, `SELECT count(*) FROM budget_transaction WHERE category_id=$1`, food) != 1 {
		t.Error("первый запуск не схлопнул дубль")
	}
	rep, err = d.Run(ctx, true)
	if err != nil || len(rep.Items) != 0 {
		t.Errorf("повторный запуск должен быть пустым: %+v err=%v", rep, err)
	}
}
