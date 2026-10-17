package appwiring

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"simpleAI/config"
	"simpleAI/internal/budget"
	"simpleAI/internal/plugin"
)

type budgetStub struct{}

func (budgetStub) Manifest() plugin.Manifest                   { return plugin.Manifest{ID: "budget"} }
func (budgetStub) Run(context.Context, string) (string, error) { return "", nil }

type noCategories struct{}

func (noCategories) ListCategories(context.Context) ([]budget.Category, error) { return nil, nil }

func registryWithBudget(t *testing.T) *plugin.Registry {
	t.Helper()
	r := plugin.NewRegistry()
	if err := r.Register(budgetStub{}); err != nil {
		t.Fatal(err)
	}
	return r
}

// Выключенный сценарий должен давать nil-интерфейс: иначе WithReceiptPhotos
// не увидит, что он не настроен, и вызовет методы nil-указателя.
func TestBuildReceipt_WhenDisabled_FlowIsNilInterface(t *testing.T) {
	got := BuildReceipt(context.Background(), config.ReceiptConfig{Model: "vision"}, registryWithBudget(t), noCategories{}, slog.New(slog.DiscardHandler))
	if got.Service != nil || got.Catalog != nil {
		t.Fatalf("disabled config built %+v", got)
	}
	if got.Flow() != nil {
		t.Fatal("Flow() of disabled receipt is a non-nil interface")
	}
}

func TestBuildReceipt_WhenEnabled_LogsCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"data":[{"model_name":"vision","model_info":{"supports_vision":true}}]}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	logs := &syncBuffer{}
	cfg := config.ReceiptConfig{BaseURL: srv.URL, APIKey: "k", Model: "vision", MaxBytes: 1 << 20, Timeout: time.Second, CatalogTTL: time.Minute}

	got := BuildReceipt(context.Background(), cfg, registryWithBudget(t), noCategories{}, slog.New(slog.NewTextHandler(logs, nil)))
	if got.Service == nil || got.Flow() == nil || got.Catalog == nil {
		t.Fatalf("enabled config not built: %+v", got)
	}
	// Лог пишется в фоне, чтобы не задерживать старт бота.
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(logs.String(), "model_in_catalog=true") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := logs.String(); !strings.Contains(s, "model_in_catalog=true") || !strings.Contains(s, "vision=vision") {
		t.Fatalf("startup log: %s", s)
	}
}

// Прокси, который не отвечает, не должен задерживать старт.
func TestBuildReceipt_DoesNotWaitForSlowProxy(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })
	cfg := config.ReceiptConfig{BaseURL: srv.URL, APIKey: "k", Model: "vision", MaxBytes: 1 << 20, Timeout: time.Second, CatalogTTL: time.Minute}

	start := time.Now()
	BuildReceipt(context.Background(), cfg, registryWithBudget(t), noCategories{}, slog.New(slog.DiscardHandler))
	if d := time.Since(start); d > time.Second {
		t.Fatalf("BuildReceipt blocked %v on a hanging proxy", d)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestBuildReceipt_WithoutBudgetSkill_IsDisabled(t *testing.T) {
	cfg := config.ReceiptConfig{BaseURL: "http://x", APIKey: "k", Model: "vision"}
	if got := BuildReceipt(context.Background(), cfg, plugin.NewRegistry(), noCategories{}, slog.New(slog.DiscardHandler)); got.Flow() != nil {
		t.Fatal("receipt flow built without budget skill")
	}
}
