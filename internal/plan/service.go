package plan

import (
	"context"
	"errors"
	"fmt"
)

// lookbackDays — на сколько дней назад разворачиваются сроки правил без факта.
// Дальше ждать «пришло» или воркера смысла нет; число то же, что у догона воркера.
const lookbackDays = 62

// horizonMonths — сколько месяцев вперёд видит «позже» (m0..m5).
const horizonMonths = 6

// ErrInvariant — итог расчёта не сходится с его слагаемыми. Это ошибка кода, а не
// данных: экран не должен показывать число, которому нельзя верить (C28).
var ErrInvariant = errors.New("plan: итог не сходится со слагаемыми")

// Clock отдаёт «сегодня» — календарный день в зоне плана. Один источник для
// экрана, бота и воркера; домен часов не читает.
type Clock func() Date

// Meta — служебные сведения к расчёту.
type Meta struct {
	Today       Date
	RatesManual bool
	HasRate     bool
	// Skipped — записи, не вошедшие в план (чужая валюта, поступление без даты).
	Skipped int
}

// Result — расчёт окна вместе со служебными сведениями.
type Result struct {
	View View
	Meta Meta
}

// Service — единственная точка входа для экрана и бота: читает данные, разворачивает
// повторы, считает окно. Ни веб, ни Telegram «можно тратить» сами не считают.
type Service struct {
	reader Reader
	today  Clock
	scope  Scope
}

// NewService собирает сервис; все зависимости приходят снаружи.
func NewService(r Reader, today Clock, sc Scope) *Service {
	return &Service{reader: r, today: today, scope: sc}
}

// View считает окно, в которое попадает «сегодня», в валюте показа cur.
func (s *Service) View(ctx context.Context, cur Currency) (Result, error) {
	snap, err := s.reader.Read(ctx, s.scope)
	if err != nil {
		return Result{}, err
	}
	today := s.today()
	if !today.Valid() {
		return Result{}, fmt.Errorf("%w: сегодня %v", ErrBadDate, today)
	}

	from := today.AddDays(-lookbackDays)
	to := Date{today.Year, today.Month, 1}.AddMonths(horizonMonths).AddDays(-1)
	expanded, err := Expand(snap.Rules, from, to)
	if err != nil {
		return Result{}, err
	}
	expanded = dropCoveredTerms(expanded, snap.Incomes, snap.Payments)

	var incomes, payments []Event
	incomes = append(incomes, snap.Incomes...)
	payments = append(payments, snap.Payments...)
	for _, e := range expanded {
		if e.Kind == KindIncome {
			incomes = append(incomes, e)
		} else {
			payments = append(payments, e)
		}
	}

	v, err := Compute(ComputeInput{
		Windows:    BuildWindows(incomes),
		Events:     payments,
		Rate:       snap.Rate,
		Cur:        cur,
		Today:      today,
		ReservePct: snap.ReservePct,
	})
	if err != nil {
		return Result{}, err
	}
	if err := checkInvariant(v); err != nil {
		return Result{}, err
	}
	return Result{
		View: v,
		Meta: Meta{Today: today, RatesManual: snap.RateManual, HasRate: snap.Rate != nil, Skipped: snap.Skipped},
	}, nil
}

// dropCoveredTerms убирает плановые сроки правил, для которых уже есть факт:
// на срок остаётся либо план, либо факт (иначе Compute вернул бы ErrDuplicateTerm).
func dropCoveredTerms(planned []Event, facts ...[]Event) []Event {
	covered := map[termKey]bool{}
	for _, list := range facts {
		for _, e := range list {
			if e.RuleID != "" && e.ScheduledFor != nil {
				covered[termKey{e.RuleID, e.ScheduledFor.String()}] = true
			}
		}
	}
	out := planned[:0:0]
	for _, e := range planned {
		if e.ScheduledFor != nil && covered[termKey{e.RuleID, e.ScheduledFor.String()}] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// checkInvariant: canSpend = income − payments − spent − reserve.
func checkInvariant(v View) error {
	if v.State == StateNone || v.Window == nil {
		return nil
	}
	want := v.Income.Received + v.Income.Expected - v.Payments - v.Spent - v.Reserve
	if v.CanSpend != want {
		return fmt.Errorf("%w: canSpend=%d, по слагаемым %d", ErrInvariant, v.CanSpend, want)
	}
	if v.PerDay != nil && (v.CanSpend < 0 || *v.PerDay < 0) {
		return fmt.Errorf("%w: perDay при недостаче", ErrInvariant)
	}
	return nil
}
