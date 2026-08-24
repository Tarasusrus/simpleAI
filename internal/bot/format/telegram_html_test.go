package botformat

import (
	"strings"
	"testing"
)

// Злые строки: имена категорий и платежей, которые реально приходят от
// оператора и от LLM. В HTML опасны ровно три символа — их и проверяем,
// плюс весь набор спецсимволов MarkdownV2 (он обязан доехать как есть).
func TestRenderHTML_EscapesEvilStrings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"амперсанд", "Кафе Мама & Папа", "Кафе Мама &amp; Папа"},
		{"угловые скобки", "аренда <дом> 15.09", "аренда &lt;дом&gt; 15.09"},
		{"похоже на тег", "<b>не тег</b>", "&lt;b&gt;не тег&lt;/b&gt;"},
		{"спецсимволы MarkdownV2", "_ [ ] ( ) ~ > # + - = | { } . !", "_ [ ] ( ) ~ &gt; # + - = | { } . !"},
		{"одиночная звёздочка", "3*4 = 12", "3*4 = 12"},
		{"непарный **", "цена ** дорого", "цена ** дорого"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderHTML(tc.in)
			if got != tc.want {
				t.Fatalf("RenderHTML(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

// Моноблок из утверждённого формата раскладки обязан доехать как настоящий
// <pre>: только в нём Telegram держит моноширинный шрифт и колонку чисел.
func TestRenderHTML_MonoblockBecomesPre(t *testing.T) {
	src := "**Куда уйдут**\n```\nЕда              8 000\nАренда     15.09 12 000\n```\nхвост"
	got := RenderHTML(src)

	if !strings.Contains(got, "<b>Куда уйдут</b>") {
		t.Errorf("заголовок блока не стал жирным: %q", got)
	}
	if strings.Contains(got, "```") {
		t.Errorf("тройные кавычки уехали литералом: %q", got)
	}
	if !strings.Contains(got, "<pre>Еда              8 000\nАренда     15.09 12 000</pre>") {
		t.Errorf("моноблок не стал pre: %q", got)
	}
	if !strings.HasSuffix(got, "\nхвост") {
		t.Errorf("текст после моноблока потерян: %q", got)
	}
}

// Внутри pre разметка не интерпретируется: имя платежа со звёздочками или
// угловой скобкой не должно порождать тегов и не должно ронять отправку.
func TestRenderHTML_PreContentIsVerbatim(t *testing.T) {
	src := "```\n**Кафе** & <дом>\n```"
	got := RenderHTML(src)
	want := "<pre>**Кафе** &amp; &lt;дом&gt;</pre>"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Незакрытый моноблок — не повод отдать Telegram сломанный HTML: 400 на такой
// разметке означает, что пользователь не получит НИЧЕГО.
func TestRenderHTML_UnclosedFenceStaysLiteral(t *testing.T) {
	got := RenderHTML("текст\n```\nЕда 8 000")
	if strings.Contains(got, "<pre>") {
		t.Fatalf("незакрытый блок открыл тег: %q", got)
	}
	if !strings.Contains(got, "```") {
		t.Fatalf("незакрытый блок должен остаться литералом: %q", got)
	}
}

// Регресс: обычный ответ скилла (трата записана, дайджест) не должен
// обрастать тегами и не должен ничего терять.
func TestRenderHTML_PlainSkillReplyUnchanged(t *testing.T) {
	src := "✅ Записал: Еда — 250 ฿ (15.09)\n👉 На день: 1 200 ฿ (12 дней до конца периода)"
	if got := RenderHTML(src); got != src {
		t.Fatalf("обычный ответ изменился:\n got: %q\nwant: %q", got, src)
	}
}

// Длинное сообщение не роняет отправку: режется на части в пределах лимита
// Telegram, и моноблок при разрезе закрывается и переоткрывается.
func TestMessagesHTML_SplitsLongMessage(t *testing.T) {
	var b strings.Builder
	b.WriteString("**Куда уйдут**\n```\n")
	for i := 0; i < 900; i++ {
		b.WriteString("Категория очень длинная      1 234\n")
	}
	b.WriteString("```\n")

	parts := MessagesHTML(b.String())
	if len(parts) < 2 {
		t.Fatalf("длинное сообщение не разрезано: частей %d", len(parts))
	}
	for i, p := range parts {
		if n := len([]rune(p)); n > telegramTextLimit {
			t.Errorf("часть %d длиной %d рун — Telegram вернёт 400", i, n)
		}
		if strings.Count(p, "<pre>") != strings.Count(p, "</pre>") {
			t.Errorf("часть %d с несбалансированным pre: %.80q", i, p)
		}
		if strings.Contains(p, "```") {
			t.Errorf("часть %d содержит литеральные кавычки", i)
		}
	}
}

// Короткое сообщение остаётся одним куском — резать нечего.
func TestMessagesHTML_ShortStaysSingle(t *testing.T) {
	parts := MessagesHTML("привет")
	if len(parts) != 1 || parts[0] != "привет" {
		t.Fatalf("got %#v", parts)
	}
}

// Пустой текст не порождает пустой отправки (Telegram вернёт 400 на пустом
// text — и это был бы ложный «сбой доставки»).
func TestMessagesHTML_EmptyStaysEmpty(t *testing.T) {
	if parts := MessagesHTML("   "); len(parts) != 0 {
		t.Fatalf("пустой текст дал части: %#v", parts)
	}
}

// Одна строка длиннее лимита (LLM умеет выдать простыню без переносов)
// режется принудительно, а не уезжает в 400.
func TestMessagesHTML_OverlongSingleLine(t *testing.T) {
	parts := MessagesHTML(strings.Repeat("я", telegramTextLimit+500))
	if len(parts) < 2 {
		t.Fatalf("сверхдлинная строка не разрезана: %d", len(parts))
	}
	for i, p := range parts {
		if n := len([]rune(p)); n > telegramTextLimit {
			t.Errorf("часть %d длиной %d рун", i, n)
		}
	}
}
