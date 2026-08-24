package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Утренний пуш идёт своим HTTP-клиентом, мимо адаптера Telegram. Без этих
// тестов разметка чинилась бы только в одном из двух мест отправки — и
// раскладка конвертов разваливалась бы ровно в пуше.

type fakeAPI struct {
	mu     sync.Mutex
	calls  []map[string]any
	failOn func(map[string]any) bool
	srv    *httptest.Server
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, payload)
		fail := f.failOn != nil && f.failOn(payload)
		f.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			t.Errorf("тестовый сервер не смог ответить: %v", err)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newTestTelegram(t *testing.T, f *fakeAPI) *Telegram {
	t.Helper()
	tg := NewTelegram("TOKEN", "42")
	tg.baseURL = f.srv.URL
	tg.client = f.srv.Client()
	return tg
}

func (f *fakeAPI) sent() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.calls...)
}

const pushLayout = "01.09 — 14.09 · 14 дней\n" +
	"**Что осталось**\n" +
	"```\n" +
	"Аренда         15.09  12 000\n" +
	"Еда                    8 000\n" +
	"```\n" +
	"**На день: 1 200 ฿**"

func TestSendToChatID_UsesHTML(t *testing.T) {
	f := newFakeAPI(t)
	tg := newTestTelegram(t, f)

	if err := tg.SendToChatID(context.Background(), 7, pushLayout); err != nil {
		t.Fatalf("send: %v", err)
	}
	calls := f.sent()
	if len(calls) != 1 {
		t.Fatalf("ожидался один вызов, получено %d", len(calls))
	}
	if calls[0]["parse_mode"] != "HTML" {
		t.Errorf("пуш ушёл без parse_mode=HTML: %#v", calls[0])
	}
	text, ok := calls[0]["text"].(string)
	if !ok {
		t.Fatalf("в запросе нет текстового поля text: %#v", calls[0])
	}
	if !strings.Contains(text, "<pre>Аренда         15.09  12 000\nЕда                    8 000</pre>") {
		t.Errorf("моноблок пуша не стал pre:\n%s", text)
	}
	if !strings.Contains(text, "<b>На день: 1 200 ฿</b>") {
		t.Errorf("заголовок пуша не жирный:\n%s", text)
	}
	if strings.Contains(text, "```") || strings.Contains(text, "**") {
		t.Errorf("разметка уехала литералом:\n%s", text)
	}
}

// Злые символы в имени платежа не роняют пуш: экранируются той же функцией,
// что и в адаптере.
func TestSendToChatID_EscapesEvilStrings(t *testing.T) {
	f := newFakeAPI(t)
	tg := newTestTelegram(t, f)

	if err := tg.SendToChatID(context.Background(), 7, "Кафе <Мама & Папа> — 15.09"); err != nil {
		t.Fatalf("send: %v", err)
	}
	text, ok := f.sent()[0]["text"].(string)
	if !ok {
		t.Fatalf("в запросе нет текстового поля text: %#v", f.sent()[0])
	}
	if !strings.Contains(text, "&lt;Мама &amp; Папа&gt;") {
		t.Fatalf("не экранировано: %q", text)
	}
}

// Длинный пуш режется по лимиту, а не роняет отправку целиком.
func TestSendToChatID_SplitsLongPush(t *testing.T) {
	f := newFakeAPI(t)
	tg := newTestTelegram(t, f)

	long := strings.Repeat("Категория очень длинная 1 234\n", 400)
	if err := tg.SendToChatID(context.Background(), 7, long); err != nil {
		t.Fatalf("send: %v", err)
	}
	calls := f.sent()
	if len(calls) < 2 {
		t.Fatalf("длинный пуш ушёл %d куском(ами)", len(calls))
	}
	for i, c := range calls {
		text, ok := c["text"].(string)
		if !ok {
			t.Fatalf("в куске %d нет текстового поля text: %#v", i, c)
		}
		if n := len([]rune(text)); n > 4096 {
			t.Errorf("кусок %d длиной %d — Telegram вернёт 400", i, n)
		}
	}
}

// 400 на разметке не оставляет пользователя без утреннего пуша: тот же текст
// уходит простым.
func TestSendToChatID_FallsBackToPlainOn400(t *testing.T) {
	f := newFakeAPI(t)
	f.failOn = func(p map[string]any) bool { return p["parse_mode"] == "HTML" }
	tg := newTestTelegram(t, f)

	if err := tg.SendToChatID(context.Background(), 7, pushLayout); err != nil {
		t.Fatalf("фоллбэк не сработал: %v", err)
	}
	calls := f.sent()
	last := calls[len(calls)-1]
	if _, has := last["parse_mode"]; has {
		t.Errorf("фоллбэк ушёл с parse_mode: %#v", last)
	}
	text, ok := last["text"].(string)
	if !ok {
		t.Fatalf("в запросе нет текстового поля text: %#v", last)
	}
	if !strings.Contains(text, "Аренда") {
		t.Errorf("фоллбэк потерял содержимое: %q", text)
	}
}
