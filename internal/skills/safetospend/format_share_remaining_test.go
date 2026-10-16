package safetospend

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"simpleAI/internal/budget"
)

// Показ ТЕКУЩИХ конвертов — тот же формат, что у раскладки прихода
// (simpleAI-faeq.11), но по каждому конверту видно потрачено / осталось, а
// дневной лимит пересчитан на ОСТАВШИЕСЯ дни (simpleAI-faeq.12).
//
// Тесты держат посимвольное совпадение по той же причине, что и у раскладки:
// формат — предмет задачи, а уехавшая на пробел колонка видна оператору сразу,
// но не тесту «содержит подстроку».

func remainingEnv() *budget.Envelope {
	return &budget.Envelope{
		PeriodStart: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC),
	}
}

// remainingReference — конверты оператора на второй день периода: часть еды
// потрачена, транспорт пробит на 200 ฿.
func remainingReference() []ShareRemaining {
	mk := func(name, kind string, limit, spent float64) ShareRemaining {
		return ShareRemaining{
			Name: name, Kind: kind, Source: budget.ShareSourceAuto,
			Allocated: limit, LimitTHB: limit, SpentTHB: spent, Remaining: limit - spent,
		}
	}
	return []ShareRemaining{
		mk("аренда", budget.ShareKindFixed, 18000, 0),
		mk("Кредит потребительский Сбербанк", budget.ShareKindFixed, 9193.55, 0),
		mk("Еда", budget.ShareKindSpend, 5400, 2000),
		mk("Транспорт", budget.ShareKindSpend, 1700, 1900),
		mk(budget.FallbackShareName, budget.ShareKindSpend, 1200, 0),
		mk(savingsShareName, budget.ShareKindSave, 858.06, 0),
	}
}

// referenceNow — 25.08, второй день периода: до конца 06.09 остаётся 13 дней.
var referenceNow = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

const referenceRemainingText = "24.08 — 06.09 · осталось 13 дней\n" +
	"Курс 3,1 ₽/฿ на 25.08\n" +
	"\n" +
	"**Что осталось**\n" +
	"```\n" +
	"               потрачено  осталось\n" +
	"Аренда               0 ฿  18 000 ฿\n" +
	"Кредит               0 ฿   9 194 ฿\n" +
	"Еда              2 000 ฿   3 400 ฿\n" +
	"Транспорт        1 900 ฿    -200 ฿\n" +
	"Прочее               0 ฿   1 200 ฿\n" +
	"Накопления           0 ฿     858 ฿\n" +
	"                 -------  --------\n" +
	"                 3 900 ฿  32 452 ฿\n" +
	"```\n" +
	"\n" +
	"**На день: 338 ฿**\n" +
	"Осталось 13 дней. Еда, транспорт, прочее. Аренда, Кредит — уже отложены.\n" +
	"\n" +
	"⚠️ Пробито: Транспорт — дальше тратишь из других конвертов."

func TestFormatShareRemaining_MatchesApprovedFormat(t *testing.T) {
	got := formatShareRemaining(remainingReference(), NewDisplay("THB", 3.1), remainingEnv(), referenceNow)
	if got != referenceRemainingText {
		t.Errorf("формат показа конвертов разошёлся с эталоном.\n--- получено ---\n%s\n--- ожидалось ---\n%s", got, referenceRemainingText)
	}
}

// Моноблок не имеет права уехать в горизонтальный скролл, и эмодзи внутри pre
// быть не должно: обе колонки цифр держатся только моноширинным шрифтом.
func TestFormatShareRemaining_MonoBlockWidth(t *testing.T) {
	got := formatShareRemaining(remainingReference(), NewDisplay("THB", 3.1), remainingEnv(), referenceNow)
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

// Итог сходится: обе колонки складываются ровно в напечатанные под ними итоги.
// Проверяется по НАПЕЧАТАННЫМ числам, а не по исходным float: расхождение
// возникает именно на округлении строк (9 193,55 → 9 194).
func TestFormatShareRemaining_ColumnsSumToTotals(t *testing.T) {
	got := formatShareRemaining(remainingReference(), NewDisplay("THB", 3.1), remainingEnv(), referenceNow)

	spentSum, remSum, spentTotal, remTotal, ok := monoColumnSums(got)
	if !ok {
		t.Fatal("в моноблоке нет строки-итога")
	}
	if spentSum != spentTotal {
		t.Errorf("колонка «потрачено» складывается в %d, а итог напечатан как %d", spentSum, spentTotal)
	}
	if remSum != remTotal {
		t.Errorf("колонка «осталось» складывается в %d, а итог напечатан как %d", remSum, remTotal)
	}
	if spentTotal != 3900 {
		t.Errorf("итог потрачено = %d, ожидалось 3900", spentTotal)
	}
	if remTotal != 32452 {
		t.Errorf("итог осталось = %d, ожидалось 32452 (18000+9194+3400−200+1200+858)", remTotal)
	}
}

// monoColumnSums читает обе числовые колонки моноблока: суммы строк до
// разделителя и напечатанные итоги после него.
func monoColumnSums(out string) (spentSum, remSum, spentTotal, remTotal int, ok bool) {
	inPre := false
	afterSep := false
	for _, line := range strings.Split(out, "\n") {
		if line == "```" {
			inPre = !inPre
			continue
		}
		if !inPre {
			continue
		}
		if strings.Contains(line, "---") {
			afterSep = true
			continue
		}
		s, r, parsed := parseTwoAmounts(line)
		if !parsed {
			continue
		}
		if afterSep {
			return spentSum, remSum, s, r, true
		}
		spentSum += s
		remSum += r
	}
	return 0, 0, 0, 0, false
}

// parseTwoAmounts достаёт из строки моноблока два числа. Разбор идёт по
// ГРАНИЦАМ КОЛОНОК, а не по strings.Fields: разряды отделены пробелом, и
// «3 900   32 452» распалось бы на четыре поля, из которых не собрать два
// числа. Ширины колонок — те же константы, которыми строка и печаталась.
func parseTwoAmounts(line string) (int, int, bool) {
	r := []rune(line)
	if len(r) < remTotalsWidth {
		return 0, 0, false
	}
	spent, ok1 := parseGrouped(string(r[remLabelWidth : remLabelWidth+remSpentWidth]))
	left, ok2 := parseGrouped(string(r[remLabelWidth+remSpentWidth : remTotalsWidth]))
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return spent, left, true
}

// parseGrouped читает число одной колонки: пробелы-разряды и знак валюты
// выкидываются, нецифровое содержимое (шапка «потрачено», линейка «-----»)
// отвергается. Знак валюты стоит у каждой суммы (simpleAI-302i) и к разбору
// колонки отношения не имеет — здесь проверяется сходимость, а не вёрстка.
func parseGrouped(cell string) (int, bool) {
	digits := strings.TrimSpace(cell)
	digits = strings.TrimSuffix(digits, "฿")
	digits = strings.TrimSuffix(digits, "₽")
	digits = strings.ReplaceAll(strings.TrimSpace(digits), " ", "")
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	if digits == "" {
		return 0, false
	}
	n := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

// Ключевое требование simpleAI-faeq.12: дневной лимит делится на ОСТАВШИЕСЯ
// дни, а не на длину периода. Купил продуктов на 2 000 — завтра планка ниже, и
// оператор видит это числом.
func TestFormatShareRemaining_DailyLimitUsesDaysLeft(t *testing.T) {
	env := remainingEnv()
	items := remainingReference()

	// Остаток гибких = 3400 − 200 + 1200 = 4400.
	// 25.08 → до 06.09 включительно 13 дней: 4400/13 = 338.
	got := formatShareRemaining(items, NewDisplay("THB", 3.1), env, referenceNow)
	if !strings.Contains(got, "**На день: 338 ฿**") {
		t.Errorf("на 25.08 ожидался лимит 338 ฿ (4400 / 13 дней), получено:\n%s", got)
	}
	// Через неделю остаток тот же, а дней меньше — планка ВЫШЕ той, что была бы
	// при делении на весь период (4400/14 = 314).
	later := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) // до 06.09 — 5 дней
	got = formatShareRemaining(items, NewDisplay("THB", 3.1), env, later)
	if !strings.Contains(got, "**На день: 880 ฿**") {
		t.Errorf("на 02.09 ожидался лимит 880 ฿ (4400 / 5 дней), получено:\n%s", got)
	}
	if !strings.Contains(got, "осталось 5 дней") {
		t.Errorf("в шапке нет числа оставшихся дней:\n%s", got)
	}
}

// Последний день периода: делим на 1, а не на длину периода.
func TestDaysLeft_LastDayIsOne(t *testing.T) {
	end := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 6, 23, 0, 0, 0, time.UTC)
	if got := DaysLeft(now, end); got != 1 {
		t.Errorf("в последний день периода осталось %d дней, ожидалось 1", got)
	}
	if got := DailyLimit(4400, DaysLeft(now, end)); !eq(got, 4400) {
		t.Errorf("в последний день лимит = %.2f, ожидалось 4400 (весь остаток на один день)", got)
	}
}

// День ПОСЛЕ конца периода: делить на ноль (и на отрицательное) нельзя.
func TestDaysLeft_AfterPeriodEndNeverZero(t *testing.T) {
	end := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	for _, now := range []time.Time{
		time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	} {
		if got := DaysLeft(now, end); got != 1 {
			t.Errorf("после конца периода (%s) осталось %d дней, ожидалось 1", now.Format("02.01"), got)
		}
	}
	out := formatShareRemaining(remainingReference(), NewDisplay("THB", 3.1), remainingEnv(),
		time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC))
	if strings.Contains(out, "Inf") || strings.Contains(out, "NaN") {
		t.Errorf("деление на ноль просочилось в ответ:\n%s", out)
	}
	if !strings.Contains(out, "**На день: 4 400 ฿**") {
		t.Errorf("после конца периода ожидался лимит 4 400 ฿ (остаток / 1 день), получено:\n%s", out)
	}
}

// Пробитый конверт видно: минусом в колонке остатка и отдельной строкой снизу.
func TestFormatShareRemaining_OverspentIsVisible(t *testing.T) {
	got := formatShareRemaining(remainingReference(), NewDisplay("THB", 3.1), remainingEnv(), referenceNow)
	if !strings.Contains(got, "Транспорт        1 900 ฿    -200 ฿") {
		t.Errorf("пробитый конверт не показан минусом в колонке остатка:\n%s", got)
	}
	if !strings.Contains(got, "⚠️ Пробито: Транспорт") {
		t.Errorf("нет строки о пробитом конверте:\n%s", got)
	}

	// Целый конверт такой строки не рождает.
	whole := remainingReference()
	whole[3].SpentTHB, whole[3].Remaining = 100, 1600
	if out := formatShareRemaining(whole, NewDisplay("THB", 3.1), remainingEnv(), referenceNow); strings.Contains(out, "Пробито") {
		t.Errorf("ни один конверт не пробит, а предупреждение напечатано:\n%s", out)
	}
}

// Валюта показа — существующий механизм Display (simpleAI-faeq.9), не своя
// арифметика: в рублях все числа умножены на курс, знак — ₽.
func TestFormatShareRemaining_DisplayRUB(t *testing.T) {
	got := formatShareRemaining(remainingReference(), NewDisplay("RUB", 2), remainingEnv(), referenceNow)
	if !strings.Contains(got, "Еда              4 000 ₽   6 800 ₽") {
		t.Errorf("суммы не переведены в рубли по курсу 2:\n%s", got)
	}
	if !strings.Contains(got, "**На день: 677 ₽**") {
		t.Errorf("дневной лимит не в рублях (ожидалось 677 ₽ = 4400*2/13):\n%s", got)
	}
	// Строка курса печатает оба знака по определению («2,0 ₽/฿») — смотрим на
	// суммы, а не на курс.
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "Курс ") && strings.Contains(line, "฿") {
			t.Errorf("в рублёвом показе остался батовый знак: %q", line)
		}
	}
}
