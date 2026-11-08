package botlimits

import (
	"context"
	"errors"
	"testing"
	"time"

	"simpleAI/internal/plan"
)

// memLengths хранит правки в снимке читателя: следующее чтение их видит.
type memLengths struct{ r *fakeReader }

func (m memLengths) SetWindowLength(_ context.Context, _ plan.Scope, l plan.WindowLength) error {
	m.ResetWindowLength(context.Background(), plan.Scope{}, l.Start) //nolint:errcheck // память, ошибок нет
	m.r.snap.WindowLengths = append(m.r.snap.WindowLengths, l)
	return nil
}

func (m memLengths) ResetWindowLength(_ context.Context, _ plan.Scope, start plan.Date) error {
	var keep []plan.WindowLength
	for _, l := range m.r.snap.WindowLengths {
		if l.Start != start {
			keep = append(keep, l)
		}
	}
	m.r.snap.WindowLengths = keep
	return nil
}

func windowPlan(f *fakeReader, chats ...int64) *Plan {
	svc := plan.NewService(f, nil, func() plan.Date { return d(2026, 3, 10) }, plan.Scope{ChatIDs: []int64{1}}).
		WithWindowLengths(memLengths{r: f})
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
	if w, _, err = p.ResizeWindow(context.Background(), 42, -7); err != nil || w.Days != 14 || w.Adjusted {
		t.Fatalf("обратно к 14: %+v err=%v", w, err)
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
	if len(f.snap.WindowLengths) != 0 {
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
