package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"simpleAI/internal/apperr"
	"simpleAI/internal/categories"
	"simpleAI/internal/constants"
	"simpleAI/internal/prompts"
)

// ClassifierClient — categories.Classifier поверх OpenAI-совместимого API (маршрут standard
// прокси LiteLLM, ADR-012 §4). Как VisionClient: без ретраев и без fallback-клиента, цепочку
// моделей ведёт сам маршрут. Защита (потолки, предохранитель, проверка id) — в
// categories.GuardedClassifier поверх этого клиента.
type ClassifierClient struct {
	api     openai.Client
	model   string
	timeout time.Duration
}

// NewClassifierClient создаёт клиент. baseURL, apiKey и model обязательны.
func NewClassifierClient(baseURL, apiKey, model string, timeout time.Duration) (*ClassifierClient, error) {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" || strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("CATEGORY_LLM_BASE_URL, CATEGORY_LLM_API_KEY and CATEGORY_LLM_MODEL must be set")
	}
	api := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
		// Описания трат — личные данные: прокси не пишет сообщения в Langfuse и spend logs.
		option.WithHeader("x-litellm-enable-message-redaction", "true"),
	)
	return &ClassifierClient{api: api, model: model, timeout: timeout}, nil
}

type classifyCandidate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Hint string `json:"hint,omitempty"`
}

type classifyInput struct {
	Description string              `json:"description"`
	Categories  []classifyCandidate `json:"categories"`
}

type classifyOutput struct {
	Action     string  `json:"action"`
	CategoryID *string `json:"category_id"`
	Name       *string `json:"name"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// classifySchema описывает ответ и ограничивает category_id списком кандидатов (ADR-012 §4). Схема
// уходит модели текстом, поэтому чужой id она вернуть может: проверка в GuardedClassifier обязательна.
func classifySchema(candidates []categories.Candidate) map[string]any {
	ids := make([]any, 0, len(candidates)+1)
	for _, c := range candidates {
		ids = append(ids, c.ID.String())
	}
	ids = append(ids, nil)
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"action", "category_id", "name", "confidence", "reason"},
		"properties": map[string]any{
			"action":      map[string]any{"type": "string", "enum": []string{categories.ActionExisting, categories.ActionCreate, categories.ActionNone}},
			"category_id": map[string]any{"type": []string{"string", "null"}, "enum": ids},
			"name":        map[string]any{"type": []string{"string", "null"}},
			"confidence":  map[string]any{"type": "number"},
			"reason":      map[string]any{"type": "string"},
		},
	}
}

// Classify шлёт описание и список кандидатов; суммы, даты и chat_id в запрос не попадают.
// Описание в логи не пишется ни здесь, ни в ошибках.
func (c *ClassifierClient) Classify(ctx context.Context, description string, candidates []categories.Candidate) (categories.Classification, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	in := classifyInput{Description: description}
	for _, k := range candidates {
		in.Categories = append(in.Categories, classifyCandidate{ID: k.ID.String(), Name: k.Name, Hint: k.Hint})
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return categories.Classification{}, fmt.Errorf("classifier: marshal: %w", err)
	}

	schema, err := json.Marshal(classifySchema(candidates))
	if err != nil {
		return categories.Classification{}, fmt.Errorf("classifier: marshal schema: %w", err)
	}
	// json_object вместо json_schema: его принимают все провайдеры, включая DeepSeek (тот отвечает 400 на
	// json_schema). Схема уходит текстом в системное сообщение, чужой id отсекает GuardedClassifier.
	system := prompts.Get("categories/classify.tmpl") + "\n\nФормат ответа: один JSON-объект, соответствующий этой JSON Schema:\n" + string(schema)

	resp, err := c.api.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: c.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage(string(payload)),
		},
		Temperature: openai.Float(0),
		// Ответ короткий, но кириллица и тайский дорого стоят в токенах: 200 обрезало JSON посреди строки
		// (замер на standard 2026-10-21).
		MaxTokens: openai.Int(400),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
	})
	if err != nil {
		return categories.Classification{}, err
	}
	if len(resp.Choices) == 0 {
		return categories.Classification{}, apperr.New(constants.ErrCodeLLMEmptyChoices, constants.ErrMsgLLMEmptyChoices, nil)
	}
	return parseClassification(resp.Choices[0].Message.Content)
}

func parseClassification(content string) (categories.Classification, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return categories.Classification{}, apperr.New(constants.ErrCodeLLMEmptyContent, constants.ErrMsgLLMEmptyContent, nil)
	}
	var out classifyOutput
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return categories.Classification{}, fmt.Errorf("classifier: ответ не JSON: %w", err)
	}
	res := categories.Classification{Action: out.Action, Confidence: out.Confidence, Reason: out.Reason}
	if out.Name != nil {
		res.Name = *out.Name
	}
	if out.CategoryID != nil && *out.CategoryID != "" {
		id, err := uuid.Parse(*out.CategoryID)
		if err != nil {
			return categories.Classification{}, fmt.Errorf("classifier: category_id не UUID: %w", err)
		}
		res.CategoryID = id
	}
	return res, nil
}
