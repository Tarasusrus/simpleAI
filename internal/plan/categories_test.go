package plan

import (
	"errors"
	"testing"
)

func doneCat(key, cat string, minor int64, cur Currency, d Date) Event {
	e := payDone(key, "траты "+key, minor, cur, d)
	e.Category = cat
	return e
}

// Окна: A 05.09–18.09 и B 05.10–18.10. Сегодня 14.10 → текущее B, прошлое A.
func catWindows() []Window {
	return BuildWindows([]Event{inc("a", Date{2026, 9, 5}, StateDone, 5_000_000), inc("b", Date{2026, 10, 5}, StateDone, 9_800_000)})
}

func catEvents() []Event {
	return []Event{
		doneCat("f1", "Еда", 500_000, CurRUB, Date{2026, 9, 10}),                 // A: 5 000
		doneCat("f2", "Еда", 200_000, CurRUB, Date{2026, 9, 12}),                 // A: 2 000
		doneCat("f3", "Еда", 59_300, CurTHB, Date{2026, 10, 13}),                 // B: 593 ฿ = 1 483 ₽
		doneCat("f4", "Еда", 31_000, CurTHB, Date{2026, 10, 14}),                 // B: 310 ฿ = 775 ₽
		doneCat("t1", "Транспорт", 100_000, CurRUB, Date{2026, 9, 15}),           // A: 1 000
		doneCat("u1", "", 140_000, CurTHB, Date{2026, 10, 14}),                   // B: без категории 3 500 ₽
		doneCat("old", "Дом", 700_000, CurRUB, Date{2026, 8, 20}),                // до обоих окон — не попадает
		payPlanned("plan1", "Плановый", 999_999, CurRUB, Date{2026, 10, 15}, ""), // план — не трата
	}
}

func cats(t *testing.T, ws []Window, ev []Event, today Date) []CategoryRow {
	t.Helper()
	v := mustCompute(t, in(ws, ev, CurRUB, r25, today, 0))
	return v.Categories
}

func TestCategories_LastAndNow_Order(t *testing.T) {
	got := cats(t, catWindows(), catEvents(), Date{2026, 10, 14})
	// «Сейчас» ↓: Еда 2 258; затем Транспорт (Сейчас 0, Прошлое 1 000); «Без категории» — всегда последней.
	want := []struct {
		name      string
		last, now int64
	}{{"Еда", 7_000, 2_258}, {"Транспорт", 1_000, 0}, {UncategorizedName, 0, 3_500}}
	if len(got) != len(want) {
		t.Fatalf("категорий %d, ждали %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Last != w.last || got[i].Now != w.now {
			t.Errorf("строка %d: %+v; ждали %+v", i, got[i], w)
		}
	}
}

func TestCategories_RecentFiveNewestFirst(t *testing.T) {
	got := cats(t, catWindows(), catEvents(), Date{2026, 10, 14})
	food := got[0]
	keys := make([]string, 0, len(food.Recent))
	for _, r := range food.Recent {
		keys = append(keys, r.Key)
	}
	// дата ↓: 14.10 (f4), 13.10 (f3), 12.09 (f2), 10.09 (f1)
	if want := []string{"f4", "f3", "f2", "f1"}; len(keys) != 4 || keys[0] != want[0] || keys[1] != want[1] || keys[2] != want[2] || keys[3] != want[3] {
		t.Fatalf("recent: %v, ждали %v", keys, want)
	}
	if r := food.Recent[0]; r.Amount == nil || *r.Amount != 775 || r.Kind != KindPayment || r.Date == nil || *r.Date != (Date{2026, 10, 14}) {
		t.Fatalf("строка траты: %+v", r)
	}
}

func TestCategories_RecentCappedAtFive_TieByKey(t *testing.T) {
	var ev []Event
	for i := 0; i < 7; i++ {
		ev = append(ev, doneCat("k"+pad(i, 2), "Еда", 10_000, CurRUB, Date{2026, 10, 10})) // одна дата
	}
	got := cats(t, catWindows(), ev, Date{2026, 10, 14})
	if len(got[0].Recent) != 5 {
		t.Fatalf("recent: %d, ждали 5", len(got[0].Recent))
	}
	for i, r := range got[0].Recent {
		if r.Key != "k"+pad(i, 2) {
			t.Fatalf("при равной дате порядок по Key ↑: %d → %s", i, r.Key)
		}
	}
}

// between/ended: «Сейчас» пусто, «Прошлое» — последнее окно.
func TestCategories_AfterWindowEnds(t *testing.T) {
	for _, today := range []Date{{2026, 10, 19}, {2026, 12, 1}} { // ended (после последнего окна)
		got := cats(t, catWindows(), catEvents(), today)
		byName := map[string]CategoryRow{}
		for _, c := range got {
			byName[c.Name] = c
		}
		if byName["Еда"].Last != 2_258 || byName["Еда"].Now != 0 {
			t.Errorf("today=%v Еда: %+v, ждали Last=2258 Now=0", today, byName["Еда"])
		}
		if _, ok := byName["Транспорт"]; ok {
			t.Errorf("today=%v: Транспорт был только в окне A и в последнем окне не виден", today)
		}
		if byName[UncategorizedName].Last != 3_500 {
			t.Errorf("today=%v: без категории %+v", today, byName[UncategorizedName])
		}
	}
}

func TestCategories_UpcomingFirstWindow(t *testing.T) {
	// Сегодня до первого окна: «прошлого» нет, «Сейчас» — первое окно (фактов там быть не должно).
	got := cats(t, catWindows(), catEvents(), Date{2026, 9, 1})
	for _, c := range got {
		if c.Last != 0 {
			t.Fatalf("у первого окна нет прошлого: %+v", c)
		}
	}
}

func TestCategories_UncategorizedOmittedWhenZero(t *testing.T) {
	ev := []Event{doneCat("e5", "Еда", 100_000, CurRUB, Date{2026, 10, 10})}
	got := cats(t, catWindows(), ev, Date{2026, 10, 14})
	if len(got) != 1 || got[0].Name != "Еда" {
		t.Fatalf("«Без категории» при нулевой сумме не нужна: %+v", got)
	}
}

func TestCategories_OrderTies(t *testing.T) {
	ev := []Event{
		doneCat("e1", "Б", 100_000, CurRUB, Date{2026, 10, 10}),
		doneCat("e2", "А", 100_000, CurRUB, Date{2026, 10, 10}),
		doneCat("e3", "В", 300_000, CurRUB, Date{2026, 9, 10}), // Сейчас 0, Прошлое 3 000
		doneCat("e4", "Г", 200_000, CurRUB, Date{2026, 9, 10}), // Сейчас 0, Прошлое 2 000
	}
	got := cats(t, catWindows(), ev, Date{2026, 10, 14})
	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.Name)
	}
	// Сейчас ↓ (А=Б=1000 → по названию), затем Прошлое ↓ (В 3000, Г 2000)
	if want := []string{"А", "Б", "В", "Г"}; len(names) != 4 || names[0] != want[0] || names[1] != want[1] || names[2] != want[2] || names[3] != want[3] {
		t.Fatalf("порядок: %v, ждали %v", names, want)
	}
}

func TestCategories_ConversionError(t *testing.T) {
	_, err := Compute(in(catWindows(), catEvents(), CurRUB, nil, Date{2026, 10, 14}, 0))
	if !errors.Is(err, ErrNoRate) {
		t.Fatalf("err=%v, ждали ErrNoRate (бат без курса)", err)
	}
}

// Окон нет совсем: «Прошлого» и «Сейчас» нет, но «Обычно» считается по датам трат.
func TestCategories_NoWindows_OnlyAverage(t *testing.T) {
	v := mustCompute(t, in([]Window{}, catEvents(), CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.LastWindow != nil {
		t.Errorf("окон нет, а LastWindow=%+v", v.LastWindow)
	}
	for _, c := range v.Categories {
		if c.Last != 0 || c.Now != 0 {
			t.Errorf("без окон Last/Now должны быть 0: %+v", c)
		}
	}
	if len(v.Categories) == 0 || v.Categories[0].Name != "Еда" || v.Categories[0].Avg == 0 {
		t.Fatalf("обычные траты по Еде должны быть: %+v", v.Categories)
	}
}

// «Обычно» = траты за AvgDays, приведённые к 14 дням, округление half-away.
//
// Мутация: заменить windowDays на другое число или убрать roundDivHalfAway — тест краснеет.
func TestCategories_Average(t *testing.T) {
	today := Date{2026, 10, 14}
	ev := []Event{
		doneCat("a1", "Еда", 90_000, CurRUB, Date{2026, 10, 14}), // последний день периода: входит
		doneCat("a2", "Еда", 90_000, CurRUB, Date{2026, 8, 3}),   // 30 дней: 14.10 − 29 = 15.09, не входит
		doneCat("a3", "Еда", 30_000, CurRUB, Date{2026, 9, 15}),  // первый день 30-дневного периода: входит
	}
	i := in(catWindows(), ev, CurRUB, r25, today, 0)
	i.AvgDays = 30
	got := mustCompute(t, i).Categories
	// за 30 дней: 900 + 300 = 1 200 ₽; на 14 дней: 1 200 * 14 / 30 = 560
	if len(got) == 0 || got[0].Name != "Еда" || got[0].Avg != 560 {
		t.Fatalf("Avg: %+v, ждали Еда=560", got)
	}
	i.AvgDays = 90 // входят все три: 900+900+300 = 2 100 ₽ → 2 100 * 14 / 90 = 326,67 → 327
	got = mustCompute(t, i).Categories
	if got[0].Avg != 327 {
		t.Errorf("90 дней: %d, ждали 327", got[0].Avg)
	}
	for _, bad := range []int{6, 731, -1} {
		i.AvgDays = bad
		if _, err := Compute(i); !errors.Is(err, ErrBadAvgDays) {
			t.Errorf("AvgDays=%d: %v", bad, err)
		}
	}
}

// Скрытое закончившееся окно: окна в View нет, «Прошлое» остаётся и подписано датами.
func TestCompute_HideEnded_KeepsLastWindowColumn(t *testing.T) {
	i := in(catWindows(), catEvents(), CurRUB, r25, Date{2026, 12, 1}, 0)
	i.HideEnded = true
	v := mustCompute(t, i)
	if v.Window != nil || v.LastWindow == nil || v.LastWindow.Start != (Date{2026, 10, 5}) {
		t.Fatalf("window=%v last=%+v", v.Window, v.LastWindow)
	}
	var food CategoryRow
	for _, c := range v.Categories {
		if c.Name == "Еда" {
			food = c
		}
	}
	if food.Last != 2_258 || food.Now != 0 {
		t.Errorf("Еда: %+v, ждали Last=2258 Now=0", food)
	}
}

// Границы окна парами: последний день ВХОДИТ, следующий — нет; то же для первого дня.
func TestCategories_WindowBoundariesPairs(t *testing.T) {
	ev := []Event{
		doneCat("b1", "Еда", 100_000, CurRUB, Date{2026, 10, 5}),   // первый день B — входит (Сейчас)
		doneCat("b2", "Еда", 200_000, CurRUB, Date{2026, 10, 18}),  // последний день B — входит
		doneCat("b3", "Еда", 400_000, CurRUB, Date{2026, 10, 19}),  // после B — нет
		doneCat("a1", "Еда", 800_000, CurRUB, Date{2026, 9, 5}),    // первый день A — входит (Прошлое)
		doneCat("a2", "Еда", 1_600_000, CurRUB, Date{2026, 9, 18}), // последний день A — входит
		doneCat("a3", "Еда", 3_200_000, CurRUB, Date{2026, 9, 19}), // зазор между окнами — нет
		doneCat("a4", "Еда", 6_400_000, CurRUB, Date{2026, 9, 4}),  // до A — нет
	}
	got := cats(t, catWindows(), ev, Date{2026, 10, 14})
	if len(got) != 1 || got[0].Now != 3_000 || got[0].Last != 24_000 {
		t.Fatalf("границы: %+v; ждали Now=3000 (1000+2000) Last=24000 (8000+16000)", got)
	}
}
