package botformat

import (
	"strings"
	"unicode/utf8"
)

// Разметка Telegram включена в режиме HTML, а не MarkdownV2 (ADR-008).
// Причина одна и она про доставку: в MarkdownV2 экранирования требуют
// восемнадцать символов, включая точку и минус, — они есть буквально в каждом
// сообщении с датой и суммой. Любой пропущенный символ в имени категории, в
// названии платежа или в тексте от LLM — это 400 от Telegram и НОЛЬ сообщений
// у пользователя. В HTML опасных символов три: & < >. Их экранирование
// централизовано здесь, и обе нужные конструкции — <pre> и <b> — покрыты.
//
// Скиллы продолжают писать текст в привычной ограниченной разметке
// (моноблок в тройных кавычках и **жирный**) — это структура намерения,
// а не готовый Markdown: рендер ниже переводит её в HTML, экранируя ВСЁ
// остальное. Постфактум-экранирование готовой разметки не выбрано намеренно:
// оно требует отличать «звёздочку-разметку» от «звёздочки из текста LLM»,
// а это ровно тот класс ошибки, который роняет отправку целиком.
const (
	// telegramTextLimit — потолок длины одного сообщения. Реальный лимит
	// Telegram 4096; берём с запасом, потому что HTML-сущности (&amp;)
	// длиннее исходного символа, а лимит считается по разобранному тексту.
	telegramTextLimit = 3800

	preOverhead = len("<pre></pre>")
)

var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// EscapeHTML экранирует три символа, опасных для parse_mode=HTML.
func EscapeHTML(s string) string { return htmlEscaper.Replace(s) }

// item — одна строка исходного текста с признаком «внутри моноблока».
type item struct {
	text string
	pre  bool
}

// isFence: строка-ограничитель моноблока. Ограничителем считается только
// целая строка — тройные кавычки посреди текста остаются текстом.
func isFence(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

// parseItems раскладывает исходный текст на строки, помечая те, что лежат
// внутри ПАРНОГО моноблока. Непарный ограничитель остаётся обычным текстом:
// отдать Telegram незакрытый <pre> — это 400, то есть пользователь не получит
// ничего вообще.
func parseItems(src string) []item {
	lines := strings.Split(src, "\n")

	fences := make([]int, 0, 4)
	for i, ln := range lines {
		if isFence(ln) {
			fences = append(fences, i)
		}
	}
	inPre := make([]bool, len(lines))
	skip := make([]bool, len(lines))
	for i := 0; i+1 < len(fences); i += 2 {
		start, end := fences[i], fences[i+1]
		skip[start], skip[end] = true, true
		for j := start + 1; j < end; j++ {
			inPre[j] = true
		}
	}

	items := make([]item, 0, len(lines))
	for i, ln := range lines {
		if skip[i] {
			continue
		}
		items = append(items, item{text: ln, pre: inPre[i]})
	}
	return items
}

// boldify превращает парные ** в <b>. Работает по УЖЕ экранированной строке:
// экранирование звёздочек не порождает, так что подмены смысла нет.
// Непарная ** остаётся литералом — она частая в тексте от LLM и не должна
// ни ронять отправку, ни съедать хвост сообщения.
func boldify(escaped string) string {
	var b strings.Builder
	rest := escaped
	for {
		open := strings.Index(rest, "**")
		if open < 0 {
			break
		}
		after := rest[open+2:]
		closeAt := strings.Index(after, "**")
		if closeAt <= 0 { // нет пары или пустое **** — литерал
			break
		}
		b.WriteString(rest[:open])
		b.WriteString("<b>")
		b.WriteString(after[:closeAt])
		b.WriteString("</b>")
		rest = after[closeAt+2:]
	}
	b.WriteString(rest)
	return b.String()
}

// renderItems собирает HTML: подряд идущие строки моноблока склеиваются в
// ОДИН <pre>, остальные экранируются и получают <b> на парных **.
func renderItems(items []item) string {
	var b strings.Builder
	for i := 0; i < len(items); i++ {
		if !items[i].pre {
			b.WriteString(boldify(EscapeHTML(items[i].text)))
			if i+1 < len(items) {
				b.WriteString("\n")
			}
			continue
		}
		j := i
		var block []string
		for ; j < len(items) && items[j].pre; j++ {
			block = append(block, EscapeHTML(items[j].text))
		}
		b.WriteString("<pre>")
		b.WriteString(strings.Join(block, "\n"))
		b.WriteString("</pre>")
		if j < len(items) {
			b.WriteString("\n")
		}
		i = j - 1
	}
	return b.String()
}

// RenderHTML переводит ограниченную разметку скиллов (моноблок в тройных
// кавычках, **жирный**) в HTML для parse_mode=HTML, экранируя всё остальное.
func RenderHTML(src string) string { return renderItems(parseItems(src)) }

// MessagesHTML — то, что реально уходит в Telegram: готовые HTML-куски в
// пределах лимита длины. Длинное сообщение режется, а не роняет отправку;
// разрез моноблока закрывает <pre> и открывает новый в следующем куске.
func MessagesHTML(src string) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	items := splitOverlong(parseItems(src))

	var out []string
	var chunk []item
	cost := 0
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		out = append(out, renderItems(chunk))
		chunk, cost = nil, 0
	}
	for _, it := range items {
		c := itemCost(it)
		if len(chunk) > 0 && cost+c > telegramTextLimit {
			flush()
		}
		chunk = append(chunk, it)
		cost += c
	}
	flush()
	return out
}

// MessagesPlain — тот же текст без всякой разметки, кусками в пределах лимита.
// Нужен для фоллбэка: если Telegram всё-таки ответил 400 на размеченный
// вариант, сообщение обязано доехать хотя бы простым текстом. Молчание бота
// хуже, чем звёздочки в чате.
func MessagesPlain(src string) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	var out []string
	var b strings.Builder
	n := 0
	for _, r := range src {
		if n >= telegramTextLimit {
			out = append(out, b.String())
			b.Reset()
			n = 0
		}
		b.WriteRune(r)
		n++
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// itemCost — верхняя оценка вклада строки в длину куска: экранированный текст,
// перевод строки и обёртка <pre> (считается на каждую строку блока, потому что
// границы блока в момент подсчёта ещё не известны).
func itemCost(it item) int {
	c := utf8.RuneCountInString(EscapeHTML(it.text)) + 1
	if it.pre {
		c += preOverhead
	}
	return c
}

// splitOverlong режет строки, которые сами по себе длиннее лимита. Такие
// приходят от LLM (простыня без переносов) и без этого шага гарантировали бы
// кусок сверх лимита, то есть 400.
func splitOverlong(items []item) []item {
	out := make([]item, 0, len(items))
	for _, it := range items {
		if itemCost(it) <= telegramTextLimit {
			out = append(out, it)
			continue
		}
		runes := []rune(it.text)
		// Шаг с запасом на экранирование: &amp; занимает 5 знаков вместо 1.
		step := telegramTextLimit/5 - preOverhead
		for start := 0; start < len(runes); start += step {
			end := start + step
			if end > len(runes) {
				end = len(runes)
			}
			out = append(out, item{text: string(runes[start:end]), pre: it.pre})
		}
	}
	return out
}
