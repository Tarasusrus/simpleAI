package safetospend

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// replyData — всё, что нужно для ответа. Числа детерминированы (из Result),
// planned/variable — разбивки, advice — нарратив LLM.
type replyData struct {
	res       Result
	rubPerTHB float64 // rates["THB"] = ₽ за 1 ฿ (курс, показываем явно)
	period    string
	planned   []CategorySpend // плановые покупки по пунктам
	variable  []CategorySpend // повседневные траты по категориям
	advice    []string
}

// formatReply: вердикт-заголовок сверху (по ИТОГУ, не по промежуточным суммам),
// затем прозрачная раскладка. Ложного зелёного на «свободно» нет.
func formatReply(d replyData) string {
	r := d.res
	rub := func(thb float64) float64 { return thb * d.rubPerTHB }
	var b strings.Builder

	// 1) ВЕРДИКТ по финальному запасу (free − повседневные).
	verdict := rub(r.RealisticFree)
	if verdict >= 0 {
		fmt.Fprintf(&b, "✅ Можно отложить ~%.0f ₽ за %s.\n", verdict, d.period)
	} else {
		fmt.Fprintf(&b, "❌ Отложить нельзя — не хватает ~%.0f ₽ за %s.\n", -verdict, d.period)
	}
	fmt.Fprintf(&b, "🗓 %s · курс %.1f ₽/฿\n\n", d.period, d.rubPerTHB)

	// 2) Раскладка.
	fmt.Fprintf(&b, "💰 Приход: %.0f ₽\n", rub(r.IncomeTHB))
	fmt.Fprintf(&b, "➖ Обязательные платежи: %.0f ₽\n", rub(r.RecurringTHB+r.DebtTHB))
	if r.PlannedTHB > 0 {
		fmt.Fprintf(&b, "➖ Запланированные покупки: %.0f ₽\n", rub(r.PlannedTHB))
		b.WriteString(formatItems(d.planned, d.rubPerTHB, len(d.planned)))
	}
	fmt.Fprintf(&b, "= Остаётся до повседневных трат: %.0f ₽\n", rub(r.FreeAfterObligations))

	// 3) Повседневные (статистика) — «на что уйдёт».
	if r.ForecastSpendTHB > 0 {
		fmt.Fprintf(&b, "\n➖ Повседневные траты (по статистике, %s): %.0f ₽\n", d.period, rub(r.ForecastSpendTHB))
		b.WriteString(formatItems(d.variable, d.rubPerTHB, categoriesTopN))
	}

	// 4) Итог + связка с советами.
	if verdict >= 0 {
		fmt.Fprintf(&b, "\n⚖️ Итог: запас %.0f ₽ — можно отложить.\n", verdict)
	} else {
		fmt.Fprintf(&b, "\n⚖️ Итог: нехватка %.0f ₽. Чтобы выйти в ноль — срезать столько же:\n", -verdict)
	}
	for _, line := range d.advice {
		fmt.Fprintf(&b, "• %s\n", line)
	}
	if r.PlannedTHB == 0 {
		b.WriteString("\nℹ️ Разовые планы (кредитка, покупки) не заданы — добавь их, чтобы расчёт был точнее.")
	}
	return b.String()
}

// formatItems печатает разбивку: до topN пунктов + свёртка остатка в «прочее».
// Категории нормализуются по регистру (единый вид).
func formatItems(items []CategorySpend, rubPerTHB float64, topN int) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	var other float64
	for i, cs := range items {
		if i >= topN {
			other += cs.THB
			continue
		}
		fmt.Fprintf(&b, "   • %-16s %.0f ₽\n", normalizeLabel(cs.Category), cs.THB*rubPerTHB)
	}
	if other > 0 {
		fmt.Fprintf(&b, "   • %-16s %.0f ₽\n", "Остальные статьи", other*rubPerTHB)
	}
	return b.String()
}

// normalizeLabel — единый регистр (Первая заглавная), чтобы «еда/Еда/ресторан»
// не выглядели как дубли из разных источников.
func normalizeLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "прочее"
	}
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(first)) + s[size:]
}

// parseAdviceLines — нарратив LLM как простые строки (1 совет = 1 строка).
// Числа игнорируются: они уже в Result. Пусто/ошибка → nil (не фейлим ответ).
func parseAdviceLines(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(raw, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(ln, "-*•0123456789. "))
		if ln == "" {
			continue
		}
		if len(ln) > 200 {
			ln = ln[:200]
		}
		out = append(out, ln)
		if len(out) >= 4 {
			break
		}
	}
	return out
}
