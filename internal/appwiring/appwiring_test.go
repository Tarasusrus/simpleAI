package appwiring

import (
	"context"
	"testing"
	"time"

	"simpleAI/internal/core"
)

// fakeLLM реализует core.LLMClient без сети — для проверки сборки обвязки.
type fakeLLM struct{}

func (fakeLLM) Ask(context.Context, string) (string, error)                   { return "", nil }
func (fakeLLM) AskWithSystem(context.Context, string, string) (string, error) { return "", nil }
func (fakeLLM) Embed(context.Context, []string) ([][]float32, error)          { return nil, nil }

var _ core.LLMClient = fakeLLM{}

func TestBuildRegistry_RegistersExpectedSkills(t *testing.T) {
	// pool=nil безопасен: конструкторы store/retriever только сохраняют пул,
	// не обращаясь к БД при сборке.
	reg, bs := BuildRegistry(fakeLLM{}, nil, nil, 30*time.Second)
	if bs == nil {
		t.Fatal("BuildRegistry вернул nil BudgetSkill")
	}
	got := make(map[string]bool)
	for _, m := range reg.List() {
		got[m.ID] = true
	}
	for _, want := range []string{"rag_search", "budget", "advisor", "safe_to_spend"} {
		if !got[want] {
			t.Errorf("реестр не содержит skill %q (есть: %v)", want, got)
		}
	}
}

func TestBuildAgentService_NotNil(t *testing.T) {
	reg, _ := BuildRegistry(fakeLLM{}, nil, nil, 30*time.Second)
	svc := BuildAgentService(fakeLLM{}, reg, nil, nil, nil, "test-model")
	if svc == nil {
		t.Fatal("BuildAgentService вернул nil")
	}
}
