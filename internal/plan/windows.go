package plan

import "sort"

// windowDays — длина нового окна по умолчанию: поступления у владельца приходят примерно раз в две недели.
// Сохранённое окно может быть другой длины (ADR-020, ADR-021).
const windowDays = 14

// Window — окно плана (ADR-021): сохранённое (ID не пуст) или расчётное — ещё не начавшееся окно, которое
// открыло поступление и которое сохранится в день начала. Хранит только поступления: платежи по окнам раскладывает
// Compute.
type Window struct {
	// ID — id сохранённого окна; пусто — окно расчётное.
	ID      string
	Start   Date
	Incomes []Event
	// Length — длина в днях; 0 — по умолчанию (windowDays).
	Length int
}

// Days — длина окна в днях.
func (w Window) Days() int {
	if w.Length > 0 {
		return w.Length
	}
	return windowDays
}

// Adjusted — длина окна отличается от стандартной.
func (w Window) Adjusted() bool { return w.Days() != windowDays }

// Stored — окно сохранено в базе.
func (w Window) Stored() bool { return w.ID != "" }

// End — последний день окна включительно (Start + Days − 1).
func (w Window) End() Date { return w.Start.AddDays(w.Days() - 1) }

// AnchorPlanned — окно открыто ожидаемым, а не пришедшим поступлением.
func (w Window) AnchorPlanned() bool {
	return len(w.Incomes) > 0 && w.Incomes[0].State == StatePlanned
}

// StoredWindow — окно из базы. Deleted — владелец удалил окно: на его датах окно само больше не появляется,
// а поступления этих дат попадают в соседние окна по дате, как обычный приход.
type StoredWindow struct {
	ID         string
	Start, End Date
	Deleted    bool
}

func (sw StoredWindow) contains(d Date) bool {
	return d.Compare(sw.Start) >= 0 && d.Compare(sw.End) <= 0
}

// BuildWindows строит окна из сохранённых окон и поступлений (платежи и события без даты пропускает).
//
// Сохранённые окна главные (ADR-021): их даты не зависят от поступлений, и это история — от начала первого до конца
// последнего сохранённого окна. Новые расчётные окна появляются только вне истории: после её конца — как раньше; до её
// начала — только если все 14 дней окна помещаются до первого сохранённого (так окна до выкатки ADR-021 считаются и
// сохраняются при первом чтении, а не сливаются с перенесённым окном; а зарплата чуть раньше сдвинутого начала окна
// остаётся в нём). Открывающее поступление (JoinsWindow=false) внутри сохранённого окна
// добавляется к нему; позже последнего сохранённого окна — открывает расчётное окно на 14 дней (как до ADR-021).
// История — все сохранённые окна, включая удалённые. Открывающее поступление внутри истории (в зазоре, до начала
// сдвинутого окна, на датах удалённого окна) окно не открывает и ведёт себя как обычный приход: иначе сдвиг начала окна плодил бы рядом крошечные окна. Обычные приходы (JoinsWindow=true, ADR-019) кладутся в окно по дате:
// внутри окна — в него, в зазоре — в следующее, после последнего — в последнее, до первого — в первое. Если окон нет
// вовсе и нет истории, первое поступление открывает окно: иначе деньги исчезли бы с экрана. Обычные добавляются после открывающих,
// поэтому Incomes[0] остаётся якорем окна.
//
// Сортировка (дата, факт раньше плана, ключ) делает результат независимым от порядка входа. Вызывающий отдаёт ВСЕ
// поступления с начала: цепочка расчётных окон зависит от точки отсечки истории.
func BuildWindows(incomes []Event, stored ...StoredWindow) []Window {
	var active, deleted []StoredWindow
	for _, sw := range stored {
		if sw.Deleted {
			deleted = append(deleted, sw)
		} else if sw.End.Compare(sw.Start) >= 0 {
			active = append(active, sw)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].Start.Compare(active[j].Start) < 0 })
	ws := make([]Window, 0, len(active))
	var lastEnd *Date
	for _, sw := range active {
		if lastEnd != nil && sw.Start.Compare(*lastEnd) <= 0 {
			continue // пересекающиеся окна разных чатов области: оставляем раннее, расчёт не падает
		}
		ws = append(ws, Window{ID: sw.ID, Start: sw.Start, Length: DaysBetween(sw.Start, sw.End) + 1})
		end := sw.End
		lastEnd = &end
	}
	// История — все сохранённые окна, и удалённые тоже: удаление окна не должно освобождать его зарплату, чтобы та
	// открыла окно заново.
	var historyStart, historyEnd *Date
	for _, sw := range stored {
		if sw.Deleted || sw.End.Compare(sw.Start) >= 0 {
			if historyStart == nil || sw.Start.Compare(*historyStart) < 0 {
				start := sw.Start
				historyStart = &start
			}
			if historyEnd == nil || sw.End.Compare(*historyEnd) > 0 {
				end := sw.End
				historyEnd = &end
			}
		}
	}

	var opening, joining []Event
	for _, e := range incomes {
		if e.Kind != KindIncome || e.Date == nil {
			continue
		}
		inHistory := historyEnd != nil && e.Date.AddDays(windowDays-1).Compare(*historyStart) >= 0 && e.Date.Compare(*historyEnd) <= 0
		if !e.JoinsWindow && (inDeleted(deleted, *e.Date) || inHistory && !inAny(ws, *e.Date)) {
			e.JoinsWindow = true // ведёт себя как обычный приход: в окно по дате, и Compute не ждёт его в границах окна
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
		if i, ok := containing(ws, *e.Date); ok {
			ws[i].Incomes = append(ws[i].Incomes, e)
			continue
		}
		ws = insertWindow(ws, provisional(*e.Date, e))
	}
	// Окон нет вовсе и истории нет — первое поступление открывает окно, иначе деньги исчезли бы с экрана. С историей
	// (например, все окна удалены) окно само не воскресает.
	if len(ws) == 0 && historyEnd == nil && len(joining) > 0 {
		ws = append(ws, provisional(*joining[0].Date, joining[0]))
		joining = joining[1:]
	}
	if len(ws) == 0 {
		return ws // все окна удалены: поступлениям некуда встать, экран покажет «окон нет»
	}
	for _, e := range joining {
		i := windowIndexFor(ws, *e.Date)
		ws[i].Incomes = append(ws[i].Incomes, e)
	}
	return ws
}

func inAny(ws []Window, d Date) bool {
	_, ok := containing(ws, d)
	return ok
}

func inDeleted(deleted []StoredWindow, d Date) bool {
	for _, sw := range deleted {
		if sw.contains(d) {
			return true
		}
	}
	return false
}

// containing — индекс окна, в которое попадает d.
func containing(ws []Window, d Date) (int, bool) {
	for i, w := range ws {
		if d.Compare(w.Start) >= 0 && d.Compare(w.End()) <= 0 {
			return i, true
		}
	}
	return -1, false
}

// provisional — расчётное окно с d на 14 дней.
func provisional(d Date, anchor Event) Window { return Window{Start: d, Incomes: []Event{anchor}} }

// insertWindow вставляет окно, сохраняя порядок по началу: окно до истории встаёт перед сохранёнными.
func insertWindow(ws []Window, w Window) []Window {
	i := sort.Search(len(ws), func(i int) bool { return ws[i].Start.Compare(w.Start) > 0 })
	ws = append(ws, Window{})
	copy(ws[i+1:], ws[i:])
	ws[i] = w
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
		return WindowState{StateUpcoming, 0, ws[0].Days()}
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
