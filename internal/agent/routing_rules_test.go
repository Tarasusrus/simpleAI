package agent

import (
	"strings"
	"testing"

	"simpleAI/internal/plugin"
)

// Разграничение «правка лимита конверта» vs «запись траты» (ADR-008) живёт
// именно в блоке ROUTING RULES, а не в описании budget-скилла: блок объявлен
// приоритетнее описаний инструментов, и проверка мутацией (ADR-008, «Где на
// самом деле живёт триггер») показала, что вырезание триггеров add_expense из
// Manifest'а маршрут не меняет — решает этот хардкод.
//
// Тест доказывает ровно одно: правило физически присутствует в промпте и стоит
// до списка инструментов. Что модель ему следует — доказывают golden-кейсы r046
// («на еду хватит 15000» → budget.set_share_limit) и r047 («купил еды на 3000»
// → budget.add_expense), они гоняются на боевой модели.
func TestBuildToolsSystemPrompt_ShareLimitRuleSeparatedFromExpense(t *testing.T) {
	prompt := buildToolsSystemPrompt([]plugin.Manifest{
		{ID: "budget", Description: "budget tracker"},
		{ID: "advisor", Description: "advisor"},
		{ID: "safe_to_spend", Description: "safe to spend"},
	})

	rules := prompt[:strings.Index(prompt, "Доступные инструменты")]
	if rules == "" {
		t.Fatal("в промпте нет блока правил до списка инструментов")
	}

	mustContain := map[string]string{
		"set_share_limit":   "нет правила про правку лимита конверта",
		"clear_share_limit": "нет правила про снятие лимита",
		"на еду хватит":     "нет фразы-триггера правки лимита",
		"купил еды на 3000": "нет контраста с записью траты (разграничение ломалось в simpleAI-399 / simpleAI-q49)",
		"хватит ли на":      "нет разграничения с advisor.advice («хватит ли на <покупку>»)",
	}
	for marker, why := range mustContain {
		if !strings.Contains(rules, marker) {
			t.Errorf("%s: в ROUTING RULES нет %q", why, marker)
		}
	}

	// Правило про лимит обязано стоять ПОСЛЕ правила про add_expense: сначала
	// модель читает общий случай траты, потом исключение из него.
	expenseIdx := strings.Index(rules, "add_expense")
	limitIdx := strings.Index(rules, "set_share_limit")
	if expenseIdx == -1 || limitIdx == -1 || limitIdx < expenseIdx {
		t.Errorf("правило про лимит должно идти после правила про add_expense (expense=%d, limit=%d)", expenseIdx, limitIdx)
	}
}
