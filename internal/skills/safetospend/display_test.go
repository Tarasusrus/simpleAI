package safetospend

import (
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

// planForDisplay — раскладка со всеми тремя видами долей: запертый регулярный
// платёж, гибкий конверт и накопления с переносом. В печати участвуют все три,
// и валюту каждой проверяет тест.
func planForDisplay() EnvelopePlan {
	due := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	return EnvelopePlan{
		Result: Result{
			IncomeTHB:            10000,
			RecurringTHB:         1000,
			DebtTHB:              0,
			FreeAfterObligations: 9000,
		},
		Shares: []budget.EnvelopeShare{
			{Name: "аренда", Kind: budget.ShareKindFixed, Allocated: 1000, DueDate: &due, Position: 0},
			{Name: "еда", Kind: budget.ShareKindSpend, Allocated: 5000, Position: 1},
			{Name: "накопления", Kind: budget.ShareKindSave, Allocated: 4000, CarriedIn: 2000, Position: 2},
		},
	}
}

func replyForDisplay(m Display) EnvelopeReply {
	from := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	return EnvelopeReply{
		Plan:           planForDisplay(),
		RubPerTHB:      2,
		Display:        m,
		Period:         "ближайшие 2 недели",
		From:           from,
		To:             from.AddDate(0, 0, 13),
		IncomeAmount:   20000,
		IncomeCurrency: "RUB",
	}
}

// Дефолт раскладки — баты: суммы конвертов печатаются как есть (хранение уже в
// THB), рублёвого знака в них нет. Курс при этом показывается всегда — он
// подпись к заголовку, а не валюта сумм.
func TestFormatEnvelopePlan_DefaultTHB(t *testing.T) {
	out := FormatEnvelopePlan(replyForDisplay(NewDisplay("", 2)))
	for _, want := range []string{
		"Пришло 20 000 ₽ · 10 000 ฿",
		"Перенос с прошлого раза 2 000 ฿",
		"Аренда             10.09   1 000",
		"Еда                        5 000",
		"Накопления                 6 000",
		"                          12 000",
		"**На день: 357 ฿**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет строки %q в батовой раскладке:\n%s", want, out)
		}
	}
}

// Просьба показать в рублях переводит ВСЕ суммы конвертов по курсу 2 ₽/฿.
// Мутация «форматтер всегда печатает одну валюту» роняет либо этот тест, либо
// предыдущий — оба одновременно пройти не могут.
func TestFormatEnvelopePlan_DisplayRUB(t *testing.T) {
	out := FormatEnvelopePlan(replyForDisplay(NewDisplay("RUB", 2)))
	for _, want := range []string{
		"Пришло 20 000 ₽",
		"Перенос с прошлого раза 4 000 ₽",
		"Аренда             10.09   2 000",
		"Еда                       10 000",
		"Накопления                12 000",
		"                          24 000",
		"**На день: 714 ₽**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет строки %q в рублёвой раскладке:\n%s", want, out)
		}
	}
	// Знак ฿ остаётся только в подписи курса «₽/฿» — сумм в батах быть не должно.
	if strings.Contains(out, " ฿") {
		t.Errorf("просили рубли, а суммы напечатаны батами:\n%s", out)
	}
}

// Хранение не зависит от валюты показа: одна и та же раскладка (те же THB в
// Shares) печатается двумя валютами, и рублёвая ровно вдвое больше батовой при
// курсе 2 ₽/฿ — то есть перевод один, а не двойной.
func TestDisplay_AmountConvertsOnce(t *testing.T) {
	thb := NewDisplay("THB", 2)
	rub := NewDisplay("RUB", 2)
	if got := thb.Amount(5000); got != 5000 {
		t.Errorf("баты обязаны печататься как хранятся: %v", got)
	}
	if got := rub.Amount(5000); got != 10000 {
		t.Errorf("ожидали 10000 ₽ из 5000 ฿ при курсе 2, получили %v", got)
	}
}

// Неназванная и неизвестная валюта уходят в дефолт (баты), а не печатаются
// чужим знаком: курса ни к чему, кроме рубля, у нас нет.
func TestNewDisplay_Default(t *testing.T) {
	for _, code := range []string{"", "  ", "USD", "кому-то"} {
		if got := NewDisplay(code, 2); got.Code != DefaultDisplayCurrency {
			t.Errorf("NewDisplay(%q) = %q, ожидали %q", code, got.Code, DefaultDisplayCurrency)
		}
	}
	if got := NewDisplay("rub", 2); got.Code != "RUB" {
		t.Errorf("NewDisplay(\"rub\") = %q", got.Code)
	}
}

// Разбор валюты из фразы оператора — страховка на случай, когда LLM не
// заполнила поле. Ловится корень С ПРЕДЛОГОМ («в рублях»): голый корень дал бы
// ложное срабатывание на «рубленом стейке» в описании траты.
func TestParseDisplayCurrency(t *testing.T) {
	cases := map[string]string{
		"покажи конверты в рублях":  "RUB",
		"сколько это в рублях":      "RUB",
		"покажи конверты в батах":   "THB",
		"на еду хватит 5000 бат":    "",
		"сколько осталось на еду":   "",
		"разложи приход по конверт": "",
		// Корень «рубл» без предлога — не просьба о валюте: этот же разбор
		// служит страховкой поверх описания траты, и «рубленый стейк» не
		// должен переключать весь ответ на рубли.
		"купил рубленый стейк": "",
		"батарейки 300":        "",
	}
	for q, want := range cases {
		if got := ParseDisplayCurrency(q); got != want {
			t.Errorf("ParseDisplayCurrency(%q) = %q, ожидали %q", q, got, want)
		}
	}
}
