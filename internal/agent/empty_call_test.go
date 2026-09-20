package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"simpleAI/internal/constants"
	"simpleAI/internal/plugin"
)

// --- Моки с манифестом ---

// schemaSkill — навык с объявленными обязательными полями во InputSchema.
// Нужен, чтобы роутер мог отбраковать вызов до Run.
type schemaSkill struct {
	id       string
	required []string
	result   string
	err      error
	callLog  []string
}

func (s *schemaSkill) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          s.id,
		Name:        s.id,
		Description: "test skill",
		InputSchema: &plugin.Schema{
			Name:    s.id + "Input",
			Version: "1.0.0",
			JSON: map[string]any{
				"type":     "object",
				"required": s.required,
			},
		},
	}
}

func (s *schemaSkill) Run(_ context.Context, input string) (string, error) {
	s.callLog = append(s.callLog, input)
	if s.err != nil {
		return "", s.err
	}
	return s.result, nil
}

func newSchemaRegistry(t *testing.T, skills ...*schemaSkill) *plugin.Registry {
	t.Helper()
	r := plugin.NewRegistry()
	for _, s := range skills {
		if err := r.Register(s); err != nil {
			t.Fatalf("register skill %q: %v", s.id, err)
		}
	}
	return r
}

// budgetSkill — навык с контрактом боевого budget: обязателен action.
func budgetSkill() *schemaSkill {
	return &schemaSkill{id: "budget", required: []string{"action"}, result: "✅ записано"}
}

// allPrompts собирает всё, что агент отправил модели.
func allPrompts(llm *mockLLM) string {
	var sb strings.Builder
	for _, c := range llm.calls {
		sb.WriteString(c.userPrompt)
		sb.WriteString("\n")
	}
	return sb.String()
}

// --- Регрессия прода: две траты одним сообщением ---

// Ответ модели без объекта input не должен приводить к вызову навыка ни разу.
func TestAsk_CallsWithoutInput_SkillNeverCalled(t *testing.T) {
	llm := &mockLLM{responses: []string{
		`[{"skill":"budget"},{"skill":"budget"}]`,
		`[{"skill":"budget"},{"skill":"budget"}]`,
	}}
	skill := budgetSkill()
	svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "Продукты 512 Бат цветы 99 тайских Бат")
	if err != nil {
		t.Fatal(err)
	}
	if len(skill.callLog) != 0 {
		t.Fatalf("навык не должен вызываться на пустых вызовах, вызван %d раз: %v", len(skill.callLog), skill.callLog)
	}
	if got != constants.MsgAgentUnclearRequest {
		t.Errorf("пользователь должен получить человеческую фразу, получил %q", got)
	}
}

// Повторная попытка ровно одна: после второго некорректного ответа агент
// прекращает и отдаёт человеческую фразу, а не текст Go-ошибки.
func TestAsk_InvalidTwice_SingleRetryThenHumanMessage(t *testing.T) {
	responses := make([]string, maxIterations+2)
	for i := range responses {
		responses[i] = `{"skill":"budget","input":{}}`
	}
	llm := &mockLLM{responses: responses}
	skill := budgetSkill()
	svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "Продукты 512 Бат")
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.calls) != 2 {
		t.Errorf("попытка должна быть ровно одна (2 обращения к модели), было %d", len(llm.calls))
	}
	if len(skill.callLog) != 0 {
		t.Errorf("навык не должен вызываться, вызван %d раз", len(skill.callLog))
	}
	if got != constants.MsgAgentUnclearRequest {
		t.Errorf("want %q, got %q", constants.MsgAgentUnclearRequest, got)
	}
	for _, leak := range []string{"unknown action", "invalid", "err=", "input"} {
		if strings.Contains(got, leak) {
			t.Errorf("ответ пользователю содержит техническую деталь %q: %q", leak, got)
		}
	}
}

// Повторная попытка объясняет модели, какого поля не хватило, и несёт
// исходное сообщение пользователя.
func TestAsk_RetryPromptNamesMissingField(t *testing.T) {
	llm := &mockLLM{responses: []string{
		`[{"skill":"budget"},{"skill":"budget"}]`,
		`[{"skill":"budget","input":{"action":"add_expense","amount":512,"currency":"THB","category":"продукты"}}]`,
		"Записал",
	}}
	skill := budgetSkill()
	svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

	if _, err := svc.Ask(context.Background(), "Продукты 512 Бат цветы 99 тайских Бат"); err != nil {
		t.Fatal(err)
	}
	if len(llm.calls) < 2 {
		t.Fatalf("ожидались 2 обращения к модели, было %d", len(llm.calls))
	}
	retry := llm.calls[1].userPrompt
	for _, want := range []string{"input", "budget", "Продукты 512 Бат цветы 99 тайских Бат"} {
		if !strings.Contains(retry, want) {
			t.Errorf("повторный промпт должен содержать %q, получен: %q", want, retry)
		}
	}
}

// Прод-сценарий целиком: после повторной попытки модель отдаёт две траты —
// обе доезжают до навыка.
func TestAsk_TwoExpensesInOneMessage_BothRecorded(t *testing.T) {
	llm := &mockLLM{responses: []string{
		`[{"skill":"budget"},{"skill":"budget"}]`,
		`[{"skill":"budget","input":{"action":"add_expense","amount":512,"currency":"THB","category":"продукты"}},
		  {"skill":"budget","input":{"action":"add_expense","amount":99,"currency":"THB","category":"цветы"}}]`,
		"Записал обе траты",
	}}
	skill := budgetSkill()
	svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "Продукты 512 Бат цветы 99 тайских Бат")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Записал обе траты" {
		t.Errorf("got %q", got)
	}
	if len(skill.callLog) != 2 {
		t.Fatalf("ожидались 2 транзакции, навык вызван %d раз: %v", len(skill.callLog), skill.callLog)
	}
	type tx struct {
		Action   string  `json:"action"`
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
		Category string  `json:"category"`
	}
	want := []tx{
		{"add_expense", 512, "THB", "продукты"},
		{"add_expense", 99, "THB", "цветы"},
	}
	for i, raw := range skill.callLog {
		var got tx
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("вызов %d не разбирается: %v", i, err)
		}
		if got != want[i] {
			t.Errorf("вызов %d: want %+v, got %+v", i, want[i], got)
		}
	}
}

// --- Утечка внутренней ошибки ---

// Текст Go-ошибки навыка не уезжает ни модели, ни пользователю.
func TestAsk_SkillError_NotLeakedToUser(t *testing.T) {
	llm := &mockLLM{responses: []string{
		`{"skill":"budget","input":{"action":"add_expense","amount":100}}`,
		"Не получилось, попробуйте ещё раз",
	}}
	skill := budgetSkill()
	skill.err = errors.New("boom")
	svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "потратил 100")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "boom") {
		t.Errorf("ответ пользователю содержит текст Go-ошибки: %q", got)
	}
	if strings.Contains(allPrompts(llm), "boom") {
		t.Errorf("текст Go-ошибки уехал модели (она отдаёт результат дословно): %q", allPrompts(llm))
	}
}

// Ошибка неизвестного навыка тоже не утекает дословно, но цикл продолжается.
func TestAsk_UnknownSkillError_NotLeakedButLoopContinues(t *testing.T) {
	llm := &mockLLM{responses: []string{
		`{"skill":"nonexistent","input":{"action":"summary"}}`,
		"Понял, инструмент недоступен.",
	}}
	skill := budgetSkill()
	svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

	got, err := svc.Ask(context.Background(), "что-то сделай")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Понял, инструмент недоступен." {
		t.Errorf("got %q", got)
	}
	if strings.Contains(allPrompts(llm), "unknown skill") {
		t.Errorf("техническая ошибка уехала модели: %q", allPrompts(llm))
	}
	if !strings.Contains(allPrompts(llm), constants.MsgSkillFailed) {
		t.Errorf("модель должна узнать о неудаче инструмента, промпты: %q", allPrompts(llm))
	}
}

// --- Property-based ---

// callShape — форма одного вызова в ответе модели.
type callShape struct {
	skill     string
	hasInput  bool
	hasAction bool
	action    string
}

func (c callShape) json() map[string]any {
	out := map[string]any{"skill": c.skill}
	if c.hasInput {
		in := map[string]any{"amount": 100}
		if c.hasAction {
			in["action"] = c.action
		}
		out["input"] = in
	}
	return out
}

func (c callShape) valid() bool {
	return c.hasInput && c.hasAction && strings.TrimSpace(c.action) != ""
}

func drawCallShape(t *rapid.T, label string) callShape {
	return callShape{
		skill:     "budget",
		hasInput:  rapid.Bool().Draw(t, label+"_has_input"),
		hasAction: rapid.Bool().Draw(t, label+"_has_action"),
		action:    rapid.SampledFrom([]string{"add_expense", "summary", "", "   "}).Draw(t, label+"_action"),
	}
}

// Инвариант: навык никогда не получает вызов без обязательного поля,
// и пачка с хотя бы одним неполным вызовом не выполняется частично.
func TestProperty_SkillNeverReceivesIncompleteCall(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		shapes := rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) callShape {
			return drawCallShape(rt, "c")
		}), 1, 4).Draw(rt, "calls")

		payload := make([]map[string]any, 0, len(shapes))
		for _, s := range shapes {
			payload = append(payload, s.json())
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			rt.Fatal(err)
		}

		llm := &mockLLM{responses: []string{string(raw), "ответ"}}
		skill := budgetSkill()
		svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

		if _, err := svc.Ask(context.Background(), "траты"); err != nil {
			rt.Fatal(err)
		}

		allValid := true
		for _, s := range shapes {
			if !s.valid() {
				allValid = false
			}
		}

		if !allValid && len(skill.callLog) != 0 {
			rt.Fatalf("пачка с неполным вызовом выполнена частично: %v (формы %+v)", skill.callLog, shapes)
		}
		if allValid && len(skill.callLog) != len(shapes) {
			rt.Fatalf("валидная пачка из %d вызовов выполнена %d раз", len(shapes), len(skill.callLog))
		}
		for _, in := range skill.callLog {
			var got map[string]any
			if err := json.Unmarshal([]byte(in), &got); err != nil {
				rt.Fatalf("навык получил неразбираемый вход %q", in)
			}
			action, _ := got["action"].(string)
			if strings.TrimSpace(action) == "" {
				rt.Fatalf("навык получил вызов без action: %q", in)
			}
		}
	})
}

// Инвариант: что бы ни вернул навык в ошибке — этот текст не доходит
// ни до модели, ни до пользователя.
func TestProperty_SkillErrorTextNeverReachesUser(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		token := rapid.StringMatching(`[a-z]{6,12}`).Draw(rt, "token")

		llm := &mockLLM{responses: []string{
			`{"skill":"budget","input":{"action":"add_expense","amount":100}}`,
			"итоговый ответ",
		}}
		skill := budgetSkill()
		skill.err = fmt.Errorf("db failure: %s", token)
		svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

		got, err := svc.Ask(context.Background(), "потратил 100")
		if err != nil {
			rt.Fatal(err)
		}
		if strings.Contains(got, token) {
			rt.Fatalf("текст ошибки в ответе пользователю: %q", got)
		}
		if strings.Contains(allPrompts(llm), token) {
			rt.Fatalf("текст ошибки уехал модели: %q", allPrompts(llm))
		}
	})
}

// Инвариант: сколько бы подряд модель ни отдавала неполные вызовы,
// обращений к ней ровно два, навык не вызывается, ответ — человеческая фраза.
func TestProperty_ExactlyOneRetryOnInvalidCalls(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(2, maxIterations+2).Draw(rt, "invalid_responses")
		variants := []string{
			`{"skill":"budget"}`,
			`{"skill":"budget","input":{}}`,
			`[{"skill":"budget"},{"skill":"budget"}]`,
			`{"skill":"budget","input":{"action":""}}`,
			`{"skill":"budget","input":{"amount":512}}`,
		}
		responses := make([]string, n)
		for i := range responses {
			responses[i] = rapid.SampledFrom(variants).Draw(rt, fmt.Sprintf("resp_%d", i))
		}

		llm := &mockLLM{responses: responses}
		skill := budgetSkill()
		svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

		got, err := svc.Ask(context.Background(), "Продукты 512 Бат цветы 99 тайских Бат")
		if err != nil {
			rt.Fatal(err)
		}
		if len(llm.calls) != 2 {
			rt.Fatalf("обращений к модели должно быть 2, было %d", len(llm.calls))
		}
		if len(skill.callLog) != 0 {
			rt.Fatalf("навык вызван %d раз на неполных вызовах", len(skill.callLog))
		}
		if got != constants.MsgAgentUnclearRequest {
			rt.Fatalf("want %q, got %q", constants.MsgAgentUnclearRequest, got)
		}
	})
}

// Инвариант: валидная пачка любой длины доезжает до навыка целиком и без
// повторной попытки — фикс не режет рабочие вызовы.
func TestProperty_ValidCallsPassThroughUnchanged(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		amounts := rapid.SliceOfN(rapid.Float64Range(1, 100000), 1, 5).Draw(rt, "amounts")

		payload := make([]map[string]any, 0, len(amounts))
		for _, a := range amounts {
			payload = append(payload, map[string]any{
				"skill": "budget",
				"input": map[string]any{"action": "add_expense", "amount": a, "currency": "THB"},
			})
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			rt.Fatal(err)
		}

		llm := &mockLLM{responses: []string{string(raw), "готово"}}
		skill := budgetSkill()
		svc := NewServiceWithRegistry(llm, newSchemaRegistry(t, skill))

		got, err := svc.Ask(context.Background(), "траты")
		if err != nil {
			rt.Fatal(err)
		}
		if got != "готово" {
			rt.Fatalf("got %q", got)
		}
		if len(llm.calls) != 2 {
			rt.Fatalf("валидная пачка не должна вызывать повторную попытку, обращений: %d", len(llm.calls))
		}
		if len(skill.callLog) != len(amounts) {
			rt.Fatalf("ожидалось %d вызовов навыка, было %d", len(amounts), len(skill.callLog))
		}
		for i, in := range skill.callLog {
			var got struct {
				Amount float64 `json:"amount"`
			}
			if err := json.Unmarshal([]byte(in), &got); err != nil {
				rt.Fatal(err)
			}
			if got.Amount != amounts[i] {
				rt.Fatalf("вызов %d: сумма искажена, want %v got %v", i, amounts[i], got.Amount)
			}
		}
	})
}

// --- Property на саму валидацию роутера ---

// Инвариант валидации: вызов проходит тогда и только тогда, когда есть объект
// input и в нём непусты все обязательные поля из схемы навыка.
func TestProperty_ValidateCallsContract(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		fields := rapid.SliceOfNDistinct(
			rapid.SampledFrom([]string{"action", "query", "name"}), 0, 3,
			func(s string) string { return s },
		).Draw(rt, "required")

		hasInput := rapid.Bool().Draw(rt, "has_input")
		var in map[string]any
		if hasInput {
			in = map[string]any{}
			for _, f := range fields {
				switch rapid.IntRange(0, 2).Draw(rt, "fill_"+f) {
				case 0: // поле отсутствует
				case 1:
					in[f] = rapid.SampledFrom([]string{"", "   "}).Draw(rt, "empty_"+f)
				case 2:
					in[f] = "значение"
				}
			}
		}

		manifest := plugin.Manifest{
			ID:          "skl",
			InputSchema: &plugin.Schema{JSON: map[string]any{"required": fields}},
		}
		bad := validateCalls([]toolCall{{Skill: "skl", Input: in}}, []plugin.Manifest{manifest})

		wantValid := hasInput
		for _, f := range fields {
			v, ok := in[f].(string)
			if !ok || strings.TrimSpace(v) == "" {
				wantValid = false
			}
		}
		if wantValid && len(bad) != 0 {
			rt.Fatalf("валидный вызов отбракован: input=%v required=%v → %+v", in, fields, bad)
		}
		if !wantValid && len(bad) == 0 {
			rt.Fatalf("неполный вызов пропущен: input=%v required=%v", in, fields)
		}
	})
}
