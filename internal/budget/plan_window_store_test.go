package budget

import (
	"reflect"
	"testing"
	"time"
)

// Правка длины окна: перезапись, область чатов, свежая правка побеждает, сброс по области.
//
// Мутации: убрать ON CONFLICT DO UPDATE (повтор падает); убрать фильтр chat_id (чужой чат виден/стирается);
// ORDER BY updated_at без DESC (побеждает старая правка).
func TestPlanWindowLength_SetReadReset(t *testing.T) {
	s, ctx := rateTestStore(t)
	chatA, chatB, chatOther := limitChat, limitChat-1, limitChat-2
	clean := func() {
		if _, err := s.pool.Exec(ctx, `DELETE FROM plan_window_length WHERE chat_id IN ($1, $2, $3)`, chatA, chatB, chatOther); err != nil {
			t.Errorf("очистка: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)
	oct5 := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	oct19 := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

	if err := s.SetPlanWindowLength(ctx, chatA, oct5, 16); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlanWindowLength(ctx, chatA, oct5, 17); err != nil { // повтор перезаписывает
		t.Fatalf("повтор: %v", err)
	}
	if err := s.SetPlanWindowLength(ctx, chatOther, oct19, 30); err != nil {
		t.Fatal(err)
	}
	got, err := s.PlanWindowLengths(ctx, []int64{chatA, chatB})
	want := []PlanWindowLength{{WindowStart: oct5, Days: 17}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("PlanWindowLengths=%+v err=%v, ждали %+v (чужой чат не виден)", got, err, want)
	}

	if err := s.SetPlanWindowLength(ctx, chatB, oct5, 10); err != nil { // свежая правка другого чата области
		t.Fatal(err)
	}
	if got, err := s.PlanWindowLengths(ctx, []int64{chatA, chatB}); err != nil || len(got) != 1 || got[0].Days != 10 {
		t.Fatalf("побеждает свежая правка: %+v err=%v", got, err)
	}

	if err := s.ResetPlanWindowLength(ctx, []int64{chatA, chatB}, oct5); err != nil {
		t.Fatal(err)
	}
	if got, err := s.PlanWindowLengths(ctx, []int64{chatA, chatB}); err != nil || len(got) != 0 {
		t.Fatalf("после сброса: %+v err=%v", got, err)
	}
	if got, err := s.PlanWindowLengths(ctx, []int64{chatOther}); err != nil || len(got) != 1 {
		t.Fatalf("сброс задел чужой чат: %+v err=%v", got, err)
	}
	if err := s.ResetPlanWindowLength(ctx, []int64{chatA}, oct5); err != nil { // сброс без правки — не ошибка
		t.Fatalf("повторный сброс: %v", err)
	}
	if err := s.SetPlanWindowLength(ctx, chatA, oct5, 63); err == nil {
		t.Fatal("база обязана отвергнуть длину вне 1..62")
	}
}
