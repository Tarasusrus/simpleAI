package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"simpleAI/internal/plugin"
)

// --- Моки ---

// mockLLM реализует core.LLM для тестов.
// responses задаются заранее и отдаются по очереди.
type mockLLM struct {
	responses []string
	calls     []mockCall
	idx       int
}

type mockCall struct {
	systemAddition string
	userPrompt     string
}

func (m *mockLLM) Ask(ctx context.Context, prompt string) (string, error) {
	return m.next(prompt)
}

func (m *mockLLM) AskWithSystem(ctx context.Context, systemAddition, userPrompt string) (string, error) {
	m.calls = append(m.calls, mockCall{systemAddition, userPrompt})
	return m.next(userPrompt)
}

func (m *mockLLM) next(_ string) (string, error) {
	if m.idx >= len(m.responses) {
		return "финальный ответ", nil
	}
	r := m.responses[m.idx]
	m.idx++
	return r, nil
}

// mockSkill выполняет фиксированный ответ и записывает вызовы.
type mockSkill struct {
	id      string
	result  string
	callLog []string
}

func (ms *mockSkill) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: ms.id, Name: ms.id, Description: "test skill"}
}

func (ms *mockSkill) Run(_ context.Context, input string) (string, error) {
	ms.callLog = append(ms.callLog, input)
	return ms.result, nil
}

func newRegistry(t *testing.T, skills ...*mockSkill) *plugin.Registry {
	t.Helper()
	r := plugin.NewRegistry()
	for _, s := range skills {
		if err := r.Register(s); err != nil {
			t.Fatalf("register skill %q: %v", s.id, err)
		}
	}
	return r
}

// --- Тесты parseToolCalls ---

func TestParseToolCalls_SingleJSON(t *testing.T) {
	resp := `{"skill":"budget","input":{"action":"add_expense","amount":100}}`
	calls := parseToolCalls(resp)
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if calls[0].Skill != "budget" {
		t.Errorf("want skill=budget, got %q", calls[0].Skill)
	}
}

func TestParseToolCalls_JSONArray(t *testing.T) {
	resp := `[
		{"skill":"budget","input":{"action":"add_expense","amount":100}},
		{"skill":"budget","input":{"action":"add_expense","amount":200}}
	]`
	calls := parseToolCalls(resp)
	if len(calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(calls))
	}
}

func TestParseToolCalls_MarkdownCodeBlock(t *testing.T) {
	resp := "Вот вызов:\n```json\n{\"skill\":\"budget\",\"input\":{\"action\":\"summary\"}}\n```"
	calls := parseToolCalls(resp)
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if calls[0].Skill != "budget" {
		t.Errorf("want skill=budget, got %q", calls[0].Skill)
	}
}

func TestParseToolCalls_MarkdownArrayBlock(t *testing.T) {
	resp := "```json\n[{\"skill\":\"s1\",\"input\":{}},{\"skill\":\"s2\",\"input\":{}}]\n```"
	calls := parseToolCalls(resp)
	if len(calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(calls))
	}
}

func TestParseToolCalls_PlainText(t *testing.T) {
	calls := parseToolCalls("Привет! Как дела?")
	if len(calls) != 0 {
		t.Errorf("want 0 calls, got %d", len(calls))
	}
}

func TestParseToolCalls_JSONWithoutSkill(t *testing.T) {
	calls := parseToolCalls(`{"foo":"bar","baz":123}`)
	if len(calls) != 0 {
		t.Errorf("want 0 calls (no skill field), got %d", len(calls))
	}
}

func TestParseToolCalls_JSONEmbeddedInText(t *testing.T) {
	resp := `Нужно вызвать инструмент: {"skill":"budget","input":{"action":"summary"}} — сделано.`
	calls := parseToolCalls(resp)
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
}

// --- Тесты Service.Ask ---

func TestAsk_NoRegistry_PassthroughToLLM(t *testing.T) {
	llm := &mockLLM{responses: []string{"просто ответ"}}
	svc := NewService(llm)

	got, err := svc.Ask(context.Background(), "привет")
	if err != nil {
		t.Fatal(err)
	}
	if got != "просто ответ" {
		t.Errorf("want %q, got %q", "просто ответ", got)
	}
}

func TestAsk_NoToolCall_ReturnsLLMResponse(t *testing.T) {
	llm := &mockLLM{responses: []string{"обычный текстовый ответ"}}
	skill := &mockSkill{id: "budget", result: "ok"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "привет")
	if err != nil {
		t.Fatal(err)
	}
	if got != "обычный текстовый ответ" {
		t.Errorf("got %q", got)
	}
	if len(skill.callLog) != 0 {
		t.Errorf("skill should not be called, but was called %d times", len(skill.callLog))
	}
}

func TestAsk_SingleToolCall_ThenFinalAnswer(t *testing.T) {
	toolResp := `{"skill":"budget","input":{"action":"add_expense","amount":100}}`
	llm := &mockLLM{responses: []string{toolResp, "Расход записан!"}}
	skill := &mockSkill{id: "budget", result: "✅ Записан расход: 100 ₽"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "записи расход 100 рублей")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Расход записан!" {
		t.Errorf("got %q", got)
	}
	if len(skill.callLog) != 1 {
		t.Errorf("want 1 skill call, got %d", len(skill.callLog))
	}
}

func TestAsk_MultipleToolCallsInArray_AllExecuted(t *testing.T) {
	toolResp := `[
		{"skill":"budget","input":{"action":"add_expense","amount":100,"description":"первый"}},
		{"skill":"budget","input":{"action":"add_expense","amount":200,"description":"второй"}},
		{"skill":"budget","input":{"action":"add_expense","amount":300,"description":"третий"}}
	]`
	llm := &mockLLM{responses: []string{toolResp, "Все 3 расхода записаны!"}}
	skill := &mockSkill{id: "budget", result: "✅ ok"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "запиши три расхода")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Все 3 расхода записаны!" {
		t.Errorf("got %q", got)
	}
	if len(skill.callLog) != 3 {
		t.Errorf("want 3 skill calls, got %d", len(skill.callLog))
	}
}

func TestAsk_MultipleIterations_SecondBatchAfterFirst(t *testing.T) {
	firstBatch := `[
		{"skill":"budget","input":{"action":"add_expense","amount":100}},
		{"skill":"budget","input":{"action":"add_expense","amount":200}}
	]`
	secondBatch := `{"skill":"budget","input":{"action":"summary"}}`
	llm := &mockLLM{responses: []string{firstBatch, secondBatch, "Готово, всё записано и сводка есть!"}}
	skill := &mockSkill{id: "budget", result: "ok"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "запиши и покажи сводку")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Готово, всё записано и сводка есть!" {
		t.Errorf("got %q", got)
	}
	if len(skill.callLog) != 3 {
		t.Errorf("want 3 skill calls total, got %d", len(skill.callLog))
	}
}

func TestAsk_UnknownSkill_ErrorInResults_LoopContinues(t *testing.T) {
	toolResp := `{"skill":"nonexistent","input":{}}`
	llm := &mockLLM{responses: []string{toolResp, "Понял, инструмент недоступен."}}
	skill := &mockSkill{id: "budget", result: "ok"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "что-то сделай")
	if err != nil {
		t.Fatal(err)
	}
	// Ошибка неизвестного скилла попадает в results и передаётся LLM, не прерывает цикл.
	if got != "Понял, инструмент недоступен." {
		t.Errorf("got %q", got)
	}
	// Второй вызов AskWithSystem должен содержать сообщение об ошибке.
	if len(llm.calls) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(llm.calls))
	}
	if !strings.Contains(llm.calls[1].userPrompt, "Ошибка") {
		t.Errorf("second LLM call should mention error, got: %q", llm.calls[1].userPrompt)
	}
}

func TestAsk_MaxIterationsReached_ReturnsFinalAnswer(t *testing.T) {
	// LLM всегда возвращает tool call — проверяем что цикл не бесконечный.
	infiniteCall := `{"skill":"budget","input":{"action":"summary"}}`
	responses := make([]string, maxIterations+2)
	for i := range responses {
		responses[i] = infiniteCall
	}
	llm := &mockLLM{responses: responses}
	skill := &mockSkill{id: "budget", result: "ok"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	_, err := svc.Ask(context.Background(), "бесконечный цикл")
	if err != nil {
		t.Fatal(err)
	}
	if len(skill.callLog) != maxIterations {
		t.Errorf("want exactly %d skill calls (maxIterations), got %d", maxIterations, len(skill.callLog))
	}
}

func TestAsk_ResultsPassedBackToLLM(t *testing.T) {
	toolResp := `{"skill":"budget","input":{"action":"summary"}}`
	llm := &mockLLM{responses: []string{toolResp, "ок"}}
	skill := &mockSkill{id: "budget", result: "Баланс: 1000 ₽"}
	svc := NewServiceWithRegistry(llm, newRegistry(t, skill))

	_, err := svc.Ask(context.Background(), "покажи сводку")
	if err != nil {
		t.Fatal(err)
	}
	// Второй вызов LLM должен содержать результат skill.
	if len(llm.calls) < 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(llm.calls))
	}
	if !strings.Contains(llm.calls[1].userPrompt, "Баланс: 1000 ₽") {
		t.Errorf("skill result should be in second LLM call, got: %q", llm.calls[1].userPrompt)
	}
}

// --- Тест buildToolsSystemPrompt ---

func TestBuildToolsSystemPrompt_ContainsSkillInfo(t *testing.T) {
	manifests := []plugin.Manifest{
		{ID: "budget", Name: "Budget", Description: "Учёт финансов"},
	}
	prompt := buildToolsSystemPrompt(manifests)

	for _, want := range []string{"budget", "Учёт финансов", "skill", "input"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt should contain %q", want)
		}
	}
}

func TestBuildToolsSystemPrompt_MentionsArraySyntax(t *testing.T) {
	prompt := buildToolsSystemPrompt([]plugin.Manifest{{ID: "x", Description: "y"}})
	if !strings.Contains(prompt, "[{") {
		t.Errorf("prompt should mention array syntax for multiple calls, got: %q", prompt)
	}
}

// --- Тест routing rules в system prompt ---

func TestBuildToolsSystemPrompt_ContainsRoutingRules(t *testing.T) {
	manifests := []plugin.Manifest{
		{ID: "budget", Description: "budget tracker"},
		{ID: "advisor", Description: "advisor"},
	}
	prompt := buildToolsSystemPrompt(manifests)

	mustContain := []string{
		"ROUTING RULES",
		"хочу купить",
		"планирую купить",
		"стоит ли",
		"advisor",
		"add_expense",
		"прошедшем времени",
	}
	for _, s := range mustContain {
		if !strings.Contains(prompt, s) {
			t.Errorf("system prompt missing routing marker %q", s)
		}
	}

	// ROUTING RULES должны идти ДО списка инструментов — приоритет позиции.
	rulesIdx := strings.Index(prompt, "ROUTING RULES")
	toolsIdx := strings.Index(prompt, "Доступные инструменты")
	if rulesIdx == -1 || toolsIdx == -1 || rulesIdx > toolsIdx {
		t.Errorf("ROUTING RULES must precede tool list (rules=%d, tools=%d)", rulesIdx, toolsIdx)
	}
}

// --- Тест контекста ---

func TestAsk_ContextCancelled_ReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // отменяем сразу

	llm := &mockLLM{}
	llm.responses = []string{"ок"}
	// Оборачиваем в LLM который проверяет контекст.
	cancellableLLM := &contextCheckLLM{ctx: ctx}
	skill := &mockSkill{id: "budget", result: "ok"}
	svc := NewServiceWithRegistry(cancellableLLM, newRegistry(t, skill))

	_, err := svc.Ask(ctx, "запрос")
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

type contextCheckLLM struct{ ctx context.Context }

func (c *contextCheckLLM) Ask(ctx context.Context, _ string) (string, error) {
	return "", ctx.Err()
}
func (c *contextCheckLLM) AskWithSystem(ctx context.Context, _, _ string) (string, error) {
	return "", fmt.Errorf("context: %w", ctx.Err())
}
