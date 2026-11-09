package planrepo

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

// Домен не может импортировать budget, поэтому держит свою копию нормализации
// имени; этот тест не даёт копиям разойтись (ADR-010).
func TestCategoryKey_MatchesBudgetNormalizeName(t *testing.T) {
	for _, s := range []string{"Еда", "  еДа ", "COFFEE", "", "\tКафе\n", "Ёлки", "ǅ"} {
		if got, want := plan.CategoryKey(s), budget.NormalizeName(s); got != want {
			t.Errorf("%q: CategoryKey=%q, NormalizeName=%q", s, got, want)
		}
	}
}

type fakeLimitSource struct {
	rows []budget.PlanLimit
	got  [][]int64
}

func (f *fakeLimitSource) PlanLimits(_ context.Context, ids []int64) ([]budget.PlanLimit, error) {
	f.got = append(f.got, ids)
	return f.rows, nil
}

func TestRead_LimitsMappedAndScoped(t *testing.T) {
	id := uuid.New()
	ls := &fakeLimitSource{rows: []budget.PlanLimit{
		{ID: id, ChatID: 7, Category: "Еда", AmountMinor: 150_000, Currency: "THB", Repeat: true, WindowStart: day(2026, 10, 5)},
		{ID: uuid.New(), Category: "Х", AmountMinor: 1, Currency: "USD", WindowStart: day(2026, 10, 5)},
	}}
	f := &fakeSource{rates: map[string]float64{"THB": 2.5}}
	snap, err := newReader(f).WithLimits(ls).Read(context.Background(), plan.Scope{ChatIDs: []int64{7, 9}})
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.Limit{{ID: id.String(), Category: "Еда", Amount: plan.Money{Minor: 150_000, Cur: plan.CurTHB}, Repeat: true, WindowStart: plan.Date{Year: 2026, Month: 10, Day: 5}}}
	if !reflect.DeepEqual(snap.Limits, want) {
		t.Fatalf("limits=%+v, ждали %+v (валюта USD должна быть пропущена)", snap.Limits, want)
	}
	if !reflect.DeepEqual(ls.got, [][]int64{{7, 9}}) {
		t.Fatalf("в источник ушла область %v", ls.got)
	}
}

func TestRead_NoLimitSource_NoLimits(t *testing.T) {
	snap, err := newReader(&fakeSource{rates: map[string]float64{"THB": 2.5}}).Read(context.Background(), plan.Scope{ChatIDs: []int64{7}})
	if err != nil || len(snap.Limits) != 0 {
		t.Fatalf("limits=%v err=%v", snap.Limits, err)
	}
}

type fakeLimitSink struct {
	added    []budget.PlanLimitInput
	addErr   error
	updScope [][]int64
	updOK    bool
	updErr   error
	updArgs  []any
	updWS    *time.Time
	delScope [][]int64
	delOK    bool
}

func (f *fakeLimitSink) AddPlanLimit(_ context.Context, in budget.PlanLimitInput) (uuid.UUID, error) {
	f.added = append(f.added, in)
	return uuid.New(), f.addErr
}

func (f *fakeLimitSink) UpdatePlanLimit(_ context.Context, ids []int64, _ uuid.UUID, minor int64, cur string, repeat bool, ws *time.Time) (bool, error) {
	f.updWS = ws
	f.updScope = append(f.updScope, ids)
	f.updArgs = []any{minor, cur, repeat}
	return f.updOK, f.updErr
}

func (f *fakeLimitSink) DeletePlanLimit(_ context.Context, ids []int64, _ uuid.UUID) (bool, error) {
	f.delScope = append(f.delScope, ids)
	return f.delOK, nil
}

var limScope = plan.Scope{ChatIDs: []int64{7, 9}}

func TestLimitWriter_AddMapsFieldsAndFirstChat(t *testing.T) {
	f := &fakeLimitSink{}
	n := plan.NewLimit{Category: "Еда", Amount: plan.Money{Minor: 150_000, Cur: plan.CurTHB}, Repeat: true, WindowStart: plan.Date{Year: 2026, Month: 10, Day: 5}}
	if _, err := NewLimitWriter(f).AddLimit(context.Background(), limScope, n); err != nil {
		t.Fatal(err)
	}
	want := budget.PlanLimitInput{ChatID: 7, Category: "Еда", AmountMinor: 150_000, Currency: "THB", Repeat: true,
		WindowStart: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}
	if len(f.added) != 1 || f.added[0] != want {
		t.Fatalf("added=%+v, ждали %+v", f.added, want)
	}
}

func TestLimitWriter_AddRejectsBeforeSinkAndMapsErrors(t *testing.T) {
	f := &fakeLimitSink{}
	w := NewLimitWriter(f)
	bad := plan.NewLimit{Category: "Без категории", Amount: plan.Money{Minor: 1, Cur: plan.CurTHB}, WindowStart: plan.Date{Year: 2026, Month: 10, Day: 5}}
	if _, err := w.AddLimit(context.Background(), limScope, bad); !errors.Is(err, plan.ErrLimitCategory) || len(f.added) != 0 {
		t.Fatalf("err=%v added=%d: невалидный лимит дошёл до базы", err, len(f.added))
	}
	if _, err := w.AddLimit(context.Background(), plan.Scope{}, bad); err == nil {
		t.Fatal("пустая область принята")
	}
	ok := plan.NewLimit{Category: "Еда", Amount: plan.Money{Minor: 1, Cur: plan.CurTHB}, WindowStart: plan.Date{Year: 2026, Month: 10, Day: 5}}
	f.addErr = budget.ErrPlanLimitDuplicate
	if _, err := w.AddLimit(context.Background(), limScope, ok); !errors.Is(err, plan.ErrLimitExists) {
		t.Fatalf("err=%v", err)
	}
	f.addErr = budget.ErrPlanLimitCategory
	if _, err := w.AddLimit(context.Background(), limScope, ok); !errors.Is(err, plan.ErrLimitCategory) {
		t.Fatalf("err=%v", err)
	}
}

func TestLimitWriter_UpdateDeleteScopeAndNotFound(t *testing.T) {
	f := &fakeLimitSink{}
	w := NewLimitWriter(f)
	id := uuid.NewString()
	e := plan.LimitEdit{Amount: plan.Money{Minor: 5, Cur: plan.CurRUB}, Repeat: true}
	if err := w.UpdateLimit(context.Background(), limScope, id, e); !errors.Is(err, plan.ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if err := w.DeleteLimit(context.Background(), limScope, id); !errors.Is(err, plan.ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(f.updScope, [][]int64{{7, 9}}) || !reflect.DeepEqual(f.delScope, [][]int64{{7, 9}}) {
		t.Fatalf("области: upd=%v del=%v", f.updScope, f.delScope)
	}
	if !reflect.DeepEqual(f.updArgs, []any{int64(5), "RUB", true}) {
		t.Fatalf("args=%v", f.updArgs)
	}
	if f.updWS != nil {
		t.Fatalf("окно задано, хотя правка его не меняла: %v", f.updWS)
	}
	f.updOK, f.delOK = true, true
	if err := w.UpdateLimit(context.Background(), limScope, id, e); err != nil {
		t.Fatal(err)
	}
	e.WindowStart = dptr(2026, 10, 5)
	if err := w.UpdateLimit(context.Background(), limScope, id, e); err != nil {
		t.Fatal(err)
	}
	if f.updWS == nil || !f.updWS.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("окно не дошло до базы: %v", f.updWS)
	}
	if err := w.DeleteLimit(context.Background(), limScope, id); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"не-uuid", ""} {
		if err := w.DeleteLimit(context.Background(), limScope, bad); !errors.Is(err, plan.ErrNotFound) {
			t.Errorf("%q: err=%v", bad, err)
		}
	}
	if err := w.DeleteLimit(context.Background(), plan.Scope{}, id); err == nil {
		t.Fatal("пустая область принята")
	}
	f.updErr = budget.ErrPlanLimitDuplicate
	if err := w.UpdateLimit(context.Background(), limScope, id, e); !errors.Is(err, plan.ErrLimitExists) {
		t.Fatalf("err=%v", err)
	}
}

type fakeDismissSrc struct {
	names []string
	got   [][]int64
}

func (f *fakeDismissSrc) PlanDismissed(_ context.Context, ids []int64) ([]string, error) {
	f.got = append(f.got, ids)
	return f.names, nil
}

func TestRead_DismissedMappedAndScoped(t *testing.T) {
	ds := &fakeDismissSrc{names: []string{"Кафе"}}
	f := &fakeSource{rates: map[string]float64{"THB": 2.5}}
	snap, err := newReader(f).WithDismissed(ds).Read(context.Background(), plan.Scope{ChatIDs: []int64{7, 9}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.Dismissed, []string{"Кафе"}) || !reflect.DeepEqual(ds.got, [][]int64{{7, 9}}) {
		t.Fatalf("Dismissed=%v, область=%v", snap.Dismissed, ds.got)
	}
	if snap, err := newReader(f).Read(context.Background(), plan.Scope{ChatIDs: []int64{7}}); err != nil || len(snap.Dismissed) != 0 {
		t.Fatalf("без источника: %v %v", snap.Dismissed, err)
	}
}

type fakeDismissSink struct {
	dChat  []int64
	dCat   []string
	rScope [][]int64
	rCat   []string
	err    error
}

func (f *fakeDismissSink) DismissPlanCategory(_ context.Context, chat int64, c string) error {
	f.dChat, f.dCat = append(f.dChat, chat), append(f.dCat, c)
	return f.err
}

func (f *fakeDismissSink) RestorePlanCategory(_ context.Context, ids []int64, c string) error {
	f.rScope, f.rCat = append(f.rScope, ids), append(f.rCat, c)
	return f.err
}

func TestCategoryHider_FirstChatOnDismissWholeScopeOnRestore(t *testing.T) {
	f := &fakeDismissSink{}
	h := NewCategoryHider(f)
	ctx := context.Background()
	if err := h.Dismiss(ctx, limScope, "Кафе"); err != nil {
		t.Fatal(err)
	}
	if err := h.Restore(ctx, limScope, "Кафе"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.dChat, []int64{7}) || !reflect.DeepEqual(f.rScope, [][]int64{{7, 9}}) || f.dCat[0] != "Кафе" || f.rCat[0] != "Кафе" {
		t.Fatalf("dismiss chat=%v restore scope=%v", f.dChat, f.rScope)
	}
	if err := h.Dismiss(ctx, plan.Scope{}, "Кафе"); err == nil {
		t.Error("пустая область принята (Dismiss)")
	}
	if err := h.Restore(ctx, plan.Scope{}, "Кафе"); err == nil {
		t.Error("пустая область принята (Restore)")
	}
	f.err = budget.ErrPlanLimitCategory
	if err := h.Dismiss(ctx, limScope, "Нет"); !errors.Is(err, plan.ErrLimitCategory) {
		t.Errorf("маппинг ошибки: %v", err)
	}
}
