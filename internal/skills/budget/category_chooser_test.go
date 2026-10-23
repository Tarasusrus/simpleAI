package budgetskill

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"simpleAI/internal/categories"
)

type fakeChooser struct {
	choice categories.Choice
	err    error
	got    []categories.ChooseInput
}

func (f *fakeChooser) Choose(_ context.Context, in categories.ChooseInput) (categories.Choice, error) {
	f.got = append(f.got, in)
	return f.choice, f.err
}

type fakeSink struct {
	got   []categories.Proposal
	txIDs []uuid.UUID
	err   error
}

func (f *fakeSink) AddProposal(_ context.Context, p categories.Proposal, id uuid.UUID) error {
	f.got = append(f.got, p)
	f.txIDs = append(f.txIDs, id)
	return f.err
}

// add_expense без названной категории: Chooser получает описание (и пустое имя), выбранная
// категория пишется в БД и видна в ответе вместе с пояснением; предложение уходит в очередь с id траты.
//
// Мутации: не передавать Description в Choose, не писать CategoryID, не добавлять Note, не звать
// saveProposal — тест краснеет.
func TestAddExpense_UsesChooserAndReportsCategory_Integration(t *testing.T) {
	skill, pool, ctx := newEnvelopeTestSkill(t)
	var other uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM budget_category WHERE name='Прочее' AND type='expense'`).Scan(&other); err != nil {
		t.Fatalf("нет «Прочее»: %v", err)
	}
	ch := &fakeChooser{choice: categories.Choice{
		Category: categories.Category{ID: other, Name: "Прочее", Type: "expense"},
		Source:   categories.SourceDegraded,
		Note:     "не удалось определить категорию, записано в «Прочее»",
		Proposal: &categories.Proposal{Type: "expense", Name: "chooser-test-краски", Description: "chooser-test-краски"},
	}}
	sink := &fakeSink{}
	skill.WithCategoryChooser(ch, sink)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM budget_transaction WHERE description = 'chooser-test-краски'`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	reply, err := skill.Run(ctx, `{"action":"add_expense","amount":643,"currency":"RUB","description":"chooser-test-краски"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.got) != 1 || ch.got[0].Name != "" || ch.got[0].Description != "chooser-test-краски" || ch.got[0].Type != "expense" {
		t.Fatalf("в Choose ушло %+v", ch.got)
	}
	if !strings.Contains(reply, "Прочее") || !strings.Contains(reply, "не удалось определить категорию") {
		t.Errorf("в ответе нет категории и пояснения:\n%s", reply)
	}
	var cat string
	var txID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT t.id, c.name FROM budget_transaction t JOIN budget_category c ON c.id=t.category_id WHERE t.description='chooser-test-краски'`).Scan(&txID, &cat); err != nil || cat != "Прочее" {
		t.Fatalf("категория в БД = %q (err %v), ждали Прочее", cat, err)
	}
	if len(sink.got) != 1 || sink.txIDs[0] != txID {
		t.Errorf("предложение ушло не с id траты: %+v %v (id траты %s)", sink.got, sink.txIDs, txID)
	}
}

// Сбой выбора категории возвращается ошибкой и трата не пишется; сбой записи предложения
// запись траты не ломает.
func TestAddExpense_ChooserFailureAndSinkFailure_Integration(t *testing.T) {
	skill, pool, ctx := newEnvelopeTestSkill(t)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM budget_transaction WHERE description LIKE 'chooser-test-%'`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	skill.WithCategoryChooser(&fakeChooser{err: errors.New("db down")}, &fakeSink{})
	if _, err := skill.Run(ctx, `{"action":"add_expense","amount":10,"currency":"RUB","description":"chooser-test-сбой"}`); err == nil {
		t.Fatal("ошибка выбора категории проглочена")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM budget_transaction WHERE description='chooser-test-сбой'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("трата записана при сбое выбора: %d (err %v)", n, err)
	}

	var other uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM budget_category WHERE name='Прочее' AND type='expense'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	skill.WithCategoryChooser(&fakeChooser{choice: categories.Choice{
		Category: categories.Category{ID: other, Name: "Прочее"}, Proposal: &categories.Proposal{Type: "expense", Name: "x"},
	}}, &fakeSink{err: errors.New("queue down")})
	if _, err := skill.Run(ctx, `{"action":"add_expense","amount":10,"currency":"RUB","description":"chooser-test-очередь"}`); err != nil {
		t.Fatalf("сбой очереди сломал запись траты: %v", err)
	}
}

// Без chooser поведение прежнее: категория из запроса через GetOrCreateCategory.
func TestAddExpense_WithoutChooserKeepsLegacy_Integration(t *testing.T) {
	skill, pool, ctx := newEnvelopeTestSkill(t)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM budget_transaction WHERE description = 'chooser-test-legacy'`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	reply, err := skill.Run(ctx, `{"action":"add_expense","amount":10,"currency":"RUB","category":"прочее","description":"chooser-test-legacy"}`)
	if err != nil || !strings.Contains(reply, "Прочее") || strings.Contains(reply, "ℹ️") {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
}
