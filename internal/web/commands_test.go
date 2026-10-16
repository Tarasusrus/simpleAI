package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"simpleAI/internal/plan"
)

func post(s *Server, target, ctype, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Host = "127.0.0.1:8090"
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestParseMinor(t *testing.T) {
	ok := map[string]int64{"0": 0, "1": 100, "1234.56": 123456, "0.5": 50, "7.05": 705, "100.": 10000}
	for in, want := range ok {
		got, err := parseMinor(json.Number(in))
		if err != nil || got != want {
			t.Errorf("%q → %d, %v; ждали %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "-5", "1.234", "1e3", "+5", "abc", ".5", "99999999999999999999"} {
		if _, err := parseMinor(json.Number(in)); err == nil {
			t.Errorf("%q принято", in)
		}
	}
}

func TestAddIncome(t *testing.T) {
	p := &fakePlanner{}
	s, logs := newServer(t, p)
	rec := post(s, "/api/plan/income?cur=THB", "application/json",
		`{"name":"аванс","amount":30000.50,"currency":"rub","date":"2026-03-20"}`, goodToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	if len(p.incomes) != 1 {
		t.Fatalf("вызовов AddIncome: %d", len(p.incomes))
	}
	in := p.incomes[0]
	if in.Name != "аванс" || in.Amount.Minor != 3000050 || in.Amount.Cur != plan.CurRUB || in.Date != (plan.Date{Year: 2026, Month: 3, Day: 20}) {
		t.Errorf("доход дошёл как %+v", in)
	}
	if len(p.curs) != 1 || p.curs[0] != plan.CurTHB {
		t.Errorf("обновлённый вид посчитан не в запрошенной валюте: %v", p.curs)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got["v"] == nil {
		t.Errorf("в ответе нет WindowView: %s", rec.Body.String())
	}
	if out := logs.String(); !strings.Contains(out, "added_by=web") || !strings.Contains(out, "command=income") || strings.Contains(out, goodToken) {
		t.Errorf("лог команды: %s", out)
	}
}

func TestAddPayment(t *testing.T) {
	p := &fakePlanner{}
	s, _ := newServer(t, p)
	// без суммы, в следующее окно, с категорией
	rec := post(s, "/api/plan/payment", "application/json", `{"name":"права","window":"next","category":"Еда"}`, goodToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	in := p.payments[0]
	if in.Amount != nil || in.Window != plan.WindowNext || in.Category != "Еда" || in.Date != nil {
		t.Errorf("платёж без суммы: %+v", in)
	}
	// с суммой в батах и датой
	rec = post(s, "/api/plan/payment", "application/json", `{"name":"аренда","amount":18000,"currency":"THB","date":"2026-03-15"}`, goodToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	in = p.payments[1]
	if in.Amount == nil || in.Amount.Minor != 1_800_000 || in.Amount.Cur != plan.CurTHB || in.Date == nil || *in.Date != (plan.Date{Year: 2026, Month: 3, Day: 15}) {
		t.Errorf("платёж с суммой: %+v", in)
	}
	// пустая сумма строкой/пустая дата — как «нет»
	rec = post(s, "/api/plan/payment", "application/json", `{"name":"уборка","amount":null,"date":""}`, goodToken)
	if rec.Code != http.StatusCreated || p.payments[2].Amount != nil || p.payments[2].Date != nil {
		t.Errorf("пустые поля: %d %+v", rec.Code, p.payments[2])
	}
}

func TestCommands_RequireAuthAndJSON(t *testing.T) {
	p := &fakePlanner{}
	s, _ := newServer(t, p)
	body := `{"name":"x","amount":1,"currency":"RUB","date":"2026-03-20"}`
	if c := post(s, "/api/plan/income", "application/json", body, "").Code; c != http.StatusUnauthorized {
		t.Errorf("без токена %d", c)
	}
	if c := post(s, "/api/plan/income", "text/plain", body, goodToken).Code; c != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain %d, ждали 415", c)
	}
	if c := post(s, "/api/plan/income", "", body, goodToken).Code; c != http.StatusUnsupportedMediaType {
		t.Errorf("без Content-Type %d, ждали 415", c)
	}
	if c := post(s, "/api/plan/income", "application/json", `{"name":"x","amount":1,"currency":"RUB","date":"2026-03-20","bogus":1}`, goodToken).Code; c != http.StatusBadRequest {
		t.Errorf("неизвестное поле %d, ждали 400", c)
	}
	if c := post(s, "/api/plan/income", "application/json", `{not json`, goodToken).Code; c != http.StatusBadRequest {
		t.Errorf("битый JSON %d", c)
	}
	big := `{"name":"` + strings.Repeat("я", maxBodyBytes) + `","amount":1,"currency":"RUB","date":"2026-03-20"}`
	if c := post(s, "/api/plan/income", "application/json", big, goodToken).Code; c != http.StatusBadRequest {
		t.Errorf("тело больше лимита %d", c)
	}
	if len(p.incomes) != 0 {
		t.Errorf("невалидные запросы дошли до сервиса: %d", len(p.incomes))
	}
}

func TestCommands_BadInputsAndErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		err  error
		code int
		text string
	}{
		{"сумма с тремя знаками", `{"name":"x","amount":1.234,"currency":"RUB","date":"2026-03-20"}`, nil, 400, "Сумма"},
		{"отрицательная", `{"name":"x","amount":-5,"currency":"RUB","date":"2026-03-20"}`, nil, 400, "Сумма"},
		{"дата кривая", `{"name":"x","amount":5,"currency":"RUB","date":"20.03.2026"}`, nil, 400, "дата"},
		{"30 февраля", `{"name":"x","amount":5,"currency":"RUB","date":"2026-02-30"}`, nil, 400, "дата"},
		{"доменная: нет названия", `{"name":"","amount":5,"currency":"RUB","date":"2026-03-20"}`, plan.ErrNameRequired, 400, "название"},
		{"доменная: внутренняя", `{"name":"x","amount":5,"currency":"RUB","date":"2026-03-20"}`, errors.New("pq: connection refused host=db"), 500, "Не удалось"},
	}
	for _, c := range cases {
		p := &fakePlanner{cmdErr: c.err}
		s, _ := newServer(t, p)
		rec := post(s, "/api/plan/income", "application/json", c.body, goodToken)
		if rec.Code != c.code || !strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(c.text)) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "pq:") || strings.Contains(rec.Body.String(), "host=") {
			t.Errorf("%s: детали ушли наружу: %s", c.name, rec.Body.String())
		}
	}
	// Ошибки платежа: нет окна, дата+окно.
	for err, code := range map[error]int{plan.ErrNoWindow: 400, plan.ErrInvalidEvent: 400, plan.ErrReadOnly: 403} {
		p := &fakePlanner{cmdErr: err}
		s, _ := newServer(t, p)
		if c := post(s, "/api/plan/payment", "application/json", `{"name":"x","window":"current"}`, goodToken).Code; c != code {
			t.Errorf("%v: %d, ждали %d", err, c, code)
		}
	}
}

func TestDeleteAndCategories(t *testing.T) {
	p := &fakePlanner{cats: []string{"Еда", "Транспорт"}}
	s, _ := newServer(t, p)
	rec := do(s, http.MethodDelete, "/api/plan/item/planned:abc", goodToken)
	if rec.Code != http.StatusOK || len(p.deleted) != 1 || p.deleted[0] != "planned:abc" {
		t.Errorf("удаление: %d %v", rec.Code, p.deleted)
	}
	// Фронт кодирует ключ: двоеточие приходит как %3A.
	rec = do(s, http.MethodDelete, "/api/plan/item/planned%3Adef", goodToken)
	if rec.Code != http.StatusOK || len(p.deleted) != 2 || p.deleted[1] != "planned:def" {
		t.Errorf("удаление с кодированным ключом: %d %v", rec.Code, p.deleted)
	}
	p.cmdErr = plan.ErrNotFound
	if c := do(s, http.MethodDelete, "/api/plan/item/zzz", goodToken).Code; c != http.StatusNotFound {
		t.Errorf("нет записи: %d, ждали 404", c)
	}
	if c := do(s, http.MethodDelete, "/api/plan/item/zzz", "").Code; c != http.StatusUnauthorized {
		t.Errorf("удаление без токена: %d", c)
	}
	p.cmdErr = nil
	rec = do(s, http.MethodGet, "/api/plan/categories", goodToken)
	var got struct {
		Categories []string `json:"categories"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.Categories) != 2 {
		t.Errorf("категории: %d %s", rec.Code, rec.Body.String())
	}
}
