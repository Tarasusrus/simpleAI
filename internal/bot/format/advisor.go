package botformat

import (
	"fmt"
	"strings"
)

// AdvisorFlow holds the step-by-step financial transition for advisor reply.
type AdvisorFlow struct {
	BalanceNow           float64
	ObligationsRemaining float64
	AfterObligations     float64
	ExpectedSpend        float64
	ForecastRemaining    float64
	AfterPurchase        float64
}

// AdvisorReplyData is the input for FormatAdvisorReply.
type AdvisorReplyData struct {
	Verdict        string
	Flow           AdvisorFlow
	Assumptions    []string
	Risk           string
	Recommendation string
	OrigAmount     float64
	OrigCurrency   string
	AmountTHB      float64
}

// FormatAdvisorReply renders a Telegram-formatted advisor reply as a financial flow simulation.
func FormatAdvisorReply(r AdvisorReplyData) string {
	var sb strings.Builder

	if r.OrigAmount > 0 && r.OrigCurrency != "" && r.OrigCurrency != "THB" {
		fmt.Fprintf(&sb, "_Сумма_: %.0f %s ≈ %.0f THB\n\n", r.OrigAmount, r.OrigCurrency, r.AmountTHB)
	}

	sb.WriteString("*Сейчас:*\n")
	fmt.Fprintf(&sb, "• Баланс месяца: %.0f THB\n", r.Flow.BalanceNow)
	fmt.Fprintf(&sb, "• Обязательства до конца месяца: %.0f THB\n", r.Flow.ObligationsRemaining)
	fmt.Fprintf(&sb, "• После обязательств: %.0f THB\n", r.Flow.AfterObligations)

	if r.Flow.ExpectedSpend > 0 {
		fmt.Fprintf(&sb, "• Ожидаемые повседневные расходы: %.0f THB\n", r.Flow.ExpectedSpend)
	}

	fmt.Fprintf(&sb, "\n*Прогноз остатка:* %.0f THB\n", r.Flow.ForecastRemaining)

	if r.AmountTHB > 0 {
		fmt.Fprintf(&sb, "*После покупки:* %.0f THB\n", r.Flow.AfterPurchase)
	}

	if len(r.Assumptions) > 0 {
		sb.WriteString("\n*Предположения:*\n")
		for _, a := range r.Assumptions {
			fmt.Fprintf(&sb, "• %s\n", EscapeTelegramMarkdown(a))
		}
	}

	fmt.Fprintf(&sb, "\n*Риск:* %s\n", EscapeTelegramMarkdown(r.Risk))

	fmt.Fprintf(&sb, "\n*Вердикт:* %s\n", EscapeTelegramMarkdown(r.Verdict))

	if rec := strings.TrimSpace(r.Recommendation); rec != "" {
		fmt.Fprintf(&sb, "\n*Рекомендация:* %s\n", EscapeTelegramMarkdown(rec))
	}

	return strings.TrimRight(sb.String(), "\n")
}

// AnalyzeReplyData is the input for FormatAnalyzeReply.
type AnalyzeReplyData struct {
	Label     string
	Anomalies []string
	Trends    []string
	Advice    []string
}

// FormatAnalyzeReply renders a Telegram-formatted spending analysis reply.
func FormatAnalyzeReply(r AnalyzeReplyData) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🧠 *Анализ за %s:*\n", EscapeTelegramMarkdown(r.Label))
	if len(r.Anomalies) > 0 {
		sb.WriteString("\n*Аномалии:*\n")
		for _, a := range r.Anomalies {
			fmt.Fprintf(&sb, "• %s\n", EscapeTelegramMarkdown(a))
		}
	}
	if len(r.Trends) > 0 {
		sb.WriteString("\n*Тренды:*\n")
		for _, t := range r.Trends {
			fmt.Fprintf(&sb, "• %s\n", EscapeTelegramMarkdown(t))
		}
	}
	sb.WriteString("\n*Советы:*\n")
	for _, a := range r.Advice {
		fmt.Fprintf(&sb, "• %s\n", EscapeTelegramMarkdown(a))
	}
	return strings.TrimRight(sb.String(), "\n")
}
