package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"simpleAI/internal/plugin"
)

const expenseReply = "🔴 Расход записан: 880 ฿ → Одежда | 09.11.2026\n⚠️ конверт «Одежда» пробит"

func TestAsk_DirectReply_ReturnsSkillResultVerbatim(t *testing.T) {
	toolResp := `{"skill":"budget","input":{"action":"add_expense","amount":880}}`
	llm := &mockLLM{responses: []string{toolResp, "Добавлено: 880 ТНВ, одежда"}}
	skill := &mockSkill{id: "budget", result: expenseReply, direct: true}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "Одежда 880 бат")
	if err != nil {
		t.Fatal(err)
	}
	if got != expenseReply {
		t.Errorf("ответ навыка должен дойти как есть, got %q", got)
	}
	if len(llm.calls) != 1 {
		t.Errorf("второго вызова LLM быть не должно, calls=%d", len(llm.calls))
	}
}

func TestAsk_DirectReply_MultiCallJoinedInOrder(t *testing.T) {
	toolResp := `[
		{"skill":"budget","input":{"action":"add_expense","amount":1}},
		{"skill":"budget","input":{"action":"add_expense","amount":2}}
	]`
	llm := &mockLLM{responses: []string{toolResp, "пересказ"}}
	skill := &mockSkill{id: "budget", result: "A", direct: true}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "два расхода")
	if err != nil {
		t.Fatal(err)
	}
	if got != "A\n\nA" {
		t.Errorf("got %q", got)
	}
	if strings.Contains(got, "[1]") {
		t.Errorf("служебный префикс не должен попасть пользователю: %q", got)
	}
}

func TestAsk_DirectReply_FailedCallFallsBackToLLM(t *testing.T) {
	toolResp := `{"skill":"budget","input":{"action":"add_expense","amount":1}}`
	llm := &mockLLM{responses: []string{toolResp, "Не получилось, попробуй ещё раз"}}
	skill := &mockSkill{id: "budget", direct: true, err: errors.New("pq: connection refused secret-host")}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "расход")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "secret-host") {
		t.Errorf("текст Go-ошибки утёк: %q", got)
	}
	if len(llm.calls) != 2 {
		t.Errorf("при ошибке ответ собирает LLM, calls=%d", len(llm.calls))
	}
}

func TestAsk_DirectReply_MixedWithNonDirectGoesThroughLLM(t *testing.T) {
	toolResp := `[
		{"skill":"budget","input":{"action":"add_expense","amount":1}},
		{"skill":"rag_search","input":{"query":"x"}}
	]`
	llm := &mockLLM{responses: []string{toolResp, "итог от LLM"}}
	direct := &mockSkill{id: "budget", result: "готовый текст", direct: true}
	raw := &mockSkill{id: "rag_search", result: "промпт с фрагментами"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, direct, raw))

	got, err := svc.Ask(context.Background(), "смесь")
	if err != nil {
		t.Fatal(err)
	}
	if got != "итог от LLM" || len(llm.calls) != 2 {
		t.Errorf("смесь должна идти через LLM: got %q calls=%d", got, len(llm.calls))
	}
}

func TestAsk_DirectReply_EmptyResultGoesThroughLLM(t *testing.T) {
	toolResp := `{"skill":"budget","input":{"action":"summary"}}`
	llm := &mockLLM{responses: []string{toolResp, "Данных пока нет"}}
	skill := &mockSkill{id: "budget", result: "  \n", direct: true}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "итоги")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Данных пока нет" {
		t.Errorf("пустой результат не должен уходить пользователю, got %q", got)
	}
}

func TestAsk_NonDirectSkillStillGoesThroughLLM(t *testing.T) {
	toolResp := `{"skill":"rag_search","input":{"query":"x"}}`
	llm := &mockLLM{responses: []string{toolResp, "ответ по фрагментам"}}
	skill := &mockSkill{id: "rag_search", result: "промпт"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "вопрос")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ответ по фрагментам" {
		t.Errorf("got %q", got)
	}
}

func TestProperty_DirectReply_AllResultsVerbatimNoSecondLLMCall(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 4).Draw(t, "n")
		result := rapid.StringMatching(`[A-Za-zА-Яа-я0-9฿→|.][A-Za-zА-Яа-я0-9 ฿→|.]{0,39}`).Draw(t, "result")
		calls := make([]string, n)
		for i := range calls {
			calls[i] = `{"skill":"budget","input":{"action":"summary"}}`
		}
		toolResp := "[" + strings.Join(calls, ",") + "]"
		llm := &mockLLM{responses: []string{toolResp, "пересказ"}}
		skill := &mockSkill{id: "budget", result: result, direct: true}
		reg := plugin.NewRegistry()
		if err := reg.Register(skill); err != nil {
			t.Fatal(err)
		}
		svc := NewServiceWithRegistry(llm, reg)

		got, err := svc.Ask(context.Background(), "q")
		if err != nil {
			t.Fatal(err)
		}
		if len(llm.calls) != 1 {
			t.Fatalf("calls=%d", len(llm.calls))
		}
		if strings.Count(got, result) < n {
			t.Fatalf("результат %q должен встретиться %d раз, got %q", result, n, got)
		}
	})
}
