package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"simpleAI/internal/plan"
)

// «Вернуть в план»: токен обязателен, ключ доходит до сервиса целиком, обновлённое
// окно отдаётся в ответе, ошибки домена превращаются в понятные статусы, а общая
// «некорректное событие» не подменяет частные причины.
//
// Мутации: убрать маршрут из mux, не звать planner.Reopen, поменять 409 на 400 или
// убрать ErrNotReopenable из writeEditError (тогда ответ станет «Укажи либо дату…») —
// краснеет соответствующая проверка.
func TestReopen(t *testing.T) {
	p := &fakePlanner{}
	s, logs := newServer(t, p)
	if c := post(s, "/api/plan/item/transaction:f1/reopen", "", "", "").Code; c != http.StatusUnauthorized {
		t.Fatalf("без токена: %d", c)
	}
	if len(p.reopened) != 0 {
		t.Fatalf("запрос без токена дошёл до сервиса: %v", p.reopened)
	}
	rec := post(s, "/api/plan/item/transaction:f1/reopen?cur=THB", "", "", goodToken)
	if rec.Code != http.StatusOK || len(p.reopened) != 1 || p.reopened[0] != "transaction:f1" {
		t.Fatalf("возврат: %d %s %v", rec.Code, rec.Body.String(), p.reopened)
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Errorf("ответ — не JSON окна: %s", rec.Body.String())
	}
	if out := logs.String(); !strings.Contains(out, "command=reopen") || strings.Contains(out, goodToken) {
		t.Errorf("лог: %s", out)
	}

	for _, c := range []struct {
		err  error
		code int
		text string
	}{
		{plan.ErrNotReopenable, http.StatusConflict, "Вернуть в план"},
		{plan.ErrNotFound, http.StatusNotFound, "не найдена"},
		{plan.ErrNoFactWriter, http.StatusForbidden, "отключена"},
	} {
		p.cmdErr = c.err
		rec := post(s, "/api/plan/item/transaction:f1/reopen", "", "", goodToken)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.text) {
			t.Errorf("%v: %d %s, ждали %d с %q", c.err, rec.Code, rec.Body.String(), c.code, c.text)
		}
	}
}

// Правка закрытой записи идёт тем же PATCH; частные причины отказа получают свои тексты.
func TestEditFact_Errors(t *testing.T) {
	p := &fakePlanner{}
	s, _ := newServer(t, p)
	body := `{"name":"связь","amount":900,"currency":"THB","category":"","date":"2026-03-09"}`
	for _, c := range []struct {
		err  error
		code int
		text string
	}{
		{plan.ErrCurrencyLocked, http.StatusBadRequest, "валюта не меняется"},
		{plan.ErrFactDateRequired, http.StatusBadRequest, "нужна дата"},
		{plan.ErrNotEditable, http.StatusBadRequest, "закрытую с этого экрана"},
	} {
		p.cmdErr = c.err
		rec := patch(s, "/api/plan/item/transaction:f1", "application/json", body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.text) {
			t.Errorf("%v: %d %s, ждали %d с %q", c.err, rec.Code, rec.Body.String(), c.code, c.text)
		}
	}
	p.cmdErr = nil
	if rec := patch(s, "/api/plan/item/transaction:f1", "application/json", body); rec.Code != http.StatusOK {
		t.Errorf("правка: %d %s", rec.Code, rec.Body.String())
	}
}

// Права строки и признак закрытой записи доходят до JSON как есть.
//
// Мутации: убрать Editable/Reopenable из rows() или Fact из toItemDTO — краснеет.
func TestRowsAndItem_CarryFactFlags(t *testing.T) {
	r := rows([]plan.Row{{Key: "transaction:f1", Editable: true, Reopenable: true}, {Key: "transaction:ext"}})
	if !r[0].Editable || !r[0].Reopenable || r[1].Editable || r[1].Reopenable {
		t.Errorf("флаги строк: %+v", r)
	}
	b, err := json.Marshal(r[0])
	if err != nil || !strings.Contains(string(b), `"editable":true`) || !strings.Contains(string(b), `"reopenable":true`) {
		t.Errorf("JSON строки: %s %v", b, err)
	}
	if it := toItemDTO(plan.EditableItem{Key: "transaction:f1", Fact: true}); !it.Fact {
		t.Errorf("признак факта потерян: %+v", it)
	}
}
