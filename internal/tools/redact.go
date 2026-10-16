package tools

import (
	"io"
	"regexp"
)

// tokenRe ловит токен Telegram-бота: «bot123456789:AA…» в URL запроса и голый
// «123456789:AA…» в тексте. Библиотека tgbotapi кладёт полный URL в текст ошибки
// (Post "https://api.telegram.org/bot<TOKEN>/getUpdates": …) и печатает её в
// стандартный log, мимо slog — поэтому маскируется и то и другое.
var tokenRe = regexp.MustCompile(`(bot)?\d{6,}:[A-Za-z0-9_-]{25,}`)

const redacted = "[REDACTED]"

// Redact заменяет токены в строке на [REDACTED]; префикс «bot» сохраняется
// только вместе с маской, чтобы путь запроса в логе оставался читаемым.
func Redact(s string) string {
	return tokenRe.ReplaceAllStringFunc(s, func(m string) string {
		if len(m) > 3 && m[:3] == "bot" {
			return "bot" + redacted
		}
		return redacted
	})
}

// redactWriter пропускает вывод через Redact. Один Write — одна запись лога
// (так пишут и log, и slog), поэтому токен не может разорваться между вызовами.
type redactWriter struct{ w io.Writer }

func (r redactWriter) Write(p []byte) (int, error) {
	if _, err := r.w.Write([]byte(Redact(string(p)))); err != nil {
		return 0, err
	}
	return len(p), nil
}
