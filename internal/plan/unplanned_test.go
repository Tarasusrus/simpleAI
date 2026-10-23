package plan

import (
	"flag"
	"testing"

	"pgregory.net/rapid"
)

func ruble(n int64) *Money { return &Money{n, CurRUB} }

func TestUnplanned_Basic(t *testing.T) {
	start := Date{2026, 10, 1}
	d := func(n int) *Date { x := start.AddDays(n); return &x }
	inc := Event{Key: "in", Kind: KindIncome, State: StateDone, Amount: ruble(100000), Date: d(0), Source: SourceTransaction}
	pay := func(k, cat string, n int64, dt *Date, rule string) Event {
		e := Event{Key: k, Kind: KindPayment, State: StateDone, Amount: ruble(n), Date: dt, Category: cat, Source: SourceTransaction}
		if rule != "" {
			e.RuleID, e.Source = rule, SourceRecurring
			e.ScheduledFor = dt
		}
		return e
	}
	v, err := Compute(ComputeInput{
		Windows: BuildWindows([]Event{inc}), Today: *d(5), Cur: CurRUB, Rate: &Rate{2_500_000},
		Events: []Event{
			pay("a", "Такси", 30000, d(1), ""),
			pay("b", " такси ", 20000, d(2), ""),   // тот же ключ категории
			pay("c", "Кафе", 90000, d(3), ""),      // больше — выше в списке
			pay("d", "Еда", 500000, d(3), ""),      // у «Еды» есть лимит
			pay("e", "Аренда", 700000, d(4), "r1"), // платёж правила — не «вне плана»
			pay("f", "", 11100, d(4), ""),          // без категории
			pay("g", "Такси", 5000, d(-3), ""),     // вне окна
		},
		Limits: []Limit{{ID: "l1", Category: "Еда", Amount: Money{300000, CurRUB}, WindowStart: start}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []UnplannedRow{{"Кафе", 900}, {"Такси", 500}}
	if len(v.Unplanned) != 2 || v.Unplanned[0] != want[0] || v.Unplanned[1].Spent != want[1].Spent {
		t.Fatalf("Unplanned = %+v, want %+v", v.Unplanned, want)
	}
	if v.UnplannedSpent != 1400 {
		t.Fatalf("UnplannedSpent = %d, want 1400", v.UnplannedSpent)
	}
}

func TestUnplanned_NoWindow(t *testing.T) {
	v, err := Compute(ComputeInput{Today: Date{2026, 10, 1}, Cur: CurRUB})
	if err != nil || v.Unplanned != nil || v.UnplannedSpent != 0 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}

// Свойство: Spent разбит без пересечений на платежи правил, траты без категории,
// траты в категориях с лимитом и UnplannedSpent (ADR-015 §2). Покрытие замеряется
// в той же форме rapid.Check, что и свойство.
func TestProp_Unplanned_PartitionsSpent(t *testing.T) {
	start := Date{2026, 10, 1}
	cats := []string{"Еда", "Такси", "Кафе", "Дом", ""}
	hits := map[string]int{}
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
		inc := Event{Key: "in", Kind: KindIncome, State: StateDone, Amount: ruble(1_000_000), Date: &start, Source: SourceTransaction}
		var evs []Event
		for i := 0; i < rapid.IntRange(0, 12).Draw(t, "число трат"); i++ {
			dt := start.AddDays(rapid.IntRange(-3, 16).Draw(t, "день"))
			cat := rapid.SampledFrom(cats).Draw(t, "категория")
			if cat != "" && rapid.Bool().Draw(t, "пробелы") {
				cat = " " + cat + " "
			}
			cur := rapid.SampledFrom([]Currency{CurRUB, CurTHB}).Draw(t, "валюта")
			e := Event{Key: "e" + pad(i, 2), Kind: KindPayment, State: StateDone, Date: &dt, Category: cat,
				Amount: &Money{int64(rapid.IntRange(100, 9_000_000).Draw(t, "сумма")), cur}, Source: SourceTransaction}
			if rapid.IntRange(0, 4).Draw(t, "правило") == 0 {
				e.RuleID, e.Source, e.ScheduledFor = "r"+pad(i, 2), SourceRecurring, &dt
			}
			evs = append(evs, e)
		}
		var ls []Limit
		for i, c := range cats[:4] {
			switch rapid.IntRange(0, 3).Draw(t, "лимит "+c) {
			case 1:
				ls = append(ls, Limit{ID: "l" + pad(i, 1), Category: c, Amount: Money{5000, CurRUB}, WindowStart: start})
			case 2:
				ls = append(ls, Limit{ID: "l" + pad(i, 1), Category: c, Amount: Money{5000, CurRUB}, Repeat: true, WindowStart: start.AddDays(-14)})
			case 3: // разовый лимит прошлого окна — категорию лимитом не делает
				ls = append(ls, Limit{ID: "l" + pad(i, 1), Category: c, Amount: Money{5000, CurRUB}, WindowStart: start.AddDays(-14)})
			}
		}
		rate := &Rate{int64(rapid.IntRange(1_500_000, 4_000_000).Draw(t, "курс"))}
		cur := rapid.SampledFrom([]Currency{CurRUB, CurTHB}).Draw(t, "показ")
		v, err := Compute(ComputeInput{Windows: BuildWindows([]Event{inc}), Events: evs, Limits: ls,
			Rate: rate, Cur: cur, Today: start.AddDays(7)})
		if err != nil {
			t.Fatal(err)
		}
		var rule, bare, limited int64
		for _, e := range evs {
			if e.Date.Compare(start) < 0 || e.Date.Compare(start.AddDays(13)) > 0 {
				continue
			}
			n, err := Convert(rate, *e.Amount, cur)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case e.RuleID != "":
				rule += n
			case e.Category == "":
				bare += n
			}
		}
		for _, l := range v.Limits {
			limited += l.Spent
		}
		if got := rule + bare + limited + v.UnplannedSpent; got != v.Spent {
			t.Fatalf("Spent=%d, разбиение даёт %d (rule=%d bare=%d limited=%d unplanned=%d)", v.Spent, got, rule, bare, limited, v.UnplannedSpent)
		}
		var sum int64
		for _, r := range v.Unplanned {
			sum += r.Spent
			for _, l := range v.Limits {
				if CategoryKey(l.Category) == CategoryKey(r.Category) {
					t.Fatalf("категория %q и в лимитах, и в «вне плана»", r.Category)
				}
			}
		}
		if sum != v.UnplannedSpent {
			t.Fatalf("Σ строк %d != UnplannedSpent %d", sum, v.UnplannedSpent)
		}
		if v.UnplannedSpent > 0 {
			hits["вне плана"]++
		}
		if len(v.Limits) > 0 && limited > 0 {
			hits["лимит с тратой"]++
		}
		if rule > 0 {
			hits["правило"]++
		}
	})
	for k, n := range hits {
		if n < 15 {
			t.Errorf("ветка %q почти не покрыта: %d", k, n)
		}
	}
	if len(hits) != 3 {
		t.Errorf("покрыты не все ветки: %v", hits)
	}
}
