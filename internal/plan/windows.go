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
// Проход 1: цепочку окон строят только открывающие поступления (JoinsWindow=false): окно открывает самое раннее
// ещё не покрытое, поступление внутри окна добавляется к нему, позже конца — открывает новое. Проход 2 (ADR-019):
// обычные приходы (JoinsWindow=true) окон не создают и не двигают, а кладутся в окно по дате: внутри окна — в него,
// в зазоре — в следующее, после последнего — в последнее, до первого — в первое. Если открывающих нет вовсе, первый
// обычный приход открывает окно: иначе деньги исчезли бы с экрана. Обычные добавляются после открывающих, поэтому
// Incomes[0] остаётся якорем окна.
//
// Сортировка (дата, факт раньше плана, ключ) делает результат независимым от порядка входа. Вызывающий отдаёт ВСЕ
// поступления с начала: цепочка окон зависит от точки отсечки истории.
func BuildWindows(incomes []Event) []Window {
	ws := []Window{}
	var opening, joining []Event
	for _, e := range incomes {
		if e.Kind != KindIncome || e.Date == nil {
			continue
		}
		if e.JoinsWindow {
			joining = append(joining, e)
		} else {
			opening = append(opening, e)
		}
	}
	sortIncomes(opening)
	sortIncomes(joining)
	for _, e := range opening {
		if n := len(ws); n > 0 && e.Date.Compare(ws[n-1].End()) <= 0 {
			ws[n-1].Incomes = append(ws[n-1].Incomes, e)
			continue
		}
		ws = append(ws, Window{Start: *e.Date, Incomes: []Event{e}})
	}
	if len(ws) == 0 && len(joining) > 0 {
		ws = append(ws, Window{Start: *joining[0].Date, Incomes: []Event{joining[0]}})
		joining = joining[1:]
	}
	for _, e := range joining {
		i := windowIndexFor(ws, *e.Date)
		ws[i].Incomes = append(ws[i].Incomes, e)
	}
	return ws
}

func sortIncomes(src []Event) {
	sort.SliceStable(src, func(i, j int) bool {
		if c := src[i].Date.Compare(*src[j].Date); c != 0 {
			return c < 0
		}
		if src[i].State != src[j].State {
			return src[i].State == StateDone // факт раньше плана
		}
		return src[i].Key < src[j].Key
	})
}

// windowIndexFor — окно для обычного прихода с датой d (ws непусто): содержащее d; в зазоре — следующее; после
// последнего — последнее; до первого — первое.
func windowIndexFor(ws []Window, d Date) int {
	for i, w := range ws {
		if d.Compare(w.Start) < 0 {
			return i // до первого окна или в зазоре перед окном i
		}
		if d.Compare(w.End()) <= 0 {
			return i
		}
	}
	return len(ws) - 1
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
