package safetospend

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"simpleAI/internal/budget"
)

// Эталон формата раскладки, утверждённый оператором 2026-08-24 (simpleAI-faeq.11).
//
// Тест держит не «примерно похоже», а посимвольное совпадение: формат — это и
// есть предмет задачи. Разъехавшаяся на один пробел колонка в моноблоке видна
// оператору сразу, а тесту «содержит подстроку» — нет.

func due(t *testing.T, s string) *time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("дата %q: %v", s, err)
	}
	return &d
}

// referenceReply — данные оператора: приход 127 000 ₽ по курсу 3,1 ₽/฿ на
// период 24.08–06.09, четыре регулярных платежа и пять гибких конвертов.
func referenceReply(t *testing.T) EnvelopeReply {
	t.Helper()
	shares := []budget.EnvelopeShare{
		{Name: "аренда", Kind: budget.ShareKindFixed, Allocated: 18000, DueDate: due(t, "2026-09-10")},
		{Name: "Кредит потребительский Сбербанк", Kind: budget.ShareKindFixed, Allocated: 9193.55, DueDate: due(t, "2026-08-27")},
		{Name: "Ежемесячный платеж 3000р", Kind: budget.ShareKindFixed, Allocated: 967.74, DueDate: due(t, "2026-09-01")},
		{Name: "подписка Клауд личная", Kind: budget.ShareKindFixed, Allocated: 548.39, DueDate: due(t, "2026-09-10")},
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5400},
		{Name: "Транспорт", Kind: budget.ShareKindSpend, Allocated: 1700},
		{Name: "Здоровье", Kind: budget.ShareKindSpend, Allocated: 1700},
		{Name: "Развлечения", Kind: budget.ShareKindSpend, Allocated: 1400},
		{Name: budget.FallbackShareName, Kind: budget.ShareKindSpend, Allocated: 1200},
		{Name: savingsShareName, Kind: budget.ShareKindSave, Allocated: 858.06},
	}
	for i := range shares {
		shares[i].Position = i
		shares[i].Source = budget.ShareSourceAuto
	}
	return EnvelopeReply{
		Plan:           EnvelopePlan{Result: Result{IncomeTHB: 40967.74}, Shares: shares},
		RubPerTHB:      3.1,
		Display:        NewDisplay("THB", 3.1),
		From:           time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
		To:             time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC),
		IncomeAmount:   127000,
		IncomeCurrency: "RUB",
	}
}

const referenceEnvelopeText = "24.08 — 06.09 · 14 дней\n" +
	"Пришло 127 000 ₽ · 40 968 ฿\n" +
	"Курс 3,1 ₽/฿ на 24.08\n" +
	"\n" +
	"**Куда уйдут**\n" +
	"```\n" +
	"Аренда             10.09  18 000\n" +
	"Кредит             27.08   9 194\n" +
	"Ежемесячный платеж 01.09     968\n" +
	"Подписка Клауд     10.09     548\n" +
	"Еда                        5 400\n" +
	"Транспорт                  1 700\n" +
	"Здоровье                   1 700\n" +
	"Развлечения                1 400\n" +
	"Прочее                     1 200\n" +
	"Накопления                   858\n" +
	"                          ------\n" +
	"                          40 968\n" +
	"```\n" +
	"\n" +
	"**На день: 814 ฿**\n" +
	"Еда, транспорт, здоровье, развлечения, прочее. Аренда, Кредит, Ежемесячный платеж, Подписка Клауд — уже отложены."

func TestFormatEnvelopePlan_MatchesApprovedReference(t *testing.T) {
	got := FormatEnvelopePlan(referenceReply(t))
	if got != referenceEnvelopeText {
		t.Errorf("формат разошёлся с эталоном оператора.\n--- получено ---\n%s\n--- ожидалось ---\n%s", got, referenceEnvelopeText)
	}
}

// Колонка в моноблоке не имеет права уезжать в горизонтальный скролл: pre в
// Telegram не переносит строки по словам. Порог 36 — из ресёрча вёрстки.
func TestFormatEnvelopePlan_MonoBlockWidth(t *testing.T) {
	got := FormatEnvelopePlan(referenceReply(t))
	inPre := false
	for _, line := range strings.Split(got, "\n") {
		if line == "```" {
			inPre = !inPre
			continue
		}
		if !inPre {
			continue
		}
		if w := utf8.RuneCountInString(line); w > 36 {
			t.Errorf("строка моноблока шире 36 знаков (%d): %q", w, line)
		}
		for _, r := range line {
			if r > 0x2000 && r != '฿' && r != '₽' && r != '—' {
				t.Errorf("нетекстовый символ %q внутри pre ломает выравнивание: %q", r, line)
			}
		}
	}
}

// Итог обязан сходиться с приходом до бата: колонка складывается ровно в ту
// сумму, которую оператор увидел в шапке.
func TestFormatEnvelopePlan_ColumnSumsToIncome(t *testing.T) {
	got := FormatEnvelopePlan(referenceReply(t))
	lines := strings.Split(got, "\n")

	var sum int
	var total int
	seenSeparator := false
	for _, line := range lines {
		if strings.Contains(line, "------") {
			seenSeparator = true
			continue
		}
		n, ok := trailingAmount(line)
		if !ok {
			continue
		}
		if seenSeparator {
			total = n
			break
		}
		sum += n
	}
	if !seenSeparator {
		t.Fatal("в моноблоке нет строки-итога")
	}
	if sum != total {
		t.Errorf("колонка складывается в %d, а итог напечатан как %d", sum, total)
	}
	if total != 40968 {
		t.Errorf("итог %d не сходится с приходом 40 968 ฿", total)
	}
}

// trailingAmount вытаскивает число из хвоста строки моноблока («18 000» → 18000).
func trailingAmount(line string) (int, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return 0, false
	}
	var digits []rune
	for i := len(fields) - 1; i >= 0; i-- {
		f := fields[i]
		allDigits := f != ""
		for _, r := range f {
			if r < '0' || r > '9' {
				allDigits = false
			}
		}
		if !allDigits {
			break
		}
		digits = append([]rune(f), digits...)
	}
	if len(digits) == 0 {
		return 0, false
	}
	n := 0
	for _, r := range digits {
		n = n*10 + int(r-'0')
	}
	return n, true
}

// Дневной лимит не зависит от прихода: он считается от гибких конвертов и
// оставшихся дней. Иначе при приходе в 10 рублей бот отвечал бы «на день 0».
func TestDailyLimit_IndependentOfIncome(t *testing.T) {
	shares := []budget.EnvelopeShare{
		{Name: "Аренда", Kind: budget.ShareKindFixed, Allocated: 18000},
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5400, CarriedIn: 0},
		{Name: "Транспорт", Kind: budget.ShareKindSpend, Allocated: 1700},
		{Name: "Здоровье", Kind: budget.ShareKindSpend, Allocated: 1700},
		{Name: "Развлечения", Kind: budget.ShareKindSpend, Allocated: 1400},
		{Name: budget.FallbackShareName, Kind: budget.ShareKindSpend, Allocated: 1200},
		{Name: savingsShareName, Kind: budget.ShareKindSave, Allocated: 858},
	}
	if got := FlexibleTHB(shares); !eq(got, 11400) {
		t.Fatalf("числитель дневного лимита = %.2f, ожидалось 11400 (только гибкие доли)", got)
	}
	if got := DailyLimit(FlexibleTHB(shares), 14); got < 814 || got >= 815 {
		t.Errorf("дневной лимит = %.2f, ожидалось ~814", got)
	}
}

// Лимит пересчитывается от ОСТАТКА: потратил 2000 в первый день — планка на
// остаток дней падает.
func TestDailyLimit_FallsAfterSpending(t *testing.T) {
	items := []ShareRemaining{
		{Name: "Аренда", Kind: budget.ShareKindFixed, Remaining: 18000},
		{Name: "Еда", Kind: budget.ShareKindSpend, Remaining: 3400},
		{Name: "Транспорт", Kind: budget.ShareKindSpend, Remaining: 1700},
		{Name: "Здоровье", Kind: budget.ShareKindSpend, Remaining: 1700},
		{Name: "Развлечения", Kind: budget.ShareKindSpend, Remaining: 1400},
		{Name: budget.FallbackShareName, Kind: budget.ShareKindSpend, Remaining: 1200},
		{Name: savingsShareName, Kind: budget.ShareKindSave, Remaining: 858},
	}
	before := DailyLimit(11400, 14)
	after := DailyLimit(FlexibleRemainingTHB(items), 13)
	if after >= before {
		t.Errorf("после траты 2000 планка не упала: было %.2f, стало %.2f", before, after)
	}
	if want := 9400.0 / 13.0; !eq(after, want) {
		t.Errorf("дневной лимит после траты = %.2f, ожидалось %.2f (остаток гибких / оставшиеся дни)", after, want)
	}
}

// Нулевой приход не ломает раскладку: конверты наполнены прошлым переносом, и
// дневной лимит по-прежнему считается.
func TestDailyLimit_WorksOnZeroIncome(t *testing.T) {
	shares := []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 0, CarriedIn: 2800},
	}
	if got := DailyLimit(FlexibleTHB(shares), 14); !eq(got, 200) {
		t.Errorf("при нулевом приходе дневной лимит = %.2f, ожидалось 200 (2800 переноса / 14 дней)", got)
	}
}
