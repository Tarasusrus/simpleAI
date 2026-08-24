package safetospend

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

// Ни одна денежная строка не печатается без знака валюты (simpleAI-302i).
//
// Оператор смотрел на колонку конвертов и не мог сказать, баты там или рубли:
// в моноблоке суммы шли голыми числами, а в safe-to-spend знак рубля был прибит
// к формату руками и стоял местами рядом с батовой суммой.
//
// Проверка — регекспом по СОБРАННОМУ ответу, а не по коду: только так тест
// ловит новый Fprintf, добавленный завтра мимо Display.
//
// Мутация: убрать знак из Display.Signed — тест краснеет.
func TestFormat_EveryAmountCarriesCurrencySign(t *testing.T) {
	from := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	due := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	shares := []budget.EnvelopeShare{
		{Name: "Кредит потребительский Сбербанк", Kind: budget.ShareKindFixed, Allocated: 11242.05,
			DueDate: ptrTime(time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC))},
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5025.73},
		{Name: "накопления", Kind: budget.ShareKindSave, Allocated: 9237.54, CarriedIn: 1200},
	}

	cases := []struct {
		name string
		out  string
	}{
		{
			name: "раскладка конвертов, баты",
			out: FormatEnvelopePlan(EnvelopeReply{
				Plan: EnvelopePlan{
					Shares: shares,
					Upcoming: []budget.EnvelopeShare{
						{Name: "аренда", Kind: budget.ShareKindFixed, Allocated: 18000, DueDate: &due},
					},
					Result: Result{IncomeTHB: 50490.91},
				},
				RubPerTHB:      2.5351,
				Display:        NewDisplay("THB", 2.5351),
				From:           from,
				To:             to,
				IncomeAmount:   128000,
				IncomeCurrency: "RUB",
			}),
		},
		{
			name: "раскладка конвертов, рубли",
			out: FormatEnvelopePlan(EnvelopeReply{
				Plan:           EnvelopePlan{Shares: shares, Result: Result{IncomeTHB: 50490.91}},
				RubPerTHB:      2.5351,
				Display:        NewDisplay("RUB", 2.5351),
				From:           from,
				To:             to,
				IncomeAmount:   128000,
				IncomeCurrency: "RUB",
			}),
		},
		{
			name: "safe-to-spend: сколько свободно",
			out: formatReply(replyData{
				res: Result{
					IncomeTHB: 50490.91, RecurringTHB: 12425.42, DebtTHB: 3000, PlannedTHB: 1500,
					ForecastSpendTHB: 10769, FreeAfterObligations: 33565.49, RealisticFree: 22796.49,
				},
				rubPerTHB: 2.5351,
				period:    "ближайшие 2 недели",
				planned:   []CategorySpend{{Category: "кредитка", THB: 1500}},
				variable:  []CategorySpend{{Category: "Еда", THB: 8000}, {Category: "Транспорт", THB: 2769}},
				advice:    []string{"срезать доставку"},
			}),
		},
		{
			// Дырка, найденная живым прогоном 24.08: показ конвертов печатал ДВЕ
			// колонки чисел, и обе без знака — этот форматтер в проверку не входил.
			name: "сколько в конвертах",
			out: formatShareRemaining([]ShareRemaining{
				{Name: "Кредит потребительский Сбербанк", Kind: budget.ShareKindFixed, Allocated: 11242.05, Remaining: 11242.05},
				{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5025.73, SpentTHB: 553, Remaining: 4472.73},
				{Name: "Транспорт", Kind: budget.ShareKindSpend, Allocated: 919.83, SpentTHB: 1100, Remaining: -180.17},
				{Name: "накопления", Kind: budget.ShareKindSave, Allocated: 27797.54, Remaining: 27797.54},
			}, NewDisplay("THB", 2.5351), &budget.Envelope{PeriodStart: from, PeriodEnd: to}, from),
		},
		{
			name: "остаток по конверту",
			out: formatRemaining(RemainingResult{
				IncomeTHB: 50490.91, RecurringTHB: 12425.42, DebtTHB: 3000,
				PlannedTHB: 1500, ActualSpentTHB: 4200, RemainingTHB: 29365.49,
			}, 2.5351, &budget.Envelope{PeriodStart: from, PeriodEnd: to}),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if bare := bareAmounts(c.out); len(bare) > 0 {
				t.Errorf("суммы без знака валюты: %v\n---\n%s", bare, c.out)
			}
		})
	}
}

// bareAmounts ищет числа, за которыми не стоит знак валюты.
//
// Из проверки исключено то, что деньгами не является: даты (02.01), длина
// периода в днях, сам курс (2,53 ₽/฿ — знак там принадлежит дроби, а не сумме)
// и номера в служебных строках. Исключения перечислены явно: молчаливое
// «похоже не на деньги» и есть способ, которым дыра вернётся.
func bareAmounts(out string) []string {
	notMoney := []*regexp.Regexp{
		regexp.MustCompile(`\d{2}\.\d{2}`),                 // дата 27.08
		regexp.MustCompile(`\d+[,.]\d+ ₽/฿`),               // курс
		regexp.MustCompile(`\d+ (день|дня|дней|недел\S*)`), // длина периода
	}
	// Число (возможно с пробелами-разрядами), за которым НЕ следует знак валюты.
	amount := regexp.MustCompile(`\d[\d  ]*\d|\d`)

	var bare []string
	for _, line := range strings.Split(out, "\n") {
		clean := line
		for _, re := range notMoney {
			clean = re.ReplaceAllString(clean, "")
		}
		for _, loc := range amount.FindAllStringIndex(clean, -1) {
			rest := strings.TrimLeft(clean[loc[1]:], " ")
			if strings.HasPrefix(rest, "฿") || strings.HasPrefix(rest, "₽") ||
				strings.HasPrefix(rest, "$") || strings.HasPrefix(rest, "€") {
				continue
			}
			bare = append(bare, strings.TrimSpace(clean[loc[0]:loc[1]])+" ← "+strings.TrimSpace(line))
		}
	}
	return bare
}

func ptrTime(t time.Time) *time.Time { return &t }
