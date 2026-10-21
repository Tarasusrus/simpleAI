# ADR-003: Eval Suite Convention — golden-set формат и процесс

**Дата:** 2026-05-06
**Статус:** Proposed
**Авторы:** @architect (через эпик simpleAI-wr7)
**Связанные ADR:** ADR-002 (LLM-reasoning skills)

---

## Context

Routing и advisor — LLM-поведение. Юнит-тесты ловят компиляцию и контракты, но не accuracy: фраза «хочу купить блендер» должна уходить в `advisor.advice`, а не `budget.add_expense`. Регрессия такого рода видна только на конкретных кейсах.

Триггер ADR — эпик `simpleAI-wr7` (Eval Suite). Перед рефакторингом по образцу assister/rag_mm (`simpleAI-4fc`) нужна сетка регрессионной защиты, иначе раздробление `budget_skill.go` и вынос промптов в файлы тихо ломает routing.

Нужно зафиксировать:
- где живёт golden-set,
- какой формат кейса,
- как добавляется новый кейс,
- как трекается baseline.

---

## Decision

### Расположение

```
evals/
├── README.md
├── golden_set.jsonl    # эталонные кейсы
└── runs/               # отчёты прогонов
    └── .gitkeep
```

`evals/` — на верхнем уровне репозитория (не внутри `internal/`), потому что это не код приложения, а артефакт качества. Образец — `assister/evals/` в work-проекте.

### Формат кейса (jsonl)

Один JSON-объект на строку. Schema:

```jsonc
{
  "id": "r001",                    // string, уникален в файле, префикс по типу
  "input": "хочу купить блендер",  // string, реальная фраза пользователя
  "expected": {
    "skill": "advisor",            // string, имя skill'а из Manifest
    "action": "advice"             // string, имя action из Manifest
  },
  "tags": ["future_purchase"],     // string[], срезы для аналитики
  "notes": "real bug 2026-04-23",  // string, optional — откуда кейс, какой класс ошибки
  "deprecated": false              // bool, optional — кейс выведен из набора
}
```

Поле `notes` — необязательное, но настоятельно рекомендуется заполнять для кейсов из реального трафика: ссылка на Langfuse trace, дата инцидента, имя пользователя/чата. Без notes через полгода непонятно зачем кейс существует.

### Формат run-файла

Артефакт прогона harness'а — `evals/runs/<timestamp>_<promptHash>.jsonl`, где `promptHash` = sha256[:12] от system prompt.

Структура: одна строка на каждый Result, последняя строка — meta + summary:

```jsonc
{"case_id":"r001","expected_skill":"advisor","expected_action":"advice","actual_skill":"advisor","actual_action":"advice","pass":true,"tags":["future_purchase"],"raw_response":"..."}
{"summary":{"total":N,"pass":N,"fail":N,"accuracy":0.0,"per_skill":{...},"per_tag":{...},"failed_ids":[...]},"timestamp":"...","commit":"abc1234","prompt_hash":"deadbeef..."}
```

`prompt_hash` в имени файла и внутри run — связывает результат с конкретной версией system prompt'а. Меняется промпт → меняется hash → видно в имени файла какой версией снято.

**Префиксы id:**
- `r###` — routing (правильный skill+action на входе)
- `a###` — advisor (правильный shape ответа advisor.advice)
- Будущие — `b###` budget actions, `f###` forecast и т.д.

**Теги (открытый словарь, не enum):** `past_tense`, `future_purchase`, `no_amount`, `low_data`, `with_currency`, ... Используются для срезов в отчёте.

**Никогда не удалять кейсы.** Если кейс перестал быть валидным — пометить `"deprecated": true` и оставить в файле. Это сохраняет историю и позволяет восстановить набор.

### Процесс добавления кейса

1. Воспроизвести реальную фразу (логи, Langfuse traces, ручной разговор).
2. Подобрать минимальный `input`, который сохраняет суть.
3. Зафиксировать `expected` — то, что **должно** произойти, не то что происходит.
4. Добавить 1-3 тега для среза.
5. Append в `golden_set.jsonl`. Без сортировки — порядок добавления = история.

### Baseline tracking

- Первый прогон на main → `evals/runs/baseline-YYYY-MM-DD.jsonl`.
- Каждый прогон в PR → `evals/runs/<timestamp>_<promptHash>.jsonl` (gitignored через `.gitignore` на `evals/runs/*` кроме `.gitkeep`, `baseline-*.jsonl` и `BASELINE.md`).
- Регрессия относительно baseline = блокер мержа. Объяснение в PR description обязательно.
- Перебитие baseline после улучшения accuracy — отдельный коммит с обновлением baseline-файла.

### Harness — отдельная задача

Сам runner (`cmd/eval` или `evals/runner/`) описывается в `simpleAI-85c`. Этот ADR — только конвенция данных и процесса.

---

## Consequences

**Плюсы:**
- Рефакторинги (`simpleAI-4fc` и подзадачи) валидируются объективно.
- Низкий порог добавления кейса — одна строка jsonl.
- Append-only история защищает от незаметной деградации набора.
- Теги дают срезы без новых файлов.

**Минусы:**
- Поддержка golden-set требует дисциплины — иначе устаревает.
- jsonl без schema-валидации на write side; ошибки парсинга вылезают в runtime harness.
- Subjective ground truth: «правильный» action может быть спорным; требуется review при добавлении.

**Действия:**
- Перевести ADR в Accepted после согласования с @architect.
- Подзадача `simpleAI-578` наполняет golden_set ≥20 кейсами по этой схеме.
- Подзадача `simpleAI-85c` реализует harness, читающий этот формат.

---

## Links

- Эпик: simpleAI-wr7 (Eval Suite)
- Подзадачи: simpleAI-85c (harness), simpleAI-578 (golden_set), simpleAI-lpy (baseline)
- Образец: `assister/evals/` (work-проект)
