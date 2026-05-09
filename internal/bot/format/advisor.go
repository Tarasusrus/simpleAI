package botformat

import (
	"fmt"
	"strings"
)

// AdvisorNumbers holds the key financial figures for advisor reply.
type AdvisorNumbers struct {
	FreeCashTHB          float64
	ForecastRemainingTHB float64
	ObligationsTHB       float64
}

// AdvisorReplyData is the input for FormatAdvisorReply.
type AdvisorReplyData struct {
	Verdict        string
	Numbers        AdvisorNumbers
	Explanation    string
	Recommendation string
	OrigAmount     float64
	OrigCurrency   string
	AmountTHB      float64
}

// FormatAdvisorReply renders a Telegram-formatted advisor reply.
func FormatAdvisorReply(r AdvisorReplyData) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*Вердикт:* %s\n", EscapeTelegramMarkdown(r.Verdict))
	if r.OrigAmount > 0 && r.OrigCurrency != "" && r.OrigCurrency != "THB" {
		fmt.Fprintf(&sb, "_Сумма_: %.0f %s ≈ %.0f THB\n", r.OrigAmount, r.OrigCurrency, r.AmountTHB)
	}
	sb.WriteString("\n*Ключевые цифры (THB):*\n")
	fmt.Fprintf(&sb, "• Свободно: %.0f\n", r.Numbers.FreeCashTHB)
	fmt.Fprintf(&sb, "• Прогноз остатка месяца: %.0f\n", r.Numbers.ForecastRemainingTHB)
	fmt.Fprintf(&sb, "• Обязательства: %.0f\n", r.Numbers.ObligationsTHB)
	fmt.Fprintf(&sb, "\n%s\n", EscapeTelegramMarkdown(strings.TrimSpace(r.Explanation)))
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
