package plan

import (
	"fmt"
	"math"
)

// ComputeInput — всё, что нужно расчёту. Events — ТОЛЬКО платежи: поступления
// окна берутся из Windows[i].Incomes, иначе они удвоились бы.
type ComputeInput struct {
	Windows    []Window
	Events     []Event
	Rate       *Rate
	Cur        Currency // валюта показа
	Today      Date
	ReservePct int // 0..100
	// HideEnded — не описывать закончившееся последнее окно: вид как при «окна нет»,
	// но «Прошлое окно» в категориях остаётся (владелец планирует будущее).
	HideEnded bool
	// AvgDays — за сколько дней считать «обычные» траты по категориям: 7..730,
	// 0 = defaultAvgDays.
	AvgDays int
	// Limits — лимиты категорий (ADR-011); выбор действующих на окно делает Compute.
	Limits []Limit
	// Dismissed — названия скрытых категорий: их строки уходят из View.Unplanned в View.Hidden.
	Dismissed []string
}

// defaultAvgDays — окно усреднения «обычных» трат по умолчанию.
const defaultAvgDays = 90

// Границы окна усреднения: меньше недели бессмысленно, больше двух лет — не «обычно».
const (
	minAvgDays = 7
	maxAvgDays = 730
)

// termKey — срок правила: по нему план и факт одного срока не могут сосуществовать.
type termKey struct{ rule, date string }

// Compute считает окно, в которое попадает «сегодня». Ошибка → пустой View и err.
func Compute(in ComputeInput) (View, error) {
	if in.ReservePct < 0 || in.ReservePct > 100 {
		return View{}, ErrBadReservePct
	}
	if !in.Cur.Supported() {
		return View{}, ErrUnsupportedCurrency
	}
	if in.Rate != nil && in.Rate.RubPerThbMicro <= 0 {
		return View{}, ErrBadRate
	}
	if err := checkInput(in); err != nil {
		return View{}, err
	}
	if in.AvgDays == 0 {
		in.AvgDays = defaultAvgDays
	}
	if in.AvgDays < minAvgDays || in.AvgDays > maxAvgDays {
		return View{}, ErrBadAvgDays
	}
	st := Locate(in.Windows, in.Today)
	if st.Kind == StateNone || (st.Kind == StateEnded && in.HideEnded) {
		// Поступлений ещё нет (или окно закончилось и скрыто), но платежи, которые
		// владелец уже внёс («Права», «Без даты», дальние), пропадать не должны:
		// показываем блоки без расчёта.
		c := calc{in: in, st: WindowState{StateNone, -1, 0}, hiddenLast: -1}
		if st.Kind == StateEnded {
			c.hiddenLast = st.Index
		}
		v := View{State: StateNone, AvgDays: in.AvgDays}
		if err := c.blocks(&v); err != nil {
			return View{}, err
		}
		var err error
		if v.Categories, err = c.categories(); err != nil {
			return View{}, err
		}
		v.LastWindow = c.lastWindow()
		return v, nil
	}
	c := calc{in: in, st: st, w: in.Windows[st.Index], hiddenLast: -1}
	v, err := c.core()
	if err != nil {
		return View{}, err
	}
	return v, nil
}

// checkWindows проверяет контракт окон: по возрастанию, без пересечений, с
// корректным стартом, и только поступления внутри своего окна. Locate и
// ResolveWindow на других окнах молча дали бы неверное состояние.
func checkWindows(ws []Window) error {
	for i, w := range ws {
		if !w.Start.Valid() {
			return fmt.Errorf("%w: окно %d с несуществующим стартом %v", ErrInvalidEvent, i, w.Start)
		}
		if i > 0 && w.Start.Compare(ws[i-1].End()) <= 0 {
			return fmt.Errorf("%w: окна %d и %d не по порядку или пересекаются", ErrInvalidEvent, i-1, i)
		}
		for _, e := range w.Incomes {
			if e.Kind != KindIncome {
				return fmt.Errorf("%w: в окне %d не поступление: %q", ErrInvalidEvent, i, e.Key)
			}
			if e.Date != nil && (e.Date.Compare(w.Start) < 0 || e.Date.Compare(w.End()) > 0) {
				return fmt.Errorf("%w: поступление %q вне окна %d", ErrInvalidEvent, e.Key, i)
			}
		}
	}
	return nil
}

func checkInput(in ComputeInput) error {
	if err := checkWindows(in.Windows); err != nil {
		return err
	}
	keys := map[string]bool{}
	terms := map[termKey]bool{}
	addTerm := func(e Event) error {
		if e.RuleID == "" || e.ScheduledFor == nil {
			return nil
		}
		k := termKey{e.RuleID, e.ScheduledFor.String()}
		if terms[k] {
			return fmt.Errorf("%w: правило %s, срок %v", ErrDuplicateTerm, e.RuleID, *e.ScheduledFor)
		}
		terms[k] = true
		return nil
	}
	for _, e := range in.Events {
		if e.Kind != KindPayment {
			return fmt.Errorf("%w: в Events только платежи, а не %q", ErrInvalidEvent, e.Kind)
		}
		if err := e.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidEvent, err)
		}
		if keys[e.Key] {
			return fmt.Errorf("%w: повтор ключа %q", ErrInvalidEvent, e.Key)
		}
		keys[e.Key] = true
	}
	for _, w := range in.Windows {
		for _, e := range w.Incomes {
			if err := e.Validate(); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidEvent, err)
			}
			if keys[e.Key] {
				return fmt.Errorf("%w: повтор ключа %q", ErrInvalidEvent, e.Key)
			}
			keys[e.Key] = true
		}
	}
	for _, e := range in.Events {
		if err := addTerm(e); err != nil {
			return err
		}
	}
	for _, w := range in.Windows {
		for _, e := range w.Incomes {
			if err := addTerm(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// calc держит контекст расчёта одного окна.
type calc struct {
	in ComputeInput
	st WindowState
	w  Window
	// hiddenLast — индекс скрытого закончившегося окна (для «Прошлого окна»), −1 нет.
	hiddenLast int
}

func (c calc) money(m *Money) (int64, error) { return Convert(c.in.Rate, *m, c.in.Cur) }

// overdue — разовый плановый платёж с датой раньше сегодня. Повторяющиеся
// просроченными не бывают: факт создаёт воркер в день срока.
func (c calc) overdue(e Event) bool {
	return e.State == StatePlanned && !e.IsRecurring() && e.Date != nil && e.Date.Compare(c.in.Today) < 0
}

func (c calc) overdueDays(e Event) int {
	if !c.overdue(e) {
		return 0
	}
	return DaysBetween(*e.Date, c.in.Today)
}

func (c calc) inWindowByDate(d Date) bool {
	if c.st.Kind == StateNone {
		return false // окна нет — нечему содержать дату
	}
	return d.Compare(c.w.Start) >= 0 && d.Compare(c.w.End()) <= 0
}

// plannedInThisWindow — относится ли плановый платёж к описываемому окну.
// Приоритет, платёж считается ОДИН раз: (1) просроченный разовый при current —
// платёж текущего окна независимо от даты; (2) дата внутри окна; (3) дата не
// задана, выбранное окно резолвится в это.
func (c calc) plannedInThisWindow(e Event) bool {
	switch {
	case e.Date != nil && c.st.Kind == StateCurrent && c.overdue(e):
		return true
	case e.Date != nil:
		return c.inWindowByDate(*e.Date)
	case e.WindowStart != nil:
		idx, ok := ResolveWindow(c.in.Windows, *e.WindowStart)
		return ok && idx == c.st.Index
	}
	return false
}

func (c calc) core() (View, error) {
	v := View{
		State: c.st.Kind,
		Window: &WindowInfo{Start: c.w.Start, End: c.w.End(), DaysLeft: c.st.DaysLeft,
			AnchorPlanned: c.w.AnchorPlanned()},
	}
	var err error
	for i := range c.w.Incomes {
		e := c.w.Incomes[i]
		n, cerr := c.money(e.Amount)
		if cerr != nil {
			return View{}, cerr
		}
		if e.State == StateDone {
			v.Income.Received, err = addI64(v.Income.Received, n)
		} else {
			v.Income.Expected, err = addI64(v.Income.Expected, n)
		}
		if err != nil {
			return View{}, err
		}
	}
	for _, e := range c.in.Events {
		switch {
		case e.State == StateDone:
			if e.Date == nil || !c.inWindowByDate(*e.Date) {
				continue
			}
			n, cerr := c.money(e.Amount)
			if cerr != nil {
				return View{}, cerr
			}
			if v.Spent, err = addI64(v.Spent, n); err != nil {
				return View{}, err
			}
		case c.plannedInThisWindow(e):
			if e.Amount == nil {
				v.Unpriced++
				continue
			}
			n, cerr := c.money(e.Amount)
			if cerr != nil {
				return View{}, cerr
			}
			if v.Payments, err = addI64(v.Payments, n); err != nil {
				return View{}, err
			}
		}
	}
	if v.SpentBefore, v.SpentBeforeFrom, err = c.spentBeforeWindow(); err != nil {
		return View{}, err
	}
	income, err := addI64(v.Income.Received, v.Income.Expected)
	if err != nil {
		return View{}, err
	}
	if income > math.MaxInt64/100 {
		return View{}, ErrOverflow
	}
	v.Reserve = roundDivHalfAway(income*int64(c.in.ReservePct), 100)
	if v.Limits, v.LimitHold, err = c.limits(); err != nil {
		return View{}, err
	}
	if v.Unplanned, v.Hidden, v.UnplannedSpent, err = c.unplanned(); err != nil {
		return View{}, err
	}
	out := income - v.Payments - v.Spent - v.Reserve - v.LimitHold
	v.CanSpend = out
	v.LimitsExceedFree = v.LimitHold > max(out+v.LimitHold, 0)
	if out >= 0 && c.st.DaysLeft > 0 {
		per := out / int64(c.st.DaysLeft) // floor: не обещаем больше, чем есть
		v.PerDay = &per
	}
	if v.Categories, err = c.categories(); err != nil {
		return View{}, err
	}
	v.AvgDays = c.in.AvgDays
	v.LastWindow = c.lastWindow()
	if err := c.blocks(&v); err != nil {
		return View{}, err
	}
	return v, nil
}

// roundDivHalfAway — num/den с округлением от нуля (num ≥ 0, den > 0).
func roundDivHalfAway(num, den int64) int64 {
	q, r := num/den, num%den
	if r >= den-r {
		q++
	}
	return q
}

// addI64 — сумма с защитой от переполнения: итог денег не должен тихо обернуться.
func addI64(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// spentBeforeWindow — траты в зазоре между предыдущим окном и ещё не начавшимся описываемым:
// они не входят ни в одно окно и без этой суммы пропадали бы с экрана. from — первый день зазора.
func (c calc) spentBeforeWindow() (int64, *Date, error) {
	if c.st.Kind == StateNone {
		return 0, nil, nil
	}
	var prevEnd *Date
	switch {
	case c.st.Kind == StateBetween:
		// Описано закончившееся окно: зазор идёт после него до сегодня.
		e := c.w.End()
		prevEnd = &e
	case c.in.Today.Compare(c.w.Start) < 0:
		for i, w := range c.in.Windows {
			if w.Start == c.w.Start && i > 0 {
				e := c.in.Windows[i-1].End()
				prevEnd = &e
			}
		}
	}
	if prevEnd == nil {
		return 0, nil, nil
	}
	from := prevEnd.AddDays(1)
	var sum int64
	for _, e := range c.in.Events {
		if e.Kind != KindPayment || e.State != StateDone || e.Date == nil || e.Amount == nil {
			continue
		}
		if e.Date.Compare(*prevEnd) <= 0 || e.Date.Compare(c.in.Today) > 0 || (c.st.Kind != StateBetween && e.Date.Compare(c.w.Start) >= 0) {
			continue
		}
		n, err := c.money(e.Amount)
		if err != nil {
			return 0, nil, err
		}
		if sum, err = addI64(sum, n); err != nil {
			return 0, nil, err
		}
	}
	return sum, &from, nil
}
