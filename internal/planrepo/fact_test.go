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

// Происхождение факта выводит адаптер по колонкам; права из него — дело домена.
//
// Мутации: считать любой added_by веб-фактом, игнорировать planned_id или убрать
// Origin из txEvent — краснеет соответствующая строка.
func TestRead_FactOrigin(t *testing.T) {
	planned := uuid.New()
	mk := func(by string, p *uuid.UUID) budget.PlanTx {
		return budget.PlanTx{ID: uuid.New(), Type: "expense", AmountMinor: 100, Currency: "THB", Date: day(2026, 3, 2), AddedBy: by, PlannedID: p}
	}
	f := &fakeSource{txs: []budget.PlanTx{mk("web", &planned), mk("web", nil), mk("bot", &planned), mk("", nil)}}
	snap, err := newReader(f).Read(context.Background(), plan.Scope{ChatIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.FactOrigin{plan.FactSettled, plan.FactWebLoose, plan.FactExternal, plan.FactExternal}
	if len(snap.Payments) != len(want) {
		t.Fatalf("платежей %d, ждали %d", len(snap.Payments), len(want))
	}
	for i, e := range snap.Payments {
		if e.Origin != want[i] {
			t.Errorf("факт %d: происхождение %q, ждали %q", i, e.Origin, want[i])
		}
	}
}

type fakeFactSink struct {
	updated    []budget.FactEdit
	updatedIDs []uuid.UUID
	reopened   []uuid.UUID
	scopes     [][]int64
	ok         bool
	err        error
}

func (f *fakeFactSink) UpdateWebFact(_ context.Context, id uuid.UUID, e budget.FactEdit) (bool, error) {
	f.updatedIDs = append(f.updatedIDs, id)
	f.updated = append(f.updated, e)
	return f.ok, f.err
}

func (f *fakeFactSink) UnsettlePlanItem(_ context.Context, ids []int64, id uuid.UUID) (bool, error) {
	f.scopes = append(f.scopes, ids)
	f.reopened = append(f.reopened, id)
	return f.ok, f.err
}

// Мутации: не переводить Date/сумму, не превращать «нет строки» в ErrNotFound, не
// проверять область или id — краснеет соответствующий кейс.
func TestFactWriter(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	e := plan.FactEdit{Name: "сотовая связь", Amount: plan.Money{Minor: 90000, Cur: plan.CurTHB}, Date: plan.Date{Year: 2026, Month: 3, Day: 9}}

	s := &fakeFactSink{ok: true}
	if err := NewFactWriter(s).UpdateFact(ctx, id.String(), e); err != nil {
		t.Fatalf("UpdateFact: %v", err)
	}
	got := s.updated[0]
	if s.updatedIDs[0] != id || got.Description != "сотовая связь" || got.AmountMinor != 90000 || !got.Date.Equal(time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("в хранилище ушло %v %+v", s.updatedIDs, got)
	}
	if err := NewFactWriter(&fakeFactSink{}).UpdateFact(ctx, id.String(), e); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("нет строки: %v", err)
	}
	if err := NewFactWriter(&fakeFactSink{ok: true}).UpdateFact(ctx, "не uuid", e); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("плохой id: %v", err)
	}
	boom := errors.New("база")
	if err := NewFactWriter(&fakeFactSink{err: boom}).UpdateFact(ctx, id.String(), e); !errors.Is(err, boom) {
		t.Errorf("ошибка хранилища потеряна: %v", err)
	}

	s = &fakeFactSink{ok: true}
	if err := NewFactWriter(s).UnsettlePlanned(ctx, plan.Scope{ChatIDs: []int64{5, 6}}, id.String()); err != nil {
		t.Fatalf("UnsettlePlanned: %v", err)
	}
	if s.reopened[0] != id || len(s.scopes[0]) != 2 || s.scopes[0][0] != 5 {
		t.Errorf("в хранилище ушло %v %v", s.reopened, s.scopes)
	}
	s = &fakeFactSink{ok: true}
	if err := NewFactWriter(s).UnsettlePlanned(ctx, plan.Scope{}, id.String()); err == nil || len(s.reopened) != 0 {
		t.Errorf("пустая область дошла до хранилища: %v", err)
	}
	if err := NewFactWriter(&fakeFactSink{}).UnsettlePlanned(ctx, plan.Scope{ChatIDs: []int64{5}}, id.String()); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("нет факта: %v", err)
	}
	if err := NewFactWriter(&fakeFactSink{ok: true}).UnsettlePlanned(ctx, plan.Scope{ChatIDs: []int64{5}}, "x"); !errors.Is(err, plan.ErrNotFound) {
		t.Errorf("плохой id: %v", err)
	}
}
