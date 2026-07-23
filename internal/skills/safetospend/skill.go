package safetospend

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
	"simpleAI/internal/prompts"
)

// store — узкий read-only контракт (ADR-002: skill только читает).
type store interface {
	GetExchangeRates(ctx context.Context) (map[string]float64, error)
	GetPeriodSnapshot(ctx context.Context, chatID int64, from, to time.Time, rates map[string]float64) (*budget.AdvisorSnapshot, error)
	GetForecastData(ctx context.Context, months int, rates map[string]float64) ([]budget.CategoryForecast, error)
	PlannedExpensesTHB(ctx context.Context, chatID int64, rates map[string]float64) (float64, int, error)
	ListPlannedExpenses(ctx context.Context, chatID int64) ([]budget.PlannedExpense, error)
	GetActiveEnvelope(ctx context.Context, chatID int64) (*budget.Envelope, bool, error)
}

// SafeToSpendSkill — read-only reasoning skill (ADR-007 фаза H1).
type SafeToSpendSkill struct {
	store      store
	llm        core.LLM
	logger     *slog.Logger
	llmTimeout time.Duration
}

func NewSafeToSpendSkill(s store, llm core.LLM, logger *slog.Logger) *SafeToSpendSkill {
	if logger == nil {
		logger = slog.Default()
	}
	return &SafeToSpendSkill{store: s, llm: llm, logger: logger, llmTimeout: defaultLLMTimeout}
}

func (s *SafeToSpendSkill) WithLLMTimeout(d time.Duration) *SafeToSpendSkill {
	if d > 0 {
		s.llmTimeout = d
	}
	return s
}

func (s *SafeToSpendSkill) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:      "safe_to_spend",
		Name:    "Safe to Spend",
		Version: "1.0.0",
		Description: "Считает СКОЛЬКО СВОБОДНЫХ ДЕНЕГ останется из ПРИШЕДШЕГО дохода на период, " +
			"после известных обязательств (регулярные платежи, долги) и прогноза трат. Приход берётся из вопроса и НЕ записывается. " +
			"Триггеры RU: «пришло X, сколько свободно?», «получил X, сколько могу потратить?», «пришла зарплата/аванс X, сколько останется?», " +
			"«на что распределить X?», «сколько свободных денег из X на этот месяц?». " +
			"Триггеры EN: 'got X income, how much is free to spend?', 'received X, what's safe to spend?'. " +
			"РЕЖИМ ОСТАТКА (БЕЗ суммы): показывает, сколько свободно осталось из ранее сохранённого конверта, пересчитывая по фактическим тратам. " +
			"Триггеры RU: «сколько свободно осталось?», «сколько осталось из прихода?», «остаток по конверту», «сколько ещё могу потратить?», «сколько денег свободно сейчас?». " +
			"НЕ используй для простой ЗАПИСИ дохода без вопроса о свободных деньгах («пришло X», «запиши доход X», «получил зарплату X» без вопроса) → budget.add_income. " +
			"НЕ используй для СОХРАНЕНИЯ прихода («запомни приход X», «заведи конверт») → budget.start_envelope. " +
			"НЕ используй для affordability конкретной покупки («хватит ли на телефон») → advisor.advice. " +
			"НЕ используй для обзора трат без прихода → advisor.analyze / budget.summary.",
		InputSchema: &plugin.Schema{
			Name:    "SafeToSpendInput",
			Version: "1.0.0",
			JSON: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"amount":   map[string]any{"type": "number", "description": "Сумма пришедшего дохода из сообщения. НЕ указывай в режиме остатка (вопрос без суммы)."},
					"currency": map[string]any{"type": "string", "description": "Валюта прихода: RUB (по умолчанию), THB, USD, EUR."},
					"period":   map[string]any{"type": "string", "description": "Горизонт расчёта. По умолчанию (пусто) — ближайшие 2 недели (интервал между приходами). 'month' — до конца текущего месяца; 'YYYY-MM' — конкретный месяц."},
					"question": map[string]any{"type": "string", "description": "Исходный вопрос пользователя."},
				},
				"required": []string{},
			},
		},
	}
}

type input struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency,omitempty"`
	Period   string  `json:"period,omitempty"`
	Question string  `json:"question,omitempty"`
}

func (s *SafeToSpendSkill) Run(ctx context.Context, raw string) (string, error) {
	var in input
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return "", fmt.Errorf("safe_to_spend: invalid input: %w", err)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "RUB"
	}

	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok {
		s.logger.WarnContext(ctx, "safe_to_spend: chatID missing — chat-scoped данные будут пусты")
	}

	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil || rates["THB"] == 0 {
		s.logger.ErrorContext(ctx, "safe_to_spend: rates", "err", err)
		return "Не могу посчитать — обнови курс валют командой /rates.", nil
	}

	// Без суммы: режим «сколько осталось» по активному конверту (ADR-007 T5).
	if in.Amount <= 0 {
		return s.runRemaining(ctx, chatID, rates)
	}

	incomeTHB, ok := budget.ToTHB(in.Amount, currency, rates)
	if !ok {
		return fmt.Sprintf("Не знаю курс валюты %s — обнови /rates.", currency), nil
	}

	h := budget.ResolveHorizon(in.Period, time.Now(), budget.DefaultHorizonDays)

	snap, err := s.store.GetPeriodSnapshot(ctx, chatID, h.From, h.To, rates)
	if err != nil {
		s.logger.ErrorContext(ctx, "safe_to_spend: period snapshot", "err", err, "chat_id", chatID)
		return "Временная ошибка при сборе финансового снимка — попробуй позже.", nil
	}

	breakdown, forecastTHB := s.forecast(ctx, rates, h.Days())
	plannedItems, plannedTHB := s.plannedBreakdown(ctx, chatID, rates)
	res := computeSafeToSpend(incomeTHB, snap, plannedTHB, forecastTHB)

	advice := s.narrate(ctx, res, rates["THB"], breakdown)

	reply := formatReply(replyData{
		res:       res,
		rubPerTHB: rates["THB"],
		period:    h.Label,
		planned:   plannedItems,
		variable:  breakdown,
		advice:    advice,
	})
	s.logger.InfoContext(ctx, "safe_to_spend",
		"chat_id", chatID, "amount", in.Amount, "currency", currency,
		"free_after_obl_thb", res.FreeAfterObligations, "realistic_free_thb", res.RealisticFree)
	return reply, nil
}

// plannedBreakdown — ручные плановые траты по пунктам (описание→THB) и их итог.
// Отдельный источник от прогноза (budget_planned_expense vs история транзакций) —
// пересечения нет: одна и та же запись не попадает в оба блока (ADR-007 §4).
func (s *SafeToSpendSkill) plannedBreakdown(ctx context.Context, chatID int64, rates map[string]float64) ([]CategorySpend, float64) {
	items, err := s.store.ListPlannedExpenses(ctx, chatID)
	if err != nil {
		s.logger.WarnContext(ctx, "safe_to_spend: planned list (continuing with 0)", "err", err)
		return nil, 0
	}
	out := make([]CategorySpend, 0, len(items))
	var total float64
	for _, p := range items {
		thb, ok := budget.ToTHB(p.Amount, p.Currency, rates)
		if !ok {
			continue
		}
		out = append(out, CategorySpend{Category: p.Description, THB: thb})
		total += thb
	}
	return out, total
}

// forecast — разбивка ожидаемых бытовых трат ЗА ПЕРИОД по категориям (на что
// уйдут деньги) и их итог. GetForecastData даёт месячный прогноз; проративим к
// длине периода. Non-consumption исключены. Best-effort: ошибка → пусто.
func (s *SafeToSpendSkill) forecast(ctx context.Context, rates map[string]float64, days int) ([]CategorySpend, float64) {
	fc, err := s.store.GetForecastData(ctx, forecastMonths, rates)
	if err != nil {
		s.logger.WarnContext(ctx, "safe_to_spend: forecast (continuing with empty)", "err", err)
		return nil, 0
	}
	return buildForecastBreakdown(fc, rates, days)
}

// narrate — LLM даёт АДРЕСНЫЕ советы по экономии на основе разбивки трат по
// категориям (числа не трогает). Best-effort → nil.
func (s *SafeToSpendSkill) narrate(ctx context.Context, res Result, rubPerTHB float64, breakdown []CategorySpend) []string {
	prompt := fmt.Sprintf(prompts.Get("safetospend/narrate.tmpl"),
		res.FreeAfterObligations*rubPerTHB,
		res.ForecastSpendTHB*rubPerTHB,
		breakdownText(breakdown, rubPerTHB))
	llmCtx, cancel := context.WithTimeout(context.Background(), s.llmTimeout)
	defer cancel()
	rawResp, err := s.llm.Ask(llmCtx, prompt)
	if err != nil {
		s.logger.WarnContext(ctx, "safe_to_spend: narrate llm (continuing without)", "err", err)
		return nil
	}
	return parseAdviceLines(rawResp)
}

// breakdownText — «категория: сумма₽» для промпта (топ-6), чтобы советы были
// адресными («в кафе уходит ~X — готовь дома»).
func breakdownText(breakdown []CategorySpend, rubPerTHB float64) string {
	if len(breakdown) == 0 {
		return "нет данных"
	}
	var parts []string
	for i, cs := range breakdown {
		if i >= 6 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s ~%.0f₽", cs.Category, cs.THB*rubPerTHB))
	}
	return strings.Join(parts, ", ")
}

// Период и горизонт вынесены в budget.ResolveHorizon (единый источник).
