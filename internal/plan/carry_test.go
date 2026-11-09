package plan

import (
	"context"
	"testing"
)

func spend(key string, at Date, units int64) Event {
	return Event{Key: "transaction:" + key, Kind: KindPayment, State: StateDone, Name: key, Amount: rub(units), Date: &at, Source: SourceTransaction}
}

// Остаток прошлых окон переходит в текущее по цепочке, и плюс, и минус (ADR-023).
//
// Мутации: убрать v.Carry из out — «Свободно» без переноса; считать ожидаемое в остатке — перенос растёт.
func TestService_CarryChain(t *testing.T) {
	r := &fakeReader{snap: Snapshot{
		Incomes: []Event{
			salary("a", d(2026, 9, 1)), salary("b", d(2026, 9, 15)), salary("c", d(2026, 9, 29)),
			expected("never", d(2026, 9, 20)), // ожидалось и не пришло — в остаток не входит
		},
		Payments: []Event{spend("x", d(2026, 9, 5), 400), spend("y", d(2026, 9, 20), 1300)},
	}}
	// Окно 01.09: 1000 − 400 = +600; окно 15.09: 1000 − 1300 = −300 (+ ожидаемые 1000 не в счёт); итого +300.
	res, err := svc(r, d(2026, 10, 1)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	v := res.View
	if v.Window.Start != d(2026, 9, 29) || v.Carry != 300 || v.CanSpend != 1300 {
		t.Fatalf("окно %v, перенос %d (ждали 300), свободно %d (ждали 1300)", v.Window.Start, v.Carry, v.CanSpend)
	}
}

// Минус прошлого окна по умолчанию НЕ переносится: без разрешения пользователя дефицит не висит на новом окне.
//
// Мутация: убрать `total = 0` в carry() — перенос −200, тест краснеет.
func TestService_CarryDebtNotCarriedByDefault(t *testing.T) {
	r := &fakeReader{snap: Snapshot{
		Incomes:    []Event{salary("a", d(2026, 9, 1)), salary("b", d(2026, 9, 15))},
		Payments:   []Event{spend("x", d(2026, 9, 5), 1100)},
		ReservePct: 10,
	}}
	// Окно 01.09: 1000 − 1100 − резерв 100 = −200: без разрешения в окно 15.09 не переходит.
	res, err := svc(r, d(2026, 9, 20)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.Carry != 0 || res.View.CanSpend != 1000-100 {
		t.Fatalf("перенос %d (ждали 0), свободно %d (ждали 900)", res.View.Carry, res.View.CanSpend)
	}
}

// С разрешением пользователя минус переносится, как в ADR-023.
func TestService_CarryDebtWhenAllowed(t *testing.T) {
	r := &fakeReader{snap: Snapshot{
		Incomes:    []Event{salary("a", d(2026, 9, 1)), salary("b", d(2026, 9, 15))},
		Payments:   []Event{spend("x", d(2026, 9, 5), 1100)},
		ReservePct: 10,
	}}
	res, err := svc(r, d(2026, 9, 20), WithCarryDebt(true)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.Carry != -200 || res.View.CanSpend != 1000-100-200 {
		t.Fatalf("перенос %d, свободно %d", res.View.Carry, res.View.CanSpend)
	}
}

// Старый долг не копится: плюс после минуса переносится целиком, а не уменьшается на давний дефицит.
//
// Мутация: убрать `total = 0` — перенос 100 вместо 600.
func TestService_OldDeficitDoesNotEatLaterSurplus(t *testing.T) {
	r := &fakeReader{snap: Snapshot{
		Incomes: []Event{
			salary("a", d(2026, 8, 1)), salary("b", d(2026, 8, 15)), salary("c", d(2026, 9, 1)), salary("d", d(2026, 9, 15)),
		},
		Payments: []Event{spend("x", d(2026, 8, 5), 1500), spend("y", d(2026, 8, 20), 400)},
	}}
	// 01.08: 1000 − 1500 = −500 (гасится); 15.08: 1000 − 400 = +600; 01.09: +1000 → переносится 1600, не 1100.
	res, err := svc(r, d(2026, 9, 20)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.Carry != 1600 {
		t.Fatalf("перенос %d, ждали 1600: давний дефицит не должен съедать последующий плюс", res.View.Carry)
	}
}

func TestService_NoCarryInFirstWindow(t *testing.T) {
	r := &fakeReader{snap: Snapshot{Incomes: []Event{salary("a", d(2026, 9, 1))}, Payments: []Event{spend("x", d(2026, 8, 20), 500)}}}
	res, err := svc(r, d(2026, 9, 5)).View(context.Background(), CurRUB)
	if err != nil || res.View.Carry != 0 {
		t.Fatalf("в первом окне переноса нет (траты до окон не в счёт): %d err=%v", res.View.Carry, err)
	}
}

// Зарплата в последние дни окна (пятница перед выходными) закрывает его и открывает новое (ADR-023).
func TestBuildGrowingWindows_EarlySalaryClosesWindow(t *testing.T) {
	a := inc("a", d(2026, 10, 1), StateDone, 1)    // окно 01–14.10
	fri := inc("b", d(2026, 10, 12), StateDone, 1) // за 3 дня до конца
	ws := BuildGrowingWindows([]Event{a, fri})
	if len(ws) != 2 || ws[0].End() != d(2026, 10, 11) || ws[1].Start != d(2026, 10, 12) {
		t.Fatalf("окна: %+v", ws)
	}
	mid := BuildGrowingWindows([]Event{a, inc("b", d(2026, 10, 11), StateDone, 1)})
	if len(mid) != 1 || len(mid[0].Incomes) != 2 {
		t.Fatalf("зарплата раньше последних 3 дней — в текущее окно: %+v", mid)
	}
	fixed := BuildGrowingWindows([]Event{a, fri}, StoredWindow{ID: "w", Start: d(2026, 10, 1), End: d(2026, 10, 14), FixedEnd: true})
	if len(fixed) != 1 {
		t.Fatalf("конец владельца ранняя зарплата не режет: %+v", fixed)
	}
	if plain := BuildWindows([]Event{a, fri}); len(plain) != 1 {
		t.Fatalf("BuildWindows без правил ADR-022/023: %+v", plain)
	}
}

// После сохранения обоих окон прежнее, хранящее старый конец, заканчивается накануне нового.
func TestService_EarlySalaryStoredWindowsStayConsistent(t *testing.T) {
	s, m := wsvc2(d(2026, 10, 13), salary("a", d(2026, 10, 1)), salary("b", d(2026, 10, 12)))
	for range 2 {
		res, err := s.View(context.Background(), CurRUB)
		if err != nil {
			t.Fatal(err)
		}
		if w := res.View.Window; w == nil || w.Start != d(2026, 10, 12) || res.View.Carry != 1000 {
			t.Fatalf("текущее окно с 12.10 и перенос 1000: %+v carry=%d", w, res.View.Carry)
		}
	}
	if len(m.creates) != 2 || m.creates[0].End != d(2026, 10, 11) {
		t.Fatalf("сохранены %+v", m.creates)
	}
}

// Ожидаемая зарплата окно не режет: деньги могут не прийти.
func TestBuildGrowingWindows_PlannedSalaryDoesNotCloseEarly(t *testing.T) {
	a := inc("a", d(2026, 10, 1), StateDone, 1)
	planned := inc("b", d(2026, 10, 13), StatePlanned, 1)
	if ws := BuildGrowingWindows([]Event{a, planned}); len(ws) != 1 || len(ws[0].Incomes) != 2 {
		t.Fatalf("окна: %+v", ws)
	}
}

// Траты в зазоре после окна с концом владельца входят в перенос.
func TestService_CarryCountsGapSpending(t *testing.T) {
	r := &fakeReader{snap: Snapshot{
		Incomes:  []Event{salary("a", d(2026, 9, 1)), salary("b", d(2026, 9, 20))},
		Payments: []Event{spend("gap", d(2026, 9, 15), 300)},
		Windows:  []StoredWindow{{ID: "w1", Start: d(2026, 9, 1), End: d(2026, 9, 10), FixedEnd: true}},
	}}
	res, err := svc(r, d(2026, 9, 25)).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if res.View.Carry != 700 {
		t.Fatalf("перенос %d, ждали 1000 − 300 = 700", res.View.Carry)
	}
}

// Окно, закрытое ранней зарплатой, переписывается в базе с новым концом.
func TestService_EarlyCloseRewritesStoredEnd(t *testing.T) {
	r := &fakeReader{snap: Snapshot{
		Incomes: []Event{salary("a", d(2026, 10, 1)), salary("b", d(2026, 10, 12))},
		Windows: []StoredWindow{{ID: "w0", Start: d(2026, 10, 1), End: d(2026, 10, 14)}},
	}}
	m := &memWindows{r: r}
	if _, err := svc(r, d(2026, 10, 13)).WithWindowStore(m).View(context.Background(), CurRUB); err != nil {
		t.Fatal(err)
	}
	if len(m.updates) != 1 || m.updates[0].ID != "w0" || m.updates[0].End != d(2026, 10, 11) || m.updates[0].FixedEnd {
		t.Fatalf("переписано %+v", m.updates)
	}
}

// Пересечение окон разных чатов, не похожее на раннюю зарплату, по-прежнему оставляет раннее окно.
func TestBuildGrowingWindows_OverlapNotEarlyKeepsEarlier(t *testing.T) {
	ws := BuildGrowingWindows(nil,
		StoredWindow{ID: "a", Start: d(2026, 10, 1), End: d(2026, 10, 20)},
		StoredWindow{ID: "b", Start: d(2026, 10, 5), End: d(2026, 10, 8)})
	if len(ws) != 1 || ws[0].ID != "a" || ws[0].End() != d(2026, 10, 20) {
		t.Fatalf("окна: %+v", ws)
	}
}

// Закрытое ранней зарплатой окно не «изменено»: его конец не ставил владелец.
func TestEarlyClosedWindowNotAdjusted(t *testing.T) {
	ws := BuildGrowingWindows([]Event{inc("a", d(2026, 10, 1), StateDone, 1), inc("b", d(2026, 10, 13), StateDone, 1)})
	if len(ws) != 2 || ws[0].Adjusted() {
		t.Fatalf("окна: %+v", ws)
	}
}
