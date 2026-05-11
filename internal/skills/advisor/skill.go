package advisorskill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"simpleAI/internal/budget"
	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
)


// advisorStore — узкий интерфейс store-зависимостей AdvisorSkill (для тестируемости).
type advisorStore interface {
	GetExchangeRates(ctx context.Context) (map[string]float64, error)
	GetAdvisorSnapshot(ctx context.Context, chatID int64, today time.Time, monthOffset int, rates map[string]float64) (*budget.AdvisorSnapshot, error)
	GetTopExpenseTransactions(ctx context.Context, today time.Time, monthOffset int, limit int, rates map[string]float64) ([]budget.TopExpense, error)
	GetForecastData(ctx context.Context, months int, rates map[string]float64) ([]budget.CategoryForecast, error)
	GetMonthlyIncomeAvg(ctx context.Context, rates map[string]float64) (float64, error)
}

// defaultAdvisorLLMTimeout — used when no timeout is configured.
const defaultAdvisorLLMTimeout = 45 * time.Second

// AdvisorSkill — LLM-reasoning skill для советов по покупкам и приоритезации (ADR-002).
type AdvisorSkill struct {
	store      advisorStore
	llm        core.LLM
	logger     *slog.Logger
	llmTimeout time.Duration // independent from Telegram handler context
}

// NewAdvisorSkill создаёт AdvisorSkill.
func NewAdvisorSkill(store advisorStore, llm core.LLM, logger *slog.Logger) *AdvisorSkill {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdvisorSkill{store: store, llm: llm, logger: logger, llmTimeout: defaultAdvisorLLMTimeout}
}

// WithLLMTimeout overrides the LLM call timeout for advisor skill.
func (s *AdvisorSkill) WithLLMTimeout(d time.Duration) *AdvisorSkill {
	if d > 0 {
		s.llmTimeout = d
	}
	return s
}

// Manifest описывает skill для LLM-роутера.
func (s *AdvisorSkill) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:      "advisor",
		Name:    "Financial Advisor",
		Version: "1.0.0",
		Description: "Financial advisor for purchase decisions, affordability checks and expense prioritization. Two actions. " +
			"action='advice' — affordability/purchase decision for a SPECIFIC item (planned purchase). " +
			"Triggers EN: 'can we afford X?', 'should I buy this now?', 'do we have enough money for X?', 'is it worth buying?', 'what should I prioritize?'. " +
			"Triggers RU: «планирую купить ...», «хочу купить ...», «думаю купить ...», «стоит ли покупать ...», «можем ли мы купить ...», «хватит ли денег на ...», «потянем ли ...», «что приоритетнее ...». " +
			"action='analyze' — FREE-FORM overview of spending for a period: anomalies, trends, savings advice. NO specific item. " +
			"Triggers EN: 'analyze my spending', 'spending overview', 'what are my spending anomalies?', 'how am I doing this month?', 'review my expenses'. " +
			"Triggers RU: «проанализируй траты», «обзор трат», «что с моими расходами», «как я в этом месяце трачу», «найди аномалии в тратах», «дай советы по экономии». " +
			"Do NOT use for RECORDING a completed purchase (past tense 'купил'/'потратил' → budget.add_expense). " +
			"Do NOT use for plain numerical summary or transaction listing — use budget skill (budget.summary / budget.list_transactions / budget.forecast). " +
			"Use 'advice' for a single planned item, 'analyze' for free-form analysis of the whole period.",
		InputSchema: &plugin.Schema{
			Name:    "AdvisorInput",
			Version: "1.0.0",
			JSON: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "Action to perform: 'advice' (specific purchase decision) or 'analyze' (free-form spending overview).",
						"enum":        []string{"advice", "analyze"},
					},
					"question": map[string]any{
						"type":        "string",
						"description": "Original user question verbatim. Required for action='advice', optional for 'analyze'.",
					},
					"period": map[string]any{
						"type":        "string",
						"description": "For action='analyze': period to analyze. 'month' (default) for current month, or 'YYYY-MM' for a specific month.",
					},
					"amount": map[string]any{
						"type":        "number",
						"description": "For action='advice': amount mentioned in the question.",
					},
					"currency": map[string]any{
						"type":        "string",
						"description": "ISO 4217 currency for amount: THB, RUB, USD, EUR. Default: THB.",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

// advisorInput — payload для AdvisorSkill.
type advisorInput struct {
	Action   string  `json:"action"`
	Question string  `json:"question,omitempty"`
	Period   string  `json:"period,omitempty"`
	Amount   float64 `json:"amount,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

// Run диспатчит по action: 'advice' или 'analyze'.
func (s *AdvisorSkill) Run(ctx context.Context, input string) (string, error) {
	var req advisorInput
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return "", fmt.Errorf("invalid advisor input: %w", err)
	}
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = "advice"
	}
	switch action {
	case "advice":
		return s.runAdvice(ctx, req)
	case "analyze":
		return s.runAnalyze(ctx, req)
	default:
		return "", fmt.Errorf("advisor: unknown action %q", action)
	}
}
