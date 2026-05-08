package advisorskill

import (
	"context"
	"fmt"
	"strings"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

// analyzeLLMResponse — JSON-структура для action='analyze'.
type analyzeLLMResponse struct {
	Anomalies []string `json:"anomalies"`
	Trends    []string `json:"trends"`
	Advice    []string `json:"advice"`
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

func parseAnalyzeLLMResponse(raw string) (*analyzeLLMResponse, error) {
	s := stripMarkdownFences(raw)
	var r analyzeLLMResponse
	if err := unmarshalJSON(s, &r); err != nil {
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
