package plan

import "sort"

// windowDays — длина окна. Жёстко 14 дней по решению владельца: поступления у него
// приходят примерно раз в две недели, и окно должно быть одинаковым всегда.
const windowDays = 14

// Window — 14 календарных дней от даты поступления. Хранит только поступления:
// платежи по окнам раскладывает Compute.
type Window struct {
	Start   Date
	Incomes []Event
}

// End — последний день окна включительно (Start + 13).
func (w Window) End() Date { return w.Start.AddDays(windowDays - 1) }

// AnchorPlanned — окно открыто ожидаемым, а не пришедшим поступлением.
func (w Window) AnchorPlanned() bool {
	return len(w.Incomes) > 0 && w.Incomes[0].State == StatePlanned
}

// BuildWindows строит окна из поступлений (платежи и события без даты пропускает).
//
// Окно открывает самое раннее ещё не покрытое поступление; поступление внутри окна
// добавляется к нему, позже конца — открывает новое. Сортировка (дата, факт раньше
// плана, ключ) делает результат независимым от порядка входа. Вызывающий отдаёт ВСЕ
// поступления с начала: цепочка окон зависит от точки отсечки истории.
func BuildWindows(incomes []Event) []Window {
	ws := []Window{}
	var src []Event
	for _, e := range incomes {
		if e.Kind == KindIncome && e.Date != nil {
			src = append(src, e)
		}
	}
	sort.SliceStable(src, func(i, j int) bool {
		if c := src[i].Date.Compare(*src[j].Date); c != 0 {
			return c < 0
		}
		if src[i].State != src[j].State {
			return src[i].State == StateDone // факт раньше плана
		}
		return src[i].Key < src[j].Key
	})
	for _, e := range src {
		if n := len(ws); n > 0 && e.Date.Compare(ws[n-1].End()) <= 0 {
			ws[n-1].Incomes = append(ws[n-1].Incomes, e)
			continue
		}
		ws = append(ws, Window{Start: *e.Date, Incomes: []Event{e}})
	}
	return ws
}

// StateKind — положение «сегодня» относительно окон.
type StateKind string

const (
	StateCurrent  StateKind = "current"  // сегодня внутри окна
	StateUpcoming StateKind = "upcoming" // первое окно ещё не началось
	StateBetween  StateKind = "between"  // зазор между закончившимся и следующим окном
	StateEnded    StateKind = "ended"    // последнее окно закончилось
	StateNone     StateKind = "none"     // поступлений нет вообще
)

// WindowState — какое окно описывать и сколько в нём осталось дней.
// Index: current → i; upcoming → 0; between → последнее закончившееся (следующее
// k+1); ended → последнее; none → −1.
type WindowState struct {
	Kind     StateKind
	Index    int
	DaysLeft int
}

// Locate определяет состояние по сегодняшней дате. DaysLeft считается с сегодняшним
// днём включительно: в последний день окна остаётся 1, после конца — 0.
func Locate(ws []Window, today Date) WindowState {
	if len(ws) == 0 {
		return WindowState{StateNone, -1, 0}
	}
	if today.Compare(ws[0].Start) < 0 {
		return WindowState{StateUpcoming, 0, windowDays}
	}
	last := len(ws) - 1
	for i, w := range ws {
		if today.Compare(w.Start) >= 0 && today.Compare(w.End()) <= 0 {
			left := DaysBetween(today, w.End()) + 1
			return WindowState{StateCurrent, i, left}
		}
	}
	if today.Compare(ws[last].End()) > 0 {
		return WindowState{StateEnded, last, 0}
	}
	// Зазор: последнее закончившееся окно — то, чей End раньше сегодня.
	k := 0
	for i, w := range ws {
		if w.End().Compare(today) < 0 {
			k = i
		}
	}
	return WindowState{StateBetween, k, 0}
}

// ResolveWindow — окно для платежа, у которого владелец выбрал «в это/следующее
// окно»: первое окно, чей конец не раньше выбранной даты. ok=false, если такого
// окна ещё нет.
func ResolveWindow(ws []Window, windowStart Date) (int, bool) {
	for i, w := range ws {
		if w.End().Compare(windowStart) >= 0 {
			return i, true
		}
	}
	return -1, false
}
