package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"simpleAI/internal/plugin"
)

type toolCall struct {
	Skill string         `json:"skill"`
	Input map[string]any `json:"input"`
}

// buildToolsSystemPrompt формирует дополнение к system prompt с описанием доступных tools.
func buildToolsSystemPrompt(manifests []plugin.Manifest) string {
	var sb strings.Builder
	now := time.Now()
	fmt.Fprintf(&sb,
		"Сегодня: %s (год %d, месяц %d). При указании месяца без года: если этот месяц уже прошёл или идёт сейчас — используй %d; если месяц ещё не наступил в этом году (например декабрь когда сейчас март) — используй %d.\n\n",
		now.Format("02.01.2006"), now.Year(), int(now.Month()), now.Year(), now.Year()-1,
	)
	sb.WriteString("У тебя есть доступ к инструментам. Если нужно вызвать инструмент — ответь ТОЛЬКО валидным JSON без markdown и пояснений.\n")
	sb.WriteString("Один вызов: {\"skill\": \"<id>\", \"input\": {<параметры>}}\n")
	sb.WriteString("Несколько вызовов за раз: [{\"skill\": \"<id>\", \"input\": {...}}, {\"skill\": \"<id>\", \"input\": {...}}]\n")
	sb.WriteString("\nROUTING RULES (приоритет выше описаний инструментов):\n")
	sb.WriteString("1. Будущая покупка / совет о покупке («хочу купить», «планирую купить», «думаю купить», «стоит ли купить», «можем ли позволить», «хватит ли денег на», «потянем ли», «что приоритетнее») — ВСЕГДА skill=advisor, action=advice. НИКОГДА не вызывай budget.add_expense для будущих/гипотетических покупок.\n")
	sb.WriteString("2. Запись СОВЕРШЁННОЙ траты (прошедшее время: «купил», «купила», «потратил», «заплатил», «оплатил») — skill=budget, action=add_expense.\n")
	sb.WriteString("3. Правка ЛИМИТА конверта (доли прихода) — skill=budget, action=set_share_limit, поля name=<категория>, amount=<сумма>: «на еду хватит 15000», «на транспорт закладывай 5000», «лимит на развлечения 3000», «ставь на еду 15000». Денег НЕ потратили — это план на будущее, поэтому НЕ add_expense.\n")
	sb.WriteString("   Снятие лимита («убери лимит на еду», «сними лимит с транспорта», «считай лимит на еду сам») — skill=budget, action=clear_share_limit, поле name=<категория>.\n")
	sb.WriteString("   Разграничение с правилом 1: ВОПРОС «хватит ли на <покупку>?» — это advisor.advice; УТВЕРЖДЕНИЕ «на <категорию> хватит <сумма>» (сумма названа, вопроса нет) — это budget.set_share_limit.\n")
	sb.WriteString("   Разграничение с правилом 2: «купил еды на 3000» — трата уже случилась (прошедшее время) → budget.add_expense; «на еду хватит 15000» — трат нет, задан лимит → budget.set_share_limit.\n")
	sb.WriteString("   set_share_limit требует НАЗВАННОЙ суммы. Вопрос без суммы («сколько откладывать на машину», «сколько закладывать на еду») — это совет, skill=advisor, action=advice.\n")
	sb.WriteString("4. РАСКЛАДКА пришедшего дохода по конвертам («пришло 127000, разложи по конвертам», «разложи приход X по конвертам», «раскидай X по конвертам», «распредели X по конвертам») — skill=budget, action=start_envelope, поля amount, currency. Раскладка ПИШЕТ конверт; safe_to_spend только считает и ничего не сохраняет, поэтому для «разложи по конвертам» он НЕ подходит. Без слова про конверты («пришло X, сколько свободно?») — по-прежнему safe_to_spend.\n")
	sb.WriteString("5. Вопрос про ОСТАТОК по категории или по конвертам («сколько осталось на еду», «сколько в конвертах», «сколько осталось в конверте на транспорт») — skill=safe_to_spend, сумму НЕ передавать. Это не budget.summary: спрашивают не сколько потрачено, а сколько ещё можно потратить.\n")
	sb.WriteString("6. ВАЛЮТА КОНВЕРТОВ. По умолчанию конверты показываются В БАТАХ (THB) — поле display_currency НЕ заполняй. Если оператор попросил рубли («покажи конверты в рублях», «разложи и покажи в рублях», «сколько это в рублях») — передай display_currency=\"RUB\"; если явно попросил баты («в батах») — display_currency=\"THB\". Поле display_currency — только про ПОКАЗ; сумму из сообщения оно не меняет.\n")
	sb.WriteString("   Валюта САМОЙ СУММЫ идёт в currency: «пришло 127000₽» → currency=\"RUB\"; «на еду хватит 5000 бат» → currency=\"THB\"; «на еду хватит 15000 рублей» → currency=\"RUB\". Не путай currency с display_currency: первая описывает названную сумму, вторая — валюту ответа.\n")
	sb.WriteString("7. Если в сообщении нет суммы и нет глагола в прошедшем времени — это НЕ add_expense.\n")
	sb.WriteString("\nДоступные инструменты:\n")
	for _, m := range manifests {
		fmt.Fprintf(&sb, "- %s: %s\n", m.ID, m.Description)
		if m.InputSchema != nil {
			b, err := json.Marshal(m.InputSchema.JSON)
			if err == nil {
				fmt.Fprintf(&sb, "  Параметры: %s\n", string(b))
			}
		}
	}
	sb.WriteString("\nЕсли инструменты не нужны — отвечай обычным текстом.")
	return sb.String()
}

// parseToolCalls извлекает все tool calls из ответа LLM.
// Поддерживает: одиночный JSON-объект, JSON-массив объектов, JSON внутри markdown.
func parseToolCalls(resp string) []toolCall {
	trimmed := strings.TrimSpace(resp)

	// Извлечь из markdown code block.
	for _, fence := range []string{"```json", "```"} {
		start := strings.Index(trimmed, fence)
		if start == -1 {
			continue
		}
		inner := trimmed[start+len(fence):]
		end := strings.Index(inner, "```")
		if end == -1 {
			continue
		}
		if calls, ok := unmarshalCalls(strings.TrimSpace(inner[:end])); ok {
			return calls
		}
	}

	// Попробовать как JSON-массив или одиночный объект напрямую.
	if calls, ok := unmarshalCalls(trimmed); ok {
		return calls
	}

	// Найти первый JSON-объект/массив в тексте.
	for _, start := range []string{"[", "{"} {
		idx := strings.Index(trimmed, start)
		if idx == -1 {
			continue
		}
		var endChar string
		if start == "[" {
			endChar = "]"
		} else {
			endChar = "}"
		}
		end := strings.LastIndex(trimmed, endChar)
		if end > idx {
			if calls, ok := unmarshalCalls(trimmed[idx : end+1]); ok {
				return calls
			}
		}
	}

	return nil
}

// unmarshalCalls пробует распарсить s как []toolCall или одиночный toolCall.
func unmarshalCalls(s string) ([]toolCall, bool) {
	// Попробовать как массив.
	var arr []toolCall
	if err := json.Unmarshal([]byte(s), &arr); err == nil {
		var valid []toolCall
		for _, c := range arr {
			if c.Skill != "" {
				valid = append(valid, c)
			}
		}
		if len(valid) > 0 {
			return valid, true
		}
	}

	// Попробовать как одиночный объект.
	var single toolCall
	if err := json.Unmarshal([]byte(s), &single); err == nil && single.Skill != "" {
		return []toolCall{single}, true
	}

	return nil, false
}

// invalidCall — вызов, отбракованный до похода в навык.
type invalidCall struct {
	Index  int    // номер вызова в ответе модели, с единицы
	Skill  string // навык, который модель пыталась вызвать
	Reason string // чего не хватило — формулировка уходит обратно модели
}

// validateCalls отбраковывает вызовы, которые заведомо не доедут до навыка:
// без объекта input и без обязательных полей из схемы навыка.
//
// Без этой проверки вызов вида {"skill":"budget"} проходил насквозь:
// json.Marshal(nil map) даёт "null", Unmarshal("null") ошибки не возвращает,
// навык получал нулевую структуру и падал на разборе пустого action.
//
// Пачка проверяется целиком до выполнения: если хоть один вызов неполон,
// не выполняется ни один. Иначе половина трат из сообщения записалась бы, а
// повторный запрос к модели продублировал бы её.
func validateCalls(calls []toolCall, manifests []plugin.Manifest) []invalidCall {
	schemas := make(map[string]*plugin.Schema, len(manifests))
	for _, m := range manifests {
		schemas[m.ID] = m.InputSchema
	}

	var bad []invalidCall
	for i, c := range calls {
		if reason := validateCall(c, schemas[c.Skill]); reason != "" {
			bad = append(bad, invalidCall{Index: i + 1, Skill: c.Skill, Reason: reason})
		}
	}
	return bad
}

// validateCall возвращает причину отбраковки или пустую строку, если вызов цел.
// Навык без схемы проверяется только на наличие input: набор его полей неизвестен.
func validateCall(call toolCall, schema *plugin.Schema) string {
	if call.Input == nil {
		return `отсутствует объект "input" с параметрами`
	}
	for _, f := range requiredFields(schema) {
		v, ok := call.Input[f]
		if !ok {
			return fmt.Sprintf("в input не заполнено обязательное поле %q", f)
		}
		if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
			return fmt.Sprintf("обязательное поле %q пустое", f)
		}
		if v == nil {
			return fmt.Sprintf("обязательное поле %q пустое", f)
		}
	}
	return ""
}

// requiredFields читает список обязательных полей из JSON-схемы навыка.
// Схема собирается в коде ([]string) и может приходить после json.Unmarshal ([]any).
func requiredFields(schema *plugin.Schema) []string {
	if schema == nil || schema.JSON == nil {
		return nil
	}
	switch v := schema.JSON["required"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// buildRetryPrompt объясняет модели, чем плох предыдущий ответ, и просит
// собрать вызовы заново. Исходное сообщение пользователя повторяется: агент
// stateless, без него модель не знает, что переспрашивают.
func buildRetryPrompt(userInput string, bad []invalidCall) string {
	var sb strings.Builder
	sb.WriteString("Предыдущий ответ некорректен, инструменты не выполнены.\n")
	for _, b := range bad {
		fmt.Fprintf(&sb, "- вызов %d (skill=%q): %s\n", b.Index, b.Skill, b.Reason)
	}
	sb.WriteString("\nПовтори ответ целиком: у каждого вызова обязательны поле \"skill\" и объект \"input\" со всеми обязательными параметрами. Отвечай только JSON.\n")
	fmt.Fprintf(&sb, "Сообщение пользователя: %s", userInput)
	return sb.String()
}

// describeInvalid сворачивает причины отбраковки в одну строку для лога.
func describeInvalid(bad []invalidCall) string {
	parts := make([]string, 0, len(bad))
	for _, b := range bad {
		parts = append(parts, fmt.Sprintf("#%d %s: %s", b.Index, b.Skill, b.Reason))
	}
	return strings.Join(parts, "; ")
}
