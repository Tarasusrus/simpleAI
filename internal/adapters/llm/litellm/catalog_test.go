package litellm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"simpleAI/internal/core"
)

// testdata/model_info.json снят с живого прокси litellm-free-models и
// обезличен: оставлены model_name, litellm_params.model и флаги возможностей.
// Группа vision в нём собрана из двух реальных vision-деплойментов: такого
// маршрута в прокси пока нет (эпик litellm-free-models-4).
func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/model_info.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type server struct {
	*httptest.Server
	calls atomic.Int32
}

func newServer(t *testing.T, status int, body []byte) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		if r.URL.Path != "/v1/model/info" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		if _, err := w.Write(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func byName(models []core.ModelInfo) map[string]core.ModelCapabilities {
	m := make(map[string]core.ModelCapabilities, len(models))
	for _, mi := range models {
		m[mi.Name] = mi.Capabilities
	}
	return m
}

func TestCatalog_GroupCapabilitiesRequireEveryDeployment(t *testing.T) {
	s := newServer(t, http.StatusOK, fixture(t))
	models, err := NewCatalog(s.URL+"/v1/", "secret", nil, time.Minute).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	got := byName(models)
	want := map[string]core.ModelCapabilities{
		// standard: один деплоймент с true, один с null, один с false.
		"standard":              {},
		"vision":                {Vision: true, PDFInput: true},
		"gemini-3.5-flash-lite": {Vision: true, PDFInput: true},
	}
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for name, caps := range want {
		if got[name] != caps {
			t.Errorf("%s: caps = %+v, want %+v", name, got[name], caps)
		}
	}
}

func TestCatalog_WhenFlagIsNull_GroupLacksIt(t *testing.T) {
	body := []byte(`{"data":[
		{"model_name":"g","model_info":{"supports_vision":true,"supports_pdf_input":true}},
		{"model_name":"g","model_info":{"supports_vision":null}},
		{"model_name":"p","model_info":{"supports_vision":true,"supports_pdf_input":null}},
		{"model_name":"p","model_info":{"supports_vision":true,"supports_pdf_input":true}}
	]}`)
	s := newServer(t, http.StatusOK, body)
	models, err := NewCatalog(s.URL+"/v1", "secret", nil, time.Minute).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	got := byName(models)
	if got["g"] != (core.ModelCapabilities{}) {
		t.Errorf("g: caps = %+v, want none", got["g"])
	}
	if want := (core.ModelCapabilities{Vision: true}); got["p"] != want {
		t.Errorf("p: caps = %+v, want %+v", got["p"], want)
	}
}

func TestCatalog_WhenProxyFails_ShouldReturnError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		key    string
	}{
		{"server error", http.StatusInternalServerError, `{}`, "secret"},
		{"wrong key", http.StatusOK, `{"data":[]}`, "wrong"},
		{"broken json", http.StatusOK, `{"data":[`, "secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(t, tt.status, []byte(tt.body))
			models, err := NewCatalog(s.URL+"/v1", tt.key, nil, time.Minute).Models(context.Background())
			if err == nil {
				t.Fatalf("models = %v, want error", models)
			}
		})
	}
}

func TestCatalog_WhenProxyIsDown_ShouldReturnError(t *testing.T) {
	s := newServer(t, http.StatusOK, fixture(t))
	url := s.URL
	s.Close()
	if _, err := NewCatalog(url+"/v1", "secret", nil, time.Minute).Models(context.Background()); err == nil {
		t.Fatal("want error from closed server")
	}
}

func TestCatalog_CachesSuccessForTTL(t *testing.T) {
	s := newServer(t, http.StatusOK, fixture(t))
	c := NewCatalog(s.URL+"/v1", "secret", nil, time.Minute)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	for range 3 {
		if _, err := c.Models(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.calls.Load(); n != 1 {
		t.Fatalf("calls within TTL = %d, want 1", n)
	}
	now = now.Add(time.Minute)
	if _, err := c.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := s.calls.Load(); n != 2 {
		t.Fatalf("calls after TTL = %d, want 2", n)
	}
}

// Прокси поднят после старта бота: первая ошибка не должна запомниться.
func TestCatalog_DoesNotCacheError(t *testing.T) {
	var up atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if _, err := w.Write(fixture(t)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewCatalog(srv.URL, "secret", nil, time.Hour)

	if _, err := c.Models(context.Background()); err == nil {
		t.Fatal("want error while proxy is down")
	}
	up.Store(true)
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("after proxy is up: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("want models after proxy is up")
	}
}
