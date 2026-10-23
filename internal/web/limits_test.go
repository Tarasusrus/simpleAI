package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"simpleAI/internal/plan"
)

type fakeLimitPlanner struct {
	err     error
	added   []plan.AddLimitInput
	edited  map[string]plan.LimitEdit
	deleted []string
	prev    []plan.PreviewLimitInput
	preview plan.LimitPreview
}

func (f *fakeLimitPlanner) AddLimit(_ context.Context, in plan.AddLimitInput) (string, error) {
	f.added = append(f.added, in)
	return "lim-1", f.err
}

func (f *fakeLimitPlanner) EditLimit(_ context.Context, id string, e plan.LimitEdit) error {
	if f.edited == nil {
		f.edited = map[string]plan.LimitEdit{}
	}
	f.edited[id] = e
	return f.err
}

func (f *fakeLimitPlanner) DeleteLimit(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

func (f *fakeLimitPlanner) PreviewLimit(_ context.Context, _ plan.Currency, in plan.PreviewLimitInput) (plan.LimitPreview, error) {
	f.prev = append(f.prev, in)
	return f.preview, f.err
}

type fakeSuggester struct {
	sug  plan.LimitSuggestion
	err  error
	cats []string
	days []int
}

func (f *fakeSuggester) Suggest(_ context.Context, cat string, days int) (plan.LimitSuggestion, error) {
	f.cats, f.days = append(f.cats, cat), append(f.days, days)
	return f.sug, f.err
}

func send(s *Server, method, target, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "127.0.0.1:8090"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func limServer(t *testing.T, p *fakePlanner, lp *fakeLimitPlanner, sg *fakeSuggester) *Server {
	t.Helper()
	s, _ := newServer(t, p)
	var lpi LimitPlanner
	if lp != nil {
		lpi = lp
	}
	var sgi plan.LimitSuggester
	if sg != nil {
		sgi = sg
	}
	return s.WithLimits(lpi, sgi)
}

var limRoutes = []struct{ method, target, body string }{
	{"POST", "/api/plan/limits", `{"category":"Еда","amount":100,"currency":"THB","repeat":true}`},
	{"PUT", "/api/plan/limits/L1", `{"amount":100,"currency":"THB"}`},
	{"DELETE", "/api/plan/limits/L1", ``},
	{"POST", "/api/plan/limits/preview", `{"category":"Еда","amount":100,"currency":"THB"}`},
	{"GET", "/api/plan/limits/suggest?category=Еда", ``},
}

// Без токена ни один маршрут лимитов не доходит до сервиса.
//
// Мутация: убрать s.auth(...) у любого маршрута лимитов — тест краснеет.
func TestLimits_RequireToken(t *testing.T) {
	lp := &fakeLimitPlanner{}
	s := limServer(t, &fakePlanner{}, lp, &fakeSuggester{})
	for _, r := range limRoutes {
		for _, tok := range []string{"", "wrong"} {
			if rec := send(s, r.method, r.target, r.body, tok); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s токен %q: %d, ждали 401", r.method, r.target, tok, rec.Code)
			}
		}
	}
	if len(lp.added)+len(lp.deleted)+len(lp.edited)+len(lp.prev) != 0 {
		t.Fatal("запрос без токена дошёл до сервиса")
	}
}

func TestLimits_NotWired_404(t *testing.T) {
	s := limServer(t, &fakePlanner{}, nil, nil)
	for _, r := range limRoutes {
		if rec := send(s, r.method, r.target, r.body, goodToken); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d, ждали 404", r.method, r.target, rec.Code)
		}
	}
}

func TestLimits_AddEditDelete(t *testing.T) {
	lp := &fakeLimitPlanner{}
	p := &fakePlanner{}
	s := limServer(t, p, lp, nil)
	rec := send(s, "POST", "/api/plan/limits?cur=THB", `{"category":"Еда","amount":1234.5,"currency":"thb","repeat":true}`, goodToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add %d: %s", rec.Code, rec.Body.String())
	}
	want := plan.AddLimitInput{Category: "Еда", Amount: plan.Money{Minor: 123450, Cur: plan.CurTHB}, Repeat: true}
	if len(lp.added) != 1 || lp.added[0] != want {
		t.Fatalf("added=%+v, ждали %+v", lp.added, want)
	}
	if len(p.curs) != 1 || p.curs[0] != plan.CurTHB {
		t.Fatalf("ответ не перечитан в валюте показа: %v", p.curs)
	}
	if rec := send(s, "PUT", "/api/plan/limits/L9", `{"amount":50,"currency":"RUB","repeat":false}`, goodToken); rec.Code != http.StatusOK {
		t.Fatalf("edit %d: %s", rec.Code, rec.Body.String())
	}
	if e := lp.edited["L9"]; e.Amount != (plan.Money{Minor: 5000, Cur: plan.CurRUB}) || e.Repeat {
		t.Fatalf("edited=%+v", lp.edited)
	}
	if rec := send(s, "DELETE", "/api/plan/limits/L9", ``, goodToken); rec.Code != http.StatusOK || len(lp.deleted) != 1 || lp.deleted[0] != "L9" {
		t.Fatalf("delete %d deleted=%v", rec.Code, lp.deleted)
	}
}

func TestLimits_ErrorMapping(t *testing.T) {
	cases := map[string]struct {
		err  error
		code int
		text string
	}{
		"конфликт":      {plan.ErrLimitExists, 409, "уже есть"},
		"нет категории": {plan.ErrLimitCategory, 400, "категорию"},
		"нет лимита":    {plan.ErrNotFound, 404, "не найдена"},
		"запись выкл.":  {plan.ErrReadOnly, 403, "отключена"},
		"нет окна":      {plan.ErrNoWindow, 400, "окна нет"},
		"сумма":         {plan.ErrAmountNonPositive, 400, "Сумма"},
		"внутренняя":    {errors.New("pq: password authentication failed"), 500, "Не удалось"},
	}
	for name, c := range cases {
		s := limServer(t, &fakePlanner{}, &fakeLimitPlanner{err: c.err}, nil)
		rec := send(s, "POST", "/api/plan/limits", `{"category":"Еда","amount":1,"currency":"THB","repeat":false}`, goodToken)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.text) || strings.Contains(rec.Body.String(), "password") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestLimits_BadBodies(t *testing.T) {
	lp := &fakeLimitPlanner{}
	s := limServer(t, &fakePlanner{}, lp, nil)
	for name, body := range map[string]string{
		"не JSON":       `{`,
		"лишнее поле":   `{"category":"Еда","amount":1,"currency":"THB","oops":1}`,
		"отрицательная": `{"category":"Еда","amount":-5,"currency":"THB"}`,
		"три знака":     `{"category":"Еда","amount":1.234,"currency":"THB"}`,
		"нет суммы":     `{"category":"Еда","currency":"THB"}`,
		"нет repeat":    `{"category":"Еда","amount":1,"currency":"THB"}`,
	} {
		if rec := send(s, "POST", "/api/plan/limits", body, goodToken); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, ждали 400", name, rec.Code)
		}
	}
	if len(lp.added) != 0 {
		t.Fatalf("плохое тело дошло до сервиса: %+v", lp.added)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/plan/limits", strings.NewReader(`{}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+goodToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("без Content-Type: %d", rec.Code)
	}
}

func TestLimits_Preview(t *testing.T) {
	lp := &fakeLimitPlanner{preview: plan.LimitPreview{CanSpendBefore: 40000, CanSpendAfter: 32000, LimitHoldBefore: 0, LimitHoldAfter: 8000,
		Limit: &plan.LimitView{ID: "preview:new", Category: "Еда", Amount: 8000, Left: 8000, LeftPct: 100}}}
	s := limServer(t, &fakePlanner{}, lp, nil)
	rec := send(s, "POST", "/api/plan/limits/preview?cur=RUB", `{"id":"","category":"Еда","amount":8000,"currency":"RUB","repeat":true}`, goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var got PreviewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.CanSpendBefore != 40000 || got.CanSpendAfter != 32000 || got.LimitHoldAfter != 8000 || got.Limit == nil || got.Limit.Left != 8000 {
		t.Fatalf("preview=%+v", got)
	}
	if len(lp.prev) != 1 || lp.prev[0].Category != "Еда" || lp.prev[0].Amount.Minor != 800000 || !lp.prev[0].Repeat {
		t.Fatalf("в сервис ушло %+v", lp.prev)
	}
	if len(lp.added)+len(lp.deleted)+len(lp.edited) != 0 {
		t.Fatal("предпросмотр вызвал запись")
	}
}

func TestLimits_Suggest(t *testing.T) {
	rate := plan.Meta{HasRate: true, RubPerThbMicro: 2_500_000}
	p := &fakePlanner{res: plan.Result{Meta: rate}}
	sg := &fakeSuggester{sug: plan.LimitSuggestion{Enough: true, Months: 3, AmountTHBMinor: 420_000}}
	s := limServer(t, p, &fakeLimitPlanner{}, sg)

	get := func(target string) SuggestDTO {
		t.Helper()
		rec := send(s, "GET", target, ``, goodToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
		var d SuggestDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := get("/api/plan/limits/suggest?category=Еда&cur=THB"); !d.Enough || d.Amount == nil || *d.Amount != 4200 || d.Cur != "THB" || d.Days != 14 {
		t.Fatalf("THB: %+v", d)
	}
	if d := get("/api/plan/limits/suggest?category=Еда&cur=RUB"); !d.Enough || d.Amount == nil || *d.Amount != 10500 {
		t.Fatalf("RUB: %+v (4200 ฿ · 2,5)", d)
	}
	if len(sg.cats) != 2 || sg.cats[0] != "Еда" || sg.days[0] != plan.WindowDays {
		t.Fatalf("в подсказку ушло %v/%v", sg.cats, sg.days)
	}
	// мало данных: суммы нет, но месяцы видны
	sg.sug = plan.LimitSuggestion{Enough: false, Months: 1}
	if d := get("/api/plan/limits/suggest?category=Еда"); d.Enough || d.Amount != nil || d.Months != 1 {
		t.Fatalf("мало данных: %+v", d)
	}
	// нет курса при показе в рублях — подсказки нет
	sg.sug = plan.LimitSuggestion{Enough: true, Months: 3, AmountTHBMinor: 420_000}
	p.res = plan.Result{Meta: plan.Meta{HasRate: false}}
	if d := get("/api/plan/limits/suggest?category=Еда&cur=RUB"); d.Enough || d.Amount != nil {
		t.Fatalf("без курса: %+v", d)
	}
	if rec := send(s, "GET", "/api/plan/limits/suggest", ``, goodToken); rec.Code != http.StatusBadRequest {
		t.Errorf("без категории: %d", rec.Code)
	}
	sg.err = errors.New("boom secret")
	if rec := send(s, "GET", "/api/plan/limits/suggest?category=Еда", ``, goodToken); rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("ошибка подсказки: %d %s", rec.Code, rec.Body.String())
	}
}

// Старые поля ответа на месте, новые добавлены, пустые лимиты — [] а не null.
func TestWindow_LimitsFieldsAdditive(t *testing.T) {
	res := plan.Result{View: plan.View{State: plan.StateCurrent, Window: &plan.WindowInfo{}, CanSpend: 100, LimitHold: 30, LimitsExceedFree: true, UnplannedSpent: 70,
		Unplanned: []plan.UnplannedRow{{Category: "Кафе", Spent: 70}},
		Limits: []plan.LimitView{{ID: "L1", Category: "Еда", Repeat: true, Amount: 50, Spent: 20, Left: 30, LeftPct: 60, Attention: true}}}}
	s, _ := newServer(t, &fakePlanner{res: res})
	rec := do(s, "GET", "/api/plan/window", goodToken)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	kpi := asMap(t, got["kpi"])
	if kpi["canSpend"] != float64(100) || kpi["limitHold"] != float64(30) || kpi["limitsExceedFree"] != true {
		t.Fatalf("kpi=%v", kpi)
	}
	if kpi["unplannedSpent"] != float64(70) {
		t.Fatalf("unplannedSpent: kpi=%v", kpi)
	}
	if up, ok := got["unplanned"].([]any); !ok || len(up) != 1 || asMap(t, up[0])["category"] != "Кафе" || asMap(t, up[0])["spent"] != float64(70) {
		t.Fatalf("unplanned=%v", got["unplanned"])
	}
	ls, ok := got["limits"].([]any)
	if !ok || len(ls) != 1 {
		t.Fatalf("limits=%v", got["limits"])
	}
	l := asMap(t, ls[0])
	if l["id"] != "L1" || l["left"] != float64(30) || l["leftPct"] != float64(60) || l["attention"] != true || l["repeat"] != true {
		t.Fatalf("limit=%v", l)
	}
	empty, _ := newServer(t, &fakePlanner{})
	rec = do(empty, "GET", "/api/plan/window", goodToken)
	if !strings.Contains(rec.Body.String(), `"limits":[]`) || !strings.Contains(rec.Body.String(), `"unplanned":[]`) {
		t.Fatalf("пустые лимиты/unplanned не []: %s", rec.Body.String())
	}
}

// repeat обязателен и у PUT/preview: без него повторяющийся лимит молча стал бы разовым.
func TestLimits_RepeatRequired(t *testing.T) {
	lp := &fakeLimitPlanner{}
	s := limServer(t, &fakePlanner{}, lp, nil)
	for _, r := range []struct{ method, target string }{
		{"PUT", "/api/plan/limits/L1"}, {"POST", "/api/plan/limits/preview"}, {"POST", "/api/plan/limits"},
	} {
		if rec := send(s, r.method, r.target, `{"category":"Еда","amount":100,"currency":"THB"}`, goodToken); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s без repeat: %d, ждали 400", r.method, r.target, rec.Code)
		}
	}
	if len(lp.added)+len(lp.edited)+len(lp.prev) != 0 {
		t.Fatal("запрос без repeat дошёл до сервиса")
	}
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("ждали объект, получили %T", v)
	}
	return m
}
