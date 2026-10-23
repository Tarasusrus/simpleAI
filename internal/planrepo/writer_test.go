package planrepo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

type fakeSink struct {
	added   []budget.PlanItemInput
	addErr  error
	delIDs  [][]int64
	delOK   bool
	delErr  error
	deleted []uuid.UUID

	settleCalls []settleCall
	settleOK    bool
	settleErr   error
	skipCalls   []skipCall
	skipOK      bool

	moveCalls []moveCall
	moveOK    bool

	rateCur string
	rateVal float64
}

type settleCall struct {
	ids    []int64
	id     uuid.UUID
	actual *budget.PlanAmount
	date   time.Time
}

type moveCall struct {
	ids        []int64
	id         uuid.UUID
	due, start *time.Time
}

type skipCall struct {
	ids  []int64
	id   uuid.UUID
	term time.Time
}

func (f *fakeSink) AddPlanItem(_ context.Context, in budget.PlanItemInput) (uuid.UUID, error) {
	f.added = append(f.added, in)
	if f.addErr != nil {
		return uuid.Nil, f.addErr
	}
	return uuid.New(), nil
}

func (f *fakeSink) DeletePlanItem(_ context.Context, ids []int64, id uuid.UUID) (bool, error) {
	f.delIDs = append(f.delIDs, ids)
	f.deleted = append(f.deleted, id)
	return f.delOK, f.delErr
}

func (f *fakeSink) SettlePlanItem(_ context.Context, ids []int64, id uuid.UUID, actual *budget.PlanAmount, date time.Time) (bool, error) {
	f.settleCalls = append(f.settleCalls, settleCall{ids, id, actual, date})
	return f.settleOK, f.settleErr
}

func (f *fakeSink) MovePlanItem(_ context.Context, ids []int64, id uuid.UUID, due, start *time.Time) (bool, error) {
	f.moveCalls = append(f.moveCalls, moveCall{ids, id, due, start})
	return f.moveOK, nil
}

func (f *fakeSink) SkipRecurringTerm(_ context.Context, ids []int64, id uuid.UUID, term time.Time) (bool, error) {
	f.skipCalls = append(f.skipCalls, skipCall{ids, id, term})
	return f.skipOK, nil
}

func (f *fakeSink) SetManualRate(_ context.Context, cur string, v float64) error {
	f.rateCur, f.rateVal = cur, v
	return nil
}

func (f *fakeSink) ExpenseCategoryNames(context.Context) ([]string, error) {
	return []string{"Еда"}, nil
}

func dptr(y, m, d int) *plan.Date { x := plan.Date{Year: y, Month: m, Day: d}; return &x }

func TestAddPlanned_MapsIncomeAndPayment(t *testing.T) {
	f := &fakeSink{}
	w := NewWriter(f)
	sc := plan.Scope{ChatIDs: []int64{7, 9}}
	ctx := context.Background()

	if _, err := w.AddPlanned(ctx, sc, plan.NewItem{
		Kind: plan.KindIncome, Name: "аванс", Amount: &plan.Money{Minor: 3_000_000, Cur: plan.CurRUB}, Date: dptr(2026, 3, 20),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddPlanned(ctx, sc, plan.NewItem{
		Kind: plan.KindPayment, Name: "права", WindowStart: dptr(2026, 3, 19), Category: "Еда",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddPlanned(ctx, sc, plan.NewItem{
		Kind: plan.KindPayment, Name: "аренда", Amount: &plan.Money{Minor: 1_800_000, Cur: plan.CurTHB}, Date: dptr(2026, 3, 15),
	}); err != nil {
		t.Fatal(err)
	}
	in, pay, thb := f.added[0], f.added[1], f.added[2]
	if in.ChatID != 7 || in.Direction != "income" || *in.AmountMinor != 3_000_000 || in.Currency != "RUB" ||
		in.DueDate == nil || !in.DueDate.Equal(time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)) || in.WindowStart != nil {
		t.Errorf("поступление: %+v", in)
	}
	if pay.Direction != "expense" || pay.AmountMinor != nil || pay.DueDate != nil || pay.WindowStart == nil || pay.Category != "Еда" {
		t.Errorf("платёж без суммы в окно: %+v", pay)
	}
	if thb.Currency != "THB" || *thb.AmountMinor != 1_800_000 {
		t.Errorf("платёж в батах: %+v", thb)
	}
}

// Пустая область и невалидная запись до хранилища не доходят.
func TestAddPlanned_RejectsBeforeSink(t *testing.T) {
	f := &fakeSink{}
	w := NewWriter(f)
	ok := plan.NewItem{Kind: plan.KindPayment, Name: "x"}
	if _, err := w.AddPlanned(context.Background(), plan.Scope{}, ok); err == nil {
		t.Error("пустая область принята")
	}
	if _, err := w.AddPlanned(context.Background(), plan.Scope{ChatIDs: []int64{1}}, plan.NewItem{Kind: plan.KindIncome, Name: "x", Date: dptr(2026, 3, 20)}); !errors.Is(err, plan.ErrAmountRequired) {
		t.Errorf("поступление без суммы: %v", err)
	}
	if len(f.added) != 0 {
		t.Errorf("в хранилище ушло %d записей", len(f.added))
	}
}

// Удаление ограничено областью: список чатов уходит в хранилище (AC-8.4).
//
// Мутация: передавать в DeletePlanItem nil вместо sc.ChatIDs — тест краснеет.
func TestDeletePlanned_ScopeAndNotFound(t *testing.T) {
	f := &fakeSink{}
	w := NewWriter(f)
	sc := plan.Scope{ChatIDs: []int64{7, 9}}
	id := uuid.New().String()

	if err := w.DeletePlanned(context.Background(), sc, id); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("запись не найдена: %v", err)
	}
	f.delOK = true
	if err := w.DeletePlanned(context.Background(), sc, id); err != nil {
		t.Errorf("удаление: %v", err)
	}
	if len(f.delIDs) != 2 || len(f.delIDs[1]) != 2 || f.delIDs[1][0] != 7 || f.delIDs[1][1] != 9 {
		t.Errorf("область не дошла до хранилища: %v", f.delIDs)
	}
	before := len(f.deleted)
	if err := w.DeletePlanned(context.Background(), sc, "not-a-uuid"); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("кривой id: %v", err)
	}
	if len(f.deleted) != before {
		t.Error("кривой id дошёл до хранилища")
	}
	if err := w.DeletePlanned(context.Background(), plan.Scope{}, id); err == nil {
		t.Error("пустая область принята")
	}
}

func TestSettlePlanned_PassesScopeAmountAndDate(t *testing.T) {
	f := &fakeSink{settleOK: true}
	w := NewWriter(f)
	sc := plan.Scope{ChatIDs: []int64{7, 9}}
	id := uuid.New()
	err := w.SettlePlanned(context.Background(), sc, id.String(), &plan.Money{Minor: 12345, Cur: plan.CurTHB}, plan.Date{Year: 2026, Month: 3, Day: 10})
	if err != nil {
		t.Fatal(err)
	}
	c := f.settleCalls[0]
	if len(c.ids) != 2 || c.id != id || c.actual == nil || c.actual.Minor != 12345 || c.actual.Currency != "THB" ||
		!c.date.Equal(time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("в хранилище ушло %+v", c)
	}
	if err := w.SettlePlanned(context.Background(), sc, id.String(), nil, plan.Date{Year: 2026, Month: 3, Day: 10}); err != nil || f.settleCalls[1].actual != nil {
		t.Errorf("без суммы: %v %+v", err, f.settleCalls[1])
	}
}

func TestSettlePlanned_NotFoundAndEmptyScope(t *testing.T) {
	f := &fakeSink{}
	w := NewWriter(f)
	ctx := context.Background()
	day := plan.Date{Year: 2026, Month: 3, Day: 10}
	if err := w.SettlePlanned(ctx, plan.Scope{ChatIDs: []int64{1}}, uuid.NewString(), nil, day); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("нет записи: %v", err)
	}
	if err := w.SettlePlanned(ctx, plan.Scope{ChatIDs: []int64{1}}, "не-uuid", nil, day); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("кривой id: %v", err)
	}
	if err := w.SettlePlanned(ctx, plan.Scope{}, uuid.NewString(), nil, day); err == nil {
		t.Error("пустая область принята")
	}
	if len(f.settleCalls) != 1 {
		t.Errorf("до хранилища дошло %d вызовов, ждали 1", len(f.settleCalls))
	}
}

func TestSkipTerm(t *testing.T) {
	f := &fakeSink{skipOK: true}
	w := NewWriter(f)
	id := uuid.New()
	sc := plan.Scope{ChatIDs: []int64{7}}
	if err := w.SkipTerm(context.Background(), sc, id.String(), plan.Date{Year: 2026, Month: 3, Day: 15}); err != nil {
		t.Fatal(err)
	}
	if c := f.skipCalls[0]; c.id != id || !c.term.Equal(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)) || c.ids[0] != 7 {
		t.Errorf("в хранилище ушло %+v", c)
	}
	f.skipOK = false
	if err := w.SkipTerm(context.Background(), sc, id.String(), plan.Date{Year: 2026, Month: 3, Day: 15}); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("чужое правило: %v", err)
	}
}

func TestSkipTerm_BadIDAndEmptyScope(t *testing.T) {
	f := &fakeSink{skipOK: true}
	w := NewWriter(f)
	day := plan.Date{Year: 2026, Month: 3, Day: 15}
	if err := w.SkipTerm(context.Background(), plan.Scope{ChatIDs: []int64{1}}, "не-uuid", day); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("кривой id: %v", err)
	}
	if err := w.SkipTerm(context.Background(), plan.Scope{}, uuid.NewString(), day); err == nil {
		t.Error("пустая область принята")
	}
	if len(f.skipCalls) != 0 {
		t.Errorf("до хранилища дошло %d вызовов", len(f.skipCalls))
	}
}

func TestSetRate_ConvertsMicroToRublesPerBaht(t *testing.T) {
	f := &fakeSink{}
	w := NewWriter(f)
	if err := w.SetRate(context.Background(), 2_650_000); err != nil {
		t.Fatal(err)
	}
	if f.rateCur != "THB" || f.rateVal != 2.65 {
		t.Errorf("в хранилище ушло %s %v", f.rateCur, f.rateVal)
	}
	if err := w.SetRate(context.Background(), 0); !errors.Is(err, plan.ErrBadRate) {
		t.Errorf("нулевой курс: %v", err)
	}
}

func TestMovePlanned(t *testing.T) {
	f := &fakeSink{moveOK: true}
	w := NewWriter(f)
	ctx := context.Background()
	sc := plan.Scope{ChatIDs: []int64{7}}
	id := uuid.New()
	if err := w.MovePlanned(ctx, sc, id.String(), nil, dptr(2026, 3, 19)); err != nil {
		t.Fatal(err)
	}
	if c := f.moveCalls[0]; c.id != id || c.due != nil || c.start == nil || !c.start.Equal(time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)) || c.ids[0] != 7 {
		t.Errorf("окно: %+v", c)
	}
	if err := w.MovePlanned(ctx, sc, id.String(), nil, nil); err != nil || f.moveCalls[1].due != nil || f.moveCalls[1].start != nil {
		t.Errorf("без даты: %v %+v", err, f.moveCalls[1])
	}
	f.moveOK = false
	if err := w.MovePlanned(ctx, sc, id.String(), nil, nil); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("нет записи: %v", err)
	}
	if err := w.MovePlanned(ctx, sc, "не-uuid", nil, nil); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("кривой id: %v", err)
	}
	if err := w.MovePlanned(ctx, plan.Scope{}, id.String(), nil, nil); err == nil {
		t.Error("пустая область принята")
	}
	if len(f.moveCalls) != 3 {
		t.Errorf("до хранилища дошло %d вызовов, ждали 3", len(f.moveCalls))
	}
}
