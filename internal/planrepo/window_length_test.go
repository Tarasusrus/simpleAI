package planrepo

import (
	"context"
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

type fakeLengthStore struct {
	rows      []budget.PlanWindowLength
	readScope [][]int64
	setChat   []int64
	setStart  []time.Time
	setDays   []int
	resetIDs  [][]int64
	resetAt   []time.Time
}

func (f *fakeLengthStore) PlanWindowLengths(_ context.Context, ids []int64) ([]budget.PlanWindowLength, error) {
	f.readScope = append(f.readScope, ids)
	return f.rows, nil
}

func (f *fakeLengthStore) SetPlanWindowLength(_ context.Context, chatID int64, start time.Time, days int) error {
	f.setChat, f.setStart, f.setDays = append(f.setChat, chatID), append(f.setStart, start), append(f.setDays, days)
	return nil
}

func (f *fakeLengthStore) ResetPlanWindowLength(_ context.Context, ids []int64, start time.Time) error {
	f.resetIDs, f.resetAt = append(f.resetIDs, ids), append(f.resetAt, start)
	return nil
}

func TestRead_WindowLengthsMappedAndScoped(t *testing.T) {
	ls := &fakeLengthStore{rows: []budget.PlanWindowLength{{WindowStart: day(2026, 10, 5), Days: 16}}}
	snap, err := newReader(&fakeSource{rates: map[string]float64{"THB": 2.5}}).WithWindowLengths(ls).
		Read(context.Background(), plan.Scope{ChatIDs: []int64{7, 9}})
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.WindowLength{{Start: plan.Date{Year: 2026, Month: 10, Day: 5}, Days: 16}}
	if !reflect.DeepEqual(snap.WindowLengths, want) || !reflect.DeepEqual(ls.readScope, [][]int64{{7, 9}}) {
		t.Fatalf("lengths=%+v scope=%v", snap.WindowLengths, ls.readScope)
	}
}

func TestWindowLengthWriter_ScopeRouting(t *testing.T) {
	ls := &fakeLengthStore{}
	w := NewWindowLengthWriter(ls)
	sc := plan.Scope{ChatIDs: []int64{7, 9}}
	start := plan.Date{Year: 2026, Month: 10, Day: 5}
	if err := w.SetWindowLength(context.Background(), sc, plan.WindowLength{Start: start, Days: 16}); err != nil {
		t.Fatal(err)
	}
	if err := w.ResetWindowLength(context.Background(), sc, start); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ls.setChat, []int64{7}) || ls.setDays[0] != 16 || !ls.setStart[0].Equal(day(2026, 10, 5)) {
		t.Fatalf("запись идёт в первый чат: chat=%v days=%v start=%v", ls.setChat, ls.setDays, ls.setStart)
	}
	if !reflect.DeepEqual(ls.resetIDs, [][]int64{{7, 9}}) || !ls.resetAt[0].Equal(day(2026, 10, 5)) {
		t.Fatalf("сброс идёт по всей области: %v %v", ls.resetIDs, ls.resetAt)
	}
	if err := w.SetWindowLength(context.Background(), plan.Scope{}, plan.WindowLength{Start: start, Days: 3}); err == nil {
		t.Fatal("пустая область должна быть ошибкой")
	}
}

// Сквозной путь сервис → адаптер → база: продление окна меняет его конец и «в день» на экране, возврат к 14 дням
// стирает правку из базы.
//
// Мутации: не подключать WithWindowLengths у читателя — правка не видна; писать в базу days−1 — конец не тот.
func TestWindowLength_EndToEnd(t *testing.T) {
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — DB-тест пропущен (это НЕ зелёный результат)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := budget.NewStore(pool)
	const chat = int64(-880002)
	today := plan.Date{Year: 2099, Month: 10, Day: 10}
	svc := plan.NewService(
		NewReader(store, slog.New(slog.NewTextHandler(io.Discard, nil))).WithWindowLengths(store),
		NewWriter(store), func() plan.Date { return today }, plan.Scope{ChatIDs: []int64{chat}}).
		WithWindowLengths(NewWindowLengthWriter(store))
	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM budget_planned_expense WHERE chat_id = $1`, chat) //nolint:errcheck // очистка после теста
		pool.Exec(ctx, `DELETE FROM plan_window_length WHERE chat_id = $1`, chat)     //nolint:errcheck // очистка после теста
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := store.AddPlanItem(ctx, budget.PlanItemInput{ChatID: chat, Direction: "income", Description: "window-length-test доход",
		AmountMinor: ptr(10_000_000), Currency: "RUB", DueDate: tp(2099, 10, 5)}); err != nil {
		t.Fatal(err)
	}

	f, err := svc.ResizeWindow(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if f.End != (plan.Date{Year: 2099, Month: 10, Day: 21}) || f.Days != 17 {
		t.Fatalf("после +3: %+v", f)
	}
	res, err := svc.View(ctx, plan.CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if w := res.View.Window; w == nil || w.Days != 17 || w.DaysLeft != 12 {
		t.Fatalf("экран: %+v", res.View.Window)
	}
	if f, err = svc.ResizeWindow(ctx, -3); err != nil || f.Days != 14 || f.Adjusted {
		t.Fatalf("обратно к 14: %+v err=%v", f, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plan_window_length WHERE chat_id = $1`, chat).Scan(&n); err != nil || n != 0 {
		t.Fatalf("после сброса в базе осталось %d строк (err=%v)", n, err)
	}
}
