package advisorskill

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"simpleAI/internal/budget"
)



func unmarshalJSON(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func trimList(items []string, maxLen int) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		t := strings.TrimSpace(it)
		if t == "" {
			continue
		}
		out = append(out, truncateRunes(t, maxLen))
	}
	return out
}

func formatSpentByCategory(m map[string]float64) string {
	if len(m) == 0 {
		return "  (нет расходов)"
	}
	type kv struct {
		k string
		v float64
	}
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].v > out[j].v })
	var sb strings.Builder
	for _, e := range out {
		fmt.Fprintf(&sb, "  - %s: %.0f\n", e.k, e.v)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatTopExpenses(top []budget.TopExpense) string {
	if len(top) == 0 {
		return "  (нет данных)"
	}
	var sb strings.Builder
	for _, e := range top {
		fmt.Fprintf(&sb, "  - %s · %s · %.0f THB\n", e.Date.Format("2006-01-02"), e.Category, e.AmountTHB)
	}
	return strings.TrimRight(sb.String(), "\n")
}
