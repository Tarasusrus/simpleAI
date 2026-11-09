package envelopepush

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/botlimits"
	"simpleAI/internal/budget"
)

type fakeStore struct {
	ok  bool
	err error
}

func (f fakeStore) GetActiveEnvelope(_ context.Context, _ int64) (*budget.Envelope, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	if !f.ok {
		return nil, false, nil
	}
	return &budget.Envelope{}, true, nil
}

type fakeSkill struct {
	out        string
	err        error
	calls      int
	sawChatID  int64
	sawRequest string
}

func (f *fakeSkill) Run(ctx context.Context, input string) (string, error) {
	f.calls++
	f.sawRequest = input
	if id, ok := ctx.Value(agent.ChatIDKey{}).(int64); ok {
		f.sawChatID = id
	}
	return f.out, f.err
}

// Нет активного конверта → тела нет и скилл даже не дёргается.
func TestMorningEnvelopes_NoActiveEnvelope(t *testing.T) {
	skill := &fakeSkill{out: "Активного конверта нет."}
	body, err := New(fakeStore{ok: false}, skill).MorningEnvelopes(context.Background(), 42, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if body != "" {
		t.Fatalf("want empty body, got %q", body)
	}
	if skill.calls != 0 {
		t.Fatalf("skill must not be called without an envelope, got %d calls", skill.calls)
	}
}

// Конверт есть → тело от скилла, chat_id доехал в контексте.
func TestMorningEnvelopes_ActiveEnvelope(t *testing.T) {
	skill := &fakeSkill{out: "🍚 Еда: 5000 ฿"}
	body, err := New(fakeStore{ok: true}, skill).MorningEnvelopes(context.Background(), 42, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(body, "Еда") || !strings.HasPrefix(body, MorningHeader) {
		t.Fatalf("unexpected body: %q", body)
	}
	if skill.sawChatID != 42 {
		t.Fatalf("chat_id not propagated: %d", skill.sawChatID)
	}
	if !strings.Contains(skill.sawRequest, "конверт") {
		t.Fatalf("request must ask for envelopes: %q", skill.sawRequest)
	}
}

// Пустой вывод скилла — не повод слать одну шапку.
func TestMorningEnvelopes_EmptySkillOutput(t *testing.T) {
	body, err := New(fakeStore{ok: true}, &fakeSkill{out: "  "}).MorningEnvelopes(context.Background(), 42, nil)
	if err != nil || body != "" {
		t.Fatalf("want empty body without error, got %q / %v", body, err)
	}
}

func TestMorningEnvelopes_StoreError(t *testing.T) {
	_, err := New(fakeStore{err: errors.New("boom")}, &fakeSkill{}).MorningEnvelopes(context.Background(), 42, nil)
	if err == nil {
		t.Fatal("want error from store")
	}
}

type fakePlan struct {
	snap  botlimits.Snapshot
	ok    bool
	err   error
	calls []string
}

func (f *fakePlan) Snapshot(_ context.Context, _ int64, cur string) (botlimits.Snapshot, bool, error) {
	f.calls = append(f.calls, cur)
	return f.snap, f.ok, f.err
}

func planSnap() botlimits.Snapshot {
	pd := int64(8888)
	return botlimits.Snapshot{
		Cur: "THB", WindowStart: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), WindowEnd: time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC),
		DaysLeft: 9, CanSpend: 80000, PerDay: &pd, LimitHold: 13000,
		Limits: []botlimits.Limit{
			{Category: "еда", Amount: 20000, Spent: 5000, Left: 13000},
			{Category: "Такси", Amount: 1000, Spent: 1300, Over: 300},
		},
	}
}

// Пуш из плана: «Свободно», «на день» и лимиты из plan.View; в шапке и теле нет слова «конверты»; конверт и скилл
// не читаются.
//
// Мутации: не вызывать план; вернуть старую шапку; читать конверт при готовом плане; не печатать лимиты.
func TestMorningEnvelopes_FromPlan(t *testing.T) {
	skill := &fakeSkill{out: "старое тело"}
	pl := &fakePlan{snap: planSnap(), ok: true}
	body, err := New(fakeStore{ok: true}, skill).WithPlan(pl).MorningEnvelopes(context.Background(), 42, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{PlanHeader, "05.03 — 18.03 · осталось 9 дней", "**На день: 8 888 ฿**", "Свободно до конца окна: 80 000 ฿",
		"• Еда — осталось 13 000 ฿ из 20 000 ฿", "• Такси — пробито на 300 ฿ (лимит 1 000 ฿)"} {
		if !strings.Contains(body, want) {
			t.Errorf("в пуше нет %q:\n%s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "конверт") {
		t.Errorf("в пуше осталось слово «конверты»:\n%s", body)
	}
	if skill.calls != 0 || len(pl.calls) != 1 || pl.calls[0] != "THB" {
		t.Fatalf("скилл вызван %d раз, план: %v", skill.calls, pl.calls)
	}
}

// План не отвечает (вне области, окна нет, ошибка) — прежний путь по конверту; план выключен — тоже.
func TestMorningEnvelopes_PlanFallsBackToEnvelope(t *testing.T) {
	for name, pl := range map[string]*fakePlan{
		"вне области / нет окна": {ok: false},
		"ошибка плана":           {ok: true, err: errors.New("boom")},
	} {
		skill := &fakeSkill{out: "старое тело"}
		body, err := New(fakeStore{ok: true}, skill).WithPlan(pl).MorningEnvelopes(context.Background(), 42, nil)
		if err != nil || body != MorningHeader+"\n\nстарое тело" || skill.calls != 1 {
			t.Errorf("%s: body=%q err=%v calls=%d", name, body, err, skill.calls)
		}
	}
	skill := &fakeSkill{out: "старое тело"}
	body, _ := New(fakeStore{ok: true}, skill).MorningEnvelopes(context.Background(), 42, nil) //nolint:errcheck // проверяется тело
	if body != MorningHeader+"\n\nстарое тело" {
		t.Errorf("без плана: %q", body)
	}
	// нет окна в плане и нет конверта — слать нечего
	if body, err := New(fakeStore{ok: false}, &fakeSkill{}).WithPlan(&fakePlan{ok: false}).MorningEnvelopes(context.Background(), 42, nil); err != nil || body != "" {
		t.Errorf("нечего слать: %q %v", body, err)
	}
}
