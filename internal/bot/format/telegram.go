package botformat

import "strings"

// EscapeTelegramMarkdown escapes special characters in Telegram legacy Markdown.
func EscapeTelegramMarkdown(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"`", "\\`",
	)
	return r.Replace(s)
}
