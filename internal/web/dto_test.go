package web

import (
	"testing"

	"simpleAI/internal/plan"
)

// Дата начала зазора доходит до контракта: без неё экран показывает «с )».
func TestToWindowView_SpentBeforeFrom(t *testing.T) {
	from := plan.Date{Year: 2026, Month: 9, Day: 18}
	got := toWindowView(plan.Result{View: plan.View{SpentBefore: 688, SpentBeforeFrom: &from}}, plan.CurRUB).KPI
	if got.SpentBefore != 688 || got.SpentBeforeFrom == nil || *got.SpentBeforeFrom != "2026-09-18" {
		t.Fatalf("KPI = %+v, ждали 688 с 2026-09-18", got)
	}
	if empty := toWindowView(plan.Result{}, plan.CurRUB).KPI; empty.SpentBeforeFrom != nil {
		t.Errorf("без зазора дата должна быть nil, пришло %v", *empty.SpentBeforeFrom)
	}
}
