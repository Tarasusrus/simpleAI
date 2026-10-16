package plan

import (
	"errors"
	"testing"
)

func rule(anchor Date, n int, unit Unit) Rule {
	return Rule{Anchor: anchor, N: n, Unit: unit, Amount: Money{100, CurRUB}}
}

func dates(t *testing.T, r Rule, from, to Date) []Date {
	t.Helper()
	got, err := Occurrences(r, from, to)
	if err != nil {
		t.Fatalf("Occurrences: %v", err)
	}
	return got
}

func eq(a, b []Date) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AC-2.3: таблица дословно. Срок n = якорь + n·N с клампингом, а не цепочка.
func TestOccurrences_AC23Table(t *testing.T) {
	cases := []struct {
		name string
		r    Rule
		to   Date
		want []Date
	}{
		{"каждые 10 дней", rule(Date{2026, 10, 22}, 10, UnitDay), Date{2026, 11, 11},
			[]Date{{2026, 10, 22}, {2026, 11, 1}, {2026, 11, 11}}},
		{"каждые 2 недели", rule(Date{2026, 10, 5}, 2, UnitWeek), Date{2026, 11, 2},
			[]Date{{2026, 10, 5}, {2026, 10, 19}, {2026, 11, 2}}},
		{"каждый месяц с 31.01.2026 (без дрейфа)", rule(Date{2026, 1, 31}, 1, UnitMonth), Date{2026, 4, 30},
			[]Date{{2026, 1, 31}, {2026, 2, 28}, {2026, 3, 31}, {2026, 4, 30}}},
		{"каждый месяц с 31.01.2024 (високосный)", rule(Date{2024, 1, 31}, 1, UnitMonth), Date{2024, 3, 31},
			[]Date{{2024, 1, 31}, {2024, 2, 29}, {2024, 3, 31}}},
		{"каждый год с 29.02.2024", rule(Date{2024, 2, 29}, 1, UnitYear), Date{2028, 2, 29},
			[]Date{{2024, 2, 29}, {2025, 2, 28}, {2026, 2, 28}, {2027, 2, 28}, {2028, 2, 29}}},
		{"каждые 6 месяцев с 31.08.2026", rule(Date{2026, 8, 31}, 6, UnitMonth), Date{2027, 8, 31},
			[]Date{{2026, 8, 31}, {2027, 2, 28}, {2027, 8, 31}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dates(t, c.r, c.r.Anchor, c.to); !eq(got, c.want) {
				t.Fatalf("Occurrences = %v\nждали       %v", got, c.want)
			}
		})
	}
}

func TestOccurrences_FromAndToAreInclusive(t *testing.T) {
	r := rule(Date{2026, 10, 22}, 10, UnitDay) // 22.10, 01.11, 11.11, 21.11
	want := []Date{{2026, 11, 1}, {2026, 11, 11}}
	if got := dates(t, r, Date{2026, 11, 1}, Date{2026, 11, 11}); !eq(got, want) {
		t.Fatalf("границы включительно: %v, ждали %v", got, want)
	}
	if got := dates(t, r, Date{2026, 11, 2}, Date{2026, 11, 10}); len(got) != 0 {
		t.Fatalf("между сроками должно быть пусто: %v", got)
	}
	if got := dates(t, r, Date{2026, 11, 11}, Date{2026, 11, 1}); len(got) != 0 {
		t.Fatalf("from > to: %v", got)
	}
	// from раньше якоря — начинаем с якоря.
	if got := dates(t, r, Date{2026, 1, 1}, Date{2026, 10, 22}); !eq(got, []Date{{2026, 10, 22}}) {
		t.Fatalf("from до якоря: %v", got)
	}
}

func TestOccurrences_UntilIsInclusive_Pair(t *testing.T) {
	r := rule(Date{2026, 10, 22}, 10, UnitDay)
	r.Until = &Date{2026, 11, 11}
	if got := dates(t, r, r.Anchor, Date{2027, 1, 1}); !eq(got, []Date{{2026, 10, 22}, {2026, 11, 1}, {2026, 11, 11}}) {
		t.Fatalf("until в день срока включается: %v", got)
	}
	r.Until = &Date{2026, 11, 10}
	if got := dates(t, r, r.Anchor, Date{2027, 1, 1}); !eq(got, []Date{{2026, 10, 22}, {2026, 11, 1}}) {
		t.Fatalf("until за день до срока исключает его: %v", got)
	}
	r.Until = &Date{2026, 10, 1} // раньше якоря
	if got := dates(t, r, r.Anchor, Date{2027, 1, 1}); len(got) != 0 {
		t.Fatalf("until < якоря → пусто без паники: %v", got)
	}
}

func TestOccurrences_Cap1000(t *testing.T) {
	r := rule(Date{2020, 1, 1}, 1, UnitDay)
	got, err := Occurrences(r, Date{2020, 1, 1}, Date{2026, 12, 31})
	if !errors.Is(err, ErrTooMany) || len(got) != 1000 {
		t.Fatalf("len=%d err=%v; ждали 1000 и ErrTooMany", len(got), err)
	}
	if got[0] != (Date{2020, 1, 1}) || got[999] != (Date{2020, 1, 1}).AddDays(999) {
		t.Fatalf("первые 1000 срока: %v … %v", got[0], got[999])
	}
}

func TestNext_StrictlyAfter(t *testing.T) {
	r := rule(Date{2026, 10, 22}, 10, UnitDay)
	cases := []struct{ after, want Date }{
		{Date{2026, 10, 21}, Date{2026, 10, 22}}, // до якоря → якорь
		{Date{2026, 10, 22}, Date{2026, 11, 1}},  // строго после: сам срок не возвращается
		{Date{2026, 11, 10}, Date{2026, 11, 11}},
		{Date{2026, 11, 11}, Date{2026, 11, 21}},
		{Date{2020, 1, 1}, Date{2026, 10, 22}},
	}
	for _, c := range cases {
		got, ok, err := Next(r, c.after)
		if err != nil || !ok || got != c.want {
			t.Errorf("Next(after=%v) = %v, %v, %v; ждали %v", c.after, got, ok, err, c.want)
		}
	}
}

func TestNext_MonthEndNoDrift(t *testing.T) {
	r := rule(Date{2026, 1, 31}, 1, UnitMonth)
	cases := []struct{ after, want Date }{
		{Date{2026, 1, 31}, Date{2026, 2, 28}},
		{Date{2026, 2, 28}, Date{2026, 3, 31}}, // не 28.03: якорь не сползает
		{Date{2026, 3, 31}, Date{2026, 4, 30}},
		{Date{2026, 4, 30}, Date{2026, 5, 31}},
	}
	for _, c := range cases {
		if got, ok, err := Next(r, c.after); err != nil || !ok || got != c.want {
			t.Errorf("Next(after=%v) = %v, %v, %v; ждали %v", c.after, got, ok, err, c.want)
		}
	}
}

// Next не имеет потолка: ежедневное правило с давним якорем считается арифметикой.
func TestNext_NoCeilingForOldAnchor(t *testing.T) {
	r := rule(Date{2020, 1, 1}, 1, UnitDay)
	got, ok, err := Next(r, Date{2026, 10, 14})
	if err != nil || !ok || got != (Date{2026, 10, 15}) {
		t.Fatalf("Next = %v, %v, %v", got, ok, err)
	}
}

func TestNext_UntilPassed(t *testing.T) {
	r := rule(Date{2026, 10, 22}, 10, UnitDay)
	r.Until = &Date{2026, 11, 11}
	if got, ok, err := Next(r, Date{2026, 11, 11}); err != nil || ok {
		t.Fatalf("после последнего срока ok должен быть false: %v, %v, %v", got, ok, err)
	}
	if got, ok, err := Next(r, Date{2026, 11, 1}); err != nil || !ok || got != (Date{2026, 11, 11}) {
		t.Fatalf("последний срок: %v, %v, %v", got, ok, err)
	}
}

func TestDayOfMonth_WinsOverAnchorDay(t *testing.T) {
	d25 := 25
	r := rule(Date{2026, 10, 10}, 1, UnitMonth)
	r.DayOfMonth = &d25
	// Первый срок — 25.10 (не 10.10): день месяца главнее дня якоря.
	if got := dates(t, r, r.Anchor, Date{2026, 12, 31}); !eq(got, []Date{{2026, 10, 25}, {2026, 11, 25}, {2026, 12, 25}}) {
		t.Fatalf("DayOfMonth=25: %v", got)
	}
	if got, ok, err := Next(r, Date{2026, 10, 10}); err != nil || !ok || got != (Date{2026, 10, 25}) {
		t.Fatalf("Next после якоря: %v, %v, %v", got, ok, err)
	}
	// День 31 клампится на коротких месяцах без дрейфа.
	d31 := 31
	r2 := rule(Date{2026, 1, 15}, 1, UnitMonth)
	r2.DayOfMonth = &d31
	if got := dates(t, r2, r2.Anchor, Date{2026, 4, 30}); !eq(got, []Date{{2026, 1, 31}, {2026, 2, 28}, {2026, 3, 31}, {2026, 4, 30}}) {
		t.Fatalf("DayOfMonth=31: %v", got)
	}
	// Срок n=0 раньше якоря пропускается (05.10 < 20.10).
	d5 := 5
	r3 := rule(Date{2026, 10, 20}, 1, UnitMonth)
	r3.DayOfMonth = &d5
	if got := dates(t, r3, r3.Anchor, Date{2026, 12, 31}); !eq(got, []Date{{2026, 11, 5}, {2026, 12, 5}}) {
		t.Fatalf("срок раньше якоря не пропущен: %v", got)
	}
	// from раньше самого этого срока (01.10 < 05.10 < якорь 20.10): срок n=0 = 05.10
	// не должен просочиться, иначе правило сработает ДО своего якоря.
	if got := dates(t, r3, Date{2026, 10, 1}, Date{2026, 12, 31}); !eq(got, []Date{{2026, 11, 5}, {2026, 12, 5}}) {
		t.Fatalf("срок до якоря просочился при from < срока: %v", got)
	}
	if got, ok, err := Next(r3, Date{2026, 9, 30}); err != nil || !ok || got != (Date{2026, 11, 5}) {
		t.Fatalf("Next до срока n=0: %v, %v, %v", got, ok, err)
	}
}

func TestRule_Invalid(t *testing.T) {
	good := func() Rule { return rule(Date{2026, 10, 22}, 10, UnitDay) }
	d := func(n int) *int { return &n }
	cases := []struct {
		name string
		mod  func(*Rule)
	}{
		{"N=0", func(r *Rule) { r.N = 0 }},
		{"N<0", func(r *Rule) { r.N = -3 }},
		{"неизвестная единица", func(r *Rule) { r.Unit = "decade" }},
		{"пустая единица", func(r *Rule) { r.Unit = "" }},
		{"DayOfMonth=0", func(r *Rule) { r.Unit = UnitMonth; r.DayOfMonth = d(0) }},
		{"DayOfMonth=32", func(r *Rule) { r.Unit = UnitMonth; r.DayOfMonth = d(32) }},
		{"DayOfMonth при днях", func(r *Rule) { r.DayOfMonth = d(5) }},
		{"DayOfMonth при неделях", func(r *Rule) { r.Unit = UnitWeek; r.DayOfMonth = d(5) }},
		{"сумма 0", func(r *Rule) { r.Amount.Minor = 0 }},
		{"сумма <0", func(r *Rule) { r.Amount.Minor = -5 }},
		{"валюта USD", func(r *Rule) { r.Amount.Cur = "USD" }},
		{"невалидный якорь", func(r *Rule) { r.Anchor = Date{2026, 2, 30} }},
		{"невалидный until", func(r *Rule) { r.Until = &Date{2026, 13, 1} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := good()
			c.mod(&r)
			if _, err := Occurrences(r, r.Anchor, Date{2027, 1, 1}); !errors.Is(err, ErrBadRule) {
				t.Errorf("Occurrences: %v, ждали ErrBadRule", err)
			}
			if _, _, err := Next(r, r.Anchor); !errors.Is(err, ErrBadRule) {
				t.Errorf("Next: %v, ждали ErrBadRule", err)
			}
		})
	}
}

func rw(id string, kind Kind, r Rule, ex ...Date) RuleWithExceptions {
	return RuleWithExceptions{ID: id, Name: "Правило " + id, Category: "Жильё", Kind: kind, Rule: r, Exceptions: ex}
}

func TestExpand_BuildsPlannedEvents(t *testing.T) {
	r := rule(Date{2026, 10, 10}, 1, UnitMonth)
	r.Amount = Money{1_800_000, CurTHB}
	ev, err := Expand([]RuleWithExceptions{rw("rent", KindPayment, r)}, Date{2026, 10, 1}, Date{2026, 12, 31})
	if err != nil || len(ev) != 3 {
		t.Fatalf("len=%d err=%v", len(ev), err)
	}
	e := ev[0]
	if e.Key != "recurring:rent@2026-10-10" || e.Source != SourceRecurring || e.State != StatePlanned ||
		e.RuleID != "rent" || e.Kind != KindPayment || e.Category != "Жильё" || e.Name != "Правило rent" {
		t.Fatalf("поля события: %+v", e)
	}
	if e.Date == nil || *e.Date != (Date{2026, 10, 10}) || e.ScheduledFor == nil || *e.ScheduledFor != *e.Date {
		t.Fatalf("Date/ScheduledFor: %v / %v", e.Date, e.ScheduledFor)
	}
	if e.Amount == nil || *e.Amount != (Money{1_800_000, CurTHB}) {
		t.Fatalf("сумма: %v", e.Amount)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("развёрнутое событие невалидно: %v", err)
	}
	// Копия суммы: изменение события не меняет правило.
	e.Amount.Minor = 1
	if r.Amount.Minor != 1_800_000 {
		t.Fatal("событие делит память с правилом")
	}
}

func TestExpand_SubtractsExceptions_AnchorUntouched(t *testing.T) {
	r := rule(Date{2026, 1, 31}, 1, UnitMonth)
	ev, err := Expand([]RuleWithExceptions{rw("x", KindPayment, r, Date{2026, 2, 28})}, Date{2026, 1, 1}, Date{2026, 4, 30})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]Date, 0, len(ev))
	for _, e := range ev {
		got = append(got, *e.Date)
	}
	// Пропуск исключает срок; следующие сохраняют день: 31.03, а не 28.03.
	if want := []Date{{2026, 1, 31}, {2026, 3, 31}, {2026, 4, 30}}; !eq(got, want) {
		t.Fatalf("после исключения: %v, ждали %v", got, want)
	}
}

func TestExpand_SortedByDateThenKey(t *testing.T) {
	a := rule(Date{2026, 10, 10}, 1, UnitMonth)
	b := rule(Date{2026, 10, 10}, 1, UnitMonth)
	c := rule(Date{2026, 10, 5}, 1, UnitMonth)
	ev, err := Expand([]RuleWithExceptions{rw("b", KindPayment, b), rw("a", KindPayment, a), rw("c", KindIncome, c)},
		Date{2026, 10, 1}, Date{2026, 10, 31})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{ev[0].Key, ev[1].Key, ev[2].Key}
	want := []string{"recurring:c@2026-10-05", "recurring:a@2026-10-10", "recurring:b@2026-10-10"}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("порядок: %v, ждали %v", keys, want)
		}
	}
	if ev[0].Kind != KindIncome {
		t.Fatal("вид события не взят из правила")
	}
}

func TestExpand_CapAppliesToWholeResult(t *testing.T) {
	r := rule(Date{2020, 1, 1}, 1, UnitDay)
	ev, err := Expand([]RuleWithExceptions{rw("a", KindPayment, r), rw("b", KindPayment, r)}, Date{2020, 1, 1}, Date{2021, 12, 31})
	if !errors.Is(err, ErrTooMany) || len(ev) != 1000 {
		t.Fatalf("len=%d err=%v; ждали 1000 и ErrTooMany", len(ev), err)
	}
}

func TestExpand_RejectsBadRuleAndMeta(t *testing.T) {
	bad := rule(Date{2026, 10, 10}, 0, UnitMonth)
	if _, err := Expand([]RuleWithExceptions{rw("a", KindPayment, bad)}, Date{2026, 1, 1}, Date{2026, 12, 31}); !errors.Is(err, ErrBadRule) {
		t.Fatalf("плохое правило: %v", err)
	}
	ok := rule(Date{2026, 10, 10}, 1, UnitMonth)
	if _, err := Expand([]RuleWithExceptions{rw("", KindPayment, ok)}, Date{2026, 1, 1}, Date{2026, 12, 31}); !errors.Is(err, ErrBadRule) {
		t.Fatalf("пустой ID: %v", err)
	}
	if _, err := Expand([]RuleWithExceptions{rw("a", "x", ok)}, Date{2026, 1, 1}, Date{2026, 12, 31}); !errors.Is(err, ErrBadRule) {
		t.Fatalf("неизвестный вид: %v", err)
	}
}

// Находка ревью: Expand различает ErrTooMany через errors.Is, а не ==. Правило, которое
// само даёт потолок, не должно превращать усечение в отказ всего разворачивания.
func TestExpand_PerRuleCapIsTruncationNotFailure(t *testing.T) {
	r := rule(Date{2020, 1, 1}, 1, UnitDay)
	ev, err := Expand([]RuleWithExceptions{rw("a", KindPayment, r)}, Date{2020, 1, 1}, Date{2026, 12, 31})
	if !errors.Is(err, ErrTooMany) || len(ev) != 1000 {
		t.Fatalf("len=%d err=%v; ждали 1000 событий и ErrTooMany", len(ev), err)
	}
}
