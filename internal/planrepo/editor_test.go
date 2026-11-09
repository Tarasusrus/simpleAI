package planrepo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// Заглушки, чтобы общий fakeSink оставался Sink; запись вызовов — в editSink.
func (f *fakeSink) UpdatePlanItem(context.Context, []int64, uuid.UUID, budget.PlanItemEdit) (bool, error) {
	return false, nil
}

func (f *fakeSink) UpdateRecurringRule(context.Context, []int64, uuid.UUID, budget.RecurringEdit) (bool, error) {
	return false, nil
}

type editSink struct {
	fakeSink
	ok      bool
	ids     [][]int64
	itemIDs []uuid.UUID
	items   []budget.PlanItemEdit
	rules   []budget.RecurringEdit
}

func (f *editSink) UpdatePlanItem(_ context.Context, ids []int64, id uuid.UUID, e budget.PlanItemEdit) (bool, error) {
	f.ids, f.itemIDs, f.items = append(f.ids, ids), append(f.itemIDs, id), append(f.items, e)
	return f.ok, nil
}

func (f *editSink) UpdateRecurringRule(_ context.Context, ids []int64, id uuid.UUID, e budget.RecurringEdit) (bool, error) {
	f.ids, f.itemIDs, f.rules = append(f.ids, ids), append(f.itemIDs, id), append(f.rules, e)
	return f.ok, nil
}

func sameTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

// Правка разовой записи доходит до хранилища как есть: направление по виду, сумма в
// минимальных единицах, валюта суммы (без суммы — RUB), флаг размещения.
//
// Мутация: не передавать SetPlace (всегда true) или перепутать направление — тест краснеет.
func TestUpdatePlanned_Maps(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		name string
		edit plan.PlannedEdit
		want budget.PlanItemEdit
	}{
		{"платёж в батах в окно", plan.PlannedEdit{SetPlace: true, Item: plan.NewItem{
			Kind: plan.KindPayment, Name: "корм", Amount: &plan.Money{Minor: 123456, Cur: plan.CurTHB}, Category: "Еда", WindowStart: dptr(2026, 3, 19),
		}}, budget.PlanItemEdit{Direction: "expense", Description: "корм", Currency: "THB", Category: "Еда", SetPlace: true}},
		{"платёж без суммы, место не трогаем", plan.PlannedEdit{Item: plan.NewItem{
			Kind: plan.KindPayment, Name: "права", Date: dptr(2026, 3, 15),
		}}, budget.PlanItemEdit{Direction: "expense", Description: "права", Currency: "RUB"}},
		{"поступление на дату", plan.PlannedEdit{SetPlace: true, Item: plan.NewItem{
			Kind: plan.KindIncome, Name: "аванс", Amount: &plan.Money{Minor: 3_000_000, Cur: plan.CurRUB}, Date: dptr(2026, 3, 20),
		}}, budget.PlanItemEdit{Direction: "income", Description: "аванс", Currency: "RUB", SetPlace: true}},
	}
	for _, c := range cases {
		f := &editSink{ok: true}
		if err := NewWriter(f).UpdatePlanned(context.Background(), plan.Scope{ChatIDs: []int64{7, 9}}, id.String(), c.edit); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := f.items[0]
		if got.Direction != c.want.Direction || got.Description != c.want.Description || got.Currency != c.want.Currency ||
			got.Category != c.want.Category || got.SetPlace != c.want.SetPlace {
			t.Errorf("%s: %+v", c.name, got)
		}
		if n := c.edit.Item; (n.Amount == nil) != (got.AmountMinor == nil) || (n.Amount != nil && *got.AmountMinor != n.Amount.Minor) {
			t.Errorf("%s: сумма %v", c.name, got.AmountMinor)
		}
		if n := c.edit.Item; !sameTime(got.DueDate, dbDate(n.Date)) || !sameTime(got.WindowStart, dbDate(n.WindowStart)) {
			t.Errorf("%s: размещение %v %v", c.name, got.DueDate, got.WindowStart)
		}
		if f.itemIDs[0] != id || len(f.ids[0]) != 2 || f.ids[0][0] != 7 || f.ids[0][1] != 9 {
			t.Errorf("%s: id/область %v %v", c.name, f.itemIDs, f.ids)
		}
	}
}

// Пустая область, битый id и невалидная запись до хранилища не доходят; «не
// обновлено» — ErrNotFound.
func TestUpdatePlanned_Rejects(t *testing.T) {
	good := plan.PlannedEdit{Item: plan.NewItem{Kind: plan.KindPayment, Name: "x"}}
	cases := []struct {
		name  string
		sc    plan.Scope
		id    string
		edit  plan.PlannedEdit
		ok    bool
		want  error
		calls int
	}{
		{"пустая область", plan.Scope{}, uuid.NewString(), good, true, nil, 0},
		{"битый id", plan.Scope{ChatIDs: []int64{1}}, "nope", good, true, plan.ErrNotFound, 0},
		{"поступление без суммы", plan.Scope{ChatIDs: []int64{1}}, uuid.NewString(),
			plan.PlannedEdit{Item: plan.NewItem{Kind: plan.KindIncome, Name: "x", Date: dptr(2026, 3, 20)}}, true, plan.ErrAmountRequired, 0},
		{"нет такой / закрыта / чужая", plan.Scope{ChatIDs: []int64{1}}, uuid.NewString(), good, false, plan.ErrNotFound, 1},
	}
	for _, c := range cases {
		f := &editSink{ok: c.ok}
		err := NewWriter(f).UpdatePlanned(context.Background(), c.sc, c.id, c.edit)
		if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: %v, ждали %v", c.name, err, c.want)
		}
		if len(f.items) != c.calls {
			t.Errorf("%s: вызовов хранилища %d, ждали %d", c.name, len(f.items), c.calls)
		}
	}
}

// Правило: тип по виду, сумма в минимальных единицах, валюта; «не обновлено» —
// ErrNotFound, пустая сумма до хранилища не доходит.
//
// Мутация: убрать проверку !ok → ErrNotFound — тест краснеет.
func TestUpdateRule(t *testing.T) {
	id := uuid.New()
	sc := plan.Scope{ChatIDs: []int64{7}}
	f := &editSink{ok: true}
	err := NewWriter(f).UpdateRule(context.Background(), sc, id.String(), plan.RuleEdit{
		Kind: plan.KindIncome, Name: "зарплата", Amount: plan.Money{Minor: 9_800_000, Cur: plan.CurRUB},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := f.rules[0]; r.Type != "income" || r.Name != "зарплата" || r.AmountMinor != 9_800_000 || r.Currency != "RUB" || f.itemIDs[0] != id {
		t.Errorf("правило: %+v", r)
	}
	if err := NewWriter(&editSink{}).UpdateRule(context.Background(), sc, id.String(), plan.RuleEdit{
		Kind: plan.KindPayment, Name: "аренда", Amount: plan.Money{Minor: 1, Cur: plan.CurTHB},
	}); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("не обновлено: %v", err)
	}
	ok := plan.RuleEdit{Kind: plan.KindPayment, Name: "x", Amount: plan.Money{Minor: 1, Cur: plan.CurRUB}}
	bad := []struct {
		name, id string
		edit     plan.RuleEdit
	}{
		{"нулевая сумма", id.String(), plan.RuleEdit{Kind: plan.KindPayment, Name: "x", Amount: plan.Money{Cur: plan.CurRUB}}},
		{"чужая валюта", id.String(), plan.RuleEdit{Kind: plan.KindPayment, Name: "x", Amount: plan.Money{Minor: 1, Cur: "USD"}}},
		{"битый id правила", "nope", ok},
	}
	for _, c := range bad {
		f := &editSink{ok: true}
		if err := NewWriter(f).UpdateRule(context.Background(), sc, c.id, c.edit); err == nil || len(f.rules) != 0 {
			t.Errorf("%s: err=%v, вызовов %d", c.name, err, len(f.rules))
		}
	}
	if err := NewWriter(&editSink{ok: true}).UpdateRule(context.Background(), plan.Scope{}, id.String(), ok); err == nil {
		t.Error("пустая область принята")
	}
}

// Вид поступления (ADR-019) доходит до хранилища аргументом вызова: false, true и nil передаются как есть.
//
// Мутация: не передавать OpensWindow в PlanItemEdit — краснеет.
func TestUpdatePlanned_PassesOpensWindow(t *testing.T) {
	f, tr := false, true
	for _, v := range []*bool{&f, &tr, nil} {
		sink := &editSink{ok: true}
		e := plan.PlannedEdit{OpensWindow: v, Item: plan.NewItem{Kind: plan.KindIncome, Name: "байк",
			Amount: &plan.Money{Minor: 100, Cur: plan.CurRUB}, Date: dptr(2026, 3, 20)}}
		if err := NewWriter(sink).UpdatePlanned(context.Background(), plan.Scope{ChatIDs: []int64{7}}, uuid.New().String(), e); err != nil {
			t.Fatal(err)
		}
		got := sink.items[0].OpensWindow
		if (got == nil) != (v == nil) || (v != nil && *got != *v) {
			t.Errorf("вид у хранилища %v, ждали %v", got, v)
		}
	}
}
