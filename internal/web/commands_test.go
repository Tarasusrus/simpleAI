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
		{"доменная: нет такой категории", `{"name":"x","amount":5,"currency":"RUB","date":"2026-03-20"}`, plan.ErrUnknownCategory, 400, "категории нет"},
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

func TestDone_WithAndWithoutAmount(t *testing.T) {
	p := &fakePlanner{}
	s, logs := newServer(t, p)
	rec := post(s, "/api/plan/item/planned:abc/done?cur=THB", "", "", goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	if p.settled[0] != "planned:abc" || p.actuals[0] != nil {
		t.Errorf("без тела: %v %v", p.settled, p.actuals)
	}
	rec = post(s, "/api/plan/item/planned:abc/done", "application/json", `{"amount":1500.5,"currency":"thb"}`, goodToken)
	if rec.Code != http.StatusOK || p.actuals[1] == nil || p.actuals[1].Minor != 150050 || p.actuals[1].Cur != plan.CurTHB {
		t.Errorf("с суммой: %d %+v", rec.Code, p.actuals[1])
	}
	if c := post(s, "/api/plan/item/planned:abc/done", "application/json", `{"amount":-1}`, goodToken).Code; c != http.StatusBadRequest {
		t.Errorf("минус: %d", c)
	}
	if len(p.settled) != 2 {
		t.Errorf("невалидный запрос дошёл до сервиса: %v", p.settled)
	}
	if out := logs.String(); !strings.Contains(out, "command=done") || strings.Contains(out, goodToken) {
		t.Errorf("лог: %s", out)
	}
	if c := post(s, "/api/plan/item/planned:abc/done", "", "", "").Code; c != http.StatusUnauthorized {
		t.Errorf("без токена %d", c)
	}
}

func TestSkipAndDoneErrors(t *testing.T) {
	p := &fakePlanner{}
	s, _ := newServer(t, p)
	if c := post(s, "/api/plan/item/recurring:r@2026-03-15/skip", "", "", goodToken).Code; c != http.StatusOK || p.skipped[0] != "recurring:r@2026-03-15" {
		t.Errorf("skip: %d %v", c, p.skipped)
	}
	p.cmdErr = plan.ErrNotFound
	if c := post(s, "/api/plan/item/planned:x/done", "", "", goodToken).Code; c != http.StatusNotFound {
		t.Errorf("нет записи: %d", c)
	}
	p.cmdErr = plan.ErrInvalidEvent
	if c := post(s, "/api/plan/item/planned:x/skip", "", "", goodToken).Code; c != http.StatusBadRequest {
		t.Errorf("неверный ключ: %d", c)
	}
}

func TestParseMicro(t *testing.T) {
	ok := map[string]int64{"2.5": 2_500_000, "2": 2_000_000, "0.000001": 1, "2.650001": 2_650_001, "100.": 100_000_000}
	for in, want := range ok {
		if got, err := parseMicro(json.Number(in)); err != nil || got != want {
			t.Errorf("%q → %d, %v; ждали %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "0.000000", "-1", "1.1234567", "1e3", "abc", ".5", "99999999999999999999"} {
		if _, err := parseMicro(json.Number(in)); err == nil {
			t.Errorf("%q принято", in)
		}
	}
}

func TestRate(t *testing.T) {
	p := &fakePlanner{}
	s, logs := newServer(t, p)
	if rec := post(s, "/api/plan/rate", "application/json", `{"rate":"2.65"}`, goodToken); rec.Code != http.StatusOK || len(p.rates) != 1 || p.rates[0] != 2_650_000 {
		t.Fatalf("код %d, курсы %v", rec.Code, p.rates)
	}
	if rec := post(s, "/api/plan/rate", "application/json", `{"rate":0}`, goodToken); rec.Code != http.StatusBadRequest {
		t.Errorf("ноль: %d", rec.Code)
	}
	if rec := post(s, "/api/plan/rate", "application/json", `{"rate":"abc"}`, goodToken); rec.Code != http.StatusBadRequest {
		t.Errorf("не число: %d", rec.Code)
	}
	if len(p.rates) != 1 {
		t.Errorf("плохой курс дошёл до сервиса: %v", p.rates)
	}
	p.cmdErr = plan.ErrBadRate
	if rec := post(s, "/api/plan/rate", "application/json", `{"rate":9999}`, goodToken); rec.Code != http.StatusBadRequest {
		t.Errorf("отказ домена: %d", rec.Code)
	}
	if !strings.Contains(logs.String(), "command=rate") {
		t.Errorf("лог: %s", logs.String())
	}
}

func TestMove(t *testing.T) {
	p := &fakePlanner{}
	s, logs := newServer(t, p)
	if rec := post(s, "/api/plan/item/planned:abc/move", "application/json", `{"window":"next"}`, goodToken); rec.Code != http.StatusOK ||
		p.moved[0] != "planned:abc" || p.moves[0].Window != plan.WindowNext || p.moves[0].Date != nil {
		t.Fatalf("в окно: %d %+v", rec.Code, p.moves)
	}
	if rec := post(s, "/api/plan/item/planned:abc/move", "application/json", `{"date":"2026-11-05"}`, goodToken); rec.Code != http.StatusOK ||
		p.moves[1].Date == nil || *p.moves[1].Date != (plan.Date{Year: 2026, Month: 11, Day: 5}) {
		t.Fatalf("на дату: %d %+v", rec.Code, p.moves)
	}
	if rec := post(s, "/api/plan/item/planned:abc/move", "application/json", `{}`, goodToken); rec.Code != http.StatusOK ||
		p.moves[2].Date != nil || p.moves[2].Window != plan.WindowNone {
		t.Fatalf("без даты: %d %+v", rec.Code, p.moves)
	}
	if rec := post(s, "/api/plan/item/planned:abc/move", "application/json", `{"date":"05.11.2026"}`, goodToken); rec.Code != http.StatusBadRequest {
		t.Errorf("кривая дата: %d", rec.Code)
	}
	if len(p.moved) != 3 {
		t.Errorf("кривая дата дошла до сервиса: %v", p.moved)
	}
	if c := post(s, "/api/plan/item/planned:abc/move", "", `{}`, goodToken).Code; c != http.StatusUnsupportedMediaType {
		t.Errorf("без Content-Type: %d", c)
	}
	p.cmdErr = plan.ErrNoWindow
	if c := post(s, "/api/plan/item/planned:abc/move", "application/json", `{"window":"current"}`, goodToken).Code; c != http.StatusBadRequest {
		t.Errorf("нет окна: %d", c)
	}
	p.cmdErr = plan.ErrNotFound
	if c := post(s, "/api/plan/item/planned:abc/move", "application/json", `{}`, goodToken).Code; c != http.StatusNotFound {
		t.Errorf("нет записи: %d", c)
	}
	if !strings.Contains(logs.String(), "command=move") {
		t.Errorf("лог: %s", logs.String())
	}
}

// opensWindow в теле прихода: false — обычный приход (JoinsWindow), нет поля или true — прежнее поведение.
//
// Мутации: игнорировать поле, инвертировать условие, трактовать отсутствие как false — краснеют кейсы.
func TestAddIncome_OpensWindowFlag(t *testing.T) {
	cases := map[string]bool{
		`"opensWindow":false,`: true,
		`"opensWindow":true,`:  false,
		``:                     false,
	}
	for field, wantJoins := range cases {
		p := &fakePlanner{}
		s, _ := newServer(t, p)
		body := `{` + field + `"name":"байк","amount":4000,"currency":"rub","date":"2026-03-20"}`
		if rec := post(s, "/api/plan/income", "application/json", body, goodToken); rec.Code != http.StatusCreated {
			t.Fatalf("%q: код %d: %s", field, rec.Code, rec.Body.String())
		}
		if len(p.incomes) != 1 || p.incomes[0].JoinsWindow != wantJoins {
			t.Errorf("%q: JoinsWindow=%v, ждали %v", field, p.incomes[0].JoinsWindow, wantJoins)
		}
	}
}
