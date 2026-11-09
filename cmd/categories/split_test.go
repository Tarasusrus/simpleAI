package main

import (
	"testing"

	"github.com/google/uuid"

	"simpleAI/internal/categories"
	"simpleAI/internal/plan"
)

// «После» плана строится из снимка без записи в базу: события и правила переразнесённых строк получают категорию цели,
// остальное и сам снимок не меняются.
//
// Мутации: не копировать срез платежей (меняется исходный снимок); переразносить события чужой категории; не
// переразносить правила; игнорировать ключ плановой записи — тест краснеет.
func TestRemapped(t *testing.T) {
	txA, txOther, pl, rule := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rest := categories.Category{ID: uuid.New(), Name: "Рестораны"}
	prod := categories.Category{ID: uuid.New(), Name: "Продукты"}
	src := categories.Category{ID: uuid.New(), Name: "Еда"}
	snap := plan.Snapshot{
		Payments: []plan.Event{
			{Key: plan.TransactionKey(txA.String()), Category: "еда"},
			{Key: plan.TransactionKey(txOther.String()), Category: "Такси"}, // не из источника — не трогаем даже по совпавшему ключу
			{Key: "planned:" + pl.String(), Category: "Еда"},
			{Key: plan.TransactionKey(uuid.New().String()), Category: "Еда"}, // нет в плане разнесения
		},
		Rules: []plan.RuleWithExceptions{{ID: rule.String(), Category: "Еда"}, {ID: uuid.New().String(), Category: "Еда"}},
	}
	p := categories.SplitPlan{Source: src, Rows: []categories.RowChoice{
		{Row: categories.SplitRow{Table: "budget_transaction", ID: txA}, Target: rest},
		{Row: categories.SplitRow{Table: "budget_transaction", ID: txOther}, Target: prod},
		{Row: categories.SplitRow{Table: "budget_planned_expense", ID: pl}, Target: prod},
		{Row: categories.SplitRow{Table: "budget_recurring", ID: rule}, Target: rest},
	}}
	got := remapped(snap, p)
	cats := []string{got.Payments[0].Category, got.Payments[1].Category, got.Payments[2].Category, got.Payments[3].Category, got.Rules[0].Category, got.Rules[1].Category}
	want := []string{"Рестораны", "Такси", "Продукты", "Еда", "Рестораны", "Еда"}
	for i := range want {
		if cats[i] != want[i] {
			t.Errorf("поз. %d: %q, ждали %q (все: %v)", i, cats[i], want[i], cats)
		}
	}
	if snap.Payments[0].Category != "еда" || snap.Rules[0].Category != "Еда" {
		t.Error("исходный снимок изменён")
	}
}

func TestFmtCur(t *testing.T) {
	if got := fmtCur(nil); got != "—" {
		t.Errorf("пусто: %q", got)
	}
	if got := fmtCur(map[string]float64{"THB": 14615.4, "RUB": 1343}); got != "1343 RUB + 14615 THB" {
		t.Errorf("две валюты: %q", got)
	}
}
