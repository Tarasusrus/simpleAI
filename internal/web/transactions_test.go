package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"simpleAI/internal/plan"
	"simpleAI/internal/txedit"
)

type fakeTxEditor struct {
	list    txedit.ListQuery
	around  string
	edit    *txedit.Input
	delID   string
	delVer  string
	page    txedit.Page
	row     txedit.Tx
	err     error
	listed  bool
	focused bool
}

func (f *fakeTxEditor) Get(context.Context, string) (txedit.Tx, error) { return f.row, f.err }
func (f *fakeTxEditor) List(_ context.Context, q txedit.ListQuery) (txedit.Page, error) {
	f.list, f.listed = q, true
	return f.page, f.err
}
func (f *fakeTxEditor) Around(_ context.Context, id string) (txedit.Page, error) {
	f.around, f.focused = id, true
	return f.page, f.err
}
func (f *fakeTxEditor) Edit(_ context.Context, in txedit.Input) (txedit.Tx, error) {
	f.edit = &in
	return f.row, f.err
}
func (f *fakeTxEditor) Delete(_ context.Context, id, version string) error {
	f.delID, f.delVer = id, version
	return f.err
}

func txServer(t *testing.T, f *fakeTxEditor) *Server {
	t.Helper()
	s, _ := newServer(t, &fakePlanner{})
	if f != nil {
		s.WithTransactions(f)
	}
	return s
}

const goodEdit = `{"version":"2026-10-26T12:00:00.123456Z","description":" шлем ","amount":"1570.50","currency":"thb","date":"2026-10-23","category":"Одежда"}`

// Без WithTransactions все маршруты 404; без токена — 401, как у остальных.
func TestTx_DisabledAndAuth(t *testing.T) {
	s := txServer(t, nil)
	for _, m := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		if rec := send(s, m, "/api/plan/transactions/x", goodEdit, goodToken); rec.Code != http.StatusNotFound {
			t.Errorf("%s без подключения: %d, ждали 404", m, rec.Code)
		}
	}
	s = txServer(t, &fakeTxEditor{})
	if rec := send(s, http.MethodGet, "/api/plan/transactions", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("без токена: %d, ждали 401", rec.Code)
	}
}

// Фильтры, курсор, лимит и даты доходят до сервиса; focus отдаёт окно и игнорирует фильтры.
//
// Мутации: не передавать from/to/cursor/limit в ListQuery — краснеют проверки; не проверять focus раньше фильтров —
// краснеет «focus вызывает Around».
func TestTx_ListQuery(t *testing.T) {
	f := &fakeTxEditor{page: txedit.Page{Rows: []txedit.Tx{{ID: "a", Amount: plan.Money{Minor: 157050, Cur: plan.CurTHB}, Date: plan.Date{Year: 2026, Month: 10, Day: 23}, Version: "v1"}}, Next: "c1"}}
	s := txServer(t, f)
	rec := send(s, http.MethodGet, "/api/plan/transactions?text=шлем&category=Одежда&from=2026-10-01&to=2026-10-31&cursor=2026-10-05_x&limit=7", "", goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("список: %d %s", rec.Code, rec.Body)
	}
	q := f.list
	if q.Text != "шлем" || q.Category != "Одежда" || q.Cursor != "2026-10-05_x" || q.Limit != 7 ||
		q.From == nil || *q.From != (plan.Date{Year: 2026, Month: 10, Day: 1}) || q.To == nil || *q.To != (plan.Date{Year: 2026, Month: 10, Day: 31}) {
		t.Errorf("запрос к сервису: %+v", q)
	}
	var out TxPageDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Rows) != 1 || out.Next != "c1" ||
		out.Rows[0].Amount != "1570.50" || out.Rows[0].Currency != "THB" || out.Rows[0].Date != "2026-10-23" || out.Rows[0].Version != "v1" {
		t.Errorf("ответ: %+v %v", out, err)
	}
	for _, bad := range []string{"from=вчера", "to=2026-13-01", "limit=0", "limit=abc"} {
		if rec := send(s, http.MethodGet, "/api/plan/transactions?"+bad, "", goodToken); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, ждали 400", bad, rec.Code)
		}
	}

	f2 := &fakeTxEditor{page: txedit.Page{Rows: []txedit.Tx{{ID: "f"}}}}
	rec = send(txServer(t, f2), http.MethodGet, "/api/plan/transactions?focus=f&text=игнор&from=2026-10-01", "", goodToken)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || !f2.focused || f2.around != "f" || f2.listed || out.Focus != "f" {
		t.Errorf("focus: code=%d around=%q listed=%v focus=%q", rec.Code, f2.around, f2.listed, out.Focus)
	}
}

// PATCH разбирает тело и передаёт сервису ровно то, что ввёл владелец: версию, сумму в копейках, валюту в верхнем
// регистре, дату и категорию. Неполное тело и неизвестные поля — 400, сервис не вызывается.
//
// Мутации: не поднимать регистр валюты, не передавать version / category — краснеют проверки.
func TestTx_Edit(t *testing.T) {
	f := &fakeTxEditor{row: txedit.Tx{ID: "id1", Amount: plan.Money{Minor: 157050, Cur: plan.CurTHB}, Version: "v2"}}
	s := txServer(t, f)
	rec := send(s, http.MethodPatch, "/api/plan/transactions/id1", goodEdit, goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("правка: %d %s", rec.Code, rec.Body)
	}
	in := f.edit
	if in == nil || in.ID != "id1" || in.Version != "2026-10-26T12:00:00.123456Z" || in.Description != " шлем " ||
		in.Amount != (plan.Money{Minor: 157050, Cur: plan.CurTHB}) || in.Date != (plan.Date{Year: 2026, Month: 10, Day: 23}) || in.Category != "Одежда" {
		t.Errorf("вход сервиса: %+v", in)
	}
	var row TxRowDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil || row.Version != "v2" || row.Amount != "1570.50" {
		t.Errorf("ответ: %+v %v", row, err)
	}
	f.edit = nil
	for name, body := range map[string]string{
		"без версии": strings.Replace(goodEdit, `"version":"2026-10-26T12:00:00.123456Z",`, "", 1),
		"без валюты": strings.Replace(goodEdit, `"currency":"thb",`, "", 1),
		"лишнее":     strings.Replace(goodEdit, `"category"`, `"chat_id":1,"category"`, 1),
		"сумма":      strings.Replace(goodEdit, "1570.50", "1.234", 1),
		"дата":       strings.Replace(goodEdit, "2026-10-23", "23.10.2026", 1),
	} {
		if rec := send(s, http.MethodPatch, "/api/plan/transactions/id1", body, goodToken); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, ждали 400", name, rec.Code)
		}
	}
	if f.edit != nil {
		t.Error("неверный запрос дошёл до сервиса")
	}
}

// Коды ошибок: нет транзакции 404, версия устарела 409, плохая категория 400.
func TestTx_ErrorCodes(t *testing.T) {
	cases := map[error]int{
		txedit.ErrNotFound:        http.StatusNotFound,
		txedit.ErrConflict:        http.StatusConflict,
		txedit.ErrUnknownCategory: http.StatusBadRequest,
		txedit.ErrCategoryRetired: http.StatusBadRequest,
		txedit.ErrBadVersion:      http.StatusBadRequest,
		plan.ErrNameRequired:      http.StatusBadRequest,
		errors.New("база упала"):  http.StatusInternalServerError,
	}
	for err, want := range cases {
		s := txServer(t, &fakeTxEditor{err: err})
		if rec := send(s, http.MethodPatch, "/api/plan/transactions/id1", goodEdit, goodToken); rec.Code != want {
			t.Errorf("%v: %d, ждали %d", err, rec.Code, want)
		}
	}
}

// DELETE передаёт id и версию из query и отвечает 204; конфликт версий — 409.
//
// Мутация: не передавать version — краснеет проверка.
func TestTx_Delete(t *testing.T) {
	f := &fakeTxEditor{}
	s := txServer(t, f)
	rec := send(s, http.MethodDelete, "/api/plan/transactions/id9?version=2026-10-26T12:00:00Z", "", goodToken)
	if rec.Code != http.StatusNoContent || f.delID != "id9" || f.delVer != "2026-10-26T12:00:00Z" {
		t.Errorf("удаление: %d id=%q ver=%q", rec.Code, f.delID, f.delVer)
	}
	f.err = txedit.ErrConflict
	if rec := send(s, http.MethodDelete, "/api/plan/transactions/id9?version=x", "", goodToken); rec.Code != http.StatusConflict {
		t.Errorf("конфликт: %d", rec.Code)
	}
}

// /transactions отдаёт страницу (HTML со скриптом страницы), в том числе со ссылкой focus; скрипт и API-клиент
// существуют, а клиент ходит ровно по маршрутам сервера.
//
// Мутация: убрать маршрут GET /transactions — краснеет код ответа (запрос уйдёт в файловый сервер и даст 404).
func TestTx_PageRoute(t *testing.T) {
	s := txServer(t, &fakeTxEditor{})
	rec := send(s, http.MethodGet, "/transactions?focus=abc", "", goodToken)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/js/transactions.js") ||
		!strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("страница: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	js, err := staticFS.ReadFile("static/js/transactions.js")
	if err != nil {
		t.Fatal(err)
	}
	api, err := staticFS.ReadFile("static/js/api.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/api/plan/transactions?", "/api/plan/transactions/", "'PATCH'", "'DELETE'", "version="} {
		if !strings.Contains(string(api), want) {
			t.Errorf("клиент API: нет %q", want)
		}
	}
	if !strings.Contains(string(js), "focus") {
		t.Error("страница не читает ?focus")
	}
}

// Кнопки «изменить» у оплаченных фактов ведут на /transactions?focus=<id> из строки плана и из трат категории через
// общий модуль txlink.js; плановые записи и повторы ссылку не получают (только ключ "transaction:").
//
// Мутация: убрать импорт txlink из row.js или cat-tx.js, поменять префикс или адрес в txlink.js — краснеет проверка.
func TestStatic_TransactionLinks(t *testing.T) {
	link, err := staticFS.ReadFile("static/js/txlink.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"'transaction:'", "/transactions?focus=", "encodeURIComponent"} {
		if !strings.Contains(string(link), want) {
			t.Errorf("txlink.js: нет %q", want)
		}
	}
	for _, f := range []string{"static/js/blocks/row.js", "static/js/blocks/cat-tx.js"} {
		b, err := staticFS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "from '../txlink.js'") || !strings.Contains(string(b), "txLink(") {
			t.Errorf("%s не использует общую ссылку на транзакцию", f)
		}
	}
}

// С главной страницы плана есть ссылка на «Транзакции».
//
// Мутация: убрать ссылку из index.html — краснеет проверка.
func TestStatic_MainPageLinksToTransactions(t *testing.T) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `href="/transactions"`) {
		t.Error("на главной нет ссылки на страницу «Транзакции»")
	}
}

// Логика формы прихода (ADR-019) проверяется на чистых функциях в node: режимы по дате, обязательный выбор вне
// окон, что уходит на сервер, подписи. Без node тест пропускается (это НЕ зелёный результат).
//
// Мутации: убрать обязательность выбора в canSubmit, отдать true вместо выбранного в opensWindowFor, сдвинуть границу
// «внутри окна» — краснеют проверки в income_mode_test.mjs.
func TestJS_IncomeFormMode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден — JS-тест формы прихода пропущен")
	}
	out, err := exec.Command(node, "jstest/income_mode_test.mjs").CombinedOutput() //nolint:gosec // фиксированный скрипт из репозитория
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
