package plan

import (
	"sort"
	"strings"
)

// laterMonths — горизонт блока «Позже»: месяцы m0..m5 от месяца «сегодня».
const laterMonths = 6

// item — строка плюс данные для сортировки.
type item struct {
	row     Row
	title   string // название в нижнем регистре: «Альфа» и «альфа» равны
	group   int    // 0 просроченные, 1 с датой, 2 без точной даты («в это окно»)
	kindOrd int    // 0 поступление-факт, 1 поступление-план, 2 платёж
}

func newItem(row Row, group, kindOrd int) item {
	return item{row: row, title: strings.ToLower(row.Name), group: group, kindOrd: kindOrd}
}

// sortItems — детерминированный порядок ленты (иначе golden JSON «мигает»):
// просроченные по дате ↑; затем по дате, при равной дате поступление-факт,
// поступление-план, платёж; затем имя без регистра; затем Key. Платежи «в это
// окно» без даты — после датированных.
func sortItems(items []item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.group != b.group {
			return a.group < b.group
		}
		if a.row.Date != nil && b.row.Date != nil {
			if c := a.row.Date.Compare(*b.row.Date); c != 0 {
				return c < 0
			}
		}
		if a.group == 1 && a.kindOrd != b.kindOrd {
			return a.kindOrd < b.kindOrd
		}
		if a.title != b.title {
			return a.title < b.title
		}
		return a.row.Key < b.row.Key
	})
}

func rowsOf(items []item) []Row {
	sortItems(items)
	out := make([]Row, 0, len(items))
	for _, it := range items {
		out = append(out, it.row)
	}
	return out
}

// nextIndex — индекс «следующего» окна относительно описываемого (−1, если нет).
func (c calc) nextIndex() int {
	switch c.st.Kind {
	case StateCurrent, StateUpcoming, StateBetween:
		if n := c.st.Index + 1; n < len(c.in.Windows) {
			return n
		}
	case StateEnded, StateNone:
		// после последнего окна следующего нет
	}
	return -1
}

func monthIndex(d Date) int { return d.Year*12 + d.Month - 1 }

// rowOfPayment — строка платежа (сумма переводится в валюту показа один раз).
func (c calc) rowOfPayment(e Event) (Row, error) {
	var amt *int64
	if e.Amount != nil {
		n, err := c.money(e.Amount)
		if err != nil {
			return Row{}, err
		}
		amt = &n
	}
	r := rowOf(e, amt)
	r.OverdueDays = c.overdueDays(e)
	return r, nil
}

// blocks раскладывает платежи по блокам экрана: лента окна, следующее окно,
// «позже» по месяцам, «без даты». Каждый платёж попадает ровно в один блок.
func (c calc) blocks(v *View) error {
	var timeline, undated, next []item
	later := map[int][]item{}
	nextIdx := c.nextIndex()

	addNext := func(r Row, g int) { next = append(next, newItem(r, g, 2)) }
	addLater := func(r Row, monthDate Date, g int) {
		m := monthIndex(monthDate)
		if m0 := monthIndex(c.in.Today); m < m0 || m > m0+laterMonths-1 {
			return // за горизонтом
		}
		later[m] = append(later[m], newItem(r, g, 2))
	}

	for _, e := range c.in.Events {
		row, err := c.rowOfPayment(e)
		if err != nil {
			return err
		}
		if e.State == StateDone {
			continue // лента — что ещё впереди; закрытое смотрится в деталях категорий
		}
		group := 1
		if row.OverdueDays > 0 {
			group = 0
		}
		switch {
		case c.plannedInThisWindow(e):
			if e.Date == nil {
				group = 2 // «в это окно» без точной даты
			}
			timeline = append(timeline, newItem(row, group, 2))
		case e.Date != nil && row.OverdueDays > 0:
			// Просрочен, но вне расчёта (окна нет или не current): виден в ленте.
			timeline = append(timeline, newItem(row, 0, 2))
		case e.Date != nil && e.Date.Compare(c.in.Today) < 0:
			// Прошлое повторяющееся вне окон: факт создаёт воркер, в плане его нет.
		case e.Date != nil:
			if nextIdx >= 0 && c.contains(nextIdx, *e.Date) {
				addNext(row, 1)
			} else {
				addLater(row, *e.Date, 1)
			}
		case e.WindowStart == nil:
			undated = append(undated, newItem(row, 2, 2))
		default:
			idx, ok := ResolveWindow(c.in.Windows, *e.WindowStart)
			switch {
			case !ok:
				row.WindowMissing = true
				undated = append(undated, newItem(row, 2, 2))
			case idx < c.st.Index || c.hiddenLast >= 0 && idx <= c.hiddenLast:
				row.WindowPassed = true
				undated = append(undated, newItem(row, 2, 2))
			case idx == nextIdx:
				addNext(row, 2)
			default:
				addLater(row, c.in.Windows[idx].Start, 2)
			}
		}
	}

	for _, e := range c.w.Incomes {
		n, err := c.money(e.Amount)
		if err != nil {
			return err
		}
		r := rowOf(e, &n)
		r.Expected = e.State == StatePlanned
		if !r.Expected {
			continue // пришедшее — уже в «пришло» шапки окна, в ленте не нужно
		}
		r.NotArrived = r.Expected && e.Date.Compare(c.in.Today) < 0
		ord := 0
		if r.Expected {
			ord = 1
		}
		timeline = append(timeline, newItem(r, 1, ord))
	}

	v.Timeline = rowsOf(timeline)
	v.Undated = undatedBlock(rowsOf(undated))
	if nextIdx >= 0 && len(next) > 0 {
		count, sum := countAndSum(next)
		v.Next = &NextBlock{Count: count, Sum: sum, Items: rowsOf(next)}
	}
	months := make([]int, 0, len(later))
	for m := range later {
		months = append(months, m)
	}
	sort.Ints(months)
	for _, m := range months {
		count, sum := countAndSum(later[m])
		v.Later = append(v.Later, MonthBucket{Year: m / 12, Month: m%12 + 1, Count: count, Sum: sum, Items: rowsOf(later[m])})
	}
	return nil
}

// countAndSum: в счётчик входят и платежи без суммы, в сумму — только с суммой.
func countAndSum(items []item) (int, int64) {
	var sum int64
	for _, it := range items {
		if it.row.Amount != nil {
			sum += *it.row.Amount
		}
	}
	return len(items), sum
}

func undatedBlock(rows []Row) UndatedBlock {
	b := UndatedBlock{Count: len(rows), Items: rows}
	for _, r := range rows {
		if r.Amount != nil {
			b.PricedCount++
			b.Sum += *r.Amount
		}
	}
	return b
}
