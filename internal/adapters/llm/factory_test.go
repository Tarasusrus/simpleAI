package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"simpleAI/config"
	openaiadapter "simpleAI/internal/adapters/llm/openai"
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

// TestComposite_503Primary_FastFallback: реальный openai клиент против
// httptest-сервера, отдающего 503. Должен фейлиться без ретраев и за <2s
// прыгать на fallback.
func TestComposite_503Primary_FastFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Service is too busy",
				"type":    "service_unavailable_error",
				"code":    "service_unavailable_error",
			},
		}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	cfg := config.Config{
		LLM: config.LLMConfig{
			APIKey:     "test",
			BaseURL:    srv.URL,
			ChatModel:  "deepseek-chat",
			Timeout:    5 * time.Second,
			RetryCount: 0,
		},
	}
	primary, err := openaiadapter.NewClient(cfg, nil)
	if err != nil {
		t.Fatalf("primary build: %v", err)
	}
	fb := &stubLLM{askSys: func(ctx context.Context, s, u string) (string, error) { return "fb-ok", nil }}
	c := newCompositeClient(primary, fb, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	out, err := c.AskWithSystem(ctx, "sys", "u")
	elapsed := time.Since(start)

	if err != nil || out != "fb-ok" {
		t.Fatalf("want fb-ok,nil; got %q,%v", out, err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("fallback took too long: %v (expected <2s)", elapsed)
	}
	if fb.calls != 1 {
		t.Fatalf("fallback calls=%d want 1", fb.calls)
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
