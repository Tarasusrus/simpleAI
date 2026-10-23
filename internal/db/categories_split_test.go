package db

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"simpleAI/internal/categories"
)

type fixedClassifier map[string]string // описание → имя цели

func (f fixedClassifier) Classify(_ context.Context, d string, cands []categories.Candidate) (categories.Classification, error) {
	for _, c := range cands {
		if c.Name == f[d] {
			return categories.Classification{Action: categories.ActionExisting, CategoryID: c.ID, Confidence: 0.9}, nil
		}
	}
	return categories.Classification{Action: categories.ActionNone}, nil
}

// Разнесение «Еды» на реальной схеме (после миграции 00026): строки переезжают, каждая в журнале со split_id,
// повтор ничего не двигает, откат возвращает, изменённая после разнесения строка не трогается, источник убирается
// только когда пуст.
//
// Мутации: не писать журнал; не фильтровать по category_id источника при переносе; откат без проверки текущей
// категории; RetireSource без проверки пустоты — соответствующие проверки краснеют.
func TestCategories_SplitApplyRollbackRetire(t *testing.T) {
	st, conn := catStoreAt(t, 26)
	ctx := context.Background()
	prod, rest, other := catID(t, conn, "Продукты"), catID(t, conn, "Рестораны"), mkCat(t, conn, "тест-прочая", "expense")
	ins := `INSERT INTO budget_transaction (id,type,amount,currency,category_id,description,transaction_date) VALUES ($1,'expense',100,'THB',$2,$3,'2026-10-05')`
	txA, txB, txC, txMoved := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mustExec(t, conn, ins, txA, edaID, "Makro")
	mustExec(t, conn, ins, txB, edaID, "Cafe Amazon")
	mustExec(t, conn, ins, txC, edaID, "")
	mustExec(t, conn, ins, txMoved, edaID, "Makro 2")
	pl := uuid.New()
	mustExec(t, conn, `INSERT INTO budget_planned_expense (id,chat_id,amount,currency,description,due_date,category_id) VALUES ($1,7,500,'THB','ужин в кафе','2026-10-30',$2)`, pl, edaID)
	rc := uuid.New()
	mustExec(t, conn, `INSERT INTO budget_recurring (id,chat_id,name,amount,category_id,currency,recurrence_type,next_date) VALUES ($1,7,'Доставка еды',300,$2,'THB','weekly','2026-10-12')`, rc, edaID)

	src, targets, err := st.SplitSource(ctx, "еда")
	if err != nil || src.ID.String() != edaID || len(targets) != 2 || !targets[0].IsDefault || targets[0].ID.String() != prod {
		t.Fatalf("SplitSource: %+v %+v %v", src, targets, err)
	}
	rows, err := st.SplitRows(ctx, src.ID)
	if err != nil || len(rows) != 6 {
		t.Fatalf("строк %d err=%v, ждали 6 (4 транзакции, плановый, повтор)", len(rows), err)
	}
	plan, err := categories.PlanSplit(ctx, src, targets, rows, fixedClassifier{"Cafe Amazon": "Рестораны", "ужин в кафе": "Рестораны", "Доставка еды": "Рестораны"}, categories.DefaultHints)
	if err != nil {
		t.Fatal(err)
	}
	// между планом и apply одну строку уже перенесли руками — apply её не должен трогать
	mustExec(t, conn, `UPDATE budget_transaction SET category_id=$1 WHERE id=$2`, other, txMoved)

	res, err := st.ApplySplit(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved["budget_transaction"] != 3 || res.Moved["budget_planned_expense"] != 1 || res.Moved["budget_recurring"] != 1 {
		t.Fatalf("перенесено %v, ждали 3/1/1", res.Moved)
	}
	catOf := func(table string, id uuid.UUID) string {
		var c string
		if err := conn.QueryRow(`SELECT category_id::text FROM `+table+` WHERE id=$1`, id).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	for table, want := range map[string]map[uuid.UUID]string{
		"budget_transaction":     {txA: prod, txB: rest, txC: prod, txMoved: other.String()},
		"budget_planned_expense": {pl: rest},
		"budget_recurring":       {rc: rest},
	} {
		for id, c := range want {
			if got := catOf(table, id); got != c {
				t.Errorf("%s %s: категория %s, ждали %s", table, id, got, c)
			}
		}
	}
	if n := count(t, conn, `SELECT count(*) FROM category_merge_log WHERE merge_id=$1 AND old_category_id=$2 AND rolled_back_at IS NULL`, res.SplitID, src.ID); n != 5 {
		t.Fatalf("в журнале %d строк разнесения, ждали 5", n)
	}

	// идемпотентность: повторный apply того же плана ничего не двигает и не пишет
	res2, err := st.ApplySplit(ctx, plan)
	if err != nil || len(res2.Moved) != 0 {
		t.Fatalf("повтор: %v %v", res2.Moved, err)
	}
	if n := count(t, conn, `SELECT count(*) FROM category_merge_log WHERE merge_id=$1`, res2.SplitID); n != 0 {
		t.Errorf("повтор записал %d строк журнала", n)
	}

	// откат: сначала строку txA меняем руками — откат её не трогает
	mustExec(t, conn, `UPDATE budget_transaction SET category_id=$1 WHERE id=$2`, other, txA)
	back, err := st.RollbackSplit(ctx, res.SplitID)
	if err != nil || back != 4 {
		t.Fatalf("откат вернул %d строк err=%v, ждали 4 (из 5 одну изменили после разнесения)", back, err)
	}
	if catOf("budget_transaction", txA) != other.String() || catOf("budget_transaction", txB) != edaID || catOf("budget_recurring", rc) != edaID {
		t.Error("откат вернул не то")
	}
	if _, err := st.RollbackSplit(ctx, res.SplitID); err == nil {
		t.Error("повторный откат принят")
	}
	// слияние откатывается другой командой: split_id слияния не принимаем и наоборот
	if _, err := st.RollbackSplit(ctx, uuid.New()); err == nil {
		t.Error("откат несуществующего разнесения принят")
	}

	// RetireSource отказывает, пока в «Еде» есть строки
	if _, err := st.RetireSource(ctx, src, targets[0].Category); err == nil {
		t.Fatal("источник со строками убран")
	}
	// после повторного разнесения источник пуст и убирается обычным слиянием: «еда» становится алиасом «Продуктов»
	plan2, err := categories.PlanSplit(ctx, src, targets, mustRows(t, st, src), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplySplit(ctx, plan2); err != nil {
		t.Fatal(err)
	}
	mr, err := st.RetireSource(ctx, src, targets[0].Category)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, conn, `SELECT count(*) FROM budget_category WHERE id=$1`, src.ID); n != 0 {
		t.Error("«Еда» не удалена")
	}
	if n := count(t, conn, `SELECT count(*) FROM budget_category_alias WHERE alias_lower='еда' AND category_id=$1`, prod); n != 1 {
		t.Error("«еда» не стала алиасом «Продуктов»")
	}
	if _, err := st.RollbackSplit(ctx, mr.MergeID); err == nil {
		t.Error("RollbackSplit принял id слияния категорий")
	}
	if err := st.Rollback(ctx, mr.MergeID); err != nil {
		t.Fatalf("откат слияния «Еды»: %v", err)
	}
}

func mustRows(t *testing.T, st *categories.Store, src categories.Category) []categories.SplitRow {
	t.Helper()
	rows, err := st.SplitRows(context.Background(), src.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
