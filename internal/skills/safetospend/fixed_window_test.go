package safetospend

import (
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

// Окно финансирования регулярных платежей = период конверта, обе границы
// включительно (simpleAI-agz4). Проверяется ровно граница: платёж день в день
// с концом периода — этот конверт, следующий день — уже следующий.
//
// Тест дословный к решению оператора 24.08.2026: приход дважды в месяц, период
// совпадает с ритмом прихода, поэтому платёж следующего периода финансируется
// приходом того периода. Прежнее поведение (месяц вперёд, sinking fund)
// запирало деньги под платежи, до которых ещё придут свои.
func TestFixedShares_WindowIsEnvelopePeriod(t *testing.T) {
	from := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	rates := map[string]float64{"RUB": 1, "THB": 2.5351}

	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}

	cases := []struct {
		name     string
		next     time.Time
		wantIn   bool
		wantUpco bool
	}{
		{"за день до конца периода", day(2026, 9, 5), true, false},
		{"день в день с концом периода", day(2026, 9, 6), true, false},
		{"на следующий день после конца", day(2026, 9, 7), false, true},
		{"первый день периода", from, true, false},
		{"за день до начала — уже просрочен", day(2026, 8, 23), false, false},
		{"дальше окна обзора", day(2026, 12, 1), false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := []budget.RecurringPayment{
				{Name: "аренда", Type: "expense", Amount: 18000, Currency: "THB", Enabled: true, NextDate: c.next},
			}
			fixed, upcoming, _ := fixedShares(rec, rates, from, to)
			if got := len(fixed) == 1; got != c.wantIn {
				t.Errorf("в конверте=%v, ожидалось %v (платёж %s, период %s–%s)",
					got, c.wantIn, c.next.Format("02.01"), from.Format("02.01"), to.Format("02.01"))
			}
			if got := len(upcoming) == 1; got != c.wantUpco {
				t.Errorf("в «впереди»=%v, ожидалось %v (платёж %s)", got, c.wantUpco, c.next.Format("02.01"))
			}
		})
	}
}

// Эталон оператора (реплика 24.08.2026): конверт 24.08–06.09, четыре регулярных
// платежа. Аренда 10.09 и подписка 10.09 уходят из конверта в «впереди», и на
// гибкие конверты высвобождается ровно их сумма.
func TestPlanEnvelope_UpcomingFreesFlexible(t *testing.T) {
	from := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	rates := map[string]float64{"RUB": 1, "THB": 2.5351}

	rec := []budget.RecurringPayment{
		{Name: "аренда", Type: "expense", Amount: 18000, Currency: "THB", Enabled: true,
			NextDate: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)},
		{Name: "подписка Клауд личная", Type: "expense", Amount: 1700, Currency: "RUB", Enabled: true,
			NextDate: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)},
		{Name: "Кредит потребительский Сбербанк", Type: "expense", Amount: 28500, Currency: "RUB", Enabled: true,
			NextDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)},
		{Name: "Ежемесячный платеж 3000р", Type: "expense", Amount: 3000, Currency: "RUB", Enabled: true,
			NextDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	}
	in := EnvelopePlanInput{
		IncomeTHB: 50490.91, // 128 000 ₽ по курсу 2,5351
		Snapshot:  &budget.AdvisorSnapshot{},
		Forecast:  []budget.CategoryForecast{{CategoryName: "Еда", Currency: "THB", ForecastAmount: 10769}},
		Rates:     rates,
		Days:      14,
		History:   map[string]int{"еда": 3},
		Recurring: rec,
		From:      from,
		To:        to,
	}

	plan := PlanEnvelope(in)

	fixed := FixedShares(plan.Shares)
	if len(fixed) != 2 {
		t.Fatalf("в конверте %d платежей, ожидалось 2 (кредит и ежемесячный): %+v", len(fixed), fixed)
	}
	for _, sh := range fixed {
		if sh.Name == "аренда" || sh.Name == "подписка Клауд личная" {
			t.Errorf("платёж 10.09 не имеет права быть в конверте до 06.09: %+v", sh)
		}
	}
	if len(plan.Upcoming) != 2 {
		t.Fatalf("в «впереди» %d платежей, ожидалось 2: %+v", len(plan.Upcoming), plan.Upcoming)
	}
	if plan.Upcoming[0].Name != "аренда" || !eq(plan.Upcoming[0].Allocated, 18000) {
		t.Errorf("первой в «впереди» ожидалась аренда 18000 ฿: %+v", plan.Upcoming[0])
	}

	// Ровно та сумма, которую оператор недосчитывался в свободных деньгах.
	freed := 18000 + roundKopecks(1700/2.5351)
	prev := PlanEnvelope(withMonthWindow(in))
	if got := plan.Result.FreeAfterObligations - prev.Result.FreeAfterObligations; !eqTol(got, freed, 0.02) {
		t.Errorf("высвободилось %.2f ฿, ожидалось %.2f ฿ (аренда + подписка)", got, freed)
	}
}

// withMonthWindow воспроизводит ПРЕЖНЕЕ поведение (окно — месяц вперёд), чтобы
// измерить разницу тем же кодом, а не константой из головы.
func withMonthWindow(in EnvelopePlanInput) EnvelopePlanInput {
	out := in
	out.To = in.From.AddDate(0, 0, 30)
	return out
}

// Отсечённый платёж обязан быть НАПЕЧАТАН: не увидев аренду нигде, оператор
// потратит её на еду. Мутация: убрать блок «впереди» — тест краснеет.
func TestFormatEnvelopePlan_PrintsUpcoming(t *testing.T) {
	due := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	out := FormatEnvelopePlan(EnvelopeReply{
		Plan: EnvelopePlan{
			Shares: []budget.EnvelopeShare{
				{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5000},
			},
			Upcoming: []budget.EnvelopeShare{
				{Name: "аренда", Kind: budget.ShareKindFixed, Allocated: 18000, DueDate: &due},
			},
		},
		RubPerTHB: 2.5351,
		Display:   NewDisplay("THB", 2.5351),
		From:      time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
		To:        time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC),
	})

	if !strings.Contains(out, "Впереди") {
		t.Fatalf("нет блока «Впереди»:\n%s", out)
	}
	for _, want := range []string{normalizeLabel("аренда"), "18 000 ฿", "10.09"} {
		if !strings.Contains(out, want) {
			t.Errorf("в блоке «Впереди» нет %q:\n%s", want, out)
		}
	}
}

func eqTol(a, b, tol float64) bool {
	d := a - b
	return d < tol && d > -tol
}
