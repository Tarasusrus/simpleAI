package budget

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Окна плана: повторное сохранение того же окна, область чатов, правка, удаление, ограничения базы.
//
// Мутации: убрать ON CONFLICT … DO NOTHING (повтор падает); убрать фильтр chat_id (чужой чат правится);
// убрать NOT deleted в UPDATE (правится удалённое окно).
func TestPlanWindow_CRUD(t *testing.T) {
	s, ctx := rateTestStore(t)
	chat, other := limitChat, limitChat-1
	clean := func() {
		if _, err := s.pool.Exec(ctx, `DELETE FROM plan_window WHERE chat_id IN ($1, $2)`, chat, other); err != nil {
			t.Errorf("очистка: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)
	day := func(m time.Month, d int) time.Time { return time.Date(2099, m, d, 0, 0, 0, 0, time.UTC) }

	id, err := s.CreatePlanWindow(ctx, chat, day(10, 5), day(10, 18), false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreatePlanWindow(ctx, chat, day(10, 5), day(10, 18), false)
	if err != nil || again != id {
		t.Fatalf("повтор вернул %v (err=%v), ждали %v", again, err, id)
	}
	if _, err := s.CreatePlanWindow(ctx, other, day(10, 5), day(10, 18), false); err != nil {
		t.Fatal(err)
	}
	got, err := s.PlanWindows(ctx, []int64{chat})
	if err != nil || len(got) != 1 || got[0].ID != id || !got[0].End.Equal(day(10, 18)) {
		t.Fatalf("PlanWindows=%+v err=%v", got, err)
	}

	if err := s.UpdatePlanWindow(ctx, []int64{other - 5}, id, day(10, 3), day(10, 20), true); !errors.Is(err, ErrPlanWindowNotFound) {
		t.Fatalf("правка из чужой области: %v", err)
	}
	if err := s.UpdatePlanWindow(ctx, []int64{chat}, id, day(10, 3), day(10, 20), true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.PlanWindows(ctx, []int64{chat}); err != nil || !got[0].Start.Equal(day(10, 3)) || !got[0].End.Equal(day(10, 20)) || !got[0].EndFixed {
		t.Fatalf("после правки: %+v err=%v", got, err)
	}

	if err := s.DeletePlanWindow(ctx, []int64{chat}, id); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePlanWindow(ctx, []int64{chat}, id, day(10, 3), day(10, 21), true); !errors.Is(err, ErrPlanWindowNotFound) {
		t.Fatalf("правка удалённого окна: %v", err)
	}
	if err := s.DeletePlanWindow(ctx, []int64{chat}, uuid.New()); !errors.Is(err, ErrPlanWindowNotFound) {
		t.Fatalf("удаление несуществующего: %v", err)
	}
	if got, err := s.PlanWindows(ctx, []int64{chat}); err != nil || len(got) != 1 || !got[0].Deleted {
		t.Fatalf("удалённое окно остаётся в базе с пометкой: %+v err=%v", got, err)
	}
	// После удаления на той же дате можно снова сохранить окно (уникальность только среди действующих).
	if id2, err := s.CreatePlanWindow(ctx, chat, day(10, 3), day(10, 16), false); err != nil || id2 == id {
		t.Fatalf("новое окно на дате удалённого: %v err=%v", id2, err)
	}
	if _, err := s.CreatePlanWindow(ctx, chat, day(12, 1), day(11, 1), false); err == nil {
		t.Fatal("база обязана отвергнуть конец раньше начала")
	}
	if _, err := s.CreatePlanWindow(ctx, chat, day(12, 1), day(12, 1).AddDate(0, 0, 62), false); err == nil {
		t.Fatal("база обязана отвергнуть окно длиннее 62 дней")
	}
}
