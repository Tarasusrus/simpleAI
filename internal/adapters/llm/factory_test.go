package llm

import (
	"context"
	"errors"
	"testing"
)

type stubLLM struct {
	ask     func(ctx context.Context, prompt string) (string, error)
	askSys  func(ctx context.Context, sys, user string) (string, error)
	embed   func(ctx context.Context, in []string) ([][]float32, error)
	calls   int
}

func (s *stubLLM) Ask(ctx context.Context, p string) (string, error) {
	s.calls++
	return s.ask(ctx, p)
}
func (s *stubLLM) AskWithSystem(ctx context.Context, sys, u string) (string, error) {
	s.calls++
	if s.askSys != nil {
		return s.askSys(ctx, sys, u)
	}
	return s.ask(ctx, u)
}
func (s *stubLLM) Embed(ctx context.Context, in []string) ([][]float32, error) {
	if s.embed != nil {
		return s.embed(ctx, in)
	}
	return nil, errors.New("not implemented")
}

func TestCompositeAsk_PrimaryOK(t *testing.T) {
	primary := &stubLLM{ask: func(ctx context.Context, p string) (string, error) { return "primary", nil }}
	fb := &stubLLM{ask: func(ctx context.Context, p string) (string, error) { return "fb", nil }}
	c := newCompositeClient(primary, fb, nil)

	out, err := c.Ask(context.Background(), "hi")
	if err != nil || out != "primary" {
		t.Fatalf("want primary,nil; got %q,%v", out, err)
	}
	if fb.calls != 0 {
		t.Fatalf("fallback should not be called, calls=%d", fb.calls)
	}
}

func TestCompositeAsk_PrimaryFails_FallbackUsed(t *testing.T) {
	primary := &stubLLM{ask: func(ctx context.Context, p string) (string, error) { return "", errors.New("429") }}
	fb := &stubLLM{ask: func(ctx context.Context, p string) (string, error) { return "fb", nil }}
	c := newCompositeClient(primary, fb, nil)

	out, err := c.Ask(context.Background(), "hi")
	if err != nil || out != "fb" {
		t.Fatalf("want fb,nil; got %q,%v", out, err)
	}
	if fb.calls != 1 {
		t.Fatalf("fallback calls=%d want 1", fb.calls)
	}
}

func TestCompositeAsk_CtxCancelled_NoFallback(t *testing.T) {
	primary := &stubLLM{ask: func(ctx context.Context, p string) (string, error) { return "", ctx.Err() }}
	fb := &stubLLM{ask: func(ctx context.Context, p string) (string, error) { return "fb", nil }}
	c := newCompositeClient(primary, fb, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Ask(ctx, "hi")
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if fb.calls != 0 {
		t.Fatalf("fallback should not be called when ctx cancelled, calls=%d", fb.calls)
	}
}

func TestCompositeAskWithSystem_FallbackUsed(t *testing.T) {
	primary := &stubLLM{askSys: func(ctx context.Context, s, u string) (string, error) { return "", errors.New("boom") }}
	fb := &stubLLM{askSys: func(ctx context.Context, s, u string) (string, error) { return "fb", nil }}
	c := newCompositeClient(primary, fb, nil)

	out, err := c.AskWithSystem(context.Background(), "sys", "u")
	if err != nil || out != "fb" {
		t.Fatalf("want fb,nil; got %q,%v", out, err)
	}
}

func TestCompositeEmbed_UsesFallback(t *testing.T) {
	primary := &stubLLM{ask: func(ctx context.Context, p string) (string, error) { return "p", nil }}
	want := [][]float32{{1, 2, 3}}
	fb := &stubLLM{
		ask:   func(ctx context.Context, p string) (string, error) { return "fb", nil },
		embed: func(ctx context.Context, in []string) ([][]float32, error) { return want, nil },
	}
	c := newCompositeClient(primary, fb, nil)
	got, err := c.Embed(context.Background(), []string{"x"})
	if err != nil || len(got) != 1 || got[0][0] != 1 {
		t.Fatalf("unexpected embed result: %v err=%v", got, err)
	}
}
