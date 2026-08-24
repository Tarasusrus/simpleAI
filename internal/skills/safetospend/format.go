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
	// Единая точка печати денег и здесь: раньше каждая строка собирала «число +
	// ₽» руками, и знак рубля стоял рядом с суммой, местами уже переведённой в
	// баты. Display держит валюту и курс вместе, разъехаться им нечем
	// (simpleAI-302i).
	m := NewDisplay("RUB", d.rubPerTHB)
	var b strings.Builder

	// 1) ВЕРДИКТ по финальному запасу (free − повседневные).
	verdict := r.RealisticFree
	if verdict >= 0 {
		fmt.Fprintf(&b, "✅ Можно отложить ~%s за %s.\n", m.Fmt(verdict), d.period)
	} else {
		fmt.Fprintf(&b, "❌ Отложить нельзя — не хватает ~%s за %s.\n", m.Fmt(-verdict), d.period)
	}
	fmt.Fprintf(&b, "🗓 %s · курс %s ₽/฿\n\n", d.period, decimalComma(d.rubPerTHB))

	// 2) Раскладка.
	fmt.Fprintf(&b, "💰 Приход: %s\n", m.Fmt(r.IncomeTHB))
	fmt.Fprintf(&b, "➖ Обязательные платежи: %s\n", m.Fmt(r.RecurringTHB+r.DebtTHB))
	if r.PlannedTHB > 0 {
		fmt.Fprintf(&b, "➖ Запланированные покупки: %s\n", m.Fmt(r.PlannedTHB))
		b.WriteString(formatItems(d.planned, m, len(d.planned)))
	}
	fmt.Fprintf(&b, "= Остаётся до повседневных трат: %s\n", m.Fmt(r.FreeAfterObligations))

	// 3) Повседневные (статистика) — «на что уйдёт».
	if r.ForecastSpendTHB > 0 {
		fmt.Fprintf(&b, "\n➖ Повседневные траты (по статистике, %s): %s\n", d.period, m.Fmt(r.ForecastSpendTHB))
		b.WriteString(formatItems(d.variable, m, categoriesTopN))
	}

	// 4) Итог + связка с советами.
	if verdict >= 0 {
		fmt.Fprintf(&b, "\n⚖️ Итог: запас %s — можно отложить.\n", m.Fmt(verdict))
	} else {
		fmt.Fprintf(&b, "\n⚖️ Итог: нехватка %s. Чтобы выйти в ноль — срезать столько же:\n", m.Fmt(-verdict))
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
	labelWidth = 18
	dueWidth   = 6
	// amountWidth — колонка суммы ВМЕСТЕ со знаком валюты. Знак стоит у каждой
	// строки, а не один раз в шапке: оператор читает колонку сверху вниз и без
	// знака не может сказать, баты это или рубли (simpleAI-302i).
	amountWidth = 10
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
			padRight(r.label, labelWidth), padLeft(r.due, dueWidth), padLeft(m.Signed(r.amount), amountWidth))
	}
	totalStr := m.Signed(lineSum)
	fmt.Fprintf(&b, "%s\n", padLeft(strings.Repeat("-", utf8.RuneCountInString(totalStr)), labelWidth+dueWidth+amountWidth))
	fmt.Fprintf(&b, "%s\n```\n", padLeft(totalStr, labelWidth+dueWidth+amountWidth))

	// Главное число — одно и внизу отдельным блоком: по нему оператор
	// действует сегодня. Приход в его формуле не участвует (см. DailyLimit).
	fmt.Fprintf(&b, "\n**На день: %s**\n", m.Fmt(DailyLimit(FlexibleTHB(d.Plan.Shares), days)))
	b.WriteString(dailyLimitScope(d.Plan.Shares))

	b.WriteString(upcomingBlock(d.Plan.Upcoming, m))

	for _, w := range d.Plan.Warnings {
		fmt.Fprintf(&b, "\n\n⚠️ %s", w)
	}
	return b.String()
}

// upcomingBlock печатает платежи, которые придутся уже на следующий период.
//
// Деньги на них этим приходом не отложены — окно финансирования равно периоду
// конверта. Но и промолчать нельзя: аренда, не показанная нигде, будет съедена
// как свободные деньги, а через две недели встретит пустой карман. Блок
// намеренно стоит ПОСЛЕ дневного лимита и вне моноблока — это не часть колонки,
// которая сходится с приходом, а напоминание на будущее.
func upcomingBlock(upcoming []budget.EnvelopeShare, m Display) string {
	if len(upcoming) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n**Впереди, из следующего прихода**\n")
	for _, sh := range upcoming {
		fmt.Fprintf(&b, "%s — %s", normalizeLabel(sh.Name), m.Fmt(sh.Allocated))
		if sh.DueDate != nil {
			fmt.Fprintf(&b, ", %s", sh.DueDate.Format("02.01"))
		}
		b.WriteString("\n")
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
	for _, sh := range SpendShares(shares) {
		flexible = append(flexible, shareLabel(sh.Name))
	}
	fixed := make([]string, 0, len(shares))
	for _, sh := range FixedShares(shares) {
		fixed = append(fixed, shareLabel(sh.Name))
	}
	return scopeSentence(flexible, fixed)
}

// scopeSentence собирает предложение из уже готовых имён: гибкие перечислением,
// фиксированные — «уже отложены». Общая для раскладки прихода и показа
// конвертов: два ответа об одних и тех же деньгах обязаны звучать одинаково.
func scopeSentence(flexible, fixed []string) string {
	for i := range flexible {
		if i > 0 {
			flexible[i] = lowerFirst(flexible[i])
		}
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
func shareLabel(name string) string { return shareLabelWidth(name, labelWidth) }

// shareLabelWidth — то же ужатие под ПРОИЗВОЛЬНУЮ ширину колонки: показ
// конвертов печатает два числа вместо одного, и имени достаётся меньше места.
func shareLabelWidth(name string, labelWidth int) string {
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
// Категории нормализуются по регистру (единый вид). Суммы — через Display, а не
// «число + ₽» руками: знак обязан приходить из того же места, что и курс.
func formatItems(items []CategorySpend, m Display, topN int) string {
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
		fmt.Fprintf(&b, "   • %-16s %s\n", normalizeLabel(cs.Category), m.Fmt(cs.THB))
	}
	if other > 0 {
		fmt.Fprintf(&b, "   • %-16s %s\n", "Остальные статьи", m.Fmt(other))
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

// Ширина колонок моноблока ПОКАЗА конвертов. Колонок здесь три: имя и два
// числа — потрачено и осталось. Сумма 14+10+10 = 34, в пределах порога 36:
// pre в Telegram не переносит строки по словам, и лишний знак уводит колонку
// в горизонтальный скролл на узком экране.
//
// Имя ужато с 18 до 14 знаков именно ради второго числа: без «потрачено»
// оператор видит остаток, но не видит, с чего тот упал.
//
// По 10 на число, а не по 9: знак валюты стоит у КАЖДОЙ суммы (simpleAI-302i).
// Самая длинная реальная строка — «27 798 ฿», восемь знаков.
const (
	remLabelWidth  = 14
	remSpentWidth  = 10
	remLeftWidth   = 10
	remTotalsWidth = remLabelWidth + remSpentWidth + remLeftWidth
)

// DaysLeft — сколько дней периода ещё осталось, СЕГОДНЯШНИЙ включительно.
//
// Считается по календарным датам, а не по разнице таймстампов: «осталось дней»
// — свойство календаря, и в 23:00 последнего дня их всё ещё один, а не ноль.
//
// Ниже единицы не опускается никогда. Это не косметика, а защита знаменателя:
// день после конца периода дал бы ноль, и дневной лимит стал бы +Inf.
func DaysLeft(now, periodEnd time.Time) int {
	d := int(dayStart(periodEnd).Sub(dayStart(now)).Hours()/24) + 1
	if d < 1 {
		d = 1
	}
	return d
}

// formatShareRemaining печатает ТЕКУЩИЕ конверты в том же формате, что и
// раскладка прихода (simpleAI-faeq.11), но с остатками (simpleAI-faeq.12).
//
// Форматтер только печатает готовые числа — не считает: иначе у остатка стало
// бы два источника, один из которых вёрстка. Единственное исключение —
// дневной лимит: он производная от уже посчитанных остатков и числа дней, и
// считается штатными DailyLimit/FlexibleRemainingTHB, а не арифметикой здесь.
//
// Что принципиально, помимо вёрстки:
//
//  1. Колонки две — потрачено и осталось. Один остаток отвечает «сколько ещё
//     можно», но не отвечает «с чего он упал»; оператор не должен вычитать
//     лимит из остатка в уме.
//  2. Пробитый конверт печатается МИНУСОМ, а не эмодзи: внутри pre эмодзи
//     шириной ≈2 знака и рендерится по-разному на iOS/Android/Desktop —
//     колонка едет. Названия пробитых собираются в строку под блоком.
//  3. Дневной лимит делится на ОСТАВШИЕСЯ дни (DaysLeft), а не на длину
//     периода. Это и есть смысл показа: потратил сегодня 2 000 — завтра планка
//     ниже, и оператор видит это числом, а не узнаёт в конце периода.
func formatShareRemaining(items []ShareRemaining, m Display, env *budget.Envelope, now time.Time) string {
	daysLeft := DaysLeft(now, env.PeriodEnd)
	var b strings.Builder

	// Шапка обычным текстом: период, сколько его осталось, курс. Курс
	// показывается всегда, любой валютой показа — по нему оператор сверяет
	// числа с тем, что видит в банке.
	fmt.Fprintf(&b, "%s — %s · осталось %d %s\n",
		env.PeriodStart.Format("02.01"), env.PeriodEnd.Format("02.01"), daysLeft, pluralDays(daysLeft))
	fmt.Fprintf(&b, "Курс %s ₽/฿ на %s\n", decimalComma(m.RubPerTHB), now.Format("02.01"))

	b.WriteString("\n**Что осталось**\n```\n")
	fmt.Fprintf(&b, "%s%s%s\n",
		padRight("", remLabelWidth), padLeft("потрачено", remSpentWidth), padLeft("осталось", remLeftWidth))

	rows := remainingRows(items, m)
	var spentTotal, leftTotal int
	for _, r := range rows {
		// Итог складывается из НАПЕЧАТАННЫХ чисел, а не из исходных float:
		// иначе колонка из округлённых строк не сошлась бы в свой же итог
		// (9 193,55 печатается как 9 194).
		spentTotal += r.spent
		leftTotal += r.left
		fmt.Fprintf(&b, "%s%s%s\n", padRight(r.label, remLabelWidth),
			padLeft(m.Signed(r.spent), remSpentWidth), padLeft(m.Signed(r.left), remLeftWidth))
	}
	spentStr, leftStr := m.Signed(spentTotal), m.Signed(leftTotal)
	fmt.Fprintf(&b, "%s%s%s\n", padRight("", remLabelWidth),
		padLeft(strings.Repeat("-", utf8.RuneCountInString(spentStr)), remSpentWidth),
		padLeft(strings.Repeat("-", utf8.RuneCountInString(leftStr)), remLeftWidth))
	fmt.Fprintf(&b, "%s%s%s\n```\n", padRight("", remLabelWidth),
		padLeft(spentStr, remSpentWidth), padLeft(leftStr, remLeftWidth))

	// Главное число — одно и внизу: по нему оператор действует сегодня.
	fmt.Fprintf(&b, "\n**На день: %s**\n", m.Fmt(DailyLimit(FlexibleRemainingTHB(items), daysLeft)))
	fmt.Fprintf(&b, "Осталось %d %s. %s", daysLeft, pluralDays(daysLeft), remainingScope(items))

	if over := overspentNames(items); len(over) > 0 {
		fmt.Fprintf(&b, "\n\n⚠️ Пробито: %s — дальше тратишь из других конвертов.", strings.Join(over, ", "))
	}
	return b.String()
}

// remainingRow — строка моноблока показа: имя и два уже целых числа в валюте
// показа. Округление делается ОДИН раз здесь, потому что складываться в итог
// обязаны именно напечатанные числа.
type remainingRow struct {
	label string
	spent int
	left  int
}

// remainingRows раскладывает конверты по строкам. Порядок тот же, что в
// раскладке прихода: сначала регулярные платежи, затем гибкие, затем
// накопления — иначе два ответа об одних и тех же деньгах читались бы как два
// разных набора конвертов.
func remainingRows(items []ShareRemaining, m Display) []remainingRow {
	rows := make([]remainingRow, 0, len(items))
	for _, kind := range []string{budget.ShareKindFixed, budget.ShareKindSpend, budget.ShareKindSave} {
		for _, it := range items {
			if it.Kind != kind {
				continue
			}
			rows = append(rows, remainingRow{
				label: shareLabelWidth(it.Name, remLabelWidth),
				spent: roundInt(m.Amount(it.SpentTHB)),
				left:  roundInt(m.Amount(it.Remaining)),
			})
		}
	}
	return rows
}

// overspentNames — имена пробитых конвертов, в порядке показа.
func overspentNames(items []ShareRemaining) []string {
	out := make([]string, 0, len(items))
	for _, kind := range []string{budget.ShareKindFixed, budget.ShareKindSpend, budget.ShareKindSave} {
		for _, it := range items {
			if it.Kind == kind && it.Overspent() {
				out = append(out, normalizeLabel(it.Name))
			}
		}
	}
	return out
}

// remainingScope — предложение под дневным лимитом: что входит в планку, а что
// уже отложено. Без него число «338 ฿» читается как «всё, что у меня есть».
func remainingScope(items []ShareRemaining) string {
	var flexible, fixed []string
	for _, it := range items {
		switch it.Kind {
		case budget.ShareKindSpend:
			flexible = append(flexible, shareLabel(it.Name))
		case budget.ShareKindFixed:
			fixed = append(fixed, shareLabel(it.Name))
		}
	}
	return scopeSentence(flexible, fixed)
}
