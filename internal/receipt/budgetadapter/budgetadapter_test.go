package budgetadapter

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/plugin"
	"simpleAI/internal/receipt"
)

type recordingSkill struct {
	input  string
	chatID any
	reply  string
	err    error
}

func (s *recordingSkill) Manifest() plugin.Manifest { return plugin.Manifest{ID: "budget"} }

func (s *recordingSkill) Run(ctx context.Context, input string) (string, error) {
	s.input = input
	s.chatID = ctx.Value(agent.ChatIDKey{})
	return s.reply, s.err
}

func TestSkillWriter_CallsAddExpenseWithReceiptFields(t *testing.T) {
	skill := &recordingSkill{reply: "✅ Расход 245.5 THB\n⚠️ Конверт «Еда» пробит"}
	r := receipt.Receipt{Merchant: "7-Eleven", Date: "2026-10-16", Currency: "THB", Total: 245.5, Category: "Еда"}

	reply, err := NewSkillWriter(skill).WriteExpense(context.Background(), 42, r)
	if err != nil {
		t.Fatalf("WriteExpense: %v", err)
	}
	if reply != skill.reply {
		t.Fatalf("reply = %q, want skill reply verbatim (ADR-008 warning included)", reply)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(skill.input), &got); err != nil {
		t.Fatalf("skill input is not JSON: %v", err)
	}
	want := map[string]any{"action": "add_expense", "amount": 245.5, "currency": "THB",
		"category": "Еда", "description": "7-Eleven", "date": "2026-10-16"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	// Навык берёт чат из контекста: без него предупреждение о конверте не
	// считается (skills/budget/share_warning.go).
	if skill.chatID != int64(42) {
		t.Fatalf("chat in skill context = %v, want 42", skill.chatID)
	}
}

func TestSkillWriter_WhenSkillFails_ShouldReturnError(t *testing.T) {
	boom := errors.New("db down")
	_, err := NewSkillWriter(&recordingSkill{err: boom}).WriteExpense(context.Background(), 1, receipt.Receipt{Total: 1, Currency: "THB"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want skill error", err)
	}
}

type stubSource struct {
	cats []budget.Category
	err  error
}

func (s stubSource) ListCategories(context.Context) ([]budget.Category, error) { return s.cats, s.err }

func TestCategories_OnlyExpense(t *testing.T) {
	src := stubSource{cats: []budget.Category{
		{Name: "Зарплата", Type: "income"}, {Name: "Еда", Type: "expense"}, {Name: "Транспорт", Type: "expense"},
	}}
	got, err := NewCategories(src).ExpenseCategories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"Еда", "Транспорт"}) {
		t.Fatalf("categories = %v", got)
	}
	if _, err := NewCategories(stubSource{err: errors.New("x")}).ExpenseCategories(context.Background()); err == nil {
		t.Fatal("want error from source")
	}
}
