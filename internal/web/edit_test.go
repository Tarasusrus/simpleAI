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

// Заглушки, чтобы общий fakePlanner оставался Planner; запись правок — в editPlanner.
func (f *fakePlanner) Item(context.Context, string) (plan.EditableItem, error) {
	return plan.EditableItem{}, f.cmdErr
}
func (f *fakePlanner) Edit(context.Context, string, plan.EditInput) error { return f.cmdErr }

type editPlanner struct {
	fakePlanner
	item  plan.EditableItem
	keys  []string
	edits []plan.EditInput
}

func (f *editPlanner) Item(_ context.Context, key string) (plan.EditableItem, error) {
	f.keys = append(f.keys, key)
	return f.item, f.cmdErr
}

func (f *editPlanner) Edit(_ context.Context, key string, in plan.EditInput) error {
	f.keys = append(f.keys, key)
	f.edits = append(f.edits, in)
	return f.cmdErr
}

func patch(s *Server, target, ctype, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, target, strings.NewReader(body))
	req.Host = "127.0.0.1:8090"
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Authorization", "Bearer "+goodToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func dptr(y, m, d int) *plan.Date { x := plan.Date{Year: y, Month: m, Day: d}; return &x }

// Форма получает сумму десятичной строкой в исходной валюте, и PATCH разбирает её
// обратно в ту же сумму (туда-обратно без потерь).
//
// Мутация: decimal через %d без копеек или minor%100 без %02d — тест краснеет.
func TestItem_DTOAndRoundTrip(t *testing.T) {
	for _, minor := range []int64{1, 5, 50, 100, 123450, 123456, 100_000_000_000} {
		got, err := parseMinor(json.Number(decimal(minor)))
		if err != nil || got != minor {
			t.Errorf("%d → %q → %d, %v", minor, decimal(minor), got, err)
		}
	}
	p := &editPlanner{item: plan.EditableItem{
		Key: "recurring:rent", Kind: plan.KindPayment, Recurring: true, Name: "аренда", Category: "Жильё",
		Amount: &plan.Money{Minor: 1_800_050, Cur: plan.CurTHB},
		Rule:   &plan.Rule{N: 1, Unit: plan.UnitMonth, Until: dptr(2026, 12, 15)},
	}}
	s, _ := newServer(t, p)
	rec := do(s, http.MethodGet, "/api/plan/item/"+"recurring:rent@2026-03-15", goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	var got ItemDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Key != "recurring:rent" || !got.Recurring || got.Amount == nil || *got.Amount != "18000.50" || got.Currency != "THB" ||
		got.Category != "Жильё" || got.Repeat == nil || got.Repeat.Unit != "month" || got.Repeat.Until == nil || *got.Repeat.Until != "2026-12-15" {
		t.Errorf("ответ: %s", rec.Body.String())
	}
	if p.keys[0] != "recurring:rent@2026-03-15" {
		t.Errorf("ключ дошёл как %q", p.keys[0])
	}

	// Платёж без суммы: amount null, валюты нет, окно датой.
	p.item = plan.EditableItem{Key: "planned:x", Kind: plan.KindPayment, Name: "права", WindowStart: dptr(2026, 3, 5)}
	rec = do(s, http.MethodGet, "/api/plan/item/planned:x", goodToken)
	if body := rec.Body.String(); !strings.Contains(body, `"amount":null`) || !strings.Contains(body, `"windowStart":"2026-03-05"`) ||
		!strings.Contains(body, `"repeat":null`) || !strings.Contains(body, `"currency":""`) {
		t.Errorf("платёж без суммы: %s", body)
	}
}

func TestItem_Errors(t *testing.T) {
	cases := map[error]int{
		plan.ErrNotEditable: http.StatusBadRequest,
		plan.ErrNotFound:    http.StatusNotFound,
		plan.ErrReadOnly:    http.StatusForbidden,
		errors.New("база"):  http.StatusInternalServerError,
	}
	for err, want := range cases {
		s, _ := newServer(t, &editPlanner{fakePlanner: fakePlanner{cmdErr: err}})
		if got := do(s, http.MethodGet, "/api/plan/item/planned:x", goodToken).Code; got != want {
			t.Errorf("%v: %d, ждали %d", err, got, want)
		}
	}
	s, _ := newServer(t, &editPlanner{})
	if got := do(s, http.MethodGet, "/api/plan/item/planned:x", "").Code; got != http.StatusUnauthorized {
		t.Errorf("без токена: %d", got)
	}
}

// Тело PATCH → команда домена. Нет date и window — место не меняется (Place nil);
// window "none" — «без даты» (пустой Place).
//
// Мутация: в editInput путать «нет поля» и window=none (оба → Place{}) — тест краснеет.
func TestEdit_Body(t *testing.T) {
	cases := []struct {
		name, body string
		amount     *plan.Money
		place      *plan.MoveInput
	}{
		{"место не трогать", `{"name":"врач","amount":"700.5","currency":"rub","category":"Здоровье"}`,
			&plan.Money{Minor: 70050, Cur: plan.CurRUB}, nil},
		{"сумма числом, на дату", `{"name":"врач","amount":700,"currency":"THB","category":"","date":"2026-03-16"}`,
			&plan.Money{Minor: 70000, Cur: plan.CurTHB}, &plan.MoveInput{Date: dptr(2026, 3, 16)}},
		{"без суммы, в следующее окно", `{"name":"права","amount":null,"category":"","window":"next"}`,
			nil, &plan.MoveInput{Window: plan.WindowNext}},
		{"сумма пустой строкой, без даты", `{"name":"права","amount":"","category":"","window":"none"}`,
			nil, &plan.MoveInput{}},
		{"пустые date и window — как нет", `{"name":"x","amount":null,"category":"","date":" ","window":""}`,
			nil, nil},
	}
	for _, c := range cases {
		p := &editPlanner{}
		s, logs := newServer(t, p)
		rec := patch(s, "/api/plan/item/planned:abc?cur=THB", "application/json", c.body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: код %d: %s", c.name, rec.Code, rec.Body.String())
		}
		in := p.edits[0]
		if p.keys[0] != "planned:abc" || (in.Amount == nil) != (c.amount == nil) || (in.Amount != nil && *in.Amount != *c.amount) {
			t.Errorf("%s: %+v", c.name, in)
		}
		if (in.Place == nil) != (c.place == nil) {
			t.Errorf("%s: Place %+v, ждали %+v", c.name, in.Place, c.place)
		} else if in.Place != nil && (in.Place.Window != c.place.Window || !sameDate(in.Place.Date, c.place.Date)) {
			t.Errorf("%s: Place %+v, ждали %+v", c.name, in.Place, c.place)
		}
		if len(p.curs) != 1 || p.curs[0] != plan.CurTHB || !strings.Contains(rec.Body.String(), `"v":`) {
			t.Errorf("%s: в ответ не ушёл вид окна в валюте показа", c.name)
		}
		if !strings.Contains(logs.String(), "command=edit") {
			t.Errorf("%s: нет строки лога команды", c.name)
		}
	}
	p := &editPlanner{}
	s, _ := newServer(t, p)
	patch(s, "/api/plan/item/planned:abc", "application/json", `{"name":" Врач ","amount":null,"category":" Еда "}`)
	if in := p.edits[0]; in.Name != " Врач " || in.Category != " Еда " {
		t.Errorf("название и категория дошли как %+v", in)
	}
}

func sameDate(a, b *plan.Date) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// Обязательные поля и ошибки. Забытое поле amount не должно стирать сумму.
//
// Мутация: убрать len(b.Amount) == 0 из проверки полей — тест краснеет.
func TestEdit_Rejects(t *testing.T) {
	cases := []struct {
		name, ctype, body string
		cmdErr            error
		want              int
		wantMsg           string
	}{
		{"нет amount", "application/json", `{"name":"x","category":""}`, nil, 400, "amount"},
		{"нет category", "application/json", `{"name":"x","amount":null}`, nil, 400, "category"},
		{"нет name", "application/json", `{"amount":null,"category":""}`, nil, 400, "name"},
		{"дата и окно", "application/json", `{"name":"x","amount":null,"category":"","date":"2026-03-16","window":"next"}`, nil, 400, "либо дату"},
		{"битая дата", "application/json", `{"name":"x","amount":null,"category":"","date":"16.03.2026"}`, nil, 400, "ГГГГ-ММ-ДД"},
		{"битая сумма", "application/json", `{"name":"x","amount":"12,5","currency":"RUB","category":""}`, nil, 400, "Сумма"},
		{"сумма объектом", "application/json", `{"name":"x","amount":{},"currency":"RUB","category":""}`, nil, 400, "Сумма"},
		{"лишнее поле", "application/json", `{"name":"x","amount":null,"category":"","kind":"income"}`, nil, 400, "разобрать"},
		{"не JSON", "text/plain", `{}`, nil, 415, "Content-Type"},
		{"повтор с датой", "application/json", `{"name":"x","amount":1,"currency":"RUB","category":""}`, plan.ErrRuleSchedule, 400, "пропустить"},
		{"факт", "application/json", `{"name":"x","amount":1,"currency":"RUB","category":""}`, plan.ErrNotEditable, 400, "плановую запись"},
		{"нет записи", "application/json", `{"name":"x","amount":1,"currency":"RUB","category":""}`, plan.ErrNotFound, 404, "не найдена"},
		{"без названия", "application/json", `{"name":"","amount":1,"currency":"RUB","category":""}`, plan.ErrNameRequired, 400, "название"},
	}
	for _, c := range cases {
		p := &editPlanner{fakePlanner: fakePlanner{cmdErr: c.cmdErr}}
		s, _ := newServer(t, p)
		rec := patch(s, "/api/plan/item/planned:abc", c.ctype, c.body)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.wantMsg) {
			t.Errorf("%s: %d %s, ждали %d «%s»", c.name, rec.Code, rec.Body.String(), c.want, c.wantMsg)
		}
		if c.cmdErr == nil && len(p.edits) != 0 {
			t.Errorf("%s: невалидное тело дошло до сервиса: %+v", c.name, p.edits)
		}
	}
}
