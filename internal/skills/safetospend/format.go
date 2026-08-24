package safetospend

import (
	"fmt"
	"math"
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
	RubPerTHB      float64 // ₽ за 1 ฿ (курс показывается всегда, любой валютой)
	Display        Display // валюта отображения конвертов (дефолт — THB)
	Period         string  // человекочитаемый горизонт («ближайшие 2 недели»)
	From, To       time.Time
	IncomeAmount   float64 // приход как его назвал оператор
	IncomeCurrency string
}

// Ширина колонок моноблока. Сумма — 18+6+8 = 32 знака, с запасом под порог 36
// из ресёрча вёрстки: pre в Telegram НЕ переносит строки по словам, длинная
// строка уезжает в горизонтальный скролл на узком экране.
//
// Колонок ровно две с половиной: метка, дата платежа (у гибких долей пустая) и
// сумма по правому краю. Третья полноценная колонка (процент, остаток) на
// телефоне уже не помещается.
const (
	labelWidth  = 18
	dueWidth    = 6
	amountWidth = 8
)

// FormatEnvelopePlan печатает раскладку прихода по конвертам в формате,
// утверждённом оператором 2026-08-24 (simpleAI-faeq.11).
//
// Что здесь принципиально, помимо вёрстки:
//
//  1. Траты НЕ делятся на «обязательные» и «на жизнь» — один список. Прямая
//     цитата оператора: «есть мне тоже надо, или ты считаешь что еда
//     необязательна?». Разница между строками только в том, что у части
//     известны сумма и дата, и это видно самими колонками, а не заголовком.
//  2. Каждый регулярный платёж — своей строкой с датой и настоящим именем.
//     Сводная строка «обязательства 12332» прятала и сумму, и повод, из-за чего
//     приход визуально не сходился.
//  3. Итог сходится с приходом до бата: колонка складывается ровно в шапку.
//     Обеспечивается тем, что накопления забирают ошибку округления всех
//     остальных строк — они и по расчёту непокрытый остаток (ADR-008 §6).
//  4. Эмодзи нет ни одного внутри pre: в моноширинном блоке эмодзи шириной ≈2
//     знака и рендерится по-разному на iOS/Android/Desktop — колонка едет.
//  5. Разделитель блоков — пустая строка. Линия из символов в пропорциональном
//     шрифте имеет случайную длину и сама становится мусором.
func FormatEnvelopePlan(d EnvelopeReply) string {
	m := d.Display
	if m.RubPerTHB == 0 {
		m.RubPerTHB = d.RubPerTHB
	}
	if m.Code == "" {
		m = NewDisplay("", d.RubPerTHB)
	}
	days := envelopeDays(d.From, d.To)
	var b strings.Builder

	// Шапка: период, приход, курс. Обычным текстом — крупно и без колонок.
	fmt.Fprintf(&b, "%s — %s · %d %s\n",
		d.From.Format("02.01"), d.To.Format("02.01"), days, pluralDays(days))
	fmt.Fprintf(&b, "Пришло %s\n", incomeLine(d, m))
	// Перенос — отдельная строка и НЕ складывается с приходом: там деньги
	// этого прихода, здесь — прошлого. Свернув их в одну сумму, мы показали бы
	// приход больше, чем он есть. Но в колонку он входит: конверты им наполнены.
	if carried := TotalCarriedIn(d.Plan.Shares); carried > 0 {
		fmt.Fprintf(&b, "Перенос с прошлого раза %s\n", m.Fmt(carried))
	}
	fmt.Fprintf(&b, "Курс %s ₽/฿ на %s\n", decimalComma(d.RubPerTHB), d.From.Format("02.01"))

	// Один моноблок на всё сообщение: колонка чисел в Telegram держится ТОЛЬКО
	// внутри pre — системный шрифт пропорциональный, и выравнивания пробелами
	// вне моноблока не существует.
	b.WriteString("\n**Куда уйдут**\n```\n")
	rows := envelopeRows(d.Plan.Shares, m, roundInt(m.Amount(shareTotalTHB(d.Plan.Shares))))
	var lineSum int
	for _, r := range rows {
		lineSum += r.amount
		fmt.Fprintf(&b, "%s%s%s\n",
			padRight(r.label, labelWidth), padLeft(r.due, dueWidth), padLeft(groupDigits(r.amount), amountWidth))
	}
	totalStr := groupDigits(lineSum)
	fmt.Fprintf(&b, "%s\n", padLeft(strings.Repeat("-", utf8.RuneCountInString(totalStr)), labelWidth+dueWidth+amountWidth))
	fmt.Fprintf(&b, "%s\n```\n", padLeft(totalStr, labelWidth+dueWidth+amountWidth))

	// Главное число — одно и внизу отдельным блоком: по нему оператор
	// действует сегодня. Приход в его формуле не участвует (см. DailyLimit).
	fmt.Fprintf(&b, "\n**На день: %s**\n", m.Fmt(DailyLimit(FlexibleTHB(d.Plan.Shares), days)))
	b.WriteString(dailyLimitScope(d.Plan.Shares))

	for _, w := range d.Plan.Warnings {
		fmt.Fprintf(&b, "\n\n⚠️ %s", w)
	}
	return b.String()
}

// envelopeRow — одна строка моноблока: метка, дата платежа (или пусто) и сумма
// уже в валюте показа и уже целая. Округление делается ОДИН раз здесь, потому
// что складываться в итог обязаны именно напечатанные числа.
type envelopeRow struct {
	label  string
	due    string
	amount int
}

// envelopeRows раскладывает доли по строкам. Порядок: сначала регулярные
// платежи (у них есть дата и они уже отложены), затем гибкие конверты, затем
// накопления — тот же порядок, в котором деньги распределяются.
//
// total — точная сумма всех долей в валюте показа, целая. Накопления получают
// РОВНО остаток до неё: округление каждой строки в отдельности даёт расхождение
// до бата на строку, и без этого шага колонка не сложилась бы в свой же итог.
//
// Опорой служит сумма ДОЛЕЙ, а не приход: форматтер не имеет права дописывать
// деньги, которых в раскладке нет. Что доли складываются ровно в приход —
// инвариант PlanEnvelope (ADR-008 §4), и проверяется он там, а не здесь.
func envelopeRows(shares []budget.EnvelopeShare, m Display, total int) []envelopeRow {
	rows := make([]envelopeRow, 0, len(shares))
	var savings *envelopeRow
	var assigned int

	add := func(sh budget.EnvelopeShare) envelopeRow {
		r := envelopeRow{
			label:  shareLabel(sh.Name),
			amount: roundInt(m.Amount(sh.Allocated + sh.CarriedIn)),
		}
		if sh.DueDate != nil {
			r.due = sh.DueDate.Format("02.01")
		}
		return r
	}
	for _, group := range [][]budget.EnvelopeShare{FixedShares(shares), SpendShares(shares)} {
		for _, sh := range group {
			r := add(sh)
			assigned += r.amount
			rows = append(rows, r)
		}
	}
	for _, sh := range SaveShares(shares) {
		r := add(sh)
		if savings == nil {
			savings = &r
			continue
		}
		assigned += r.amount
		rows = append(rows, r)
	}
	if savings != nil {
		// Ошибка округления всех строк оседает здесь — накопления и по расчёту
		// непокрытый остаток, а не самостоятельный лимит (ADR-008 §6).
		savings.amount = total - assigned
		if savings.amount < 0 {
			savings.amount = 0
		}
		rows = append(rows, *savings)
	}
	return rows
}

// shareTotalTHB — точная сумма раскладки: лимиты плюс перенесённое.
func shareTotalTHB(shares []budget.EnvelopeShare) float64 {
	var total float64
	for _, sh := range shares {
		total += sh.Allocated + sh.CarriedIn
	}
	return total
}

// dailyLimitScope — предложение под дневным лимитом: что в него входит и что
// уже отложено. Без него число «814 ฿» читается как «всё, что у меня есть».
func dailyLimitScope(shares []budget.EnvelopeShare) string {
	flexible := make([]string, 0, len(shares))
	for i, sh := range SpendShares(shares) {
		label := shareLabel(sh.Name)
		if i > 0 {
			label = lowerFirst(label)
		}
		flexible = append(flexible, label)
	}
	fixed := make([]string, 0, len(shares))
	for _, sh := range FixedShares(shares) {
		fixed = append(fixed, shareLabel(sh.Name))
	}

	var b strings.Builder
	if len(flexible) > 0 {
		fmt.Fprintf(&b, "%s.", strings.Join(flexible, ", "))
	}
	if len(fixed) > 0 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s — уже отложены.", strings.Join(fixed, ", "))
	}
	return b.String()
}

// incomeLine — приход в шапке. Валюту, которой его назвал оператор, показываем
// первой и всегда: «пришло 127000₽» обязано остаться 127000₽, иначе он не
// узнает свой собственный приход. Расчётная сумма идёт рядом — и только если
// это другая валюта, иначе строка дублировала бы сама себя.
func incomeLine(d EnvelopeReply, m Display) string {
	own := fmt.Sprintf("%s %s", groupDigits(roundInt(d.IncomeAmount)), currencySign(d.IncomeCurrency))
	shown := m.Fmt(d.Plan.Result.IncomeTHB)
	if currencySign(d.IncomeCurrency) == m.Sign() {
		return shown
	}
	return own + " · " + shown
}

// shareLabel ужимает имя доли под ширину колонки по границе СЛОВА: обрезанное
// посередине «Кредит потребит…» читается хуже, чем честное «Кредит». Ellipsis
// остаётся страховкой на случай, когда не помещается даже первое слово.
func shareLabel(name string) string {
	label := normalizeLabel(name)
	if utf8.RuneCountInString(label) <= labelWidth {
		return label
	}
	var out string
	for _, w := range strings.Fields(label) {
		next := w
		if out != "" {
			next = out + " " + w
		}
		if utf8.RuneCountInString(next) > labelWidth {
			break
		}
		out = next
	}
	if out != "" {
		return out
	}
	return string([]rune(label)[:labelWidth-1]) + "…"
}

// envelopeDays — длина периода в днях, обе границы включительно.
func envelopeDays(from, to time.Time) int {
	d := int(to.Sub(from).Hours()/24) + 1
	if d < 1 {
		d = 1
	}
	return d
}

func pluralDays(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return "день"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return "дня"
	}
	return "дней"
}

// groupDigits — разряды через пробел, без копеек (ресёрч вёрстки: группировка
// читается в один проход, дробная часть в сводке — шум).
func groupDigits(n int) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	s := fmt.Sprintf("%d", n)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return sign + strings.Join(append([]string{s}, parts...), " ")
}

// decimalComma — курс с запятой: русский текст, «3.1» в нём читается как сбой.
func decimalComma(v float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
}

func roundInt(v float64) int { return int(math.Round(v)) }

// padRight / padLeft считают ширину В РУНАХ: %-18s в Go меряет БАЙТЫ, и на
// кириллице колонка разъезжается ровно вдвое.
func padRight(s string, width int) string {
	if pad := width - utf8.RuneCountInString(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func padLeft(s string, width int) string {
	if pad := width - utf8.RuneCountInString(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(first)) + s[size:]
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
func formatShareRemaining(items []ShareRemaining, m Display, env *budget.Envelope) string {
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
		fmt.Fprintf(&b, "%s %-16s осталось %s из %s",
			icon, normalizeLabel(it.Name), m.Fmt(it.Remaining), m.Fmt(it.LimitTHB))
		if it.CarriedIn != 0 {
			fmt.Fprintf(&b, " (в т.ч. перенос %s)", m.Fmt(it.CarriedIn))
		}
		b.WriteString("\n")
	}

	b.WriteString("   ──────────────────────\n")
	fmt.Fprintf(&b, "   Итого осталось: %s из %s\n", m.Fmt(totalRemaining), m.Fmt(totalLimit))

	// Дневной лимит пересчитывается ЗДЕСЬ, от остатка гибких конвертов и
	// оставшихся дней — то есть после каждой траты (simpleAI-faeq.11 §5).
	// Приход в формуле не участвует: конверты уже наполнены, и вопрос «сколько
	// можно сегодня» от факта нового прихода не зависит.
	daysLeft := envelopeDays(time.Now(), env.PeriodEnd)
	fmt.Fprintf(&b, "\n👉 На день: %s (%d %s до конца периода)\n",
		m.Fmt(DailyLimit(FlexibleRemainingTHB(items), daysLeft)), daysLeft, pluralDays(daysLeft))

	if len(overspent) > 0 {
		fmt.Fprintf(&b, "\n⚠️ Пробито: %s — дальше тратишь из других конвертов.\n", strings.Join(overspent, ", "))
	}
	b.WriteString("\nℹ️ Обязательные платежи по подпискам и переводы в конверты не входят — они уже вычтены отдельно.")
	return b.String()
}
