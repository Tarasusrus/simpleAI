package plan

import (
	"math"
	"sort"
)

// UncategorizedName — строка трат без категории. Показывается всегда последней и
// только если по ней есть траты.
const UncategorizedName = "Без категории"

// recentLimit — сколько последних трат категории отдавать для раскрытия строки.
const recentLimit = 5

// rowOf собирает строку списка из события. amount — уже в валюте показа или nil.
func rowOf(e Event, amount *int64) Row {
	return Row{Key: e.Key, Date: e.Date, Name: e.Name, Kind: e.Kind, Amount: amount, NoAmount: e.Amount == nil}
}

// windowIndexes — какие окна дают колонки «Сейчас» и «Прошлое окно» (−1 = нет).
// current/upcoming: сейчас — описываемое окно, прошлое — предыдущее по списку;
// between/ended: «Сейчас» пусто, прошлое — последнее закончившееся окно.
func (c calc) windowIndexes() (now, last int) {
	switch c.st.Kind {
	case StateCurrent, StateUpcoming:
		now, last = c.st.Index, c.st.Index-1
	case StateBetween, StateEnded:
		now, last = -1, c.st.Index
	default:
		now, last = -1, c.hiddenLast
	}
	if last < 0 {
		last = -1
	}
	return now, last
}

// lastWindow — описание окна «Прошлое окно» (nil, если его нет).
func (c calc) lastWindow() *WindowInfo {
	_, last := c.windowIndexes()
	if last < 0 || last >= len(c.in.Windows) {
		return nil
	}
	w := c.in.Windows[last]
	return &WindowInfo{Start: w.Start, End: w.End()}
}

func (c calc) contains(idx int, d Date) bool {
	if idx < 0 || idx >= len(c.in.Windows) {
		return false
	}
	w := c.in.Windows[idx]
	return d.Compare(w.Start) >= 0 && d.Compare(w.End()) <= 0
}

// categories считает траты (оплаченные платежи) по категориям за прошлое и текущее
// окна. «Прошлое окно» — окно с индексом i−1 списка, а не «последние 14 дней»:
// между окнами бывает зазор.
func (c calc) categories() ([]CategoryRow, error) {
	nowIdx, lastIdx := c.windowIndexes()
	byName := map[string]*CategoryRow{}
	recent := map[string][]Event{}
	amounts := map[string]int64{} // ключ события → сумма в валюте показа
	avgSums := map[string]int64{}
	avgFrom := c.in.Today.AddDays(-(c.in.AvgDays - 1))
	for _, e := range c.in.Events {
		if e.State != StateDone || e.Date == nil || e.Amount == nil {
			continue
		}
		inNow, inLast := c.contains(nowIdx, *e.Date), c.contains(lastIdx, *e.Date)
		inAvg := e.Date.Compare(avgFrom) >= 0 && e.Date.Compare(c.in.Today) <= 0
		if !inNow && !inLast && !inAvg {
			continue
		}
		n, err := c.money(e.Amount)
		if err != nil {
			return nil, err
		}
		name := e.Category
		if name == "" {
			name = UncategorizedName
		}
		row := byName[name]
		if row == nil {
			row = &CategoryRow{Name: name}
			byName[name] = row
		}
		if inNow {
			row.Now += n
		}
		if inLast {
			row.Last += n
		}
		if inAvg {
			var aerr error
			if avgSums[name], aerr = addI64(avgSums[name], n); aerr != nil {
				return nil, aerr
			}
		}
		recent[name] = append(recent[name], e)
		amounts[e.Key] = n
	}
	var out []CategoryRow
	var uncategorized *CategoryRow
	for name, row := range byName {
		if sum := avgSums[name]; sum != 0 {
			if sum > math.MaxInt64/windowDays {
				return nil, ErrOverflow
			}
			row.Avg = roundDivHalfAway(sum*windowDays, int64(c.in.AvgDays))
		}
		evs := recent[name]
		sort.SliceStable(evs, func(i, j int) bool {
			if cmp := evs[i].Date.Compare(*evs[j].Date); cmp != 0 {
				return cmp > 0 // новее — раньше
			}
			return evs[i].Key < evs[j].Key
		})
		if len(evs) > recentLimit {
			evs = evs[:recentLimit]
		}
		for _, e := range evs {
			n := amounts[e.Key]
			row.Recent = append(row.Recent, rowOf(e, &n))
		}
		if name == UncategorizedName {
			uncategorized = row
			continue
		}
		out = append(out, *row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Avg != b.Avg:
			return a.Avg > b.Avg
		case a.Last != b.Last:
			return a.Last > b.Last
		case a.Now != b.Now:
			return a.Now > b.Now
		}
		return a.Name < b.Name
	})
	if uncategorized != nil {
		out = append(out, *uncategorized)
	}
	return out, nil
}
