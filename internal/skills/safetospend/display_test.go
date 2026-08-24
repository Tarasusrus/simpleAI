package safetospend

import (
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

// planForDisplay — раскладка с одной тратной долей, одной сберегательной и
// переносом с прошлого раза: в печати участвуют все три строки, валюту которых
// проверяет тест.
func planForDisplay() EnvelopePlan {
	return EnvelopePlan{
		Result: Result{
			IncomeTHB:            10000,
			RecurringTHB:         1000,
			DebtTHB:              0,
			FreeAfterObligations: 9000,
		},
		Shares: []budget.EnvelopeShare{
			{Name: "еда", Kind: budget.ShareKindSpend, Allocated: 5000, Position: 0},
			{Name: "накопления", Kind: budget.ShareKindSave, Allocated: 4000, CarriedIn: 2000, Position: 1},
		},
	}
}

func replyForDisplay(m Display) EnvelopeReply {
	now := time.Now()
	return EnvelopeReply{
		Plan:           planForDisplay(),
		RubPerTHB:      2,
		Display:        m,
		Period:         "ближайшие 2 недели",
		From:           now,
		To:             now.AddDate(0, 0, 14),
		IncomeAmount:   20000,
		IncomeCurrency: "RUB",
		OutsideTHB:     300,
	}
}

// Дефолт раскладки — баты: суммы конвертов печатаются как есть (хранение уже в
// THB), рублёвого знака в них нет. Курс при этом показывается всегда — он
// подпись к заголовку, а не валюта сумм.
func TestFormatEnvelopePlan_DefaultTHB(t *testing.T) {
	out := FormatEnvelopePlan(replyForDisplay(NewDisplay("", 2)))
	for _, want := range []string{
		"💰 Приход: 10000 ฿",
		"= К раскладке: 9000 ฿",
		"5000 ฿",
		"🆓 Свободно: 4000 ฿",
		"Перенесено с прошлого раза: 2000 ฿",
		"Вне конвертов: 300 ฿",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет строки %q в батовой раскладке:\n%s", want, out)
		}
	}
	// Заголовок остаётся в валюте прихода: «пришло 20000 ₽» обязано остаться
	// 20000 ₽, иначе оператор не узнает собственный приход.
	if !strings.Contains(out, "Конверт заведён: 20000 ₽") {
		t.Errorf("приход в заголовке потерял валюту оператора:\n%s", out)
	}
}

// Просьба показать в рублях переводит ВСЕ суммы конвертов по курсу 2 ₽/฿.
// Мутация «форматтер всегда печатает одну валюту» роняет либо этот тест, либо
// предыдущий — оба одновременно пройти не могут.
func TestFormatEnvelopePlan_DisplayRUB(t *testing.T) {
	out := FormatEnvelopePlan(replyForDisplay(NewDisplay("RUB", 2)))
	for _, want := range []string{
		"💰 Приход: 20000 ₽",
		"= К раскладке: 18000 ₽",
		"10000 ₽",
		"🆓 Свободно: 8000 ₽",
		"Перенесено с прошлого раза: 4000 ₽",
		"Вне конвертов: 600 ₽",
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
