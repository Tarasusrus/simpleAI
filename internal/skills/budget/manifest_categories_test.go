package budgetskill

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func categoryDesc(t *testing.T, s *BudgetSkill) string {
	t.Helper()
	props, ok := s.Manifest().InputSchema.JSON["properties"].(map[string]any)
	if !ok {
		t.Fatal("в схеме нет properties")
	}
	cat, ok := props["category"].(map[string]any)
	if !ok {
		t.Fatal("в схеме нет поля category")
	}
	d, _ := cat["description"].(string) //nolint:errcheck // проверяется ниже по содержимому
	return d
}

// Список категорий в схеме берётся из БД, а не из кода: появившаяся категория видна модели, а
// «Еда» (не назначаемая) в список не попадает, если провайдер её не отдал.
//
// Мутация: вернуть хардкод-список в categoryFieldDescription/expenseCategoryNames — тест краснеет.
func TestManifest_CategoriesComeFromProvider(t *testing.T) {
	s := NewBudgetSkill(nil).WithCategoryNames(func(context.Context) ([]string, error) {
		return []string{"Продукты", "Рестораны", "Хобби-из-базы"}, nil
	})
	d := categoryDesc(t, s)
	if !strings.Contains(d, "Хобби-из-базы") || !strings.Contains(d, "Рестораны") {
		t.Errorf("категории из БД не попали в схему: %s", d)
	}
	if strings.Contains(d, "Транспорт") || strings.Contains(d, "еда (food)") {
		t.Errorf("в схеме остался хардкод: %s", d)
	}
}

// Инструкция модели: категорию называет только пользователь, иначе поле пустое.
//
// Мутация: убрать фразу про пустую категорию — тест краснеет.
func TestManifest_TellsModelToLeaveCategoryEmpty(t *testing.T) {
	d := categoryDesc(t, NewBudgetSkill(nil))
	for _, want := range []string{"ONLY when the user explicitly named", "LEAVE category EMPTY", "never use 'прочее' as a guess"} {
		if !strings.Contains(strings.ToLower(d), strings.ToLower(want)) {
			t.Errorf("в описании категории нет %q:\n%s", want, d)
		}
	}
}

// Сбой БД не ломает схему: запасной список; ранее полученный список из БД переживает сбой;
// список кэшируется на TTL и обновляется после него.
//
// Мутации: убрать кэш, убрать запасной список, убрать «последний удачный» — тест краснеет.
func TestManifest_CacheAndFallback(t *testing.T) {
	calls := 0
	var fail bool
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := NewBudgetSkill(nil).WithCategoryNames(func(context.Context) ([]string, error) {
		calls++
		if fail {
			return nil, errors.New("db down")
		}
		return []string{"Первая"}, nil
	})
	s.now = func() time.Time { return now }

	if d := categoryDesc(t, s); !strings.Contains(d, "Первая") {
		t.Fatalf("первый запрос: %s", d)
	}
	categoryDesc(t, s)
	if calls != 1 {
		t.Errorf("провайдер вызван %d раз за два запроса внутри TTL, ждали 1", calls)
	}
	now = now.Add(categoryTTL + time.Second)
	fail = true
	if d := categoryDesc(t, s); !strings.Contains(d, "Первая") {
		t.Errorf("после сбоя пропал последний удачный список: %s", d)
	}

	fresh := NewBudgetSkill(nil).WithCategoryNames(func(context.Context) ([]string, error) { return nil, errors.New("db down") })
	if d := categoryDesc(t, fresh); !strings.Contains(d, "Продукты") || !strings.Contains(d, "Прочее") {
		t.Errorf("запасной список не сработал: %s", d)
	}
}

// После сбоя БД провайдер не дёргается на каждый запрос схемы, а спустя паузу пробуется снова.
//
// Мутация: убрать проверку catRetryAt — тест краснеет.
func TestManifest_NoRetryStormAfterFailure(t *testing.T) {
	calls := 0
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := NewBudgetSkill(nil).WithCategoryNames(func(context.Context) ([]string, error) {
		calls++
		return nil, errors.New("db down")
	})
	s.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		categoryDesc(t, s)
	}
	if calls != 1 {
		t.Fatalf("провайдер вызван %d раз за 5 запросов после сбоя, ждали 1", calls)
	}
	now = now.Add(categoryRetry + time.Second)
	categoryDesc(t, s)
	if calls != 2 {
		t.Errorf("после паузы повтор не случился: %d вызовов", calls)
	}
}
