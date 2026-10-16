package plan

import (
	"flag"
	"testing"

	"pgregory.net/rapid"
)

// AC-4.3: «Можно тратить» сверяется с ОРАКУЛОМ — простым независимым подсчётом по
// правилам спеки (не вызывающим внутренностей Compute). Если формула в Compute
// потеряет слагаемое или посчитает платёж дважды, оракул разойдётся с ним.

type scenario struct {
	ws      []Window
	events  []Event
	rate    *Rate
	cur     Currency
	today   Date
	pct     int
	start   Date
	incomes []Event
}

func genScenario(t *rapid.T) scenario {
	start := Date{2026, 10, 1}.AddDays(rapid.IntRange(0, 60).Draw(t, "старт окна"))
	var incomes []Event
	for i := 0; i < rapid.IntRange(1, 3).Draw(t, "число поступлений"); i++ {
		d := start.AddDays(rapid.IntRange(0, 13).Draw(t, "день поступления"))
		if i == 0 {
			d = start
		}
		st := rapid.SampledFrom([]State{StateDone, StatePlanned}).Draw(t, "состояние поступления")
		cur := rapid.SampledFrom([]Currency{CurRUB, CurTHB}).Draw(t, "валюта поступления")
		incomes = append(incomes, Event{Key: "in" + pad(i, 2), Kind: KindIncome, State: st, Name: "доход",
			Amount: &Money{int64(rapid.IntRange(1, 5_000_000).Draw(t, "сумма дохода")), cur}, Date: &d, Source: SourceTransaction})
	}
	// «Сегодня» берём с запасом на все состояния: до окна, внутри, после.
	var today Date
	switch rapid.IntRange(0, 4).Draw(t, "положение сегодня") {
	case 0:
		today = start.AddDays(rapid.IntRange(-4, -1).Draw(t, "до окна"))
	case 1, 2:
		today = start.AddDays(rapid.IntRange(0, 13).Draw(t, "внутри окна"))
	default:
		today = start.AddDays(rapid.IntRange(14, 25).Draw(t, "после окна"))
	}
	var evs []Event
	for i := 0; i < rapid.IntRange(0, 14).Draw(t, "число платежей"); i++ {
		e := Event{Key: "p" + pad(i, 2), Kind: KindPayment, Name: "платёж", Source: SourcePlanned}
		e.State = rapid.SampledFrom([]State{StatePlanned, StateDone}).Draw(t, "состояние")
		if rapid.IntRange(0, 5).Draw(t, "есть ли сумма") > 0 || e.State == StateDone {
			cur := rapid.SampledFrom([]Currency{CurRUB, CurTHB}).Draw(t, "валюта")
			e.Amount = &Money{int64(rapid.IntRange(1, 3_000_000).Draw(t, "сумма")), cur}
		}
		if e.State == StateDone || rapid.IntRange(0, 4).Draw(t, "есть ли дата") > 0 {
			// Половина дат — вокруг «сегодня»: так появляются просроченные платежи.
			base := start
			if rapid.Bool().Draw(t, "дата от сегодня") {
				base = today
			}
			d := base.AddDays(rapid.IntRange(-12, 15).Draw(t, "дата платежа"))
			e.Date = &d
		} else if rapid.Bool().Draw(t, "выбрано окно") {
			ws := start.AddDays(rapid.IntRange(-20, 30).Draw(t, "дата окна"))
			e.WindowStart = &ws
		}
		if e.State == StatePlanned && e.Amount != nil && e.Date != nil && rapid.Bool().Draw(t, "повторяющийся") {
			e.RuleID = "r" + pad(i, 2) // у каждого свой RuleID → дублей сроков нет
			e.Source, e.ScheduledFor = SourceRecurring, e.Date
		}
		evs = append(evs, e)
	}
	return scenario{
		ws: BuildWindows(incomes), events: evs, incomes: incomes, start: start,
		rate:  &Rate{int64(rapid.IntRange(1_500_000, 4_000_000).Draw(t, "курс"))},
		cur:   rapid.SampledFrom([]Currency{CurRUB, CurTHB}).Draw(t, "валюта показа"),
		today: today,
		pct:   rapid.IntRange(0, 100).Draw(t, "резерв %"),
	}
}

func whole(t *rapid.T, s scenario, m *Money) int64 {
	n, err := Convert(s.rate, *m, s.cur)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return n
}

// oracle — отдельная от Compute реализация правил R8 для ОДНОГО окна.
type expectation struct {
	income, payments, spent, reserve, canSpend int64
	unpriced                                   int
}

func oracle(t *rapid.T, s scenario) expectation {
	var income, payments, spent, reserve int64
	var unpriced int
	end := s.start.AddDays(13)
	current := s.today.Compare(s.start) >= 0 && s.today.Compare(end) <= 0
	for _, e := range s.incomes {
		income += whole(t, s, e.Amount)
	}
	for _, e := range s.events {
		inWin := e.Date != nil && e.Date.Compare(s.start) >= 0 && e.Date.Compare(end) <= 0
		if e.State == StateDone {
			if inWin {
				spent += whole(t, s, e.Amount)
			}
			continue
		}
		overdue := e.RuleID == "" && e.Date != nil && e.Date.Compare(s.today) < 0
		counted := inWin || (current && overdue) ||
			(e.Date == nil && e.WindowStart != nil && e.WindowStart.Compare(end) <= 0)
		if !counted {
			continue
		}
		if e.Amount == nil {
			unpriced++
			continue
		}
		payments += whole(t, s, e.Amount)
	}
	reserve = (income*int64(s.pct) + 50) / 100
	return expectation{income, payments, spent, reserve, income - payments - spent - reserve, unpriced}
}

func TestProp_Compute_MatchesIndependentOracle(t *testing.T) {
	// Покрытие генератора замеряется В ТОЙ ЖЕ форме, что и свойство (один rapid.Check
	// с тем же genScenario): иначе замер по отдельной стратегии доказывал бы
	// покрытие ветки, которой свойство не касалось.
	hits := map[string]int{}
	// 100 сценариев по умолчанию мало для редких веток (просрочка в текущем окне);
	// прогон по-прежнему миллисекунды, а покрытие замеряется ниже в той же форме.
	old := flag.Lookup("rapid.checks").Value.String()
	if err := flag.Set("rapid.checks", "600"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := flag.Set("rapid.checks", old); err != nil {
			t.Error(err)
		}
	}()
	rapid.Check(t, func(t *rapid.T) {
		s := genScenario(t)
		noteCoverage(hits, s)
		v, err := Compute(ComputeInput{Windows: s.ws, Events: s.events, Rate: s.rate, Cur: s.cur, Today: s.today, ReservePct: s.pct})
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		exp := oracle(t, s)
		income, payments, spent, reserve, canSpend, unpriced := exp.income, exp.payments, exp.spent, exp.reserve, exp.canSpend, exp.unpriced
		if got := v.Income.Received + v.Income.Expected; got != income {
			t.Fatalf("income %d, оракул %d", got, income)
		}
		if v.Payments != payments || v.Spent != spent || v.Reserve != reserve || v.Unpriced != unpriced {
			t.Fatalf("payments/spent/reserve/unpriced: %d/%d/%d/%d, оракул %d/%d/%d/%d",
				v.Payments, v.Spent, v.Reserve, v.Unpriced, payments, spent, reserve, unpriced)
		}
		if v.CanSpend != canSpend {
			t.Fatalf("canSpend %d, оракул %d", v.CanSpend, canSpend)
		}
		if canSpend < 0 {
			hits["недостача"]++
		}
		// perDay: nil при недостаче или нуле дней, иначе floor(canSpend/daysLeft).
		left := v.Window.DaysLeft
		switch {
		case canSpend < 0 || left == 0:
			if v.PerDay != nil {
				t.Fatalf("perDay %d при canSpend=%d daysLeft=%d, ждали nil", *v.PerDay, canSpend, left)
			}
		default:
			if v.PerDay == nil || *v.PerDay != canSpend/int64(left) {
				t.Fatalf("perDay %v, ждали %d", v.PerDay, canSpend/int64(left))
			}
		}
	})
	for _, k := range []string{"current", "upcoming", "ended", "просроченный в current", "платёж без суммы", "без даты с окном",
		"план-поступление", "факт-поступление", "повторяющийся", "оплачен в окне", "показ ฿", "показ ₽", "резерв > 0", "недостача", "perDay nil при 0 дней"} {
		if hits[k] < 15 {
			t.Errorf("генератор почти не доходит до ветки %q: %d попаданий (<15) — свойство бы её не проверяло", k, hits[k])
		}
	}
}

// noteCoverage считает, сколько сгенерированных сценариев задели интересные ветки.
func noteCoverage(h map[string]int, s scenario) {
	end := s.start.AddDays(13)
	switch {
	case s.today.Compare(s.start) < 0:
		h["upcoming"]++
	case s.today.Compare(end) <= 0:
		h["current"]++
	default:
		h["ended"]++
	}
	current := s.today.Compare(s.start) >= 0 && s.today.Compare(end) <= 0
	for _, e := range s.incomes {
		if e.State == StatePlanned {
			h["план-поступление"]++
		} else {
			h["факт-поступление"]++
		}
	}
	for _, e := range s.events {
		if e.State == StatePlanned && e.Amount == nil {
			h["платёж без суммы"]++
		}
		if e.Date == nil && e.WindowStart != nil {
			h["без даты с окном"]++
		}
		if e.RuleID != "" {
			h["повторяющийся"]++
		}
		if e.State == StateDone && e.Date != nil && e.Date.Compare(s.start) >= 0 && e.Date.Compare(end) <= 0 {
			h["оплачен в окне"]++
		}
		if current && e.State == StatePlanned && e.RuleID == "" && e.Date != nil && e.Date.Compare(s.today) < 0 && e.Amount != nil {
			h["просроченный в current"]++
		}
	}
	if s.cur == CurTHB {
		h["показ ฿"]++
	} else {
		h["показ ₽"]++
	}
	if s.pct > 0 {
		h["резерв > 0"]++
	}
	if s.today.Compare(end) > 0 {
		h["perDay nil при 0 дней"]++
	}
}

// Порядок платежей на входе не влияет на итог.
func TestProp_Compute_OrderIndependent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genScenario(t)
		in := ComputeInput{Windows: s.ws, Events: s.events, Rate: s.rate, Cur: s.cur, Today: s.today, ReservePct: s.pct}
		a, err := Compute(in)
		if err != nil {
			t.Fatal(err)
		}
		in.Events = rapid.Permutation(s.events).Draw(t, "перестановка")
		b, err := Compute(in)
		if err != nil {
			t.Fatal(err)
		}
		if a.CanSpend != b.CanSpend || a.Payments != b.Payments || a.Spent != b.Spent || a.Unpriced != b.Unpriced {
			t.Fatalf("порядок повлиял: %+v vs %+v", a, b)
		}
	})
}
