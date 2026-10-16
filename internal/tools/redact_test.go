package tools

import (
	"bytes"
	"errors"
	"log"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

// Токен выдуманный, по форме совпадает с настоящим.
const fakeToken = "123456789:AAFakeTokenFakeTokenFakeToken_abcd"

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"Post \"https://api.telegram.org/bot" + fakeToken + "/getUpdates\": EOF": "Post \"https://api.telegram.org/bot[REDACTED]/getUpdates\": EOF",
		"token=" + fakeToken:        "token=[REDACTED]",
		"без токена 12:34 и 2026:1": "без токена 12:34 и 2026:1",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// Ошибка getUpdates из tgbotapi: *url.Error с полным URL. Обязана пройти и
// через slog (поле err), и через стандартный log.
func TestLogger_NoTokenInAnyOutput(t *testing.T) {
	orig := log.Writer()
	defer log.SetOutput(orig)
	for _, format := range []string{"text", "json"} {
		var out, std bytes.Buffer
		l := newLogger(&out, &std, slog.LevelInfo, format)

		err := &url.Error{Op: "Post", URL: "https://api.telegram.org/bot" + fakeToken + "/getUpdates", Err: errors.New("EOF")}
		l.Error("getUpdates failed", "err", err, "msg2", "x "+fakeToken)
		log.Println(err)

		for name, got := range map[string]string{"slog": out.String(), "log": std.String()} {
			if strings.Contains(got, fakeToken) || strings.Contains(got, "AAFakeToken") {
				t.Errorf("%s/%s: токен в выводе: %s", format, name, got)
			}
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("%s/%s: маски нет, лог пуст или не тот: %q", format, name, got)
			}
		}
	}
}
