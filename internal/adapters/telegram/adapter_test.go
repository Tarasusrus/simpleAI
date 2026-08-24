package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"simpleAI/internal/core"
)

// fakeTelegram — подставной api.telegram.org: пишет все sendMessage-вызовы и
// умеет отвечать 400, как настоящий Telegram на кривой разметке.
type fakeTelegram struct {
	mu     sync.Mutex
	calls  []map[string]string
	failOn func(call map[string]string) bool
	srv    *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		call := map[string]string{"method": r.URL.Path}
		for k := range r.Form {
			call[k] = r.Form.Get(k)
		}
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			writeJSON(w, http.StatusOK, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"t"}}`)
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, call)
		fail := f.failOn != nil && f.failOn(call)
		f.mu.Unlock()
		if fail {
			writeJSON(w, http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"can't parse entities"}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":1,"type":"private"}}}`)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func (f *fakeTelegram) sendCalls() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]string, 0, len(f.calls))
	for _, c := range f.calls {
		if strings.HasSuffix(c["method"], "/sendMessage") || strings.HasSuffix(c["method"], "/editMessageText") {
			out = append(out, c)
		}
	}
	return out
}

func newTestAdapter(t *testing.T, f *fakeTelegram) *Adapter {
	t.Helper()
	bot, err := tgbotapi.NewBotAPIWithAPIEndpoint("TOKEN", f.srv.URL+"/bot%s/%s")
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	return &Adapter{bot: bot, pollingTimeout: time.Second, httpClient: f.srv.Client()}
}

const approvedLayout = "01.09 — 14.09 · 14 дней\n" +
	"Пришло 40 968 ฿\n" +
	"**Куда уйдут**\n" +
	"```\n" +
	"Аренда         15.09  12 000\n" +
	"Еда                    8 000\n" +
	"```\n" +
	"**На день: 1 200 ฿**"

// Утверждённый формат раскладки доезжает разметкой: parse_mode=HTML,
// моноблок — настоящий <pre>, заголовки — <b>, тройных кавычек в payload нет.
func TestAdapterSend_EnvelopeLayoutGoesAsHTML(t *testing.T) {
	f := newFakeTelegram(t)
	a := newTestAdapter(t, f)

	if err := a.Send(context.Background(), 1, approvedLayout); err != nil {
		t.Fatalf("send: %v", err)
	}
	calls := f.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("ожидался один sendMessage, получено %d", len(calls))
	}
	got := calls[0]
	if got["parse_mode"] != "HTML" {
		t.Errorf("parse_mode = %q, ожидался HTML", got["parse_mode"])
	}
	text := got["text"]
	if !strings.Contains(text, "<pre>Аренда         15.09  12 000\nЕда                    8 000</pre>") {
		t.Errorf("моноблок не доехал как pre:\n%s", text)
	}
	if !strings.Contains(text, "<b>Куда уйдут</b>") || !strings.Contains(text, "<b>На день: 1 200 ฿</b>") {
		t.Errorf("заголовки не жирные:\n%s", text)
	}
	if strings.Contains(text, "```") || strings.Contains(text, "**") {
		t.Errorf("разметка уехала литералом:\n%s", text)
	}
}

// Регресс: обычный ответ скилла уходит одним сообщением и без изменений —
// разметка не должна ничего добавлять к тексту без спецсимволов.
func TestAdapterSend_PlainSkillReplyIntact(t *testing.T) {
	f := newFakeTelegram(t)
	a := newTestAdapter(t, f)

	const reply = "✅ Записал: Еда — 250 ฿ (15.09)"
	if err := a.Send(context.Background(), 1, reply); err != nil {
		t.Fatalf("send: %v", err)
	}
	calls := f.sendCalls()
	if len(calls) != 1 || calls[0]["text"] != reply {
		t.Fatalf("обычный ответ изменился: %#v", calls)
	}
}

// Злые строки от оператора и LLM экранируются, а не роняют отправку.
func TestAdapterSend_EscapesEvilStrings(t *testing.T) {
	f := newFakeTelegram(t)
	a := newTestAdapter(t, f)

	if err := a.Send(context.Background(), 1, "Кафе <Мама & Папа> 15.09 — 1 200 ฿"); err != nil {
		t.Fatalf("send: %v", err)
	}
	text := f.sendCalls()[0]["text"]
	if !strings.Contains(text, "&lt;Мама &amp; Папа&gt;") {
		t.Fatalf("не экранировано: %q", text)
	}
}

// Длинное сообщение не роняет отправку: режется на части, каждая в лимите.
func TestAdapterSend_LongMessageSplit(t *testing.T) {
	f := newFakeTelegram(t)
	a := newTestAdapter(t, f)

	long := strings.Repeat("Категория очень длинная 1 234\n", 400)
	if err := a.Send(context.Background(), 1, long); err != nil {
		t.Fatalf("send: %v", err)
	}
	calls := f.sendCalls()
	if len(calls) < 2 {
		t.Fatalf("длинное сообщение ушло %d куском(ами)", len(calls))
	}
	for i, c := range calls {
		if n := len([]rune(c["text"])); n > 4096 {
			t.Errorf("кусок %d длиной %d — Telegram вернёт 400", i, n)
		}
	}
}

// Если Telegram всё-таки ответил 400 на разметку — сообщение уходит простым
// текстом. Пользователь обязан получить содержимое, а не тишину.
func TestAdapterSend_FallsBackToPlainOn400(t *testing.T) {
	f := newFakeTelegram(t)
	f.failOn = func(call map[string]string) bool { return call["parse_mode"] == "HTML" }
	a := newTestAdapter(t, f)

	if err := a.Send(context.Background(), 1, approvedLayout); err != nil {
		t.Fatalf("фоллбэк не сработал: %v", err)
	}
	calls := f.sendCalls()
	last := calls[len(calls)-1]
	if last["parse_mode"] != "" {
		t.Errorf("фоллбэк ушёл с parse_mode=%q", last["parse_mode"])
	}
	if !strings.Contains(last["text"], "Аренда") {
		t.Errorf("фоллбэк потерял содержимое: %q", last["text"])
	}
}

// Кнопки вешаются на ПОСЛЕДНИЙ кусок: иначе при разрезе клавиатура уезжает в
// середину переписки и остаётся выше текста, к которому относится.
func TestAdapterSendWithButtons_KeyboardOnLastChunk(t *testing.T) {
	f := newFakeTelegram(t)
	a := newTestAdapter(t, f)

	long := strings.Repeat("строка отчёта 1 234\n", 400)
	rows := [][]core.Button{{{Text: "Да", Data: "yes"}}}
	if err := a.SendWithButtons(context.Background(), 1, long, rows); err != nil {
		t.Fatalf("send: %v", err)
	}
	calls := f.sendCalls()
	if len(calls) < 2 {
		t.Fatalf("ожидался разрез, получено %d", len(calls))
	}
	for i, c := range calls {
		_, has := c["reply_markup"]
		if want := i == len(calls)-1; has != want {
			t.Errorf("кусок %d: reply_markup=%v, ожидалось %v", i, has, want)
		}
	}
	var kb tgbotapi.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(calls[len(calls)-1]["reply_markup"]), &kb); err != nil {
		t.Fatalf("клавиатура не разобралась: %v", err)
	}
	if len(kb.InlineKeyboard) != 1 || kb.InlineKeyboard[0][0].Text != "Да" {
		t.Fatalf("клавиатура потерялась: %#v", kb)
	}
}

// Правка сообщения тоже идёт с разметкой — иначе отредактированная раскладка
// разваливается там, где исходная была цела.
func TestAdapterEditWithButtons_UsesHTML(t *testing.T) {
	f := newFakeTelegram(t)
	a := newTestAdapter(t, f)

	if err := a.EditWithButtons(context.Background(), 1, 7, approvedLayout, nil); err != nil {
		t.Fatalf("edit: %v", err)
	}
	calls := f.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("ожидался один editMessageText, получено %d", len(calls))
	}
	if calls[0]["parse_mode"] != "HTML" || !strings.Contains(calls[0]["text"], "<pre>") {
		t.Fatalf("правка ушла без разметки: %#v", calls[0])
	}
}
