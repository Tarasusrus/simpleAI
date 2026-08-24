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

	rules := rulesBlock(t, prompt)

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

// Правило про курс живёт в том же блоке ROUTING RULES и разграничено с двумя
// соседями, на которых оно естественно налипает (simpleAI-su6l):
//   - «покажи конверты в рублях» — это ПОКАЗ (display_currency), курс не трогаем;
//   - «на еду хватит 15000» — лимит конверта, там названа КАТЕГОРИЯ.
//
// Как и тест выше, доказывает только присутствие правила в промпте. Что модель
// ему следует — гоняют golden-кейсы r053–r056 на боевой модели.
func TestBuildToolsSystemPrompt_RateRuleSeparatedFromDisplayAndLimit(t *testing.T) {
	prompt := buildToolsSystemPrompt([]plugin.Manifest{
		{ID: "budget", Description: "budget tracker"},
		{ID: "safe_to_spend", Description: "safe to spend"},
	})
	rules := rulesBlock(t, prompt)

	mustContain := map[string]string{
		"set_rate":           "нет правила про ручной курс",
		"clear_rate":         "нет правила про возврат автокурса",
		"rate_status":        "нет правила про «какой сейчас курс»",
		"курс авто":          "нет фразы-триггера возврата к автокурсу",
		"РУБЛЕЙ за один БАТ": "не сказано, что число — курс, а не сумма денег",
	}
	for marker, why := range mustContain {
		if !strings.Contains(rules, marker) {
			t.Errorf("%s: в ROUTING RULES нет %q", why, marker)
		}
	}

	// Правило про курс обязано стоять ПОСЛЕ правил про display_currency и про
	// лимит: разграничение читается как уточнение к ним, а не наоборот.
	rateAt := strings.Index(rules, "КУРС БАТА")
	displayAt := strings.Index(rules, "ВАЛЮТА КОНВЕРТОВ")
	limitAt := strings.Index(rules, "Правка ЛИМИТА конверта")
	if rateAt < 0 || displayAt < 0 || limitAt < 0 {
		t.Fatalf("не найдены заголовки правил: курс=%d показ=%d лимит=%d", rateAt, displayAt, limitAt)
	}
	if rateAt < displayAt || rateAt < limitAt {
		t.Errorf("правило про курс стоит раньше правил, от которых его отделяют (курс=%d показ=%d лимит=%d)",
			rateAt, displayAt, limitAt)
	}
}

// rulesBlock вырезает блок ROUTING RULES — всё до списка инструментов.
//
// Отдельной функцией, а не срезом по strings.Index на месте: Index возвращает
// −1, когда маркера нет, и prompt[:-1] уронил бы тест паникой вместо внятного
// «в промпте нет блока правил».
func rulesBlock(t *testing.T, prompt string) string {
	t.Helper()
	at := strings.Index(prompt, "Доступные инструменты")
	if at <= 0 {
		t.Fatal("в промпте нет блока правил до списка инструментов")
	}
	return prompt[:at]
}
