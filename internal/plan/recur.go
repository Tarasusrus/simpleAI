package plan

import (
	"errors"
	"fmt"
	"sort"
)

// Unit — единица шага повтора.
type Unit string

const (
	UnitDay   Unit = "day"
	UnitWeek  Unit = "week"
	UnitMonth Unit = "month"
	UnitYear  Unit = "year"
)

// maxOccurrences — защита от случайно огромных рядов (ежедневное правило с давним
// якорем на годы вперёд). Next потолка не имеет: он считает арифметикой.
const maxOccurrences = 1000

// Rule — правило повтора «каждые N единиц» от якоря.
//
// n-й срок = Anchor + n·N с прижатием к концу месяца, а НЕ цепочка от предыдущего
// срока: цепочка 31.01 → 28.02 → 28.03 навсегда теряет «31», а якорная формула даёт
// 31.01, 28.02, 31.03.
type Rule struct {
	Anchor Date
	N      int
	Unit   Unit
	// DayOfMonth — день месяца для month/year; если задан, главнее дня якоря.
	DayOfMonth *int
	Until      *Date // включительно
	Amount     Money
}

// RuleWithExceptions — правило с метаданными для разворачивания в события.
// Exceptions — пропущенные сроки: якорь при пропуске не двигается, срок просто
// вычитается, поэтому следующие сроки сохраняют день.
type RuleWithExceptions struct {
	ID, Name, Category string
	Kind               Kind
	Rule               Rule
	Exceptions         []Date
}

func (r Rule) check() error {
	switch {
	case !r.Anchor.Valid():
		return fmt.Errorf("%w: якорь %v", ErrBadRule, r.Anchor)
	case r.N < 1:
		return fmt.Errorf("%w: шаг N=%d", ErrBadRule, r.N)
	case r.Unit != UnitDay && r.Unit != UnitWeek && r.Unit != UnitMonth && r.Unit != UnitYear:
		return fmt.Errorf("%w: единица %q", ErrBadRule, r.Unit)
	case r.Amount.Minor <= 0 || !r.Amount.Cur.Supported():
		return fmt.Errorf("%w: сумма правила", ErrBadRule)
	case r.Until != nil && !r.Until.Valid():
		return fmt.Errorf("%w: until %v", ErrBadRule, *r.Until)
	}
	if r.DayOfMonth != nil {
		if r.Unit != UnitMonth && r.Unit != UnitYear {
			return fmt.Errorf("%w: день месяца допустим только для месяцев и лет", ErrBadRule)
		}
		if *r.DayOfMonth < 1 || *r.DayOfMonth > 31 {
			return fmt.Errorf("%w: день месяца %d", ErrBadRule, *r.DayOfMonth)
		}
	}
	return nil
}

func (r Rule) day() int {
	if r.DayOfMonth != nil {
		return *r.DayOfMonth
	}
	return r.Anchor.Day
}

// stepMonths — шаг в месяцах для month/year; 0 для day/week.
func (r Rule) stepMonths() int {
	switch r.Unit {
	case UnitMonth:
		return r.N
	case UnitYear:
		return r.N * 12
	case UnitDay, UnitWeek:
		return 0
	}
	return 0
}

// term — срок номер n (n ≥ 0). Может оказаться раньше якоря, если DayOfMonth
// меньше дня якоря: такие сроки пропускает вызывающий.
func (r Rule) term(n int) Date {
	switch r.Unit {
	case UnitDay:
		return r.Anchor.AddDays(n * r.N)
	case UnitWeek:
		return r.Anchor.AddDays(n * r.N * 7)
	case UnitMonth, UnitYear:
		return monthShift(r.Anchor, n*r.stepMonths(), r.day())
	}
	return r.Anchor // неизвестную единицу отсекает check()
}

// firstCandidate — номер, с которого безопасно искать первый срок строго после
// after: ниже него все сроки заведомо не позже after. Арифметика вместо перебора
// с начала, поэтому якорь пятилетней давности не упирается ни в какой потолок.
func (r Rule) firstCandidate(after Date) int {
	if after.Compare(r.Anchor) < 0 {
		return 0
	}
	var n int
	if r.Unit == UnitDay || r.Unit == UnitWeek {
		step := r.N
		if r.Unit == UnitWeek {
			step *= 7
		}
		n = DaysBetween(r.Anchor, after) / step
	} else {
		months := (after.Year-r.Anchor.Year)*12 + (after.Month - r.Anchor.Month)
		n = months/r.stepMonths() - 1
	}
	if n < 0 {
		n = 0
	}
	return n
}

// Next возвращает ближайший срок СТРОГО после after (иначе воркер зациклился бы
// на одном и том же сроке). ok=false, если срок позже Until.
func Next(r Rule, after Date) (Date, bool, error) {
	if err := r.check(); err != nil {
		return Date{}, false, err
	}
	t, ok := r.next(after)
	return t, ok, nil
}

// next — Next без повторной проверки правила: Occurrences проверяет его один раз,
// а не на каждом из тысячи сроков.
func (r Rule) next(after Date) (Date, bool) {
	for n := r.firstCandidate(after); ; n++ {
		t := r.term(n)
		if t.Compare(r.Anchor) < 0 || t.Compare(after) <= 0 {
			continue
		}
		if r.Until != nil && t.Compare(*r.Until) > 0 {
			return Date{}, false
		}
		return t, true
	}
}

// Occurrences — сроки на отрезке [from, to], обе границы включительно. Якорь входит
// как нулевой срок, если он не раньше from. Не больше maxOccurrences: при
// превышении возвращаются первые maxOccurrences и ErrTooMany.
func Occurrences(r Rule, from, to Date) ([]Date, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	if from.Compare(to) > 0 {
		return nil, nil
	}
	var out []Date
	t, ok := r.next(from.AddDays(-1))
	for ok && t.Compare(to) <= 0 {
		if len(out) == maxOccurrences {
			return out, ErrTooMany
		}
		out = append(out, t)
		t, ok = r.next(t)
	}
	return out, nil
}

// Expand разворачивает правила в плоские плановые события на [from, to]. Домену
// дальше всё равно, из правила событие или разовое: он видит только Event.
// Потолок maxOccurrences — на весь результат после вычитания исключений.
func Expand(rules []RuleWithExceptions, from, to Date) ([]Event, error) {
	var out []Event
	tooMany := false
	for _, re := range rules {
		if re.ID == "" || (re.Kind != KindIncome && re.Kind != KindPayment) {
			return nil, fmt.Errorf("%w: у правила нужны ID и направление", ErrBadRule)
		}
		ds, err := Occurrences(re.Rule, from, to)
		switch {
		case errors.Is(err, ErrTooMany):
			tooMany = true
		case err != nil:
			return nil, err
		}
		for _, d := range ds {
			if isException(re.Exceptions, d) {
				continue
			}
			amount, date, sched := re.Rule.Amount, d, d
			out = append(out, Event{
				Key:  "recurring:" + re.ID + "@" + d.String(),
				Kind: re.Kind, State: StatePlanned, Name: re.Name,
				Amount: &amount, Date: &date, ScheduledFor: &sched,
				Category: re.Category, Source: SourceRecurring, RuleID: re.ID,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if c := out[i].Date.Compare(*out[j].Date); c != 0 {
			return c < 0
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > maxOccurrences {
		out, tooMany = out[:maxOccurrences], true
	}
	if tooMany {
		return out, ErrTooMany
	}
	return out, nil
}

func isException(ex []Date, d Date) bool {
	for _, e := range ex {
		if e == d {
			return true
		}
	}
	return false
}
