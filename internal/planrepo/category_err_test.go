package planrepo

import (
	"context"
	"errors"
	"testing"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// Категория, которой нет, доходит до веба доменной ошибкой, а не строкой БД.
//
// Мутация: убрать mapCategoryErr в AddPlanned — тест краснеет.
func TestWriter_UnknownCategoryIsDomainError(t *testing.T) {
	sink := &fakeSink{addErr: budget.ErrUnknownCategory}
	w := NewWriter(sink)
	_, err := w.AddPlanned(context.Background(), plan.Scope{ChatIDs: []int64{1}}, plan.NewItem{
		Kind: plan.KindPayment, Name: "права", WindowStart: dptr(2026, 3, 19), Category: "нет такой",
	})
	if !errors.Is(err, plan.ErrUnknownCategory) {
		t.Fatalf("err=%v, ждали plan.ErrUnknownCategory", err)
	}
}
