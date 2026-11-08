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
	resets  int
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

func (f *fakeWindowEditor) ResetWindow(context.Context) (plan.WindowFrame, error) {
	f.resets++
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
		{"DELETE", "/api/plan/window/length", ``},
	} {
		if rec := send(s, c.method, c.path, c.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s без токена: %d", c.method, c.path, rec.Code)
		}
	}
	if len(e.resized)+len(e.ends)+e.resets != 0 {
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
	if rec := send(s, "DELETE", "/api/plan/window/length", ``, goodToken); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	if len(e.resized) != 1 || e.resized[0] != -2 || len(e.ends) != 1 || e.ends[0] != (plan.Date{Year: 2026, Month: 10, Day: 25}) || e.resets != 1 {
		t.Fatalf("до сервиса дошло resized=%v ends=%v resets=%d", e.resized, e.ends, e.resets)
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
		{"запись выключена", "DELETE", "/api/plan/window/length", ``, plan.ErrReadOnly, http.StatusForbidden},
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
