package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
)

// advisorStore — узкий интерфейс store-зависимостей AdvisorSkill (для тестируемости).
// *budget.Store удовлетворяет его автоматически.
type advisorStore interface {
	GetExchangeRates(ctx context.Context) (map[string]float64, error)
	GetAdvisorSnapshot(ctx context.Context, chatID int64, today time.Time, monthOffset int, rates map[string]float64) (*budget.AdvisorSnapshot, error)
	GetTopExpenseTransactions(ctx context.Context, today time.Time, monthOffset int, limit int, rates map[string]float64) ([]budget.TopExpense, error)
	GetForecastData(ctx context.Context, months int, rates map[string]float64) ([]budget.CategoryForecast, error)
}

// AdvisorSkill — LLM-reasoning skill для советов по покупкам и приоритезации (ADR-002).
//
// Auto-routed: LLM-роутер выбирает skill по manifest description при финансовых
// вопросах вида «можем купить X?», «хватит ли денег?», «что приоритетнее?».
// Stateless: каждый вызов независим, никакой conversation history.
type AdvisorSkill struct {
	store  advisorStore
	llm    core.LLM
	logger *slog.Logger
}

// NewAdvisorSkill создаёт AdvisorSkill.
func NewAdvisorSkill(store advisorStore, llm core.LLM, logger *slog.Logger) *AdvisorSkill {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdvisorSkill{store: store, llm: llm, logger: logger}
}

// Manifest описывает skill для LLM-роутера.
//
// Description формулируется так, чтобы LLM однозначно выбирала advisor для
// affordability-вопросов и НЕ путала с budget.summary / budget.forecast /
// budget.add_expense (negative phrase ниже).
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

// advisorInput — payload для AdvisorSkill (actions: advice, analyze).
type advisorInput struct {
	Action   string  `json:"action"`
	Question string  `json:"question,omitempty"`
	Period   string  `json:"period,omitempty"` // для action='analyze'
	Amount   float64 `json:"amount,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

// advisorLLMResponse — JSON-структура которую обязана вернуть LLM.
type advisorLLMResponse struct {
	Verdict string `json:"verdict"` // "Да" | "Нет" | "Условно"
	Numbers struct {
		FreeCashTHB           float64 `json:"free_cash_thb"`
		ForecastRemainingTHB  float64 `json:"forecast_remaining_thb"`
		ObligationsTHB        float64 `json:"obligations_thb"`
	} `json:"numbers"`
	Explanation    string `json:"explanation"`
	Recommendation string `json:"recommendation,omitempty"`
}

const advisorPromptTemplate = `Ты — финансовый советник для семьи экспатов. Все суммы в батах (THB).

Вопрос пользователя: %s
%s

Финансовый контекст (THB):
- Баланс месяца к дате (доходы − расходы): %.0f
- Прогноз остатка до конца месяца: %.0f
- Свободные деньги (баланс − обязательства): %.0f
- Предстоящие повторяющиеся платежи до конца месяца: %.0f
- Активные долги к погашению до конца месяца: %.0f
- Транзакций в этом месяце: %d%s

Расходы по категориям этого месяца (THB):
%s

Ответь СТРОГО валидным JSON без markdown-форматирования по схеме:
{
  "verdict": "Да" | "Нет" | "Условно",
  "numbers": {
    "free_cash_thb": <число>,
    "forecast_remaining_thb": <число>,
    "obligations_thb": <число — recurring + долги>
  },
  "explanation": "<2-3 предложения по-русски: почему такой вердикт>",
  "recommendation": "<опционально: альтернатива или предупреждение если verdict не 'Да'>"
}

Правила:
- Если verdict не 'Да' — recommendation обязательна.
- Если low_data=true — verdict должен быть 'Условно' с явным упоминанием недостатка данных.
- Числа — округлённые THB, без валютных символов.
`

// Run диспатчит по action: 'advice' (default, для совместимости) или 'analyze'.
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

// runAdvice — старая логика action='advice': снимок + LLM-вердикт по конкретной покупке.
func (s *AdvisorSkill) runAdvice(ctx context.Context, req advisorInput) (string, error) {
	start := time.Now()

	if strings.TrimSpace(req.Question) == "" {
		return "", fmt.Errorf("advisor: question is required for action='advice'")
	}

	chatID, hasChatID := ctx.Value(agent.ChatIDKey{}).(int64)
	if !hasChatID {
		s.logger.WarnContext(ctx, "advisor: chatID missing in context — recurring snapshot будет пуст")
		chatID = 0
	}

	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor: get exchange rates", "err", err, "chat_id", chatID)
		return "Временная ошибка с курсами валют — попробуй позже.", nil
	}
	if _, ok := rates["THB"]; !ok || rates["THB"] == 0 {
		s.logger.ErrorContext(ctx, "advisor: THB rate missing", "chat_id", chatID)
		return "Не могу посчитать в THB — обнови курс валют командой /rates.", nil
	}

	// Конверсия суммы из вопроса (если указана).
	origAmount, origCurrency, amountTHB := req.Amount, strings.ToUpper(req.Currency), req.Amount
	if origCurrency == "" {
		origCurrency = "THB"
	}
	if origAmount > 0 && origCurrency != "THB" {
		rubRate, ok := rates[origCurrency]
		if !ok || rubRate == 0 {
			s.logger.ErrorContext(ctx, "advisor: currency rate missing", "currency", origCurrency, "chat_id", chatID)
			return fmt.Sprintf("Не знаю курс %s — обнови курс или укажи сумму в THB.", origCurrency), nil
		}
		amountTHB = origAmount * rubRate / rates["THB"]
	}

	snap, err := s.store.GetAdvisorSnapshot(ctx, chatID, time.Now(), 0, rates)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor: get snapshot", "err", err, "chat_id", chatID)
		return "Временная ошибка при сборе финансового снимка — попробуй позже.", nil
	}

	forecasts, err := s.store.GetForecastData(ctx, 0, rates)
	if err != nil {
		s.logger.WarnContext(ctx, "advisor: get forecast (continuing without)", "err", err, "chat_id", chatID)
	}
	snap.ForecastRemaining = computeForecastRemaining(snap, forecasts, rates)

	prompt := buildAdvisorPrompt(req, snap, amountTHB, origAmount, origCurrency)

	raw, err := s.llm.Ask(ctx, prompt)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor: llm call", "err", err, "chat_id", chatID)
		return "Не удалось получить совет — попробуй позже.", nil
	}

	parsed, err := parseAdvisorLLMResponse(raw)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor: parse llm response", "err", err, "raw", raw, "chat_id", chatID)
		return "Не смог разобрать ответ советника — попробуй переформулировать вопрос.", nil
	}

	reply := formatAdvisorReply(parsed, origAmount, origCurrency, amountTHB)

	q := truncateRunes(req.Question, 200)
	s.logger.InfoContext(ctx, "advisor",
		"skill", "advisor",
		"action", "advice",
		"chat_id", chatID,
		"question", q,
		"verdict", parsed.Verdict,
		"free_cash_thb", parsed.Numbers.FreeCashTHB,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return reply, nil
}

// computeForecastRemaining оценивает остаток месяца в THB.
//
// projected_total_expense_thb = SUM(forecast.ForecastAmount, конвертированный в THB).
// remaining_expected = max(0, projected − spent_so_far).
// ForecastRemaining = BalanceMTD − remaining_expected.
func computeForecastRemaining(snap *budget.AdvisorSnapshot, forecasts []budget.CategoryForecast, rates map[string]float64) float64 {
	thbRate := rates["THB"]
	if thbRate == 0 {
		return snap.BalanceMTD
	}
	var projectedTotal float64
	for _, f := range forecasts {
		rubRate, ok := rates[f.Currency]
		if !ok || rubRate == 0 {
			continue
		}
		projectedTotal += f.ForecastAmount * rubRate / thbRate
	}
	var spentSoFar float64
	for _, v := range snap.SpentByCategory {
		spentSoFar += v
	}
	remainingExpected := projectedTotal - spentSoFar
	if remainingExpected < 0 {
		remainingExpected = 0
	}
	return snap.BalanceMTD - remainingExpected
}

// buildAdvisorPrompt рендерит prompt по фикстуре snapshot.
func buildAdvisorPrompt(req advisorInput, snap *budget.AdvisorSnapshot, amountTHB, origAmount float64, origCurrency string) string {
	var amountLine string
	if origAmount > 0 {
		if origCurrency == "THB" {
			amountLine = fmt.Sprintf("Сумма из вопроса: %.0f THB", amountTHB)
		} else {
			amountLine = fmt.Sprintf("Сумма из вопроса: %.0f %s ≈ %.0f THB", origAmount, origCurrency, amountTHB)
		}
	}

	lowDataNote := ""
	if snap.LowData {
		lowDataNote = fmt.Sprintf(" (low_data=true, порог=%d — данных недостаточно для уверенного прогноза)", budget.MinTxForConfidence)
	}

	return fmt.Sprintf(advisorPromptTemplate,
		strings.TrimSpace(req.Question),
		amountLine,
		snap.BalanceMTD,
		snap.ForecastRemaining,
		snap.FreeCash,
		snap.UpcomingRecurring,
		snap.ActiveDebtDue,
		snap.TxCount,
		lowDataNote,
		formatSpentByCategory(snap.SpentByCategory),
	)
}

func formatSpentByCategory(m map[string]float64) string {
	if len(m) == 0 {
		return "  (нет расходов)"
	}
	type kv struct {
		k string
		v float64
	}
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].v > out[j].v })
	var sb strings.Builder
	for _, e := range out {
		fmt.Fprintf(&sb, "  - %s: %.0f\n", e.k, e.v)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// parseAdvisorLLMResponse — парсит JSON-ответ LLM.
//
// Допускает обрамление ```json ... ``` если LLM проигнорировала инструкцию.
// Валидирует verdict и обязательность recommendation при verdict != 'Да'.
func parseAdvisorLLMResponse(raw string) (*advisorLLMResponse, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	var r advisorLLMResponse
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	switch r.Verdict {
	case "Да", "Нет", "Условно":
	default:
		return nil, fmt.Errorf("invalid verdict: %q", r.Verdict)
	}
	if strings.TrimSpace(r.Explanation) == "" {
		return nil, fmt.Errorf("explanation is empty")
	}
	if r.Verdict != "Да" && strings.TrimSpace(r.Recommendation) == "" {
		return nil, fmt.Errorf("recommendation required when verdict=%q", r.Verdict)
	}
	return &r, nil
}

// truncateRunes обрезает строку до n рун (UTF-8 safe).
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// escapeTelegramMarkdown экранирует символы Telegram legacy Markdown в произвольном
// тексте от LLM, чтобы несбалансированные `*`/`_`/`[`/`` ` `` не сломали parse_mode.
func escapeTelegramMarkdown(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"`", "\\`",
	)
	return r.Replace(s)
}

// analyzeLLMResponse — JSON-структура для action='analyze'.
type analyzeLLMResponse struct {
	Anomalies []string `json:"anomalies"` // 0..N коротких пунктов
	Trends    []string `json:"trends"`    // 0..N коротких пунктов
	Advice    []string `json:"advice"`    // 2..3 коротких пункта
}

const analyzePromptTemplate = `Ты — финансовый аналитик для семьи экспатов. Все суммы в батах (THB).

Период анализа: %s
%s

Финансовый контекст текущего периода (THB):
- Доходы − расходы: %.0f
- Свободные деньги: %.0f
- Транзакций: %d%s

Расходы по категориям текущего периода (THB):
%s

Расходы по категориям предыдущего периода (THB) — для сравнения:
%s

Топ-%d самых дорогих расходов текущего периода (THB):
%s

Задача: проанализируй паттерны, найди аномалии и дай практические советы.

Ответь СТРОГО валидным JSON без markdown по схеме:
{
  "anomalies": ["<короткий пункт по-русски>", ...],
  "trends": ["<короткий пункт>", ...],
  "advice": ["<совет 1>", "<совет 2>", "<совет 3>"]
}

Правила:
- anomalies: 0-3 пункта, только реально отклоняющиеся факты (рост/падение категории > 30%%, единичная крупная покупка > 20%% бюджета).
- trends: 0-3 пункта, общие наблюдения (распределение трат, доминирующая категория).
- advice: 2-3 практических совета по оптимизации, конкретно к этому периоду.
- Каждый пункт — одна строка, не длиннее 120 символов.
- Не упоминай данные которых нет в контексте.
`

// parseAnalyzeLLMResponse — парсит JSON-ответ LLM для action='analyze'.
//
// Допускает обрамление ```json ... ```. Валидирует: advice непустой
// (минимум 1 пункт), длина каждого пункта обрезается до 200 символов.
func parseAnalyzeLLMResponse(raw string) (*analyzeLLMResponse, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	var r analyzeLLMResponse
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if len(r.Advice) == 0 {
		return nil, fmt.Errorf("advice is empty")
	}
	r.Anomalies = trimList(r.Anomalies, 200)
	r.Trends = trimList(r.Trends, 200)
	r.Advice = trimList(r.Advice, 200)
	return &r, nil
}

func trimList(items []string, maxLen int) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		t := strings.TrimSpace(it)
		if t == "" {
			continue
		}
		out = append(out, truncateRunes(t, maxLen))
	}
	return out
}

// parseAnalyzePeriod разбирает строку period для action='analyze'.
//
//   - "" или "month" → текущий месяц (now, label "текущий месяц")
//   - "YYYY-MM" → конкретный месяц (15-е число этого месяца, label "YYYY-MM")
//
// Возвращает дату для GetAdvisorSnapshot (monthOffset=0) и человекочитаемый label.
func parseAnalyzePeriod(period string, now time.Time) (time.Time, string, error) {
	p := strings.TrimSpace(strings.ToLower(period))
	if p == "" || p == "month" {
		return now, "текущий месяц", nil
	}
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid period %q (expected 'month' or 'YYYY-MM')", period)
	}
	return time.Date(t.Year(), t.Month(), 15, 0, 0, 0, 0, time.UTC), period, nil
}

// runAnalyze — action='analyze': обзорный анализ трат за период.
//
// Собирает: snapshot текущего и прошлого периода + top-20 расходов. Шлёт в LLM
// единый prompt, парсит JSON {anomalies, trends, advice}, форматирует под Telegram.
func (s *AdvisorSkill) runAnalyze(ctx context.Context, req advisorInput) (string, error) {
	start := time.Now()

	target, label, err := parseAnalyzePeriod(req.Period, time.Now())
	if err != nil {
		return fmt.Sprintf("Не понял period: %s. Используй 'month' или 'YYYY-MM'.", req.Period), nil
	}

	chatID, hasChatID := ctx.Value(agent.ChatIDKey{}).(int64)
	if !hasChatID {
		s.logger.WarnContext(ctx, "advisor.analyze: chatID missing — recurring будет пуст")
		chatID = 0
	}

	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor.analyze: get rates", "err", err, "chat_id", chatID)
		return "Временная ошибка с курсами валют — попробуй позже.", nil
	}
	if _, ok := rates["THB"]; !ok || rates["THB"] == 0 {
		s.logger.ErrorContext(ctx, "advisor.analyze: THB rate missing", "chat_id", chatID)
		return "Не могу посчитать в THB — обнови курс валют командой /rates.", nil
	}

	curSnap, err := s.store.GetAdvisorSnapshot(ctx, chatID, target, 0, rates)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor.analyze: cur snapshot", "err", err, "chat_id", chatID)
		return "Временная ошибка при сборе финансового снимка — попробуй позже.", nil
	}

	prevSnap, err := s.store.GetAdvisorSnapshot(ctx, chatID, target, 1, rates)
	if err != nil {
		s.logger.WarnContext(ctx, "advisor.analyze: prev snapshot (continuing without)", "err", err, "chat_id", chatID)
		prevSnap = &budget.AdvisorSnapshot{SpentByCategory: map[string]float64{}}
	}

	const topLimit = 20
	top, err := s.store.GetTopExpenseTransactions(ctx, target, 0, topLimit, rates)
	if err != nil {
		s.logger.WarnContext(ctx, "advisor.analyze: top expenses (continuing without)", "err", err, "chat_id", chatID)
		top = nil
	}

	if curSnap.TxCount == 0 {
		return fmt.Sprintf("За %s нет транзакций — нечего анализировать.", label), nil
	}

	prompt := buildAnalyzePrompt(label, curSnap, prevSnap, top, topLimit)

	raw, err := s.llm.Ask(ctx, prompt)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor.analyze: llm call", "err", err, "chat_id", chatID)
		return "Не удалось получить анализ — попробуй позже.", nil
	}

	parsed, err := parseAnalyzeLLMResponse(raw)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor.analyze: parse llm response", "err", err, "raw", raw, "chat_id", chatID)
		return "Не смог разобрать ответ аналитика — попробуй ещё раз.", nil
	}

	reply := formatAnalyzeReply(label, parsed)

	s.logger.InfoContext(ctx, "advisor",
		"skill", "advisor",
		"action", "analyze",
		"chat_id", chatID,
		"period", label,
		"anomalies", len(parsed.Anomalies),
		"trends", len(parsed.Trends),
		"advice", len(parsed.Advice),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return reply, nil
}

func buildAnalyzePrompt(label string, cur, prev *budget.AdvisorSnapshot, top []budget.TopExpense, topLimit int) string {
	lowDataNote := ""
	if cur.LowData {
		lowDataNote = fmt.Sprintf(" (low_data=true, порог=%d — данных может быть недостаточно)", budget.MinTxForConfidence)
	}
	return fmt.Sprintf(analyzePromptTemplate,
		label,
		"",
		cur.BalanceMTD,
		cur.FreeCash,
		cur.TxCount,
		lowDataNote,
		formatSpentByCategory(cur.SpentByCategory),
		formatSpentByCategory(prev.SpentByCategory),
		topLimit,
		formatTopExpenses(top),
	)
}

func formatTopExpenses(top []budget.TopExpense) string {
	if len(top) == 0 {
		return "  (нет данных)"
	}
	var sb strings.Builder
	for _, e := range top {
		fmt.Fprintf(&sb, "  - %s · %s · %.0f THB\n", e.Date.Format("2006-01-02"), e.Category, e.AmountTHB)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatAnalyzeReply(label string, r *analyzeLLMResponse) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🧠 *Анализ за %s:*\n", escapeTelegramMarkdown(label))
	if len(r.Anomalies) > 0 {
		sb.WriteString("\n*Аномалии:*\n")
		for _, a := range r.Anomalies {
			fmt.Fprintf(&sb, "• %s\n", escapeTelegramMarkdown(a))
		}
	}
	if len(r.Trends) > 0 {
		sb.WriteString("\n*Тренды:*\n")
		for _, t := range r.Trends {
			fmt.Fprintf(&sb, "• %s\n", escapeTelegramMarkdown(t))
		}
	}
	sb.WriteString("\n*Советы:*\n")
	for _, a := range r.Advice {
		fmt.Fprintf(&sb, "• %s\n", escapeTelegramMarkdown(a))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatAdvisorReply форматирует ответ для Telegram (Markdown, русский).
func formatAdvisorReply(r *advisorLLMResponse, origAmount float64, origCurrency string, amountTHB float64) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*Вердикт:* %s\n", escapeTelegramMarkdown(r.Verdict))
	if origAmount > 0 && origCurrency != "" && origCurrency != "THB" {
		fmt.Fprintf(&sb, "_Сумма_: %.0f %s ≈ %.0f THB\n", origAmount, origCurrency, amountTHB)
	}
	sb.WriteString("\n*Ключевые цифры (THB):*\n")
	fmt.Fprintf(&sb, "• Свободно: %.0f\n", r.Numbers.FreeCashTHB)
	fmt.Fprintf(&sb, "• Прогноз остатка месяца: %.0f\n", r.Numbers.ForecastRemainingTHB)
	fmt.Fprintf(&sb, "• Обязательства: %.0f\n", r.Numbers.ObligationsTHB)
	fmt.Fprintf(&sb, "\n%s\n", escapeTelegramMarkdown(strings.TrimSpace(r.Explanation)))
	if rec := strings.TrimSpace(r.Recommendation); rec != "" {
		fmt.Fprintf(&sb, "\n*Рекомендация:* %s\n", escapeTelegramMarkdown(rec))
	}
	return strings.TrimRight(sb.String(), "\n")
}
