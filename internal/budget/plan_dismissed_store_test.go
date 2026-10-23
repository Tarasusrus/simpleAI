package budget

import (
	"errors"
	"reflect"
	"testing"
)

// Скрытие категорий: идемпотентность, область чатов, алиасы, неизвестная категория.
//
// Мутации: убрать ON CONFLICT DO NOTHING (повтор падает); убрать фильтр chat_id в
// RestorePlanCategory или PlanDismissed (чужой чат виден/стирается).
func TestPlanDismissed_DismissRestoreScope(t *testing.T) {
	s, ctx := limitFixture(t)
	t.Cleanup(func() {
		if _, err := s.pool.Exec(ctx, `DELETE FROM plan_category_dismissed WHERE chat_id IN ($1, $2)`, limitChat, limitChat-1); err != nil {
			t.Errorf("очистка: %v", err)
		}
	})
	if err := s.DismissPlanCategory(ctx, limitChat, " "+limitCatName+" "); err != nil {
		t.Fatal(err)
	}
	if err := s.DismissPlanCategory(ctx, limitChat, limitCatName); err != nil { // повтор безопасен
		t.Fatalf("повтор: %v", err)
	}
	if err := s.DismissPlanCategory(ctx, limitChat-1, limitCatAnother); err != nil {
		t.Fatal(err)
	}
	got, err := s.PlanDismissed(ctx, []int64{limitChat})
	if err != nil || !reflect.DeepEqual(got, []string{limitCatName}) {
		t.Fatalf("PlanDismissed(чат)=%v err=%v", got, err)
	}
	if err := s.RestorePlanCategory(ctx, []int64{limitChat}, limitCatAnother); err != nil { // чужая категория чужого чата
		t.Fatal(err)
	}
	if got, _ := s.PlanDismissed(ctx, []int64{limitChat - 1}); len(got) != 1 {
		t.Fatalf("Restore затронул чужой чат: %v", got)
	}
	if err := s.RestorePlanCategory(ctx, []int64{limitChat}, limitCatName); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PlanDismissed(ctx, []int64{limitChat}); len(got) != 0 {
		t.Fatalf("после Restore: %v", got)
	}
	if err := s.DismissPlanCategory(ctx, limitChat, "нет-такой-категории-xyz"); !errors.Is(err, ErrPlanLimitCategory) {
		t.Fatalf("неизвестная категория: %v", err)
	}
	if err := s.RestorePlanCategory(ctx, []int64{limitChat}, "нет-такой-категории-xyz"); !errors.Is(err, ErrPlanLimitCategory) {
		t.Fatalf("restore неизвестной: %v", err)
	}
}
