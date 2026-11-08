package planrepo

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

type fakeWindowDB struct {
	rows      []budget.PlanWindow
	readScope [][]int64
	created   []int64
	updScope  [][]int64
	fixed     []bool
	err       error
}

func (f *fakeWindowDB) PlanWindows(_ context.Context, ids []int64) ([]budget.PlanWindow, error) {
	f.readScope = append(f.readScope, ids)
	return f.rows, nil
}

func (f *fakeWindowDB) CreatePlanWindow(_ context.Context, chatID int64, _, _ time.Time, _ bool) (uuid.UUID, error) {
	f.created = append(f.created, chatID)
	return uuid.MustParse("11111111-1111-1111-1111-111111111111"), nil
}

func (f *fakeWindowDB) UpdatePlanWindow(_ context.Context, ids []int64, _ uuid.UUID, _, _ time.Time, fixed bool) error {
	f.fixed = append(f.fixed, fixed)
	f.updScope = append(f.updScope, ids)
	return f.err
}

func (f *fakeWindowDB) DeletePlanWindow(_ context.Context, ids []int64, _ uuid.UUID) error {
	f.updScope = append(f.updScope, ids)
	return f.err
}

func TestRead_WindowsMappedAndScoped(t *testing.T) {
	id := uuid.New()
	db := &fakeWindowDB{rows: []budget.PlanWindow{{ID: id, Start: day(2026, 10, 5), End: day(2026, 10, 20), Deleted: true, EndFixed: true}}}
	snap, err := newReader(&fakeSource{rates: map[string]float64{"THB": 2.5}}).WithWindows(db).
		Read(context.Background(), plan.Scope{ChatIDs: []int64{7, 9}})
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.StoredWindow{{ID: id.String(), Start: plan.Date{Year: 2026, Month: 10, Day: 5}, End: plan.Date{Year: 2026, Month: 10, Day: 20}, Deleted: true, FixedEnd: true}}
	if !reflect.DeepEqual(snap.Windows, want) || !reflect.DeepEqual(db.readScope, [][]int64{{7, 9}}) {
		t.Fatalf("windows=%+v scope=%v", snap.Windows, db.readScope)
	}
}

func TestWindowStore_ScopeAndErrors(t *testing.T) {
	db := &fakeWindowDB{}
	w := NewWindowStore(db)
	sc := plan.Scope{ChatIDs: []int64{7, 9}}
	d := plan.Date{Year: 2026, Month: 10, Day: 5}
	id, err := w.CreateWindow(context.Background(), sc, d, d.AddDays(13), false)
	if err != nil || id != "11111111-1111-1111-1111-111111111111" || !reflect.DeepEqual(db.created, []int64{7}) {
		t.Fatalf("создание идёт в первый чат: id=%s chats=%v err=%v", id, db.created, err)
	}
	if err := w.UpdateWindow(context.Background(), sc, id, d, d, true); err != nil || !reflect.DeepEqual(db.updScope, [][]int64{{7, 9}}) || !db.fixed[0] {
		t.Fatalf("правка по всей области: %v %v fixed=%v", db.updScope, err, db.fixed)
	}
	if err := w.UpdateWindow(context.Background(), sc, "не-uuid", d, d, false); !errors.Is(err, plan.ErrNotFound) {
		t.Fatalf("кривой id: %v", err)
	}
	db.err = budget.ErrPlanWindowNotFound
	if err := w.DeleteWindow(context.Background(), sc, id); !errors.Is(err, plan.ErrNotFound) {
		t.Fatalf("нет окна: %v", err)
	}
	if _, err := w.CreateWindow(context.Background(), plan.Scope{}, d, d, false); err == nil {
		t.Fatal("пустая область должна быть ошибкой")
	}
}

// Сквозной путь сервис → адаптер → база: начавшееся окно сохраняется само, правка двигает его даты, удалённое окно
// не появляется снова.
//
// Мутации: не подключать WithWindows у читателя — окно сохраняется на каждом чтении заново (ON CONFLICT спасает
// базу, но правка дат не видна); не подключать WithWindowStore — правка отвечает ErrReadOnly.
func TestWindowEntity_EndToEnd(t *testing.T) {
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
	const chat = int64(-880003)
	today := plan.Date{Year: 2099, Month: 10, Day: 20}
	svc := plan.NewService(
		NewReader(store, slog.New(slog.NewTextHandler(io.Discard, nil))).WithWindows(store),
		NewWriter(store), func() plan.Date { return today }, plan.Scope{ChatIDs: []int64{chat}}).
		WithWindowStore(NewWindowStore(store))
	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM budget_planned_expense WHERE chat_id = $1`, chat) //nolint:errcheck // очистка после теста
		pool.Exec(ctx, `DELETE FROM plan_window WHERE chat_id = $1`, chat)            //nolint:errcheck // очистка после теста
	}
	cleanup()
	t.Cleanup(cleanup)
	// Две зарплаты в прошлом и ожидаемая 05.11: после удаления текущего окна на экране останется будущее.
	for _, due := range []*time.Time{tp(2099, 10, 1), tp(2099, 10, 15), tp(2099, 11, 5)} {
		if _, err := store.AddPlanItem(ctx, budget.PlanItemInput{ChatID: chat, Direction: "income", Description: "window-entity-test зп",
			AmountMinor: ptr(10_000_000), Currency: "RUB", DueDate: due}); err != nil {
			t.Fatal(err)
		}
	}
	count := func() (active, deleted int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT deleted), count(*) FILTER (WHERE deleted)
			FROM plan_window WHERE chat_id = $1`, chat).Scan(&active, &deleted); err != nil {
			t.Fatal(err)
		}
		return
	}

	if _, err := svc.View(ctx, plan.CurRUB); err != nil {
		t.Fatal(err)
	}
	if a, _ := count(); a != 2 {
		t.Fatalf("два начавшихся окна сохранены: %d", a)
	}
	// Окно с 15.10 само тянется до дня перед ожидаемой зарплатой 05.11 (ADR-022).
	cur, err := svc.CurrentWindow(ctx)
	if err != nil || cur.End != (plan.Date{Year: 2099, Month: 11, Day: 4}) {
		t.Fatalf("рост до следующего поступления: %+v err=%v", cur, err)
	}
	f, err := svc.ResizeWindow(ctx, 2)
	if err != nil || f.End != (plan.Date{Year: 2099, Month: 11, Day: 6}) {
		t.Fatalf("+2: %+v err=%v", f, err)
	}
	var fixed bool
	if err := pool.QueryRow(ctx, `SELECT end_fixed FROM plan_window WHERE chat_id = $1 AND start_date = '2099-10-15'`, chat).Scan(&fixed); err != nil || !fixed {
		t.Fatalf("правка конца сохраняет «конец владельца»: %v err=%v", fixed, err)
	}
	if f, err = svc.SetWindowStart(ctx, plan.Date{Year: 2099, Month: 10, Day: 16}); err != nil || f.Days != 22 {
		t.Fatalf("начало 16.10: %+v err=%v", f, err)
	}
	if _, err = svc.DeleteWindow(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.View(ctx, plan.CurRUB); err != nil {
		t.Fatal(err)
	}
	if a, del := count(); a != 1 || del != 1 {
		t.Fatalf("после удаления: действующих %d, удалённых %d (удалённое не должно вернуться)", a, del)
	}
}
