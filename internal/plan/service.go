package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// RubPerThbMicro — действующий курс бата, 0 если курса нет.
	RubPerThbMicro int64
	// LastEnd — конец последнего окна, когда оно закончилось, а нового нет (State=ended
	// без окна в View). Иначе nil.
	LastEnd *Date
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
	writer Writer // nil — запись отключена
	today  Clock
	scope  Scope
}

// NewService собирает сервис; все зависимости приходят снаружи. Writer может быть
// nil: тогда команды записи возвращают ErrReadOnly.
func NewService(r Reader, w Writer, today Clock, sc Scope) *Service {
	return &Service{reader: r, writer: w, today: today, scope: sc}
}

// state — снимок и окна, посчитанные от одной «сегодня»; общий для расчёта и
// для команд, которым нужно знать, какое окно сейчас на экране.
type state struct {
	snap    Snapshot
	today   Date
	windows []Window
	events  []Event // платежи: факты, разовые и развёрнутые повторы
	st      WindowState
}

func (s *Service) load(ctx context.Context) (state, error) {
	snap, err := s.reader.Read(ctx, s.scope)
	if err != nil {
		return state{}, err
	}
	today := s.today()
	if !today.Valid() {
		return state{}, fmt.Errorf("%w: сегодня %v", ErrBadDate, today)
	}
	from := today.AddDays(-lookbackDays)
	to := Date{today.Year, today.Month, 1}.AddMonths(horizonMonths).AddDays(-1)
	expanded, err := Expand(snap.Rules, from, to)
	if err != nil {
		return state{}, err
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
	ws := BuildWindows(incomes)
	return state{snap: snap, today: today, windows: ws, events: payments, st: Locate(ws, today)}, nil
}

// focus — окно, которое описывает экран, и «сегодня» для его расчёта. В зазоре
// между окнами (прошлое закончилось, следующее ещё не началось) экран показывает
// СЛЕДУЮЩЕЕ окно: именно в нём владелец планирует расходы. Расчёт ведётся от его
// первого дня, поэтому платежи с датой в зазоре попадают в него.
func (st state) focus() (idx int, asOf Date, upcoming bool) {
	switch st.st.Kind {
	case StateBetween:
		return st.st.Index + 1, st.windows[st.st.Index+1].Start, true
	case StateUpcoming:
		return 0, st.today, true
	default:
		return st.st.Index, st.today, false
	}
}

// View считает окно, которое видит владелец, в валюте показа cur. Если последнее
// окно закончилось, а нового нет, показывается не оно, а «нового окна ещё нет»:
// владелец планирует будущее, и прошлые траты ему по умолчанию не нужны.
func (s *Service) View(ctx context.Context, cur Currency) (Result, error) {
	return s.view(ctx, cur, false)
}

// ViewPast — то же, но закончившееся последнее окно показывается как есть.
func (s *Service) ViewPast(ctx context.Context, cur Currency) (Result, error) {
	return s.view(ctx, cur, true)
}

func (s *Service) view(ctx context.Context, cur Currency, showPast bool) (Result, error) {
	ld, err := s.load(ctx)
	if err != nil {
		return Result{}, err
	}
	asOf, upcoming := ld.today, false
	if ld.st.Kind == StateBetween {
		_, asOf, upcoming = ld.focus()
	}
	windows := ld.windows
	var lastEnd *Date
	if ld.st.Kind == StateEnded && !showPast {
		end := ld.windows[ld.st.Index].End()
		lastEnd = &end
		windows = nil
	}
	v, err := Compute(ComputeInput{
		Windows:    windows,
		Events:     ld.events,
		Rate:       ld.snap.Rate,
		Cur:        cur,
		Today:      asOf,
		ReservePct: ld.snap.ReservePct,
	})
	if err != nil {
		return Result{}, err
	}
	if upcoming {
		v.State = StateUpcoming
	}
	if lastEnd != nil {
		v.State = StateEnded
	}
	if err := checkInvariant(v); err != nil {
		return Result{}, err
	}
	return Result{
		View: v,
		Meta: Meta{Today: ld.today, RatesManual: ld.snap.RateManual, HasRate: ld.snap.Rate != nil, RubPerThbMicro: rateMicro(ld.snap.Rate), LastEnd: lastEnd, Skipped: ld.snap.Skipped},
	}, nil
}

func rateMicro(r *Rate) int64 {
	if r == nil {
		return 0
	}
	return r.RubPerThbMicro
}

// maxRateMicro — потолок ручного курса: 1000 ₽ за бат. Опечатка вроде 25 вместо 2,5
// ещё проходит, а 25000 — нет.
const maxRateMicro = 1_000 * 1_000_000

// SetRate задаёт ручной курс «рублей за бат» (микроединицы: 2,5 ₽ = 2_500_000).
func (s *Service) SetRate(ctx context.Context, rubPerThbMicro int64) error {
	if err := s.canWrite(); err != nil {
		return err
	}
	if rubPerThbMicro <= 0 || rubPerThbMicro > maxRateMicro {
		return ErrBadRate
	}
	return s.writer.SetRate(ctx, rubPerThbMicro)
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

// WindowChoice — в какое окно поставить платёж без даты.
type WindowChoice string

const (
	WindowNone    WindowChoice = ""        // пока без даты, в расчёт не входит
	WindowCurrent WindowChoice = "current" // в окно, которое сейчас на экране
	WindowNext    WindowChoice = "next"    // в следующее за ним
)

// AddIncomeInput — ожидаемое поступление (дата обязательна: оно открывает окно).
type AddIncomeInput struct {
	Name   string
	Amount Money
	Date   Date
}

// AddPaymentInput — плановый платёж: либо с датой, либо в выбранное окно, либо без
// даты. Сумма может быть неизвестна (nil).
type AddPaymentInput struct {
	Name     string
	Amount   *Money
	Date     *Date
	Window   WindowChoice
	Category string
}

func (s *Service) canWrite() error {
	if s.writer == nil {
		return ErrReadOnly
	}
	return nil
}

// AddIncome сохраняет ожидаемое поступление.
func (s *Service) AddIncome(ctx context.Context, in AddIncomeInput) (string, error) {
	if err := s.canWrite(); err != nil {
		return "", err
	}
	amount, date := in.Amount, in.Date
	n := NewItem{Kind: KindIncome, Name: in.Name, Amount: &amount, Date: &date}
	if err := n.Validate(); err != nil {
		return "", err
	}
	n.Name = strings.TrimSpace(n.Name)
	return s.writer.AddPlanned(ctx, s.scope, n)
}

// AddPayment сохраняет плановый платёж. «В окно» превращается в дату начала
// выбранного окна; если такого окна нет — ErrNoWindow.
func (s *Service) AddPayment(ctx context.Context, in AddPaymentInput) (string, error) {
	if err := s.canWrite(); err != nil {
		return "", err
	}
	n := NewItem{Kind: KindPayment, Name: in.Name, Amount: in.Amount, Date: in.Date, Category: strings.TrimSpace(in.Category)}
	switch in.Window {
	case WindowNone:
	case WindowCurrent, WindowNext:
		if in.Date != nil {
			return "", fmt.Errorf("%w: дата и окно вместе", ErrInvalidEvent)
		}
		ld, err := s.load(ctx)
		if err != nil {
			return "", err
		}
		ws, err := ld.chosenWindowStart(in.Window)
		if err != nil {
			return "", err
		}
		n.WindowStart = &ws
	default:
		return "", fmt.Errorf("%w: неизвестный выбор окна %q", ErrInvalidEvent, in.Window)
	}
	if err := n.Validate(); err != nil {
		return "", err
	}
	n.Name = strings.TrimSpace(n.Name)
	return s.writer.AddPlanned(ctx, s.scope, n)
}

// chosenWindowStart — дата, по которой платёж привяжется к окну. Для «следующего»,
// которого ещё нет, берём день после конца текущего: окно появится, когда владелец
// добавит поступление, и платёж к нему присоединится.
func (st state) chosenWindowStart(c WindowChoice) (Date, error) {
	if len(st.windows) == 0 || st.st.Kind == StateEnded || st.st.Kind == StateNone {
		return Date{}, ErrNoWindow
	}
	idx, _, _ := st.focus()
	if c == WindowCurrent {
		return st.windows[idx].Start, nil
	}
	if idx+1 < len(st.windows) {
		return st.windows[idx+1].Start, nil
	}
	return st.windows[idx].End().AddDays(1), nil
}

// Delete удаляет плановую запись по ключу вида "planned:<id>" или по самому id.
func (s *Service) Delete(ctx context.Context, key string) error {
	if err := s.canWrite(); err != nil {
		return err
	}
	return s.writer.DeletePlanned(ctx, s.scope, strings.TrimPrefix(key, "planned:"))
}

// Settle закрывает плановую запись («оплачено» / «пришло»): факт ложится на сегодня.
// Сумма нужна, если у записи её не было; если задана — заменяет плановую.
func (s *Service) Settle(ctx context.Context, key string, actual *Money) error {
	if err := s.canWrite(); err != nil {
		return err
	}
	id, ok := strings.CutPrefix(key, "planned:")
	if !ok {
		return fmt.Errorf("%w: закрыть можно только плановую запись", ErrInvalidEvent)
	}
	if actual != nil {
		if actual.Minor <= 0 {
			return ErrAmountNonPositive
		}
		if !actual.Cur.Supported() {
			return ErrUnsupportedCurrency
		}
		if actual.Minor > maxAmountMinor {
			return ErrAmountTooLarge
		}
	}
	today := s.today()
	if !today.Valid() {
		return fmt.Errorf("%w: сегодня %v", ErrBadDate, today)
	}
	return s.writer.SettlePlanned(ctx, s.scope, id, actual, today)
}

// Skip пропускает один срок повторяющегося правила («пропустить этот раз»); ключ
// вида "recurring:<ruleID>@YYYY-MM-DD".
func (s *Service) Skip(ctx context.Context, key string) error {
	if err := s.canWrite(); err != nil {
		return err
	}
	rest, ok := strings.CutPrefix(key, "recurring:")
	ruleID, termStr, found := strings.Cut(rest, "@")
	if !ok || !found || ruleID == "" {
		return fmt.Errorf("%w: пропустить можно только срок повтора", ErrInvalidEvent)
	}
	term, err := ParseDate(termStr)
	if err != nil {
		return err
	}
	return s.writer.SkipTerm(ctx, s.scope, ruleID, term)
}

// ExpenseCategories — названия категорий расходов для формы.
func (s *Service) ExpenseCategories(ctx context.Context) ([]string, error) {
	if err := s.canWrite(); err != nil {
		return nil, err
	}
	return s.writer.ExpenseCategories(ctx)
}
