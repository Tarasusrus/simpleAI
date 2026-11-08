package botlimits

import (
	"context"
	"errors"
	"testing"
	"time"

	"simpleAI/internal/plan"
)

// memWindows — хранилище окон в снимке читателя: следующее чтение их видит.
type memWindows struct {
	r *fakeReader
	n int
}

func (m *memWindows) CreateWindow(_ context.Context, _ plan.Scope, start, end plan.Date) (string, error) {
	for _, w := range m.r.snap.Windows {
		if !w.Deleted && w.Start == start {
			return w.ID, nil
		}
	}
	m.n++
	id := string(rune('a' + m.n))
	m.r.snap.Windows = append(m.r.snap.Windows, plan.StoredWindow{ID: id, Start: start, End: end})
	return id, nil
}

func (m *memWindows) UpdateWindow(_ context.Context, _ plan.Scope, id string, start, end plan.Date) error {
	for i, w := range m.r.snap.Windows {
		if w.ID == id {
			m.r.snap.Windows[i].Start, m.r.snap.Windows[i].End = start, end
			return nil
		}
	}
	return plan.ErrNotFound
}

func (m *memWindows) DeleteWindow(_ context.Context, _ plan.Scope, id string) error {
	for i, w := range m.r.snap.Windows {
		if w.ID == id {
			m.r.snap.Windows[i].Deleted = true
			return nil
		}
	}
	return plan.ErrNotFound
}

func windowPlan(f *fakeReader, chats ...int64) *Plan {
	svc := plan.NewService(f, nil, func() plan.Date { return d(2026, 3, 10) }, plan.Scope{ChatIDs: []int64{1}}).
		WithWindowStore(&memWindows{r: f})
	return NewPlan(svc, ScopeOfChats(chats))
}

// Команды бота проходят через ядро плана: «добавь день» сдвигает конец и пересчитывает «на день».
func TestWindow_ResizeThroughCore(t *testing.T) {
	f := &fakeReader{snap: fixture()}
	p := windowPlan(f, 42)
	w, ok, err := p.ResizeWindow(context.Background(), 42, 1)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !w.End.Equal(time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)) || w.Days != 15 || w.DaysLeft != 10 || !w.Adjusted {
		t.Fatalf("окно после +1: %+v", w)
	}
	snap, _, err := p.Snapshot(context.Background(), 42, "RUB")
	if err != nil || snap.DaysLeft != 10 || *snap.PerDay != 8000 {
		t.Fatalf("числа бота не видят правку: %+v err=%v", snap, err)
	}
	if w, _, err = p.SetWindowEnd(context.Background(), 42, time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC)); err != nil || w.Days != 21 {
		t.Fatalf("конец 25.03: %+v err=%v", w, err)
	}
	if w, _, err = p.SetWindowStart(context.Background(), 42, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)); err != nil || w.Days != 23 {
		t.Fatalf("начало 03.03: %+v err=%v", w, err)
	}
	if _, _, err := p.DeleteWindow(context.Background(), 42); !errors.Is(err, ErrLastWindow) {
		t.Fatalf("единственное окно: %v", err)
	}
	if cur, _, err := p.CurrentWindow(context.Background(), 42); err != nil || cur != w {
		t.Fatalf("CurrentWindow=%+v err=%v", cur, err)
	}
}

func TestWindow_OutOfScopeAndErrors(t *testing.T) {
	f := &fakeReader{snap: fixture()}
	p := windowPlan(f, 42)
	if _, ok, err := p.ResizeWindow(context.Background(), 7, 1); ok || err != nil {
		t.Fatalf("чат вне области: ok=%v err=%v", ok, err)
	}
	if len(f.snap.Windows) != 0 {
		t.Fatal("чат вне области не должен ничего менять")
	}
	if _, _, err := p.ResizeWindow(context.Background(), 42, 100); !errors.Is(err, ErrBadWindowLength) {
		t.Fatalf("длина вне пределов: %v", err)
	}
	empty := windowPlan(&fakeReader{}, 42)
	if _, _, err := empty.ResizeWindow(context.Background(), 42, 1); !errors.Is(err, ErrNoWindow) {
		t.Fatalf("окон нет: %v", err)
	}
}
