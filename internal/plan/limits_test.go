package plan

import (
	"errors"
	"math"
	"testing"

	"pgregory.net/rapid"
)

// Суммы helper-ов — в рублях (View тоже в рублях).
// Окно golden: 05.10–18.10, приход 98 000 ₽ (9_800_000 коп.), сегодня 14.10, показ в ₽.

var limToday = Date{2026, 10, 14}

func catDone(key, cat string, minor int64, d Date) Event {
	e := payDone(key, key, minor*100, CurRUB, d)
	e.Category = cat
	return e
}

func catPlanned(key, cat string, minor int64, d Date) Event {
	e := payPlanned(key, key, minor*100, CurRUB, d, "")
	e.Category = cat
	return e
}

func lim(cat string, minor int64, repeat bool) Limit {
	return Limit{ID: "l-" + cat, Category: cat, Amount: Money{minor * 100, CurRUB}, Repeat: repeat, WindowStart: Date{2026, 10, 5}}
}

type fataler interface {
	Helper()
	Fatalf(string, ...any)
}

func limCompute(t fataler, ev []Event, ls ...Limit) View {
	t.Helper()
	i := in(goldenWindows(), ev, CurRUB, r25, limToday, 0)
	i.Limits = ls
	v, err := Compute(i)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return v
}

func TestLimits_NoSpend_HoldsFullAmount(t *testing.T) {
	base := limCompute(t, nil)
	v := limCompute(t, nil, lim("Еда", 1_000_00, false))
	if v.LimitHold != 100_000 || v.CanSpend != base.CanSpend-100_000 {
		t.Fatalf("hold=%d canSpend=%d (без лимита %d)", v.LimitHold, v.CanSpend, base.CanSpend)
	}
	if len(v.Limits) != 1 || v.Limits[0].Left != 100_000 || v.Limits[0].LeftPct != 100 || v.Limits[0].Over != 0 {
		t.Fatalf("limits=%+v", v.Limits)
	}
}

// Трата внутри лимита не двигает CanSpend: лимит уже вычтен.
func TestLimits_SpendInsideLimit_KeepsCanSpend(t *testing.T) {
	v0 := limCompute(t, nil, lim("Еда", 100_000, false))
	v1 := limCompute(t, []Event{catDone("a", "Еда", 30_000, Date{2026, 10, 12})}, lim("Еда", 100_000, false))
	if v1.CanSpend != v0.CanSpend {
		t.Fatalf("canSpend %d → %d, должно остаться", v0.CanSpend, v1.CanSpend)
	}
	if v1.LimitHold != 70_000 || v1.Limits[0].Spent != 30_000 || v1.Limits[0].LeftPct != 70 {
		t.Fatalf("hold=%d limits=%+v", v1.LimitHold, v1.Limits)
	}
}

func TestLimits_Overrun_ReducesFreeAndShowsOver(t *testing.T) {
	base := limCompute(t, []Event{catDone("a", "Еда", 130_000, Date{2026, 10, 12})})
	v := limCompute(t, []Event{catDone("a", "Еда", 130_000, Date{2026, 10, 12})}, lim("Еда", 100_000, false))
	l := v.Limits[0]
	if l.Over != 30_000 || l.Left != 0 || v.LimitHold != 0 || !l.Attention {
		t.Fatalf("limit=%+v hold=%d", l, v.LimitHold)
	}
	if v.CanSpend != base.CanSpend {
		t.Fatalf("canSpend=%d, без лимита %d: перерасход не должен вычитаться дважды", v.CanSpend, base.CanSpend)
	}
}

func TestLimits_SeveralCategories_SumsLeft(t *testing.T) {
	ev := []Event{catDone("a", "Еда", 40_000, Date{2026, 10, 12}), catDone("b", "Кафе", 90_000, Date{2026, 10, 13})}
	v := limCompute(t, ev, lim("Еда", 100_000, false), lim("Кафе", 50_000, false), lim("Такси", 20_000, false))
	if v.LimitHold != 60_000+0+20_000 {
		t.Fatalf("hold=%d", v.LimitHold)
	}
	if len(v.Limits) != 3 {
		t.Fatalf("limits=%+v", v.Limits)
	}
}

func TestLimits_RecurringFactDoesNotEatLimit(t *testing.T) {
	f := catDone("rent", "Еда", 50_000, Date{2026, 10, 10})
	f.RuleID, f.ScheduledFor = "r1", &Date{2026, 10, 10}
	v := limCompute(t, []Event{f}, lim("Еда", 100_000, false))
	if v.Limits[0].Spent != 0 || v.LimitHold != 100_000 {
		t.Fatalf("limit=%+v hold=%d", v.Limits[0], v.LimitHold)
	}
}

func TestLimits_UnpaidPlannedEatsLimit(t *testing.T) {
	ev := []Event{catPlanned("p", "Еда", 40_000, Date{2026, 10, 16})}
	base := limCompute(t, ev)
	v := limCompute(t, ev, lim("Еда", 100_000, false))
	if v.Limits[0].Planned != 40_000 || v.LimitHold != 60_000 {
		t.Fatalf("limit=%+v hold=%d", v.Limits[0], v.LimitHold)
	}
	// платёж уже вычтен как Payments, лимит держит только остаток после него
	if v.CanSpend != base.CanSpend-60_000 {
		t.Fatalf("canSpend=%d base=%d", v.CanSpend, base.CanSpend)
	}
}

func TestLimits_SpendOutsideWindowIgnored(t *testing.T) {
	ev := []Event{catDone("old", "Еда", 90_000, Date{2026, 10, 1}), catDone("far", "Еда", 90_000, Date{2026, 10, 25})}
	v := limCompute(t, ev, lim("Еда", 100_000, false))
	if v.Limits[0].Spent != 0 {
		t.Fatalf("spent=%d: траты вне окна лимит не едят", v.Limits[0].Spent)
	}
}

func TestLimits_ExceedsFree(t *testing.T) {
	v := limCompute(t, nil, lim("Еда", 10_000_000, false))
	if v.CanSpend >= 0 || !v.LimitsExceedFree {
		t.Fatalf("canSpend=%d exceed=%v", v.CanSpend, v.LimitsExceedFree)
	}
	if v.PerDay != nil {
		t.Fatalf("perDay=%v при недостаче", *v.PerDay)
	}
	ok := limCompute(t, nil, lim("Еда", 1_000, false))
	if ok.LimitsExceedFree {
		t.Fatal("флаг при небольшом лимите")
	}
}

func TestLimits_CategoryMatchCaseAndSpaces(t *testing.T) {
	v := limCompute(t, []Event{catDone("a", " еДа ", 10_000, Date{2026, 10, 12})}, lim("Еда", 100_000, false))
	if v.Limits[0].Spent != 10_000 {
		t.Fatalf("spent=%d", v.Limits[0].Spent)
	}
	v = limCompute(t, []Event{catDone("a", "", 10_000, Date{2026, 10, 12})}, lim("Еда", 100_000, false))
	if v.Limits[0].Spent != 0 {
		t.Fatal("трата без категории съела лимит")
	}
}

func TestLimits_CurrencyConversion(t *testing.T) {
	// 1000 ฿ при курсе 2,5 = 2500 ₽ (250_000 коп.); трата 100 ฿ = 250 ₽
	l := Limit{ID: "x", Category: "Еда", Amount: Money{100_000, CurTHB}, WindowStart: Date{2026, 10, 5}}
	ev := payDone("a", "a", 10_000, CurTHB, Date{2026, 10, 12})
	ev.Category = "Еда"
	v := limCompute(t, []Event{ev}, l)
	if v.Limits[0].Amount != 2_500 || v.Limits[0].Spent != 250 || v.LimitHold != 2_250 {
		t.Fatalf("limit=%+v hold=%d", v.Limits[0], v.LimitHold)
	}
}

func TestLimits_Applicability(t *testing.T) {
	// два окна: 05.10 и 25.10
	ws := BuildWindows([]Event{inc("in1", Date{2026, 10, 5}, StateDone, 9_800_000), inc("in2", Date{2026, 10, 25}, StateDone, 9_800_000)})
	cases := []struct {
		name  string
		l     Limit
		today Date
		want  int
	}{
		{"разовый в своём окне", Limit{Category: "Еда", Amount: Money{1, CurRUB}, WindowStart: Date{2026, 10, 5}}, Date{2026, 10, 14}, 1},
		{"разовый в чужом окне", Limit{Category: "Еда", Amount: Money{1, CurRUB}, WindowStart: Date{2026, 10, 5}}, Date{2026, 10, 26}, 0},
		{"повтор в следующих окнах", Limit{Category: "Еда", Amount: Money{1, CurRUB}, Repeat: true, WindowStart: Date{2026, 10, 5}}, Date{2026, 10, 26}, 1},
		{"повтор не действует до старта", Limit{Category: "Еда", Amount: Money{1, CurRUB}, Repeat: true, WindowStart: Date{2026, 10, 25}}, Date{2026, 10, 14}, 0},
	}
	for _, c := range cases {
		i := in(ws, nil, CurRUB, r25, c.today, 0)
		i.Limits = []Limit{c.l}
		if got := len(mustCompute(t, i).Limits); got != c.want {
			t.Errorf("%s: лимитов %d, ждали %d", c.name, got, c.want)
		}
	}
}

func TestLimits_OneOffOverridesRepeat(t *testing.T) {
	a, b := lim("Еда", 100_000, true), lim("Еда", 50_000, false)
	for _, ls := range [][]Limit{{a, b}, {b, a}} {
		v := limCompute(t, nil, ls...)
		if len(v.Limits) != 1 || v.Limits[0].Amount != 50_000 {
			t.Fatalf("limits=%+v: разовый должен перекрыть повтор независимо от порядка", v.Limits)
		}
	}
}

func TestLimits_NoIncome_NoLimits(t *testing.T) {
	i := in(nil, nil, CurRUB, r25, limToday, 0)
	i.Limits = []Limit{lim("Еда", 100, false)}
	v := mustCompute(t, i)
	if len(v.Limits) != 0 || v.LimitHold != 0 {
		t.Fatalf("limits=%+v", v.Limits)
	}
}

func TestLimits_Overflow(t *testing.T) {
	i := in(goldenWindows(), nil, CurRUB, r25, limToday, 0)
	for n := range 120 { // каждый лимит ≈ 9,2e16 ₽ — вместе не помещаются в int64
		i.Limits = append(i.Limits, Limit{Category: "к" + pad(n, 3), Amount: Money{math.MaxInt64 - 1, CurRUB}, WindowStart: Date{2026, 10, 5}})
	}
	if _, err := Compute(i); !errors.Is(err, ErrOverflow) {
		t.Fatalf("err=%v, ждали ErrOverflow", err)
	}
}

func TestLimits_Attention(t *testing.T) {
	// остаток 20% → внимание; 90% и давняя трата → нет; свежая трата → да
	ev := []Event{catDone("a", "Еда", 80_000, Date{2026, 10, 6}), catDone("b", "Кафе", 10_000, Date{2026, 10, 7}), catDone("c", "Такси", 10_000, Date{2026, 10, 13})}
	v := limCompute(t, ev, lim("Еда", 100_000, false), lim("Кафе", 100_000, false), lim("Такси", 100_000, false))
	got := map[string]bool{}
	for _, l := range v.Limits {
		got[l.Category] = l.Attention
	}
	if !got["Еда"] || got["Кафе"] || !got["Такси"] {
		t.Fatalf("attention=%v", got)
	}
}

func TestNewLimit_Validate(t *testing.T) {
	ok := NewLimit{Category: "Еда", Amount: Money{100, CurTHB}, WindowStart: Date{2026, 10, 5}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*NewLimit){
		"пустая категория": func(n *NewLimit) { n.Category = "  " },
		"без категории":    func(n *NewLimit) { n.Category = " без КАТЕГОРИИ " },
		"сумма 0":          func(n *NewLimit) { n.Amount.Minor = 0 },
		"валюта":           func(n *NewLimit) { n.Amount.Cur = "USD" },
		"огромная сумма":   func(n *NewLimit) { n.Amount.Minor = maxAmountMinor + 1 },
		"дата":             func(n *NewLimit) { n.WindowStart = Date{2026, 13, 1} },
	}
	for name, mut := range cases {
		n := ok
		mut(&n)
		if n.Validate() == nil {
			t.Errorf("%s: принято", name)
		}
	}
}

func TestLimits_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		limit := int64(rapid.IntRange(1, 5_000_000).Draw(t, "лимит"))
		spend := int64(rapid.IntRange(1, 5_000_000).Draw(t, "трата"))
		repeat := rapid.Bool().Draw(t, "повтор")
		ev := []Event{catDone("a", "Еда", spend, Date{2026, 10, 12})}
		base := limCompute(t, ev)
		v := limCompute(t, ev, lim("Еда", limit, repeat))
		if v.LimitHold < 0 {
			t.Fatalf("hold=%d", v.LimitHold)
		}
		if v.CanSpend > base.CanSpend {
			t.Fatalf("лимит увеличил CanSpend: %d > %d", v.CanSpend, base.CanSpend)
		}
		if want := base.CanSpend - max(limit-spend, 0); v.CanSpend != want {
			t.Fatalf("canSpend=%d, ждали %d (лимит %d, трата %d)", v.CanSpend, want, limit, spend)
		}
		// трата внутри лимита не двигает CanSpend относительно «лимит без траты»
		if spend <= limit {
			free := limCompute(t, nil, lim("Еда", limit, repeat))
			// без траты свободное выше на spend (трата — в Spent), с лимитом разницы нет
			if v.CanSpend != free.CanSpend {
				t.Fatalf("трата внутри лимита сдвинула CanSpend: %d vs %d", v.CanSpend, free.CanSpend)
			}
		}
	})
}
