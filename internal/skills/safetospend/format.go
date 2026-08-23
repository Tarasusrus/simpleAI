package safetospend

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"simpleAI/internal/budget"
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

// EnvelopeReply — данные для ответа о заведённом конверте с раскладкой
// (ADR-008). Числа приходят готовыми из PlanEnvelope — форматтер только
// печатает, не считает (числа не проходят ни через LLM, ни через вёрстку).
type EnvelopeReply struct {
	Plan           EnvelopePlan
	RubPerTHB      float64 // ₽ за 1 ฿
	Period         string  // человекочитаемый горизонт («ближайшие 2 недели»)
	From, To       time.Time
	IncomeAmount   float64 // приход как его назвал оператор
	IncomeCurrency string
	OutsideTHB     float64 // факт «вне конвертов» за период (ADR-008 §4)
}

// FormatEnvelopePlan печатает раскладку прихода по конвертам.
//
// Порядок строк повторяет порядок расчёта, чтобы каждое число можно было
// проверить глазами сверху вниз: приход → обязательства → что осталось делить →
// сами конверты → свободный остаток → факт вне конвертов → предупреждения.
//
// «Вне конвертов» показывается ВСЕГДА, даже нулевым: это тот член, из-за
// которого сумма конвертов не сходится с остатком по ADR-007 (§4 ADR-008).
// Спрятать его при нуле значит спрятать и объяснение расхождения, когда он
// перестанет быть нулём.
func FormatEnvelopePlan(d EnvelopeReply) string {
	r := d.Plan.Result
	rub := func(thb float64) float64 { return thb * d.RubPerTHB }
	var b strings.Builder

	fmt.Fprintf(&b, "🧧 Конверт заведён: %.0f %s на %s (%s — %s)\n",
		d.IncomeAmount, currencySign(d.IncomeCurrency), d.Period,
		d.From.Format("02.01"), d.To.Format("02.01"))
	fmt.Fprintf(&b, "🗓 курс %.2f ₽/฿\n\n", d.RubPerTHB)

	fmt.Fprintf(&b, "💰 Приход: %.0f ₽\n", rub(r.IncomeTHB))
	fmt.Fprintf(&b, "➖ Обязательства: %.0f ₽ (регулярные %.0f + долги %.0f)\n",
		rub(r.RecurringTHB+r.DebtTHB), rub(r.RecurringTHB), rub(r.DebtTHB))
	if r.PlannedTHB > 0 {
		fmt.Fprintf(&b, "➖ Плановые покупки: %.0f ₽\n", rub(r.PlannedTHB))
	}
	fmt.Fprintf(&b, "= К раскладке: %.0f ₽\n\n", rub(r.FreeAfterObligations))

	b.WriteString("📦 Конверты:\n")
	for _, sh := range SpendShares(d.Plan.Shares) {
		mark := ""
		if sh.Source == budget.ShareSourceOverride {
			mark = " (вручную)"
		}
		fmt.Fprintf(&b, "   • %-16s %.0f ₽%s\n", normalizeLabel(sh.Name), rub(sh.Allocated), mark)
	}

	var free float64
	for _, sh := range SaveShares(d.Plan.Shares) {
		free += sh.Allocated
	}
	b.WriteString("   ──────────────────────\n")
	fmt.Fprintf(&b, "   🆓 Свободно: %.0f ₽ (уходит в накопления)\n", rub(free))

	fmt.Fprintf(&b, "\n🚪 Вне конвертов: %.0f ₽ — фиксированные траты и платежи по подпискам за период; они уже вычтены в обязательствах.\n",
		rub(d.OutsideTHB))

	for _, w := range d.Plan.Warnings {
		fmt.Fprintf(&b, "⚠️ %s\n", w)
	}

	b.WriteString("\nСкажи «на еду хватит 15000», если лимит не тот, или спроси «сколько осталось в конвертах?».")
	return b.String()
}

// currencySign — знак валюты для заголовка. Неизвестная валюта печатается своим
// кодом: выдумывать знак хуже, чем показать «USD».
func currencySign(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "RUB", "":
		return "₽"
	case "THB":
		return "฿"
	case "USD":
		return "$"
	case "EUR":
		return "€"
	}
	return strings.ToUpper(code)
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
		// Обрезка по РУНАМ, а не по байтам: в кириллице ln[:200] режет символ
		// пополам, и в ответ уезжает «д<U+FFFD>» (видно на живом прогоне
		// safe_to_spend). Длина считается в символах — она и имелась в виду.
		if r := []rune(ln); len(r) > adviceLineRunes {
			ln = string(r[:adviceLineRunes])
		}
		out = append(out, ln)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

// formatShareRemaining печатает остаток по каждому конверту (ADR-008 §8).
// Форматтер только печатает готовые числа — не считает: иначе у остатка стало
// бы два источника, один из которых вёрстка.
//
// По каждой доле видно лимит и остаток, пробитые помечены явно: главный вопрос
// оператора — «на что уже нельзя тратить», и он не должен вычитать в уме.
func formatShareRemaining(items []ShareRemaining, rubPerTHB float64, env *budget.Envelope) string {
	rub := func(thb float64) float64 { return thb * rubPerTHB }
	var b strings.Builder

	fmt.Fprintf(&b, "🧧 Конверты (%s — %s)\n\n",
		env.PeriodStart.Format("02.01"), env.PeriodEnd.Format("02.01"))

	var overspent []string
	var totalLimit, totalRemaining float64
	for _, it := range items {
		totalLimit += it.LimitTHB
		totalRemaining += it.Remaining
		icon := "🟢"
		if it.Overspent() {
			icon = "🔴"
			overspent = append(overspent, normalizeLabel(it.Name))
		} else if it.LimitTHB > 0 && it.Remaining < it.LimitTHB*lowShareFraction {
			icon = "🟡"
		}
		if it.Kind == budget.ShareKindSave {
			icon = "🏦"
		}
		fmt.Fprintf(&b, "%s %-16s осталось %.0f ₽ из %.0f ₽",
			icon, normalizeLabel(it.Name), rub(it.Remaining), rub(it.LimitTHB))
		if it.CarriedIn != 0 {
			fmt.Fprintf(&b, " (в т.ч. перенос %.0f ₽)", rub(it.CarriedIn))
		}
		b.WriteString("\n")
	}

	b.WriteString("   ──────────────────────\n")
	fmt.Fprintf(&b, "   Итого осталось: %.0f ₽ из %.0f ₽\n", rub(totalRemaining), rub(totalLimit))

	if len(overspent) > 0 {
		fmt.Fprintf(&b, "\n⚠️ Пробито: %s — дальше тратишь из других конвертов.\n", strings.Join(overspent, ", "))
	}
	b.WriteString("\nℹ️ Обязательные платежи по подпискам и переводы в конверты не входят — они уже вычтены отдельно.")
	return b.String()
}
