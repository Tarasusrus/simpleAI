package plan

import (
	"context"
	"errors"
	"testing"
)

type fakeLimitWriter struct {
	lastEdit LimitEdit
	added    []NewLimit
	scopes   []Scope
	edited   []string
	deleted  []string
	err      error
}

func (f *fakeLimitWriter) AddLimit(_ context.Context, sc Scope, n NewLimit) (string, error) {
	f.added = append(f.added, n)
	f.scopes = append(f.scopes, sc)
	return "lim-1", f.err
}

func (f *fakeLimitWriter) UpdateLimit(_ context.Context, sc Scope, id string, e LimitEdit) error {
	f.lastEdit = e
	f.edited = append(f.edited, id)
	f.scopes = append(f.scopes, sc)
	return f.err
}

func (f *fakeLimitWriter) DeleteLimit(_ context.Context, sc Scope, id string) error {
	f.deleted = append(f.deleted, id)
	f.scopes = append(f.scopes, sc)
	return f.err
}

func (f *fakeLimitWriter) calls() int { return len(f.added) + len(f.edited) + len(f.deleted) }

// Окно 05.03–18.03, сегодня 10.03; доход 100 000, аренда 45 000, еда 5 000, резерв 10 %.
func limSvc(snap Snapshot, w LimitWriter) *Service {
	s := NewService(&fakeReader{snap: snap}, nil, func() Date { return d(2026, 3, 10) }, Scope{ChatIDs: []int64{42}})
	if w != nil {
		s.WithLimitWriter(w)
	}
	return s
}

func TestLimitService_ReadOnlyWithoutWriter(t *testing.T) {
	s := limSvc(baseSnap(), nil)
	ctx := context.Background()
	if _, err := s.AddLimit(ctx, AddLimitInput{Category: "Еда", Amount: *rub(1000)}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("AddLimit: %v", err)
	}
	if err := s.EditLimit(ctx, "x", LimitEdit{Amount: *rub(1)}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("EditLimit: %v", err)
	}
	if err := s.DeleteLimit(ctx, "x"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("DeleteLimit: %v", err)
	}
}

func TestLimitService_AddUsesScreenWindowAndScope(t *testing.T) {
	w := &fakeLimitWriter{}
	id, err := limSvc(baseSnap(), w).AddLimit(context.Background(), AddLimitInput{Category: "  Еда ", Amount: *rub(1000), Repeat: true})
	if err != nil || id != "lim-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	want := NewLimit{Category: "Еда", Amount: *rub(1000), Repeat: true, WindowStart: d(2026, 3, 5)}
	if len(w.added) != 1 || w.added[0] != want {
		t.Fatalf("added=%+v, ждали %+v", w.added, want)
	}
	if len(w.scopes) != 1 || w.scopes[0].ChatIDs[0] != 42 {
		t.Fatalf("область не дошла: %+v", w.scopes)
	}
}

func TestLimitService_AddValidatesBeforeWriter(t *testing.T) {
	w := &fakeLimitWriter{}
	s := limSvc(baseSnap(), w)
	for name, in := range map[string]AddLimitInput{
		"без категории": {Category: "без категории", Amount: *rub(1)},
		"нулевая сумма": {Category: "Еда", Amount: Money{0, CurRUB}},
	} {
		if _, err := s.AddLimit(context.Background(), in); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	if w.calls() != 0 {
		t.Fatalf("невалидный лимит дошёл до писателя: %+v", w.added)
	}
}

func TestLimitService_AddNoWindow(t *testing.T) {
	snap := baseSnap()
	snap.Incomes = nil
	w := &fakeLimitWriter{}
	if _, err := limSvc(snap, w).AddLimit(context.Background(), AddLimitInput{Category: "Еда", Amount: *rub(1)}); !errors.Is(err, ErrNoWindow) {
		t.Fatalf("err=%v", err)
	}
	if w.calls() != 0 {
		t.Fatal("записано без окна")
	}
}

func TestLimitService_AddConflicts(t *testing.T) {
	cases := map[string]struct {
		existing Limit
		in       AddLimitInput
		conflict bool
	}{
		"разовый на то же окно":           {Limit{ID: "a", Category: "еда", Amount: *rub(1), WindowStart: d(2026, 3, 5)}, AddLimitInput{Category: "Еда", Amount: *rub(1)}, true},
		"повторяющийся уже действует":     {Limit{ID: "a", Category: "Еда", Amount: *rub(1), Repeat: true, WindowStart: d(2026, 3, 5)}, AddLimitInput{Category: "Еда", Amount: *rub(1)}, true},
		"повторяющийся при повторяющемся": {Limit{ID: "a", Category: "Еда", Amount: *rub(1), Repeat: true, WindowStart: d(2026, 2, 1)}, AddLimitInput{Category: "Еда", Amount: *rub(1), Repeat: true}, true},
		"повторяющийся с будущим стартом": {Limit{ID: "a", Category: "Еда", Amount: *rub(1), Repeat: true, WindowStart: d(2026, 4, 1)}, AddLimitInput{Category: "Еда", Amount: *rub(1), Repeat: true}, true},
		"другая категория":                {Limit{ID: "a", Category: "Такси", Amount: *rub(1), WindowStart: d(2026, 3, 5)}, AddLimitInput{Category: "Еда", Amount: *rub(1)}, false},
		"разовый чужого окна не мешает":   {Limit{ID: "a", Category: "Еда", Amount: *rub(1), WindowStart: d(2026, 4, 1)}, AddLimitInput{Category: "Еда", Amount: *rub(1)}, false},
	}
	for name, c := range cases {
		snap := baseSnap()
		snap.Limits = []Limit{c.existing}
		w := &fakeLimitWriter{}
		_, err := limSvc(snap, w).AddLimit(context.Background(), c.in)
		if c.conflict != errors.Is(err, ErrLimitExists) {
			t.Errorf("%s: err=%v, конфликт ждали=%v", name, err, c.conflict)
		}
		if c.conflict && w.calls() != 0 {
			t.Errorf("%s: конфликт дошёл до писателя", name)
		}
	}
}

func TestLimitService_EditDelete(t *testing.T) {
	w := &fakeLimitWriter{}
	snap := baseSnap()
	snap.Limits = []Limit{{ID: "L1", Category: "Еда", Amount: *rub(1), Repeat: true, WindowStart: d(2026, 3, 5)}}
	s := limSvc(snap, w)
	if err := s.EditLimit(context.Background(), "L1", LimitEdit{Amount: *rub(50), Repeat: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.EditLimit(context.Background(), "L1", LimitEdit{Amount: Money{0, CurRUB}}); err == nil {
		t.Error("нулевая сумма принята")
	}
	if err := s.DeleteLimit(context.Background(), "L2"); err != nil {
		t.Fatal(err)
	}
	if len(w.edited) != 1 || w.edited[0] != "L1" || len(w.deleted) != 1 || w.deleted[0] != "L2" {
		t.Fatalf("edited=%v deleted=%v", w.edited, w.deleted)
	}
	for _, sc := range w.scopes {
		if len(sc.ChatIDs) != 1 || sc.ChatIDs[0] != 42 {
			t.Fatalf("область: %+v", sc)
		}
	}
}

// Предпросмотр совпадает с тем, что покажет экран после сохранения, и ничего не пишет.
func TestLimitService_PreviewMatchesRealViewAndWritesNothing(t *testing.T) {
	w := &fakeLimitWriter{}
	ctx := context.Background()
	s := limSvc(baseSnap(), w)
	pv, err := s.PreviewLimit(ctx, CurRUB, PreviewLimitInput{Category: "Еда", Amount: *rub(8000)})
	if err != nil {
		t.Fatal(err)
	}
	if w.calls() != 0 {
		t.Fatal("предпросмотр записал")
	}
	if pv.CanSpendBefore != 40000 {
		t.Fatalf("до: %d", pv.CanSpendBefore)
	}
	// «после» = экран, на котором такой лимит уже сохранён
	snap := baseSnap()
	snap.Limits = []Limit{{ID: "x", Category: "Еда", Amount: *rub(8000), WindowStart: d(2026, 3, 5)}}
	shown, err := limSvc(snap, nil).View(ctx, CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if pv.CanSpendAfter != shown.View.CanSpend || pv.LimitHoldAfter != shown.View.LimitHold || pv.CanSpendAfter != 32000 {
		t.Fatalf("предпросмотр %+v, экран canSpend=%d hold=%d", pv, shown.View.CanSpend, shown.View.LimitHold)
	}
	if pv.Limit == nil || pv.Limit.Amount != 8000 || pv.ExceedsFree {
		t.Fatalf("limit=%+v exceeds=%v", pv.Limit, pv.ExceedsFree)
	}
}

func TestLimitService_PreviewEditExistingAndErrors(t *testing.T) {
	snap := baseSnap()
	snap.Limits = []Limit{{ID: "L1", Category: "Еда", Amount: *rub(8000), Repeat: true, WindowStart: d(2026, 3, 5)}}
	s := limSvc(snap, nil)
	ctx := context.Background()
	pv, err := s.PreviewLimit(ctx, CurRUB, PreviewLimitInput{ID: "L1", Amount: *rub(50000)})
	if err != nil {
		t.Fatal(err)
	}
	if pv.LimitHoldBefore != 8000 || pv.LimitHoldAfter != 50000 || pv.CanSpendBefore != 32000 || pv.CanSpendAfter != -10000 {
		t.Fatalf("pv=%+v", pv)
	}
	if !pv.ExceedsFree {
		t.Fatalf("лимит 50 000 при свободных 32 000 — флаг должен быть: %+v", pv)
	}
	if _, err := s.PreviewLimit(ctx, CurRUB, PreviewLimitInput{ID: "нет", Amount: *rub(1)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v", err)
	}
	if _, err := s.PreviewLimit(ctx, CurRUB, PreviewLimitInput{Category: "Еда", Amount: *rub(1)}); !errors.Is(err, ErrLimitExists) {
		t.Errorf("повтор той же категории: %v", err)
	}
}

// Повторяющийся → разовый: лимит привязывается к окну на экране, а не остаётся в старом.
//
// Мутация: убрать `e.WindowStart = &start` в EditLimit — тест краснеет.
func TestLimitService_EditRepeatToOneOffRebindsWindow(t *testing.T) {
	snap := baseSnap()
	snap.Limits = []Limit{
		{ID: "R", Category: "Еда", Amount: *rub(1000), Repeat: true, WindowStart: d(2026, 1, 1)},
		{ID: "O", Category: "Такси", Amount: *rub(1000), WindowStart: d(2026, 3, 5)},
	}
	w := &fakeLimitWriter{}
	s := limSvc(snap, w)
	ctx := context.Background()
	if err := s.EditLimit(ctx, "R", LimitEdit{Amount: *rub(1000), Repeat: false}); err != nil {
		t.Fatal(err)
	}
	if got := w.lastEdit.WindowStart; got == nil || *got != d(2026, 3, 5) {
		t.Fatalf("окно=%v, ждали 2026-03-05 (окно на экране)", got)
	}
	// остаётся повторяющимся или уже разовый — окно не трогаем
	for _, c := range []struct {
		id     string
		repeat bool
	}{{"R", true}, {"O", false}} {
		if err := s.EditLimit(ctx, c.id, LimitEdit{Amount: *rub(5), Repeat: c.repeat}); err != nil {
			t.Fatal(err)
		}
		if w.lastEdit.WindowStart != nil {
			t.Errorf("%s repeat=%v: окно изменено: %v", c.id, c.repeat, w.lastEdit.WindowStart)
		}
	}
	if err := s.EditLimit(ctx, "нет", LimitEdit{Amount: *rub(5)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("несуществующий лимит: %v", err)
	}
}

// Предпросмотр снятия «повторять» не теряет лимит из текущего окна.
func TestLimitService_PreviewRepeatToOneOffKeepsLimit(t *testing.T) {
	snap := baseSnap()
	// ранее окно 01.02–14.02: старый старт повтора резолвится в него, а не в текущее 05.03
	snap.Incomes = append([]Event{incomeFact("transaction:in0", d(2026, 2, 1), rub(1))}, snap.Incomes...)
	snap.Limits = []Limit{{ID: "R", Category: "Еда", Amount: *rub(1000), Repeat: true, WindowStart: d(2026, 2, 1)}}
	pv, err := limSvc(snap, nil).PreviewLimit(context.Background(), CurRUB, PreviewLimitInput{ID: "R", Amount: *rub(1000), Repeat: false})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Limit == nil || pv.LimitHoldAfter != 1000 || pv.LimitHoldAfter != pv.LimitHoldBefore {
		t.Fatalf("pv=%+v: лимит пропал из окна", pv)
	}
}

type fakeHider struct {
	dismissed, restored []string
	scopes              []Scope
	err                 error
}

func (f *fakeHider) Dismiss(_ context.Context, sc Scope, c string) error {
	f.dismissed = append(f.dismissed, c)
	f.scopes = append(f.scopes, sc)
	return f.err
}

func (f *fakeHider) Restore(_ context.Context, sc Scope, c string) error {
	f.restored = append(f.restored, c)
	f.scopes = append(f.scopes, sc)
	return f.err
}

func TestHider_ServiceValidationAndScope(t *testing.T) {
	ctx := context.Background()
	if err := limSvc(baseSnap(), nil).DismissCategory(ctx, "Кафе"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("без hider: %v", err)
	}
	if err := limSvc(baseSnap(), nil).RestoreCategory(ctx, "Кафе"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("без hider, restore: %v", err)
	}
	h := &fakeHider{}
	s := limSvc(baseSnap(), nil).WithCategoryHider(h)
	for _, bad := range []string{"", "  ", UncategorizedName} {
		if err := s.DismissCategory(ctx, bad); !errors.Is(err, ErrLimitCategory) {
			t.Errorf("Dismiss(%q): %v", bad, err)
		}
	}
	if len(h.dismissed) != 0 {
		t.Fatalf("плохая категория дошла до порта: %v", h.dismissed)
	}
	if err := s.DismissCategory(ctx, "  Кафе "); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreCategory(ctx, "Кафе"); err != nil {
		t.Fatal(err)
	}
	if len(h.dismissed) != 1 || h.dismissed[0] != "Кафе" || len(h.restored) != 1 || h.scopes[0].ChatIDs[0] != 42 {
		t.Fatalf("dismissed=%v restored=%v scopes=%v", h.dismissed, h.restored, h.scopes)
	}
	if err := s.RestoreCategory(ctx, " "); !errors.Is(err, ErrLimitCategory) {
		t.Errorf("Restore пустой: %v", err)
	}
}

// Скрытые категории из снимка доходят до расчёта.
func TestHider_SnapshotDismissedReachesView(t *testing.T) {
	snap := baseSnap()
	snap.Payments[0].Category = "Еда"
	snap.Dismissed = []string{"еда"}
	res, err := limSvc(snap, nil).View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.View.Unplanned) != 0 || len(res.View.Hidden) != 1 || res.View.Hidden[0].Category != "Еда" {
		t.Fatalf("Unplanned=%+v Hidden=%+v", res.View.Unplanned, res.View.Hidden)
	}
}

// ForScope меняет область только у копии: чтение и запись идут в чаты запроса, исходный сервис не тронут.
//
// Мутация: не присваивать scope в ForScope — тест краснеет.
func TestService_ForScopeIsPerRequest(t *testing.T) {
	w := &fakeLimitWriter{}
	rd := &fakeReader{snap: baseSnap()}
	base := NewService(rd, nil, func() Date { return d(2026, 3, 10) }, Scope{ChatIDs: []int64{42}}).WithLimitWriter(w)
	other := base.ForScope(Scope{ChatIDs: []int64{7, 8}})
	ctx := context.Background()
	if _, err := other.AddLimit(ctx, AddLimitInput{Category: "Еда", Amount: *rub(1000)}); err != nil {
		t.Fatal(err)
	}
	if _, err := base.View(ctx, CurRUB); err != nil {
		t.Fatal(err)
	}
	if len(w.scopes) != 1 || len(w.scopes[0].ChatIDs) != 2 || w.scopes[0].ChatIDs[0] != 7 {
		t.Fatalf("запись ушла в область %+v, ждали [7 8]", w.scopes)
	}
	if len(rd.got) != 2 || rd.got[0].ChatIDs[0] != 7 || rd.got[1].ChatIDs[0] != 42 {
		t.Fatalf("чтение: области %+v, ждали [7 8] затем [42]", rd.got)
	}
}
