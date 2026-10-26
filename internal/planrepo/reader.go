// Package planrepo — адаптер плана окна к хранилищу бюджета (budget.Store).
// Здесь, и только здесь, деньги и курс переводятся из базы в типы домена plan.
package planrepo

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// Source — то, что адаптеру нужно от budget.Store (узкий интерфейс, ISP).
type Source interface {
	PlanTransactions(ctx context.Context) ([]budget.PlanTx, error)
	PlanRecurringRules(ctx context.Context, chatIDs []int64) ([]budget.PlanRecurring, error)
	PlanPlannedItems(ctx context.Context, chatIDs []int64) ([]budget.PlanPlanned, error)
	PlanReservePct(ctx context.Context) (int, error)
	GetExchangeRates(ctx context.Context) (map[string]float64, error)
	GetRateSource(ctx context.Context, currency string) (budget.RateSource, bool, error)
}

// Reader реализует plan.Reader поверх Source.
type Reader struct {
	src    Source
	limits LimitSource     // nil — лимиты не читаются (WithLimits)
	dism   DismissedSource // nil — скрытые категории не читаются (WithDismissed)
	logger *slog.Logger
}

// NewReader собирает читателя; logger может быть nil.
func NewReader(src Source, logger *slog.Logger) *Reader {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reader{src: src, logger: logger}
}

var _ plan.Reader = (*Reader)(nil)

// Read собирает снимок. Пустая область — ошибка: иначе чат-таблицы молча
// прочитались бы «ни для кого» и экран показал бы пустой план.
func (r *Reader) Read(ctx context.Context, sc plan.Scope) (plan.Snapshot, error) {
	if len(sc.ChatIDs) == 0 {
		return plan.Snapshot{}, fmt.Errorf("planrepo: пустая область чатов")
	}
	var snap plan.Snapshot

	txs, err := r.src.PlanTransactions(ctx)
	if err != nil {
		return plan.Snapshot{}, err
	}
	for _, t := range txs {
		ev, ok := r.txEvent(t)
		if !ok {
			snap.Skipped++
			continue
		}
		if ev.Kind == plan.KindIncome {
			snap.Incomes = append(snap.Incomes, ev)
		} else {
			snap.Payments = append(snap.Payments, ev)
		}
	}

	planned, err := r.src.PlanPlannedItems(ctx, sc.ChatIDs)
	if err != nil {
		return plan.Snapshot{}, err
	}
	for _, p := range planned {
		ev, ok := r.plannedEvent(p)
		if !ok {
			snap.Skipped++
			continue
		}
		if ev.Kind == plan.KindIncome {
			snap.Incomes = append(snap.Incomes, ev)
		} else {
			snap.Payments = append(snap.Payments, ev)
		}
	}

	rules, err := r.src.PlanRecurringRules(ctx, sc.ChatIDs)
	if err != nil {
		return plan.Snapshot{}, err
	}
	for _, rule := range rules {
		rw, ok := r.ruleEvent(rule)
		if !ok {
			snap.Skipped++
			continue
		}
		snap.Rules = append(snap.Rules, rw)
	}

	if snap.Limits, err = r.readLimits(ctx, sc); err != nil {
		return plan.Snapshot{}, err
	}
	if snap.Dismissed, err = r.readDismissed(ctx, sc); err != nil {
		return plan.Snapshot{}, err
	}
	if snap.ReservePct, err = r.src.PlanReservePct(ctx); err != nil {
		return plan.Snapshot{}, err
	}
	if snap.Rate, snap.RateManual, err = r.rate(ctx); err != nil {
		return plan.Snapshot{}, err
	}
	return snap, nil
}

// rate переводит курс «рублей за бат» из float64 базы в микроединицы домена.
// Нет курса бата — nil: Convert сам вернёт ErrNoRate там, где курс нужен.
func (r *Reader) rate(ctx context.Context) (*plan.Rate, bool, error) {
	rates, err := r.src.GetExchangeRates(ctx)
	if err != nil {
		return nil, false, err
	}
	v, ok := rates[string(plan.CurTHB)]
	if !ok || v <= 0 {
		return nil, false, nil
	}
	micro := int64(math.Round(v * 1_000_000))
	if micro <= 0 {
		return nil, false, nil
	}
	src, found, err := r.src.GetRateSource(ctx, string(plan.CurTHB))
	if err != nil {
		return nil, false, err
	}
	return &plan.Rate{RubPerThbMicro: micro}, found && src.Manual, nil
}

func money(minor int64, cur string) (plan.Money, bool) {
	c := plan.Currency(cur)
	if !c.Supported() {
		return plan.Money{}, false
	}
	return plan.Money{Minor: minor, Cur: c}, true
}

// date берёт календарный день из DATE (в базе без зоны; pgx отдаёт UTC-полночь).
func date(t time.Time) plan.Date {
	return plan.Date{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}
}

func datePtr(t *time.Time) *plan.Date {
	if t == nil {
		return nil
	}
	d := date(*t)
	return &d
}

func (r *Reader) skip(what, id, cur string) {
	r.logger.Warn("planrepo: запись не вошла в план", "что", what, "id", id, "валюта", cur)
}

func (r *Reader) txEvent(t budget.PlanTx) (plan.Event, bool) {
	m, ok := money(t.AmountMinor, t.Currency)
	if !ok {
		r.skip("транзакция", t.ID.String(), t.Currency)
		return plan.Event{}, false
	}
	kind := plan.KindPayment
	if t.Type == "income" {
		kind = plan.KindIncome
	}
	d := date(t.Date)
	ev := plan.Event{
		Key:      plan.TransactionKey(t.ID.String()),
		Kind:     kind,
		State:    plan.StateDone,
		Name:     firstNonEmpty(t.Description, t.Category, plan.UnnamedName),
		Amount:   &m,
		Date:     &d,
		Category: t.Category,
		Source:   plan.SourceTransaction,
		Origin:   factOrigin(t),
		// Обычный приход окна не открывает (ADR-019); у расходов флаг не используется.
		JoinsWindow: kind == plan.KindIncome && !t.OpensWindow,
	}
	if t.RecurringID != nil {
		ev.RuleID = t.RecurringID.String()
		// Срок факта: scheduled_for, а у старых строк — дата транзакции (C13).
		if t.ScheduledFor != nil {
			ev.ScheduledFor = datePtr(t.ScheduledFor)
		} else {
			ev.ScheduledFor = &d
		}
	}
	return ev, true
}

// factOrigin — происхождение факта для прав строки (plan.Event.Actions). Решает
// адаптер, потому что знает колонки; что с этим можно делать — решает домен.
func factOrigin(t budget.PlanTx) plan.FactOrigin {
	switch {
	case t.AddedBy != budget.AddedByWeb:
		return plan.FactExternal
	case t.PlannedID != nil:
		return plan.FactSettled
	default:
		return plan.FactWebLoose
	}
}

func (r *Reader) plannedEvent(p budget.PlanPlanned) (plan.Event, bool) {
	kind := plan.KindPayment
	if p.Direction == "income" {
		kind = plan.KindIncome
	}
	if kind == plan.KindIncome && p.DueDate == nil {
		// Ожидаемое поступление без даты окно открыть не может.
		r.logger.Warn("planrepo: поступление без даты не вошло в план", "id", p.ID.String())
		return plan.Event{}, false
	}
	ev := plan.Event{
		Key:         "planned:" + p.ID.String(),
		Kind:        kind,
		State:       plan.StatePlanned,
		Name:        firstNonEmpty(p.Description, p.Category, plan.UnnamedName),
		Date:        datePtr(p.DueDate),
		WindowStart: datePtr(p.WindowStart),
		Category:    p.Category,
		Source:      plan.SourcePlanned,
		JoinsWindow: kind == plan.KindIncome && !p.OpensWindow,
	}
	if p.AmountMinor != nil {
		m, ok := money(*p.AmountMinor, p.Currency)
		if !ok {
			r.skip("плановая запись", p.ID.String(), p.Currency)
			return plan.Event{}, false
		}
		ev.Amount = &m
	}
	return ev, true
}

func (r *Reader) ruleEvent(x budget.PlanRecurring) (plan.RuleWithExceptions, bool) {
	m, ok := money(x.AmountMinor, x.Currency)
	if !ok {
		r.skip("повторяющееся правило", x.ID.String(), x.Currency)
		return plan.RuleWithExceptions{}, false
	}
	kind := plan.KindPayment
	if x.Type == "income" {
		kind = plan.KindIncome
	}
	rule := plan.Rule{
		Anchor: date(x.Anchor),
		N:      x.IntervalN,
		Unit:   plan.Unit(x.IntervalUnit),
		Until:  datePtr(x.Until),
		Amount: m,
	}
	// Точный день месяца важен только для месячного шага: якорь мог быть прижат
	// к концу короткого месяца.
	if x.DayOfMonth != nil && rule.Unit == plan.UnitMonth {
		d := *x.DayOfMonth
		rule.DayOfMonth = &d
	}
	ex := make([]plan.Date, 0, len(x.Skipped))
	for _, s := range x.Skipped {
		ex = append(ex, date(s))
	}
	return plan.RuleWithExceptions{
		ID: x.ID.String(), Name: x.Name, Category: x.Category,
		Kind: kind, Rule: rule, Exceptions: ex, JoinsWindow: kind == plan.KindIncome && !x.OpensWindow,
	}, true
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
