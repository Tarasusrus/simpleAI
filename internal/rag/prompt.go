package rag

import "strings"

func BuildPrompt(query string, results []SearchResult) string {
	var sb strings.Builder
	sb.WriteString("Контекст:\n")
	if len(results) == 0 {
		sb.WriteString("- (нет данных)\n")
	} else {
		for _, item := range results {
			sb.WriteString("- ")
			sb.WriteString(item.Content)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\nВопрос:\n")
	sb.WriteString(query)
	sb.WriteString("\n\nОтвет:")
	return sb.String()
}
