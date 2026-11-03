package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"simpleAI/internal/plan"
)

type fakeRuleViewer struct {
	rows []plan.RuleRow
	err  error
	cur  []plan.Currency
}

func (f *fakeRuleViewer) ListRules(_ context.Context, cur plan.Currency) ([]plan.RuleRow, error) {
	f.cur = append(f.cur, cur)
	return f.rows, f.err
}

func i64(v int64) *int64 { return &v }

func dp(y, m, d int) *plan.Date { return &plan.Date{Year: y, Month: m, Day: d} }

func rowOf(id string, kind plan.Kind, paused, invalid bool, minor int64, monthly *int64, next *plan.Date) plan.RuleRow {
	return plan.RuleRow{
		ID: id, Name: "n-" + id, Category: "Связь", Kind: kind, Paused: paused, Invalid: invalid, Next: next, Monthly: monthly,
		Rule: plan.Rule{N: 1, Unit: plan.UnitMonth, Amount: plan.Money{Minor: minor, Cur: plan.CurTHB}},
	}
}

// Без токена список не доходит до сервиса.
//
// Мутация: убрать s.auth(...) у маршрута — тест краснеет.
func TestRules_RequireToken(t *testing.T) {
	v := &fakeRuleViewer{}
	for _, tok := range []string{"", "wrong"} {
		s, _ := newServer(t, &fakePlanner{})
		s.WithRuleViewer(v)
		if rec := send(s, "GET", "/api/plan/recurring", "", tok); rec.Code != http.StatusUnauthorized {
			t.Errorf("токен %q: %d, ждали 401", tok, rec.Code)
		}
	}
	if len(v.cur) != 0 {
		t.Fatal("запрос без токена дошёл до сервиса")
	}
}

func TestRules_NotWired_404(t *testing.T) {
	s, _ := newServer(t, &fakePlanner{})
	if rec := send(s, "GET", "/api/plan/recurring", "", goodToken); rec.Code != http.StatusNotFound {
		t.Fatalf("%d, ждали 404", rec.Code)
	}
}

func TestRules_ServiceOff_404(t *testing.T) {
	s, _ := newServer(t, &fakePlanner{})
	s.WithRuleViewer(&fakeRuleViewer{err: plan.ErrRulesOff})
	if rec := send(s, "GET", "/api/plan/recurring", "", goodToken); rec.Code != http.StatusNotFound {
		t.Fatalf("%d, ждали 404", rec.Code)
	}
}

// Итог «в месяц» берёт только действующие расходы с посчитанной суммой; расходы без курса считаются отдельно.
//
// Мутация: убрать проверку Paused/Invalid/Kind в handleRules — итог меняется, тест краснеет.
func TestRules_TotalAndFields(t *testing.T) {
	until := plan.Date{Year: 2027, Month: 3, Day: 1}
	withUntil := rowOf("a", plan.KindPayment, false, false, 12345, i64(300), dp(2026, 11, 5))
	withUntil.Rule.Until = &until
	v := &fakeRuleViewer{rows: []plan.RuleRow{
		withUntil,
		rowOf("b", plan.KindPayment, false, false, 100, i64(50), dp(2026, 11, 6)),
		rowOf("paused", plan.KindPayment, true, false, 100, i64(1000), nil),
		rowOf("bad", plan.KindPayment, false, true, 100, nil, nil),
		rowOf("inc", plan.KindIncome, false, false, 100, i64(7000), dp(2026, 11, 7)),
		rowOf("norate", plan.KindPayment, false, false, 100, nil, dp(2026, 11, 8)),
	}}
	s, _ := newServer(t, &fakePlanner{})
	s.WithRuleViewer(v)
	rec := send(s, "GET", "/api/plan/recurring?cur=THB", "", goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d: %s", rec.Code, rec.Body)
	}
	var got RulesDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 350 || got.Missing != 1 || len(got.Rows) != 6 {
		t.Fatalf("total=%d missing=%d rows=%d, ждали 350/1/6", got.Total, got.Missing, len(got.Rows))
	}
	if len(v.cur) != 1 || v.cur[0] != plan.CurTHB {
		t.Fatalf("валюта показа: %v", v.cur)
	}
	a := got.Rows[0]
	if a.Amount != "123.45" || a.Currency != "THB" || a.Every != 1 || a.Unit != "month" || a.Kind != "payment" ||
		a.Until == nil || *a.Until != "2027-03-01" || a.Next == nil || *a.Next != "2026-11-05" || a.Monthly == nil || *a.Monthly != 300 {
		t.Fatalf("строка a: %+v", a)
	}
	if !got.Rows[2].Paused || got.Rows[2].Next != nil || !got.Rows[3].Invalid || got.Rows[3].Monthly != nil {
		t.Fatalf("пауза/неверное правило: %+v %+v", got.Rows[2], got.Rows[3])
	}
}

func TestRules_EmptyIsArray(t *testing.T) {
	s, _ := newServer(t, &fakePlanner{})
	s.WithRuleViewer(&fakeRuleViewer{})
	rec := send(s, "GET", "/api/plan/recurring", "", goodToken)
	if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), `"rows":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// Подпись периода («каждые 2 недели») проверяется на чистой функции в node. Без node тест пропускается
// (это НЕ зелёный результат).
//
// Мутация: убрать ветку n === 1 или перепутать формы множественного числа в fmt.js — краснеют проверки
// в recurring_test.mjs.
func TestJS_EveryLabel(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден — JS-тест подписи периода пропущен")
	}
	out, err := exec.Command(node, "jstest/recurring_test.mjs").CombinedOutput() //nolint:gosec // фиксированный скрипт из репозитория
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
