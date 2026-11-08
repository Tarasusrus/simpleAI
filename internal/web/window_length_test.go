package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"testing"

	"simpleAI/internal/plan"
)

type fakeWindowEditor struct {
	resized []int
	ends    []plan.Date
	starts  []plan.Date
	deleted int
	err     error
}

func (f *fakeWindowEditor) ResizeWindow(_ context.Context, days int) (plan.WindowFrame, error) {
	f.resized = append(f.resized, days)
	return plan.WindowFrame{}, f.err
}

func (f *fakeWindowEditor) SetWindowEnd(_ context.Context, end plan.Date) (plan.WindowFrame, error) {
	f.ends = append(f.ends, end)
	return plan.WindowFrame{}, f.err
}

func (f *fakeWindowEditor) SetWindowStart(_ context.Context, start plan.Date) (plan.WindowFrame, error) {
	f.starts = append(f.starts, start)
	return plan.WindowFrame{}, f.err
}

func (f *fakeWindowEditor) DeleteWindow(context.Context) (plan.WindowFrame, error) {
	f.deleted++
	return plan.WindowFrame{}, f.err
}

func winServer(t *testing.T, e *fakeWindowEditor) (*Server, *fakePlanner) {
	t.Helper()
	p := &fakePlanner{res: plan.Result{View: plan.View{State: plan.StateCurrent, Window: &plan.WindowInfo{
		Start: plan.Date{Year: 2026, Month: 10, Day: 5}, End: plan.Date{Year: 2026, Month: 10, Day: 20}, DaysLeft: 11, Days: 16, Adjusted: true,
	}}}}
	s, _ := newServer(t, p)
	if e != nil {
		s.WithWindowEditor(e)
	}
	return s, p
}

// Мутация: убрать s.auth(...) у маршрута — запрос без токена дойдёт до сервиса.
func TestWindowEdit_RequireToken(t *testing.T) {
	e := &fakeWindowEditor{}
	s, _ := winServer(t, e)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/plan/window/resize", `{"days":1}`},
		{"PUT", "/api/plan/window/end", `{"end":"2026-10-25"}`},
		{"PUT", "/api/plan/window/start", `{"start":"2026-10-03"}`},
		{"DELETE", "/api/plan/window", ``},
	} {
		if rec := send(s, c.method, c.path, c.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s без токена: %d", c.method, c.path, rec.Code)
		}
	}
	if len(e.resized)+len(e.ends)+len(e.starts)+e.deleted != 0 {
		t.Fatal("запрос без токена дошёл до сервиса")
	}
}

func TestWindowEdit_NotWired404(t *testing.T) {
	s, _ := winServer(t, nil)
	if rec := send(s, "POST", "/api/plan/window/resize", `{"days":1}`, goodToken); rec.Code != http.StatusNotFound {
		t.Fatalf("%d, ждали 404", rec.Code)
	}
}

// Команды доходят до сервиса и отвечают свежим окном с длиной.
func TestWindowEdit_CommandsReachServiceAndReturnView(t *testing.T) {
	e := &fakeWindowEditor{}
	s, _ := winServer(t, e)
	rec := send(s, "POST", "/api/plan/window/resize", `{"days":-2}`, goodToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("resize: %d %s", rec.Code, rec.Body.String())
	}
	var v struct {
		Window struct {
			Days     int  `json:"days"`
			Adjusted bool `json:"adjusted"`
		} `json:"window"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.Window.Days != 16 || !v.Window.Adjusted {
		t.Fatalf("ответ без длины окна: %s (err=%v)", rec.Body.String(), err)
	}
	if rec := send(s, "PUT", "/api/plan/window/end", `{"end":"2026-10-25"}`, goodToken); rec.Code != http.StatusOK {
		t.Fatalf("end: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(s, "PUT", "/api/plan/window/start", `{"start":"2026-10-03"}`, goodToken); rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(s, "DELETE", "/api/plan/window", ``, goodToken); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if len(e.starts) != 1 || e.starts[0] != (plan.Date{Year: 2026, Month: 10, Day: 3}) || e.deleted != 1 {
		t.Fatalf("start/delete: starts=%v deleted=%d", e.starts, e.deleted)
	}
	if len(e.resized) != 1 || e.resized[0] != -2 || len(e.ends) != 1 || e.ends[0] != (plan.Date{Year: 2026, Month: 10, Day: 25}) {
		t.Fatalf("до сервиса дошло resized=%v ends=%v", e.resized, e.ends)
	}
}

func TestWindowEdit_Errors(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		err                      error
		want                     int
	}{
		{"кривая дата", "PUT", "/api/plan/window/end", `{"end":"25.10"}`, nil, http.StatusBadRequest},
		{"лишнее поле", "POST", "/api/plan/window/resize", `{"days":1,"x":2}`, nil, http.StatusBadRequest},
		{"вне пределов", "POST", "/api/plan/window/resize", `{"days":99}`, plan.ErrBadWindowLength, http.StatusBadRequest},
		{"нет окна", "POST", "/api/plan/window/resize", `{"days":1}`, plan.ErrNoWindow, http.StatusBadRequest},
		{"запись выключена", "POST", "/api/plan/window/resize", `{"days":1}`, plan.ErrReadOnly, http.StatusForbidden},
		{"конец в прошлом", "PUT", "/api/plan/window/end", `{"end":"2026-10-01"}`, plan.ErrWindowEndPast, http.StatusBadRequest},
		{"пересечение", "PUT", "/api/plan/window/start", `{"start":"2026-10-01"}`, plan.ErrWindowOverlap, http.StatusBadRequest},
		{"кривое начало", "PUT", "/api/plan/window/start", `{"start":"1.10"}`, nil, http.StatusBadRequest},
		{"единственное окно", "DELETE", "/api/plan/window", ``, plan.ErrLastWindow, http.StatusBadRequest},
	}
	for _, c := range cases {
		s, _ := winServer(t, &fakeWindowEditor{err: c.err})
		if rec := send(s, c.method, c.path, c.body, goodToken); rec.Code != c.want {
			t.Errorf("%s: %d, ждали %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
}

// Кнопки правки окна видны только у окна на экране; подпись длины — только у поправленного. Без node тест
// пропускается (это НЕ зелёный результат).
//
// Мутация: убрать 'between' из canEditWindow или условие adjusted в lengthNote — краснеет window_test.mjs.
func TestJS_WindowEdit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден — JS-тест правки окна пропущен")
	}
	out, err := exec.Command(node, "jstest/window_test.mjs").CombinedOutput() //nolint:gosec // фиксированный скрипт из репозитория
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
