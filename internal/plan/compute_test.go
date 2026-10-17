package plan

import (
	"errors"
	"testing"
)

// ---------- фикстуры golden-сценария (AC-4.1) ----------
// Окно 05.10–18.10, поступление 98 000 ₽, сегодня 14.10.

func payPlanned(key, name string, minor int64, cur Currency, d Date, ruleID string) Event {
	dd := d
	e := Event{Key: key, Kind: KindPayment, State: StatePlanned, Name: name,
		Amount: &Money{minor, cur}, Date: &dd, Source: SourcePlanned, RuleID: ruleID}
	if ruleID != "" {
		sc := d
		e.Source, e.ScheduledFor = SourceRecurring, &sc
	}
	return e
}

func payDone(key, name string, minor int64, cur Currency, d Date) Event {
	dd := d
	return Event{Key: key, Kind: KindPayment, State: StateDone, Name: name,
		Amount: &Money{minor, cur}, Date: &dd, Source: SourceTransaction}
}

func goldenWindows() []Window {
	return BuildWindows([]Event{inc("in1", Date{2026, 10, 5}, StateDone, 9_800_000)})
}

func goldenEvents() []Event {
	return []Event{
		payPlanned("recurring:rent@2026-10-10", "Аренда", 1_800_000, CurTHB, Date{2026, 10, 10}, "rent"),
		payPlanned("recurring:sub@2026-10-10", "Подписка", 170_000, CurRUB, Date{2026, 10, 10}, "sub"),
		payDone("t1", "Rimping", 59_300, CurTHB, Date{2026, 10, 13}),
		payDone("t2", "Макро", 140_000, CurTHB, Date{2026, 10, 14}),
		payDone("t3", "Рынок", 31_000, CurTHB, Date{2026, 10, 14}),
		// кредит: срок 27.09 исполнен фактом (вне окна), срок 27.10 после окна
		{Key: "t0", Kind: KindPayment, State: StateDone, Name: "Кредит", Amount: &Money{2_850_000, CurRUB},
			Date: &Date{2026, 9, 27}, Source: SourceTransaction, RuleID: "loan", ScheduledFor: &Date{2026, 9, 27}},
		payPlanned("recurring:loan@2026-10-27", "Кредит", 2_850_000, CurRUB, Date{2026, 10, 27}, "loan"),
	}
}

func in(ws []Window, ev []Event, cur Currency, r *Rate, today Date, pct int) ComputeInput {
	return ComputeInput{Windows: ws, Events: ev, Rate: r, Cur: cur, Today: today, ReservePct: pct, AvgDays: minAvgDays}
}

func mustCompute(t *testing.T, i ComputeInput) View {
	t.Helper()
	v, err := Compute(i)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return v
}

var r25, r27 = &Rate{2_500_000}, &Rate{2_700_000}

func TestCompute_Golden_AC41_Rubles(t *testing.T) {
	v := mustCompute(t, in(goldenWindows(), goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.Payments != 46_700 || v.Spent != 5_758 || v.CanSpend != 45_542 {
		t.Fatalf("payments=%d spent=%d canSpend=%d; ждали 46700/5758/45542", v.Payments, v.Spent, v.CanSpend)
	}
	if v.PerDay == nil || *v.PerDay != 9_108 || v.Window == nil || v.Window.DaysLeft != 5 {
		t.Fatalf("perDay=%v window=%+v; ждали 9108 и 5 дней", v.PerDay, v.Window)
	}
	if v.Income.Received != 98_000 || v.Income.Expected != 0 || v.Reserve != 0 || v.Unpriced != 0 {
		t.Fatalf("income=%+v reserve=%d unpriced=%d", v.Income, v.Reserve, v.Unpriced)
	}
	if v.State != StateCurrent || v.Window.Start != (Date{2026, 10, 5}) || v.Window.End != (Date{2026, 10, 18}) || v.Window.AnchorPlanned {
		t.Fatalf("state=%s window=%+v", v.State, v.Window)
	}
}

// AC-4.1b: аренда и подписка уже факты (создал воркер) — то же число, другие поля.
func TestCompute_Golden_AC41b_RecurringAlreadyFacts(t *testing.T) {
	ev := goldenEvents()
	ev[0] = payDone("t-rent", "Аренда", 1_800_000, CurTHB, Date{2026, 10, 10})
	ev[1] = payDone("t-sub", "Подписка", 170_000, CurRUB, Date{2026, 10, 10})
	v := mustCompute(t, in(goldenWindows(), ev, CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.Payments != 0 || v.Spent != 52_458 || v.CanSpend != 45_542 {
		t.Fatalf("payments=%d spent=%d canSpend=%d; ждали 0/52458/45542", v.Payments, v.Spent, v.CanSpend)
	}
}

// AC-4.1c: аренда разовая с датой 10.10 без факта — просрочена, но считается ОДИН раз.
func TestCompute_Golden_AC41c_OverdueCountedOnce(t *testing.T) {
	ev := goldenEvents()
	ev[0] = payPlanned("planned:rent", "Аренда", 1_800_000, CurTHB, Date{2026, 10, 10}, "")
	v := mustCompute(t, in(goldenWindows(), ev, CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.Payments != 46_700 || v.CanSpend != 45_542 {
		t.Fatalf("payments=%d canSpend=%d; ждали 46700/45542 (дубль условий «в окне» и «просрочен»?)", v.Payments, v.CanSpend)
	}
}

// AC-4.2: баты и курсы; слагаемые округляются по одному, итог — сумма округлённых.
func TestCompute_Golden_AC42_BahtAndRates(t *testing.T) {
	cases := []struct {
		name string
		cur  Currency
		r    *Rate
		want int64
	}{
		{"฿ при 2,5", CurTHB, r25, 18_217},
		{"฿ при 2,7 (не 15 364 — итог не округляется заново)", CurTHB, r27, 15_363},
		{"₽ при 2,7", CurRUB, r27, 41_482},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := mustCompute(t, in(goldenWindows(), goldenEvents(), c.cur, c.r, Date{2026, 10, 14}, 0))
			if v.CanSpend != c.want {
				t.Fatalf("canSpend = %d, ждали %d", v.CanSpend, c.want)
			}
		})
	}
}

func TestCompute_Reserve_AC46(t *testing.T) {
	v := mustCompute(t, in(goldenWindows(), goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 10))
	if v.Reserve != 9_800 || v.CanSpend != 35_742 {
		t.Fatalf("reserve=%d canSpend=%d; ждали 9800 и 35742", v.Reserve, v.CanSpend)
	}
	// Резерв считается от поступлений окна, в том числе ожидаемых.
	ws := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 9_800_000), inc("b", Date{2026, 10, 12}, StatePlanned, 13_000_000)})
	v = mustCompute(t, in(ws, goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 10))
	if v.Reserve != 22_800 {
		t.Fatalf("резерв от 228 000 = %d, ждали 22800", v.Reserve)
	}
}

// AC-4.7: пришло 98 000, ожидается 130 000 — «Можно тратить» учитывает обе суммы.
func TestCompute_ExpectedIncomeIncluded_AC47(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 9_800_000), inc("b", Date{2026, 10, 12}, StatePlanned, 13_000_000)})
	v := mustCompute(t, in(ws, goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.Income.Received != 98_000 || v.Income.Expected != 130_000 || v.CanSpend != 175_542 {
		t.Fatalf("income=%+v canSpend=%d; ждали 98000/130000/175542", v.Income, v.CanSpend)
	}
}

// AC-4.5: perDay — nil при daysLeft=0 и при недостаче; в последний день равен canSpend.
func TestCompute_PerDayRules(t *testing.T) {
	ws, ev := goldenWindows(), goldenEvents()
	last := mustCompute(t, in(ws, ev, CurRUB, r25, Date{2026, 10, 18}, 0))
	if last.PerDay == nil || *last.PerDay != last.CanSpend || last.Window.DaysLeft != 1 {
		t.Fatalf("последний день: perDay=%v canSpend=%d", last.PerDay, last.CanSpend)
	}
	between := mustCompute(t, in(ws, ev, CurRUB, r25, Date{2026, 10, 19}, 0))
	if between.State != StateEnded || between.PerDay != nil || between.Window.DaysLeft != 0 {
		t.Fatalf("после окна: state=%s perDay=%v", between.State, between.PerDay)
	}
	if between.CanSpend != 45_542 {
		t.Fatalf("canSpend после конца окна считается как обычно, получили %d", between.CanSpend)
	}
	// недостача: огромный платёж
	big := append(goldenEvents(), payPlanned("big", "Телевизор", 10_000_000, CurRUB, Date{2026, 10, 15}, ""))
	neg := mustCompute(t, in(ws, big, CurRUB, r25, Date{2026, 10, 14}, 0))
	if neg.CanSpend >= 0 || neg.PerDay != nil {
		t.Fatalf("недостача: canSpend=%d perDay=%v", neg.CanSpend, neg.PerDay)
	}
	// floor: 45 542 / 5 = 9 108,4 → 9 108; 45 542 / 4 = 11 385,5 → 11 385 (не 11 386)
	four := mustCompute(t, in(ws, ev, CurRUB, r25, Date{2026, 10, 15}, 0))
	if four.PerDay == nil || *four.PerDay != 11_385 {
		t.Fatalf("floor при 4 днях: %v, ждали 11385", four.PerDay)
	}
}

// AC-3.6: просрочка входит в текущее окно один раз; вне current — не входит.
func TestCompute_Overdue(t *testing.T) {
	ws := goldenWindows()
	over := payPlanned("planned:dent", "Стоматолог", 50_000, CurTHB, Date{2026, 10, 2}, "") // 500 ฿ → 1 250 ₽
	cur := mustCompute(t, in(ws, []Event{over}, CurRUB, r25, Date{2026, 10, 14}, 0))
	if cur.Payments != 1_250 {
		t.Fatalf("просроченный в current: payments=%d, ждали 1250", cur.Payments)
	}
	ended := mustCompute(t, in(ws, []Event{over}, CurRUB, r25, Date{2026, 10, 19}, 0))
	if ended.Payments != 0 {
		t.Fatalf("вне current просроченный не считается: payments=%d", ended.Payments)
	}
	// Повторяющееся плановое с датой в прошлом вне окон просроченным не бывает.
	rec := payPlanned("recurring:x@2026-10-02", "Платёж", 50_000, CurTHB, Date{2026, 10, 2}, "x")
	if v := mustCompute(t, in(ws, []Event{rec}, CurRUB, r25, Date{2026, 10, 14}, 0)); v.Payments != 0 {
		t.Fatalf("повторяющееся без факта вне окна не просрочено: payments=%d", v.Payments)
	}
}

func TestCompute_WithoutAmountIsUnpricedNotSummed(t *testing.T) {
	ev := append(goldenEvents(),
		Event{Key: "planned:p", Kind: KindPayment, State: StatePlanned, Name: "Права", Date: &Date{2026, 10, 15}, Source: SourcePlanned})
	v := mustCompute(t, in(goldenWindows(), ev, CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.Unpriced != 1 || v.CanSpend != 45_542 {
		t.Fatalf("unpriced=%d canSpend=%d; ждали 1 и 45542", v.Unpriced, v.CanSpend)
	}
}

// AC-3.2 (домен) / AC-3.3: платёж без даты с выбранным окном попадает в то окно,
// в какое резолвится дата; неотнесённый платёж в расчёт не входит.
func TestCompute_WindowStartResolution(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 9_800_000), inc("b", Date{2026, 10, 20}, StateDone, 13_000_000)})
	mk := func(key string, w *Date) Event {
		return Event{Key: key, Kind: KindPayment, State: StatePlanned, Name: key, Amount: &Money{100_000, CurRUB}, WindowStart: w, Source: SourcePlanned}
	}
	evs := []Event{mk("cur", &Date{2026, 10, 5}), mk("next", &Date{2026, 10, 19}), mk("none", nil)}
	first := mustCompute(t, in(ws, evs, CurRUB, r25, Date{2026, 10, 14}, 0))
	if first.Payments != 1_000 {
		t.Fatalf("первое окно: payments=%d, ждали 1000 (только «cur»)", first.Payments)
	}
	second := mustCompute(t, in(ws, evs, CurRUB, r25, Date{2026, 10, 21}, 0))
	if second.Payments != 1_000 {
		t.Fatalf("второе окно: payments=%d, ждали 1000 (только «next»)", second.Payments)
	}
	// Дата главнее WindowStart.
	both := mk("both", &Date{2026, 10, 20})
	both.Date = &Date{2026, 10, 10}
	if v := mustCompute(t, in(ws, []Event{both}, CurRUB, r25, Date{2026, 10, 14}, 0)); v.Payments != 1_000 {
		t.Fatalf("при заданной Date WindowStart игнорируется: payments=%d", v.Payments)
	}
}

// AC-4.10: события прошлого окна не влияют на «Можно тратить» текущего.
func TestCompute_PreviousWindowDoesNotLeak(t *testing.T) {
	ws := BuildWindows([]Event{inc("old", Date{2026, 9, 5}, StateDone, 5_000_000), inc("in1", Date{2026, 10, 5}, StateDone, 9_800_000)})
	base := mustCompute(t, in(ws, goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 0))
	oldSpend := append(goldenEvents(), payDone("old-spend", "Старая трата", 100_000, CurRUB, Date{2026, 9, 10}),
		payPlanned("old-plan", "Старый платёж", 100_000, CurRUB, Date{2026, 9, 12}, "oldrule"))
	got := mustCompute(t, in(ws, oldSpend, CurRUB, r25, Date{2026, 10, 14}, 0))
	if got.CanSpend != base.CanSpend || got.CanSpend != 45_542 {
		t.Fatalf("прошлое окно повлияло: %d vs %d", got.CanSpend, base.CanSpend)
	}
}

func TestCompute_NoWindows(t *testing.T) {
	v := mustCompute(t, in([]Window{}, goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.State != StateNone || v.Window != nil || v.CanSpend != 0 || v.PerDay != nil {
		t.Fatalf("без окон: %+v", v)
	}
}

func TestCompute_Errors(t *testing.T) {
	ws := goldenWindows()
	base := func() ComputeInput { return in(ws, goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 0) }
	cases := []struct {
		name string
		mod  func(*ComputeInput)
		want error
	}{
		{"резерв 101", func(i *ComputeInput) { i.ReservePct = 101 }, ErrBadReservePct},
		{"резерв -1", func(i *ComputeInput) { i.ReservePct = -1 }, ErrBadReservePct},
		{"валюта показа USD", func(i *ComputeInput) { i.Cur = "USD" }, ErrUnsupportedCurrency},
		{"нет курса при ฿-событиях", func(i *ComputeInput) { i.Rate = nil }, ErrNoRate},
		{"курс 0", func(i *ComputeInput) { i.Rate = &Rate{0} }, ErrBadRate},
		{"курс < 0", func(i *ComputeInput) { i.Rate = &Rate{-1} }, ErrBadRate},
		{"поступление в Events", func(i *ComputeInput) { i.Events = append(i.Events, inc("x", Date{2026, 10, 6}, StateDone, 100)) }, ErrInvalidEvent},
		{"невалидное событие", func(i *ComputeInput) {
			i.Events = append(i.Events, Event{Key: "bad", Kind: KindPayment, State: StateDone, Date: &Date{2026, 10, 6}})
		}, ErrInvalidEvent},
		{"повтор Key", func(i *ComputeInput) { i.Events = append(i.Events, i.Events[0]) }, ErrInvalidEvent},
		{"план и факт одного срока", func(i *ComputeInput) {
			f := payDone("fact", "Аренда", 1_800_000, CurTHB, Date{2026, 10, 10})
			f.RuleID, f.ScheduledFor = "rent", &Date{2026, 10, 10}
			i.Events = append(i.Events, f)
		}, ErrDuplicateTerm},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			i := base()
			c.mod(&i)
			v, err := Compute(i)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, ждали %v", err, c.want)
			}
			if v.State != "" || v.Window != nil || v.CanSpend != 0 {
				t.Fatalf("при ошибке View должен быть пустым: %+v", v)
			}
		})
	}
}

// Совпадение валют курса не требует (AC-4.4): только ₽-события и показ в ₽ — без курса.
func TestCompute_NoRateNeededWhenSameCurrency(t *testing.T) {
	ev := []Event{payPlanned("a", "Подписка", 170_000, CurRUB, Date{2026, 10, 10}, "sub")}
	if v, err := Compute(in(goldenWindows(), ev, CurRUB, nil, Date{2026, 10, 14}, 0)); err != nil || v.Payments != 1_700 {
		t.Fatalf("payments=%d err=%v", v.Payments, err)
	}
}

// Курс ≤ 0 — ошибка всегда, даже когда перевод между валютами не нужен
// (только ₽-события и показ в ₽): это сигнал битых данных, а не повод молчать.
func TestCompute_BadRateRejectedEvenWithoutConversion(t *testing.T) {
	ev := []Event{payPlanned("a", "Подписка", 170_000, CurRUB, Date{2026, 10, 10}, "sub")}
	for _, bad := range []int64{0, -2_500_000} {
		if _, err := Compute(in(goldenWindows(), ev, CurRUB, &Rate{bad}, Date{2026, 10, 14}, 0)); !errors.Is(err, ErrBadRate) {
			t.Fatalf("курс %d: err=%v, ждали ErrBadRate", bad, err)
		}
		// Без окон переводить нечего, но битый курс всё равно сигнал: проверка в начале Compute.
		if _, err := Compute(in([]Window{}, nil, CurRUB, &Rate{bad}, Date{2026, 10, 14}, 0)); !errors.Is(err, ErrBadRate) {
			t.Fatalf("курс %d без окон: err=%v, ждали ErrBadRate", bad, err)
		}
	}
}

// Находка ревью: без поступлений платежи «без даты», дальние и просроченные не должны пропадать.
func TestCompute_NoWindows_KeepsUndatedLaterAndOverdue(t *testing.T) {
	d := func(y, m, day int) *Date { x := Date{y, m, day}; return &x }
	ev := []Event{
		planAmount("u1", "Без даты", 100_000, nil, nil),
		planNoAmount("u2", "Права", nil, nil),
		planAmount("miss", "Окно ещё не создано", 100_000, nil, &Date{2026, 12, 1}),
		planAmount("far", "Декабрь", 300_000, d(2026, 12, 10), nil),
		planAmount("over", "Давно", 50_000, d(2026, 10, 2), nil),
	}
	v := mustCompute(t, in([]Window{}, ev, CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.State != StateNone || v.Window != nil || v.CanSpend != 0 || v.PerDay != nil {
		t.Fatalf("расчёта без окон быть не должно: %+v", v)
	}
	if v.Undated.Count != 3 || v.Undated.PricedCount != 2 || v.Undated.Sum != 2_000 {
		t.Fatalf("undated=%+v; ждали 3 пункта (2 с суммой, 2 000 ₽)", v.Undated)
	}
	missing := false
	for _, r := range v.Undated.Items {
		if r.Key == "miss" && r.WindowMissing {
			missing = true
		}
	}
	if !missing {
		t.Fatalf("«окно ещё не создано» не помечено: %+v", v.Undated.Items)
	}
	if len(v.Later) != 1 || v.Later[0].Month != 12 || v.Later[0].Count != 1 {
		t.Fatalf("later=%+v", v.Later)
	}
	if len(v.Timeline) != 1 || v.Timeline[0].Key != "over" || v.Timeline[0].OverdueDays != 12 {
		t.Fatalf("просроченный должен быть виден: %+v", v.Timeline)
	}
}

// Находка ревью: контракт окон проверяется, а не принимается на веру.
func TestCompute_RejectsBrokenWindows(t *testing.T) {
	w := func(start Date, ev ...Event) Window { return Window{Start: start, Incomes: ev} }
	a, b := inc("a", Date{2026, 10, 5}, StateDone, 100), inc("b", Date{2026, 10, 20}, StateDone, 100)
	cases := []struct {
		name string
		ws   []Window
	}{
		{"не по порядку", []Window{w(Date{2026, 10, 20}, b), w(Date{2026, 10, 5}, a)}},
		{"пересекаются", []Window{w(Date{2026, 10, 5}, a), w(Date{2026, 10, 18}, inc("c", Date{2026, 10, 18}, StateDone, 100))}},
		{"несуществующий старт", []Window{w(Date{2026, 2, 30})}},
		{"поступление вне окна", []Window{w(Date{2026, 10, 5}, b)}},
		{"платёж среди поступлений", []Window{w(Date{2026, 10, 5}, payDone("p", "Платёж", 100, CurRUB, Date{2026, 10, 5}))}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := Compute(in(c.ws, nil, CurRUB, r25, Date{2026, 10, 14}, 0))
			if !errors.Is(err, ErrInvalidEvent) || v.State != "" {
				t.Fatalf("err=%v state=%q; ждали ErrInvalidEvent и пустой View", err, v.State)
			}
		})
	}
	// Окна ровно впритык (End+1 = Start) — это нормально.
	ok := []Window{w(Date{2026, 10, 5}, a), w(Date{2026, 10, 19}, inc("c", Date{2026, 10, 19}, StateDone, 100))}
	if _, err := Compute(in(ok, nil, CurRUB, r25, Date{2026, 10, 14}, 0)); err != nil {
		t.Fatalf("соседние окна без пересечения должны приниматься: %v", err)
	}
}

// Находка ревью: охранные проверки переполнения должны быть покрыты тестом.
func TestCompute_OverflowGuards(t *testing.T) {
	const maxMinor = int64(1<<63 - 1)
	big := func(key string, d Date) Event { return inc(key, d, StateDone, maxMinor) }
	// Два гигантских поступления: сумма ещё помещается, а резерв (×100) — нет.
	ws := BuildWindows([]Event{big("a", Date{2026, 10, 5}), big("b", Date{2026, 10, 5})})
	if _, err := Compute(in(ws, nil, CurRUB, r25, Date{2026, 10, 14}, 10)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("резерв от гигантской суммы: err=%v, ждали ErrOverflow", err)
	}
	// Сто с лишним гигантских поступлений: сама сумма не помещается в int64.
	var many []Event
	for i := 0; i < 105; i++ {
		many = append(many, big("k"+pad(i, 3), Date{2026, 10, 5}))
	}
	if _, err := Compute(in(BuildWindows(many), nil, CurRUB, r25, Date{2026, 10, 14}, 0)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("сумма поступлений: err=%v, ждали ErrOverflow", err)
	}
}
