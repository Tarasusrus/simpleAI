package budgetskill

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"simpleAI/internal/botlimits"
)

type fakePlanWriter struct {
	fakePlanLimits
	setCalls   []string
	clearCalls []string
	ok, found  bool
	err        error
}

func (f *fakePlanWriter) SetLimit(_ context.Context, chatID int64, category string, amount float64, currency string) (bool, error) {
	f.setCalls = append(f.setCalls, fmt.Sprintf("%d|%s|%v|%s", chatID, category, amount, currency))
	return f.ok, f.err
}

func (f *fakePlanWriter) ClearLimit(_ context.Context, chatID int64, category string) (bool, bool, error) {
	f.clearCalls = append(f.clearCalls, fmt.Sprintf("%d|%s", chatID, category))
	return f.ok, f.found, f.err
}

func writerSkill(w *fakePlanWriter, fromPlan bool) *BudgetSkill {
	return (&BudgetSkill{buckets: defaultBuckets()}).WithLimits(w, fromPlan)
}

// set_share_limit при FromPlan пишет в ядро плана, а не в override; старый путь (store=nil) не вызывается.
//
// Мутации: не вызывать setPlanLimit; игнорировать флаг; терять валюту/категорию; печатать не ту сумму.
func TestSetShareLimit_WritesThroughPlan(t *testing.T) {
	w := &fakePlanWriter{ok: true}
	out, err := writerSkill(w, true).setShareLimit(warnCtx(), budgetInput{Name: "еда", Amount: 15000, Currency: "THB"})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.setCalls) != 1 || w.setCalls[0] != fmt.Sprintf("%d|еда|15000|THB", warnChatID) {
		t.Fatalf("в план ушло %v", w.setCalls)
	}
	for _, want := range []string{"Лимит на «еда»", "15 000 ฿", "на окно плана", "повторяется каждое окно"} {
		if !strings.Contains(out, want) {
			t.Errorf("в ответе нет %q: %s", want, out)
		}
	}
	// валюта не названа — пустая строка уходит в план, дефолт RUB выбирает ядро; в ответе — рубли
	w2 := &fakePlanWriter{ok: true}
	out, _ = writerSkill(w2, true).setShareLimit(warnCtx(), budgetInput{Category: "Еда", Amount: 15000}) //nolint:errcheck // текст проверяется ниже
	if !strings.Contains(out, "15 000 ₽") || w2.setCalls[0] != fmt.Sprintf("%d|Еда|15000|RUB", warnChatID) {
		t.Fatalf("без валюты: %q %v", out, w2.setCalls)
	}
}

// План отказал в категории — понятный ответ, не «ошибка»; прочие сбои — общий текст без подробностей.
func TestSetShareLimit_PlanErrors(t *testing.T) {
	w := &fakePlanWriter{ok: true, err: fmt.Errorf("%w: boom", botlimits.ErrUnknownCategory)}
	out, err := writerSkill(w, true).setShareLimit(warnCtx(), budgetInput{Name: "марсиане", Amount: 1})
	if err != nil || !strings.Contains(out, "Не нашёл категорию расходов «марсиане»") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	w = &fakePlanWriter{ok: true, err: errors.New("secret db detail")}
	out, err = writerSkill(w, true).setShareLimit(warnCtx(), budgetInput{Name: "еда", Amount: 1})
	if err != nil || !strings.Contains(out, "Не удалось поставить лимит") || strings.Contains(out, "secret") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestClearShareLimit_ThroughPlanIdempotent(t *testing.T) {
	w := &fakePlanWriter{ok: true, found: true}
	out, err := writerSkill(w, true).clearShareLimit(warnCtx(), budgetInput{Name: "еда"})
	if err != nil || !strings.Contains(out, "Лимит на «еда» снят") || len(w.clearCalls) != 1 {
		t.Fatalf("out=%q err=%v calls=%v", out, err, w.clearCalls)
	}
	w.found = false
	out, err = writerSkill(w, true).clearShareLimit(warnCtx(), budgetInput{Name: "еда"})
	if err != nil || !strings.Contains(out, "и так нет") {
		t.Fatalf("нет лимита: out=%q err=%v", out, err)
	}
	w.err = errors.New("boom")
	if out, _ := writerSkill(w, true).clearShareLimit(warnCtx(), budgetInput{Name: "еда"}); !strings.Contains(out, "Не удалось снять лимит") { //nolint:errcheck // проверяется текст
		t.Fatalf("ошибка: %q", out)
	}
}

// Флаг выключен, чат вне области или нет окна — запись остаётся на старом пути (plan не пишется).
func TestPlanLimitWrite_FallsBackToOldPath(t *testing.T) {
	cases := map[string]struct {
		w        *fakePlanWriter
		fromPlan bool
		calls    int
	}{
		"флаг выключен": {&fakePlanWriter{ok: true}, false, 0},
		"вне области":   {&fakePlanWriter{ok: false}, true, 1},
	}
	for name, c := range cases {
		s := writerSkill(c.w, c.fromPlan)
		if reply, done := s.setPlanLimit(warnCtx(), warnChatID, "еда", 1, "RUB"); done || reply != "" {
			t.Errorf("%s: set done=%v reply=%q", name, done, reply)
		}
		if reply, done := s.clearPlanLimit(warnCtx(), warnChatID, "еда"); done || reply != "" {
			t.Errorf("%s: clear done=%v reply=%q", name, done, reply)
		}
		if len(c.w.setCalls) != c.calls || len(c.w.clearCalls) != c.calls {
			t.Errorf("%s: вызовов плана set=%d clear=%d, ждали %d", name, len(c.w.setCalls), len(c.w.clearCalls), c.calls)
		}
	}
	// писателя нет (читатель без записи) — тоже старый путь
	r := &fakePlanLimits{ok: true}
	s := (&BudgetSkill{buckets: defaultBuckets()}).WithLimits(r, true)
	if _, done := s.setPlanLimit(warnCtx(), warnChatID, "еда", 1, "RUB"); done {
		t.Error("без писателя ответили из плана")
	}
}
