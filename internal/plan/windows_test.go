package plan

import (
	"testing"

	"pgregory.net/rapid"
)

func inc(key string, d Date, state State, minor int64) Event {
	return Event{Key: key, Kind: KindIncome, State: state, Name: "Поступление " + key,
		Amount: &Money{minor, CurRUB}, Date: &d, Source: SourceTransaction}
}

func TestBuildWindows_ThreeDayBoundaryPair(t *testing.T) {
	// AC-1.1/1.3: поступление 05.10 → окно 05.10–18.10 (14 дней включительно).
	ws := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 9_800_000)})
	if len(ws) != 1 || ws[0].Start != (Date{2026, 10, 5}) || ws[0].End() != (Date{2026, 10, 18}) {
		t.Fatalf("окно: %+v, ждали 05.10–18.10", ws)
	}
	// Пара границы: 18.10 ВХОДИТ, 19.10 открывает новое окно.
	in18 := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 1), inc("b", Date{2026, 10, 18}, StateDone, 1)})
	if len(in18) != 1 || len(in18[0].Incomes) != 2 {
		t.Fatalf("18.10 должно войти в окно 05.10: %+v", in18)
	}
	out19 := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 1), inc("b", Date{2026, 10, 19}, StateDone, 1)})
	if len(out19) != 2 || out19[1].Start != (Date{2026, 10, 19}) || out19[1].End() != (Date{2026, 11, 1}) {
		t.Fatalf("19.10 должно открыть окно 19.10–01.11: %+v", out19)
	}
}

func TestBuildWindows_SecondIncomeInsideWindow(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 1), inc("b", Date{2026, 10, 12}, StateDone, 1)})
	if len(ws) != 1 || len(ws[0].Incomes) != 2 || ws[0].Start != (Date{2026, 10, 5}) {
		t.Fatalf("второе поступление внутри окна не сдвигает его: %+v", ws)
	}
}

// AC-1.2: два поступления в один день — одно окно; результат не зависит от порядка.
func TestBuildWindows_SameDayOneWindow(t *testing.T) {
	a := inc("a", Date{2026, 10, 5}, StateDone, 1)
	b := inc("b", Date{2026, 10, 5}, StateDone, 2)
	ws1 := BuildWindows([]Event{a, b})
	ws2 := BuildWindows([]Event{b, a})
	if len(ws1) != 1 || len(ws2) != 1 || len(ws1[0].Incomes) != 2 {
		t.Fatalf("ws1=%+v ws2=%+v", ws1, ws2)
	}
	if ws1[0].Incomes[0].Key != ws2[0].Incomes[0].Key {
		t.Fatalf("порядок входа повлиял на результат: %s vs %s", ws1[0].Incomes[0].Key, ws2[0].Incomes[0].Key)
	}
}

// AC-1.4: 05.10, 19.10, 25.10 → два окна; третье поступление входит во второе.
func TestBuildWindows_Chain(t *testing.T) {
	ws := BuildWindows([]Event{
		inc("a", Date{2026, 10, 5}, StateDone, 1),
		inc("b", Date{2026, 10, 19}, StateDone, 1),
		inc("c", Date{2026, 10, 25}, StateDone, 1),
	})
	if len(ws) != 2 || len(ws[1].Incomes) != 2 || ws[1].Start != (Date{2026, 10, 19}) {
		t.Fatalf("цепочка: %+v", ws)
	}
}

func TestBuildWindows_FactBeforePlanOnSameDay(t *testing.T) {
	plan := inc("p", Date{2026, 10, 5}, StatePlanned, 1)
	fact := inc("z", Date{2026, 10, 5}, StateDone, 1)
	ws := BuildWindows([]Event{plan, fact})
	if ws[0].Incomes[0].State != StateDone {
		t.Fatalf("в день поступления факт должен стоять раньше плана: %+v", ws[0].Incomes)
	}
	if ws[0].AnchorPlanned() {
		t.Fatal("якорь окна с фактом в первый день не может быть «ожидаемым»")
	}
}

func TestBuildWindows_EmptyAndSkipped(t *testing.T) {
	if ws := BuildWindows(nil); ws == nil || len(ws) != 0 {
		t.Fatalf("пустой вход должен дать пустой НЕ-nil срез, а не %#v", ws)
	}
	pay := Event{Key: "p", Kind: KindPayment, State: StatePlanned, Date: &Date{2026, 10, 5}}
	noDate := Event{Key: "n", Kind: KindIncome, State: StateDone}
	if ws := BuildWindows([]Event{pay, noDate}); len(ws) != 0 {
		t.Fatalf("платёж и поступление без даты окон не открывают: %+v", ws)
	}
}

// 2024-02: окно 20.02–04.03 не теряет и не дублирует 29.02 (AC-1.11).
func TestBuildWindows_LeapFebruary(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", Date{2024, 2, 20}, StateDone, 1)})
	if ws[0].End() != (Date{2024, 3, 4}) {
		t.Fatalf("End = %v, ждали 04.03.2024", ws[0].End())
	}
	days := 0
	for d := ws[0].Start; d.Compare(ws[0].End()) <= 0; d = d.AddDays(1) {
		days++
	}
	if days != 14 {
		t.Fatalf("в окне %d дней, ждали 14", days)
	}
}

func TestAnchorPlanned(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", Date{2026, 10, 20}, StatePlanned, 1)})
	if !ws[0].AnchorPlanned() {
		t.Fatal("окно, открытое ожидаемым поступлением, должно быть AnchorPlanned")
	}
}

// AC-1.5: результат не зависит от порядка входа (свойство, rapid).
func TestProp_BuildWindows_OrderIndependent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 12).Draw(t, "число поступлений")
		var evs []Event
		for i := 0; i < n; i++ {
			day := rapid.IntRange(0, 120).Draw(t, "день")
			st := rapid.SampledFrom([]State{StateDone, StatePlanned}).Draw(t, "состояние")
			evs = append(evs, inc("k"+pad(i, 2), Date{2026, 1, 1}.AddDays(day), st, int64(i+1)))
		}
		base := BuildWindows(evs)
		shuffled := rapid.Permutation(evs).Draw(t, "перестановка")
		got := BuildWindows(shuffled)
		if len(base) != len(got) {
			t.Fatalf("число окон %d vs %d", len(base), len(got))
		}
		for i := range base {
			if base[i].Start != got[i].Start || len(base[i].Incomes) != len(got[i].Incomes) {
				t.Fatalf("окно %d: %v/%d vs %v/%d", i, base[i].Start, len(base[i].Incomes), got[i].Start, len(got[i].Incomes))
			}
			for j := range base[i].Incomes {
				if base[i].Incomes[j].Key != got[i].Incomes[j].Key {
					t.Fatalf("порядок поступлений окна %d отличается", i)
				}
			}
		}
		// Инвариант окон: каждое поступление лежит в [Start, End] своего окна, окна не пересекаются.
		for i, w := range base {
			for _, e := range w.Incomes {
				if e.Date.Compare(w.Start) < 0 || e.Date.Compare(w.End()) > 0 {
					t.Fatalf("поступление %v вне окна %v–%v", *e.Date, w.Start, w.End())
				}
			}
			if i > 0 && w.Start.Compare(base[i-1].End()) <= 0 {
				t.Fatalf("окна пересекаются: %v и %v", base[i-1].End(), w.Start)
			}
		}
	})
}

func TestLocate_States(t *testing.T) {
	ws := BuildWindows([]Event{
		inc("a", Date{2026, 10, 5}, StateDone, 1),  // 05.10–18.10
		inc("b", Date{2026, 10, 20}, StateDone, 1), // 20.10–02.11
	})
	cases := []struct {
		name  string
		today Date
		kind  StateKind
		idx   int
		left  int
	}{
		{"за день до первого окна", Date{2026, 10, 4}, StateUpcoming, 0, 14},
		{"первый день окна", Date{2026, 10, 5}, StateCurrent, 0, 14},
		{"сегодня 14.10", Date{2026, 10, 14}, StateCurrent, 0, 5},
		{"последний день окна", Date{2026, 10, 18}, StateCurrent, 0, 1},
		{"зазор 19.10", Date{2026, 10, 19}, StateBetween, 0, 0},
		{"первый день второго окна", Date{2026, 10, 20}, StateCurrent, 1, 14},
		{"после последнего окна", Date{2026, 11, 3}, StateEnded, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Locate(ws, c.today)
			if got.Kind != c.kind || got.Index != c.idx || got.DaysLeft != c.left {
				t.Fatalf("Locate(%v) = %+v; ждали {%s %d %d}", c.today, got, c.kind, c.idx, c.left)
			}
		})
	}
}

// AC-1.8: для одного окна today ∈ {start−1, start, end, end+1} → daysLeft {14, 14, 1, 0}.
func TestLocate_DaysLeftTable(t *testing.T) {
	ws := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 1)})
	want := map[Date]int{{2026, 10, 4}: 14, {2026, 10, 5}: 14, {2026, 10, 18}: 1, {2026, 10, 19}: 0}
	for today, left := range want {
		if got := Locate(ws, today).DaysLeft; got != left {
			t.Errorf("today=%v: daysLeft=%d, ждали %d", today, got, left)
		}
	}
}

func TestLocate_NoWindows(t *testing.T) {
	got := Locate([]Window{}, Date{2026, 10, 14})
	if got.Kind != StateNone || got.Index != -1 {
		t.Fatalf("Locate без окон: %+v", got)
	}
}

func TestResolveWindow(t *testing.T) {
	ws := BuildWindows([]Event{
		inc("a", Date{2026, 10, 5}, StateDone, 1),  // 05.10–18.10
		inc("b", Date{2026, 10, 20}, StateDone, 1), // 20.10–02.11
	})
	cases := []struct {
		ws  Date
		idx int
		ok  bool
	}{
		{Date{2026, 10, 5}, 0, true},
		{Date{2026, 10, 18}, 0, true}, // конец окна включительно
		{Date{2026, 10, 19}, 1, true}, // зазор → следующее окно (первое с end ≥ даты)
		{Date{2026, 10, 25}, 1, true},
		{Date{2026, 11, 3}, -1, false}, // окна ещё нет
		{Date{2026, 9, 1}, 0, true},    // раньше всех → первое
	}
	for _, c := range cases {
		idx, ok := ResolveWindow(ws, c.ws)
		if idx != c.idx || ok != c.ok {
			t.Errorf("ResolveWindow(%v) = %d, %v; ждали %d, %v", c.ws, idx, ok, c.idx, c.ok)
		}
	}
}
