package envelopepush

import (
	"context"
	"errors"
	"strings"
	"testing"

	"simpleAI/internal/agent"
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
