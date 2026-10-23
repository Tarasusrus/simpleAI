package plan

import (
	"testing"
)

func keysOf(rows []Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Key)
	}
	return out
}

func sameKeys(a, b []string) bool {
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

func planNoAmount(key, name string, d *Date, ws *Date) Event {
	return Event{Key: key, Kind: KindPayment, State: StatePlanned, Name: name, Date: d, WindowStart: ws, Source: SourcePlanned}
}

func planAmount(key, name string, minor int64, d *Date, ws *Date) Event {
	e := planNoAmount(key, name, d, ws)
	e.Amount = &Money{minor, CurRUB}
	return e
}

// Окна: W0 05.10–18.10, W1 20.10–02.11; сегодня 14.10.
func tlWindows() []Window {
	return BuildWindows([]Event{inc("w0", Date{2026, 10, 5}, StateDone, 9_800_000), inc("w1", Date{2026, 10, 20}, StatePlanned, 13_000_000)})
}

func tl(t *testing.T, ev []Event, today Date) View {
	t.Helper()
	return mustCompute(t, in(tlWindows(), ev, CurRUB, r25, today, 0))
}

// AC-5.1 (домен): порядок ленты — просроченные ↑, дата, при равной дате:
// поступление-факт, поступление-план, платёж; затем имя без регистра, затем Key.
func TestTimeline_Order(t *testing.T) {
	d := func(day int) *Date { x := Date{2026, 10, day}; return &x }
	ev := []Event{
		planAmount("z-late", "Позже всех", 100, d(16), nil),
		planAmount("b-pay", "бета", 100, d(12), nil),
		planAmount("a-pay2", "альфа", 100, d(12), nil), // то же имя без регистра → решает Key (подан раньше нарочно)
		planAmount("a-pay", "Альфа", 100, d(12), nil),
		planAmount("over2", "Просрочено поздно", 100, d(8), nil),
		planAmount("over1", "Просрочено давно", 100, d(6), nil),
	}
	ws := BuildWindows([]Event{
		inc("fact", Date{2026, 10, 5}, StateDone, 9_800_000),
		inc("plan", Date{2026, 10, 12}, StatePlanned, 1_000_000),
		inc("fact12", Date{2026, 10, 12}, StateDone, 500_000),
	})
	v := mustCompute(t, in(ws, ev, CurRUB, r25, Date{2026, 10, 11}, 0)) // сегодня 11.10: просрочены только 06.10 и 08.10
	got := keysOf(v.Timeline)
	// просроченные (06.10, 08.10) первыми по дате ↑; 05.10 факт — прошлое, в ленту не идёт;
	// 12.10: факт, план, потом платежи (Альфа/альфа/бета); 16.10
	want := []string{"over1", "over2", "fact12", "plan", "a-pay", "a-pay2", "b-pay", "z-late"}
	if !sameKeys(got, want) {
		t.Fatalf("порядок ленты:\n got  %v\n want %v", got, want)
	}
}

func TestTimeline_RowFields(t *testing.T) {
	d := Date{2026, 10, 2}
	ev := []Event{planAmount("over", "Стоматолог", 50_000, &d, nil)} // 500 ₽, просрочен на 12 дн.
	v := tl(t, ev, Date{2026, 10, 14})
	var row *Row
	for i := range v.Timeline {
		if v.Timeline[i].Key == "over" {
			row = &v.Timeline[i]
		}
	}
	if row == nil || row.OverdueDays != 12 || row.Amount == nil || *row.Amount != 500 || row.Kind != KindPayment {
		t.Fatalf("строка просроченного: %+v", row)
	}
	// Ожидаемое поступление, дата которого прошла, — «не пришло»; будущее ожидаемое — только expected.
	ws := BuildWindows([]Event{inc("a", Date{2026, 10, 5}, StateDone, 9_800_000), inc("b", Date{2026, 10, 10}, StatePlanned, 100_000), inc("c", Date{2026, 10, 16}, StatePlanned, 100_000)})
	v = mustCompute(t, in(ws, nil, CurRUB, r25, Date{2026, 10, 14}, 0))
	rows := map[string]Row{}
	for _, r := range v.Timeline {
		rows[r.Key] = r
	}
	if !rows["b"].Expected || !rows["b"].NotArrived || !rows["c"].Expected || rows["c"].NotArrived {
		t.Fatalf("флаги поступлений: b=%+v c=%+v", rows["b"], rows["c"])
	}
	if rows["b"].Kind != KindIncome {
		t.Fatal("вид строки поступления")
	}
	if _, ok := rows["a"]; ok {
		t.Fatal("пришедшее в прошлые дни не «ближайшее»")
	}
}

// OverdueDays — только у плановых разовых платежей.
func TestTimeline_OverdueOnlyForOneOffPlanned(t *testing.T) {
	d := Date{2026, 10, 8}
	rec := planAmount("rec", "Повторяющийся", 100, &d, nil)
	rec.RuleID, rec.ScheduledFor, rec.Source = "r", &d, SourceRecurring
	v := tl(t, []Event{rec, payDone("done", "Оплачено", 100, CurRUB, d)}, Date{2026, 10, 14})
	for _, r := range v.Timeline {
		if (r.Key == "rec" || r.Key == "done") && r.OverdueDays != 0 {
			t.Fatalf("%s: OverdueDays=%d, ждали 0", r.Key, r.OverdueDays)
		}
	}
}

// Лента «Ближайшие платежи» смотрит вперёд: оплаченное сегодня остаётся (можно вернуть
// в план), оплаченное в прошлые дни и вне окна — нет.
func TestTimeline_FactsOnlyTodayOnward(t *testing.T) {
	v := tl(t, []Event{
		payDone("today", "Сегодня", 100_000, CurRUB, Date{2026, 10, 14}),
		payDone("past", "Вчера", 100_000, CurRUB, Date{2026, 10, 13}),
		payDone("out", "Вне окна", 100, CurRUB, Date{2026, 10, 25}),
	}, Date{2026, 10, 14})
	k := keysOf(v.Timeline)
	if !contains(k, "today") || contains(k, "past") || contains(k, "out") {
		t.Fatalf("лента: %v", k)
	}
}

func contains(ks []string, k string) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// «Без даты»: платёж без даты и без выбора окна — в блоке, в расчёт не входит.
func TestUndated_Block(t *testing.T) {
	ev := []Event{
		planAmount("u1", "С суммой", 100_000, nil, nil),
		planNoAmount("u2", "Права", nil, nil),
		planAmount("u3", "Ещё с суммой", 50_000, nil, nil),
	}
	v := tl(t, ev, Date{2026, 10, 14})
	if v.Undated.Count != 3 || v.Undated.PricedCount != 2 || v.Undated.Sum != 1_500 {
		t.Fatalf("undated=%+v; ждали count=3 priced=2 sum=1500", v.Undated)
	}
	if v.Payments != 0 || v.Unpriced != 0 {
		t.Fatalf("без даты не входит в расчёт: payments=%d unpriced=%d", v.Payments, v.Unpriced)
	}
	var noAmt Row
	for _, r := range v.Undated.Items {
		if r.Key == "u2" {
			noAmt = r
		}
	}
	if !noAmt.NoAmount || noAmt.Amount != nil || noAmt.Date != nil {
		t.Fatalf("строка без суммы: %+v", noAmt)
	}
}

// «Окно ещё не создано» и «окно прошло» — не теряем напоминание.
func TestUndated_WindowMissingAndPassed(t *testing.T) {
	missing := planAmount("miss", "В следующее", 100, nil, &Date{2026, 12, 1}) // окна 01.12 ещё нет
	passed := planAmount("past", "В прошлое", 100, nil, &Date{2026, 10, 5})    // окно W0
	// сегодня 21.10 → текущее W1; окно W0 прошло
	v := tl(t, []Event{missing, passed}, Date{2026, 10, 21})
	rows := map[string]Row{}
	for _, r := range v.Undated.Items {
		rows[r.Key] = r
	}
	if !rows["miss"].WindowMissing || rows["miss"].WindowPassed {
		t.Fatalf("miss: %+v", rows["miss"])
	}
	if !rows["past"].WindowPassed || rows["past"].WindowMissing {
		t.Fatalf("past: %+v", rows["past"])
	}
	if v.Payments != 0 {
		t.Fatalf("напоминания не входят в расчёт: %d", v.Payments)
	}
}

func TestTimeline_UndatedWithChosenWindowIsInTimelineAfterDated(t *testing.T) {
	d := Date{2026, 10, 16}
	ev := []Event{
		planAmount("dated", "С датой", 100, &d, nil),
		planAmount("chosen-b", "бета в окне", 100, nil, &Date{2026, 10, 5}),
		planAmount("chosen-a", "Альфа в окне", 100, nil, &Date{2026, 10, 5}),
	}
	v := tl(t, ev, Date{2026, 10, 14})
	got := keysOf(v.Timeline)
	// В ленте: сначала датированные, потом «в это окно» без даты по названию (без регистра).
	idx := func(k string) int {
		for i, x := range got {
			if x == k {
				return i
			}
		}
		return -1
	}
	if idx("chosen-a") < 0 || idx("dated") >= idx("chosen-a") || idx("chosen-a") >= idx("chosen-b") {
		t.Fatalf("порядок «в это окно»: %v", got)
	}
	// И они не остаются в блоке «Без даты».
	if v.Undated.Count != 0 {
		t.Fatalf("выбранное окно ≠ без даты: %+v", v.Undated)
	}
}

// AC-5.2: Next = nil без следующего окна; Count включает платежи без суммы, Sum — нет.
func TestNext_Block(t *testing.T) {
	d := func(day int) *Date { x := Date{2026, 10, day}; return &x }
	ev := []Event{
		planAmount("n1", "Платёж 1", 1_000_000, d(22), nil), // в W1 (20.10–02.11) → 10 000 ₽
		planNoAmount("n2", "Без суммы", d(25), nil),
		planAmount("n3", "Через выбор", 500_000, nil, &Date{2026, 10, 19}), // резолвится в W1 → 5 000 ₽
	}
	v := tl(t, ev, Date{2026, 10, 14})
	if v.Next == nil || v.Next.Count != 3 || v.Next.Sum != 15_000 {
		t.Fatalf("Next=%+v; ждали Count=3 (с платежом без суммы), Sum=15000", v.Next)
	}
	if len(v.Next.Items) != 3 {
		t.Fatalf("items=%d", len(v.Next.Items))
	}
	// Следующих окон нет → Next == nil
	single := mustCompute(t, in(goldenWindows(), goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 0))
	if single.Next != nil {
		t.Fatalf("Next без следующего окна: %+v", single.Next)
	}
}

// Дальние платежи: бакеты по месяцам m0..m5 от месяца today, за границей — отброшены.
func TestLater_MonthBuckets(t *testing.T) {
	d := func(y, m, day int) *Date { x := Date{y, m, day}; return &x }
	ev := []Event{
		planAmount("l-oct", "Октябрь", 100_000, d(2026, 10, 30), nil), // вне W0 и W1 (после 02.11)? 30.10 — внутри W1: не later
		planAmount("l-nov", "Ноябрь", 200_000, d(2026, 11, 15), nil),
		planNoAmount("l-nov2", "Ноябрь без суммы", d(2026, 11, 20), nil),
		planAmount("l-dec", "Декабрь", 110_000, d(2026, 12, 10), nil),
		planAmount("l-jan", "Январь", 120_000, d(2027, 1, 10), nil),
		planAmount("l-feb", "Февраль", 130_000, d(2027, 2, 10), nil),
		planAmount("l-mar", "Март", 400_000, d(2027, 3, 31), nil),  // m5 (окт..мар)
		planAmount("l-apr", "Апрель", 800_000, d(2027, 4, 1), nil), // за горизонтом
	}
	v := tl(t, ev, Date{2026, 10, 14})
	got := map[[2]int]MonthBucket{}
	for _, b := range v.Later {
		got[[2]int{b.Year, b.Month}] = b
	}
	if _, ok := got[[2]int{2026, 10}]; ok {
		t.Fatalf("30.10 внутри следующего окна — в Later не должно быть: %+v", v.Later)
	}
	nov := got[[2]int{2026, 11}]
	if nov.Count != 2 || nov.Sum != 2_000 {
		t.Fatalf("ноябрь: %+v; ждали Count=2 (с платежом без суммы) Sum=2000", nov)
	}
	if got[[2]int{2027, 3}].Count != 1 {
		t.Fatalf("март (m5) обязан попасть: %+v", v.Later)
	}
	if _, ok := got[[2]int{2027, 4}]; ok {
		t.Fatalf("апрель за горизонтом: %+v", v.Later)
	}
	for i := 1; i < len(v.Later); i++ {
		a, b := v.Later[i-1], v.Later[i]
		if a.Year > b.Year || (a.Year == b.Year && a.Month >= b.Month) {
			t.Fatalf("бакеты по возрастанию: %+v", v.Later)
		}
	}
}

// Без следующего окна платежи после конца текущего уходят в Later.
func TestLater_WithoutNextWindow(t *testing.T) {
	d := Date{2026, 10, 25}
	v := mustCompute(t, in(goldenWindows(), []Event{planAmount("x", "После окна", 100_000, &d, nil)}, CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.Next != nil || len(v.Later) != 1 || v.Later[0].Month != 10 || v.Later[0].Count != 1 {
		t.Fatalf("Next=%v Later=%+v", v.Next, v.Later)
	}
}

// Фикстура AC-4.1: кредит 27.10 при единственном окне — в Later октября, Next скрыт.
func TestLater_Golden_LoanInOctober(t *testing.T) {
	v := mustCompute(t, in(goldenWindows(), goldenEvents(), CurRUB, r25, Date{2026, 10, 14}, 0))
	if v.Next != nil || len(v.Later) != 1 || v.Later[0].Year != 2026 || v.Later[0].Month != 10 || v.Later[0].Count != 1 || v.Later[0].Sum != 28_500 {
		t.Fatalf("Next=%v Later=%+v", v.Next, v.Later)
	}
}

func TestTimeline_PastDatedOutsideWindowsNotShown(t *testing.T) {
	// Повторяющееся плановое с прошлой датой вне окон в View не попадает.
	d := Date{2026, 10, 2} // октябрь = m0, но раньше первого окна (05.10) и раньше сегодня
	rec := planAmount("recurring:loan@2026-10-02", "Кредит", 2_850_000, &d, nil)
	rec.RuleID, rec.ScheduledFor, rec.Source = "loan", &d, SourceRecurring
	v := mustCompute(t, in(goldenWindows(), []Event{rec}, CurRUB, r25, Date{2026, 10, 14}, 0))
	for _, r := range v.Timeline {
		if r.Key == rec.Key {
			t.Fatal("прошлое повторяющееся вне окон попало в ленту")
		}
	}
	if len(v.Later) != 0 || v.Next != nil || v.Undated.Count != 0 {
		t.Fatalf("и в другие блоки не должно: later=%+v next=%+v undated=%+v", v.Later, v.Next, v.Undated)
	}
}

// Вне current просроченные разовые не считаются, но видны в ленте с OverdueDays.
func TestTimeline_OverdueVisibleOutsideCurrent(t *testing.T) {
	d := Date{2026, 10, 2}
	ev := []Event{planAmount("over", "Давно", 50_000, &d, nil)}
	v := tl(t, ev, Date{2026, 10, 19}) // между окнами W0 и W1
	if v.Payments != 0 {
		t.Fatalf("payments=%d", v.Payments)
	}
	var row *Row
	for i := range v.Timeline {
		if v.Timeline[i].Key == "over" {
			row = &v.Timeline[i]
		}
	}
	if row == nil || row.OverdueDays != 17 {
		t.Fatalf("просроченный вне current должен быть виден: %+v", row)
	}
}

// Бакеты «Позже» собираются через карту, чей обход случайный. Проверка порядка
// повторяется: единичный прогон мог бы случайно получить отсортированный порядок.
func TestLater_BucketsAlwaysAscending(t *testing.T) {
	d := func(y, m, day int) *Date { x := Date{y, m, day}; return &x }
	ev := []Event{
		planAmount("m1", "Ноябрь", 100, d(2026, 11, 15), nil),
		planAmount("m2", "Декабрь", 100, d(2026, 12, 15), nil),
		planAmount("m3", "Январь", 100, d(2027, 1, 15), nil),
		planAmount("m4", "Февраль", 100, d(2027, 2, 15), nil),
		planAmount("m5", "Март", 100, d(2027, 3, 15), nil),
	}
	for i := 0; i < 40; i++ {
		v := tl(t, ev, Date{2026, 10, 14})
		if len(v.Later) != 5 {
			t.Fatalf("бакетов %d, ждали 5", len(v.Later))
		}
		for j := 1; j < len(v.Later); j++ {
			a, b := v.Later[j-1], v.Later[j]
			if a.Year > b.Year || (a.Year == b.Year && a.Month >= b.Month) {
				t.Fatalf("прогон %d: бакеты не по возрастанию: %+v", i, v.Later)
			}
		}
	}
}
