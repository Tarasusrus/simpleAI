package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"simpleAI/internal/plan"
)

const goodToken = "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWprbA" // 46 символов base64url

type fakePlanner struct {
	res  plan.Result
	err  error
	curs []plan.Currency
}

func (f *fakePlanner) View(_ context.Context, cur plan.Currency) (plan.Result, error) {
	f.curs = append(f.curs, cur)
	return f.res, f.err
}

func cfg() Config { return Config{Enabled: true, Addr: "127.0.0.1:0", Token: goodToken} }

func newServer(t *testing.T, p Planner) (*Server, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	s, err := New(cfg(), p, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, &logs
}

func do(s *Server, method, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, http.NoBody)
	req.Host = "127.0.0.1:8090"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestValidateToken(t *testing.T) {
	cases := map[string]struct {
		tok string
		ok  bool
	}{
		"пустой":        {"", false},
		"короткий":      {"abc", false},
		"42 символа":    {strings.Repeat("a", 41) + "_", false},
		"43 символа":    {strings.Repeat("aB1_-", 8) + "aB1", true},
		"недопустимый":  {strings.Repeat("a", 42) + "!", false},
		"hex 63":        {strings.Repeat("a", 63), false},
		"hex 64":        {strings.Repeat("a", 64), true},
		"пробел внутри": {strings.Repeat("a", 30) + " " + strings.Repeat("b", 30), false},
	}
	for name, c := range cases {
		err := ValidateToken(c.tok)
		if (err == nil) != c.ok {
			t.Errorf("%s: ok=%v, err=%v", name, c.ok, err)
		}
		if err != nil && c.tok != "" && strings.Contains(err.Error(), c.tok) {
			t.Errorf("%s: значение токена попало в текст ошибки", name)
		}
	}
}

// Слабый токен и не-loopback без разрешения — сервер не создаётся (fail-closed).
func TestNew_FailClosed(t *testing.T) {
	bad := cfg()
	bad.Token = "short"
	if _, err := New(bad, &fakePlanner{}, nil); err == nil {
		t.Error("сервер создан со слабым токеном")
	}
	open := cfg()
	open.Addr = "0.0.0.0:8090"
	if _, err := New(open, &fakePlanner{}, nil); err == nil {
		t.Error("сервер создан на не-loopback без разрешения")
	}
	open.AllowNonLoopback = true
	if _, err := New(open, &fakePlanner{}, nil); err != nil {
		t.Errorf("с разрешением должно работать: %v", err)
	}
}

func TestAuth(t *testing.T) {
	s, _ := newServer(t, &fakePlanner{})
	if c := do(s, "GET", "/api/plan/window", "").Code; c != http.StatusUnauthorized {
		t.Errorf("без токена %d, ждали 401", c)
	}
	if c := do(s, "GET", "/api/plan/window", "wrong").Code; c != http.StatusUnauthorized {
		t.Errorf("неверный токен %d, ждали 401", c)
	}
	if c := do(s, "GET", "/api/plan/window", goodToken).Code; c != http.StatusOK {
		t.Errorf("верный токен %d, ждали 200", c)
	}
	// Токен без префикса Bearer не принимается.
	req := httptest.NewRequest(http.MethodGet, "/api/plan/window", http.NoBody)
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", goodToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("токен без Bearer принят: %d", rec.Code)
	}
}

// Лимит считает только неверные попытки; верный токен им не блокируется.
//
// Мутация: убрать вызов limiter.allow() из auth — тест краснеет.
func TestAuth_RateLimitOnlyBadAttempts(t *testing.T) {
	s, _ := newServer(t, &fakePlanner{})
	var last int
	for i := 0; i < maxBadPerMin+3; i++ {
		last = do(s, "GET", "/api/plan/window", "wrong").Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("после серии неверных попыток код %d, ждали 429", last)
	}
	if c := do(s, "GET", "/api/plan/window", goodToken).Code; c != http.StatusOK {
		t.Errorf("верный токен заблокирован лимитом: %d", c)
	}
}

func TestHostCheckAndHeaders(t *testing.T) {
	s, _ := newServer(t, &fakePlanner{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("чужой Host: %d, ждали 403", rec.Code)
	}

	rec = do(s, "GET", "/healthz", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("healthz: %d %q", rec.Code, rec.Body.String())
	}
	want := map[string]string{
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
		"X-Robots-Tag":            "noindex",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'; base-uri 'none'",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("заголовок %s = %q, ждали %q", k, got, v)
		}
	}
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "unsafe-inline") {
		t.Error("CSP допускает unsafe-inline")
	}
}

func TestWindow_ContractShape(t *testing.T) {
	per := int64(300)
	amt := int64(1500)
	date := plan.Date{Year: 2026, Month: 3, Day: 15}
	p := &fakePlanner{res: plan.Result{
		Meta: plan.Meta{Today: plan.Date{Year: 2026, Month: 3, Day: 10}, HasRate: true},
		View: plan.View{
			State:  plan.StateCurrent,
			Window: &plan.WindowInfo{Start: plan.Date{Year: 2026, Month: 3, Day: 5}, End: plan.Date{Year: 2026, Month: 3, Day: 18}, DaysLeft: 9},
			Income: plan.IncomeSum{Received: 100000}, CanSpend: 40000, Payments: 45000, Spent: 5000, Reserve: 10000, PerDay: &per,
			Timeline: []plan.Row{{Key: "k", Date: &date, Name: "аренда", Kind: plan.KindPayment, Amount: &amt}},
		},
	}}
	s, _ := newServer(t, p)
	rec := do(s, "GET", "/api/plan/window?cur=THB", goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	if len(p.curs) != 1 || p.curs[0] != plan.CurTHB {
		t.Errorf("валюта не дошла до сервиса: %v", p.curs)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("не JSON: %v", err)
	}
	for _, k := range []string{"v", "meta", "state", "window", "kpi", "timeline", "next", "later", "inbox", "categories"} {
		if _, ok := got[k]; !ok {
			t.Errorf("в ответе нет ключа %q", k)
		}
	}
	for _, k := range []string{"timeline", "later", "inbox", "categories"} {
		if _, ok := got[k].([]any); !ok {
			t.Errorf("%q должен быть массивом (даже пустым), а не %T", k, got[k])
		}
	}
	kpi, ok := got["kpi"].(map[string]any)
	if !ok {
		t.Fatalf("kpi не объект: %T", got["kpi"])
	}
	for _, k := range []string{"canSpend", "perDay", "payments", "spent", "reserve", "received", "expectedIncome", "unpriced", "undatedCount", "undatedSum"} {
		if _, ok := kpi[k]; !ok {
			t.Errorf("в kpi нет ключа %q", k)
		}
	}
	if kpi["canSpend"] != float64(40000) || kpi["perDay"] != float64(300) {
		t.Errorf("kpi: %v", kpi)
	}
	tl, ok := got["timeline"].([]any)
	if !ok || len(tl) != 1 {
		t.Fatalf("лента: %v", got["timeline"])
	}
	row, ok := tl[0].(map[string]any)
	if !ok {
		t.Fatalf("строка ленты не объект: %T", tl[0])
	}
	if row["date"] != "2026-03-15" || row["amount"] != float64(1500) || row["name"] != "аренда" {
		t.Errorf("строка ленты: %v", row)
	}
	if meta, ok := got["meta"].(map[string]any); !ok || meta["cur"] != "THB" {
		t.Errorf("meta.cur: %v", got["meta"])
	}
}

func TestWindow_Errors(t *testing.T) {
	cases := []struct {
		name   string
		target string
		err    error
		code   int
		text   string
	}{
		{"чужая валюта", "/api/plan/window?cur=USD", nil, 400, "RUB или THB"},
		{"нет курса", "/api/plan/window", plan.ErrNoRate, 400, "курс"},
		{"дубль срока", "/api/plan/window", plan.ErrDuplicateTerm, 500, "дубль"},
		{"внутренняя", "/api/plan/window", errors.New("pq: password authentication failed for user simpleai"), 500, "Не удалось"},
	}
	for _, c := range cases {
		s, _ := newServer(t, &fakePlanner{err: c.err})
		rec := do(s, "GET", c.target, goodToken)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.text) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "password") || strings.Contains(rec.Body.String(), "pq:") {
			t.Errorf("%s: детали внутренней ошибки ушли наружу: %s", c.name, rec.Body.String())
		}
	}
}

type panicPlanner struct{}

func (panicPlanner) View(context.Context, plan.Currency) (plan.Result, error) { panic("boom") }

func TestPanicBecomes500(t *testing.T) {
	s, logs := newServer(t, panicPlanner{})
	rec := do(s, "GET", "/api/plan/window", goodToken)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("паника: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "panic") {
		t.Error("паника не залогирована")
	}
}

// В лог не попадают URL, query и токен.
//
// Мутация: писать в лог r.URL.String() — тест краснеет.
func TestLog_NoURLNoToken(t *testing.T) {
	s, logs := newServer(t, &fakePlanner{})
	do(s, "GET", "/api/plan/window?secret=SUPERSECRET", goodToken)
	do(s, "GET", "/api/plan/window", "wrongtoken")
	out := logs.String()
	for _, bad := range []string{"SUPERSECRET", goodToken, "wrongtoken", "secret="} {
		if strings.Contains(out, bad) {
			t.Errorf("в логе %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "route=") {
		t.Errorf("нет строки запроса в логе: %s", out)
	}
}
