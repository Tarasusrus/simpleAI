# Verification: Financial Advisor Skill

Review of proposal/requirements/acceptance_criteria/constraints. Findings classified BLOCKER (stop), MAJOR (fix before code), MINOR (note).

---

## Completeness

### MAJOR-1 — Курс отсутствует: SQL-ветвление не определено
- **Issue**: AC-E3 говорит «вернуть friendly-ошибку если курса нет», но constraints §6 даёт ту же норму без указания, какая именно валюта триггерит ошибку.
- **Risk**: snapshot всегда нуждается в THB rate (минимум). Если в `exchange_rate` нет THB — ВСЕ ответы упадут. Если нет курса для валюты которая встретилась только в одной recurring записи — стоит ли падать или skip'нуть запись?
- **Resolution**: уточнить — отсутствие THB rate = blocker (friendly error), отсутствие rate для отдельной транзакции/recurring = skip + log warn. Внести в constraints §6.

### MINOR-1 — Low_data threshold magic number
- **Issue**: «< 5 транзакций» (constraints §7) — где-то магическое число.
- **Resolution**: вынести в const `MinTxForConfidence = 5` в коде; зафиксировать в plan.

### MINOR-2 — manifest description: точный текст не задан
- **Issue**: constraints §4 описывает требования к description, но не финальный текст.
- **Resolution**: финальный текст — задача шага реализации, не спеки. Допустимо оставить как guideline.

---

## Consistency

### MAJOR-2 — chat_id асимметрия → AC-F1 не покрыт
- **Issue**: AC-F1 требует «SQL фильтрует по chat_id». Но `budget_transaction` и `budget_debt` НЕ имеют `chat_id` (constraints §1). То есть фильтрация невозможна для этих таблиц — снэпшот включит все транзакции/долги для всех чатов, что нарушает spirit AC-F1 в multi-tenant.
- **Risk**: бот сейчас deploy на ОДИН чат (taras+жена). При расширении на другие чаты advisor будет утекать данные между чатами.
- **Resolution**: 
  - В MVP: задокументировать в constraints что AC-F1 применим только к recurring (которая имеет chat_id), а transactions/debts — глобальные (как у budget_skill сейчас, единый бюджет на инстанс)
  - Поправить AC-F1: «filter by chat_id where applicable; for tables without chat_id (`budget_transaction`, `budget_debt`) — read globally as does budget_skill»

### MAJOR-3 — Multi-user определение в proposal vs реальная схема
- **Issue**: proposal Users описывает «taras + жена», но `TELEGRAM_ALLOWED_CHATS` в env (см. simpleAI.md) — может быть несколько чатов. Реальная семантика «общего бюджета» = один инстанс бота / одна БД, а не один чат.
- **Resolution**: уточнить в requirements FR-6 — «один общий бюджет на весь инстанс бота; chat_id используется только там где таблица его имеет (recurring/reminder)».

### MINOR-3 — currency exchange chain
- **Issue**: constraints §6 «валюта → RUB → THB» — двойная конверсия может усугубить погрешности.
- **Resolution**: округление на финальном шаге (THB), хранить промежуточные значения в `float64`. Внести в constraints или в план как note.

---

## Implementability

### MAJOR-4 — `Store.GetAdvisorSnapshot` смешивает per-chat и global
- **Issue**: один CTE с одновременным фильтром `WHERE chat_id = $1` (для recurring) и без фильтра (для transaction, debt) технически делается, но семантически странен. Может вводить в заблуждение читателя.
- **Resolution**: либо два метода (`GetAdvisorSnapshotGlobal` + reuse `ListRecurring`), либо один CTE с явными комментариями. План должен явно зафиксировать выбор.

### MINOR-4 — Forecast reuse — параллельно или внутри CTE
- **Issue**: constraints §3 «переиспользовать `GetForecastData` или вызывать параллельно». Неоднозначность.
- **Resolution**: в plan.yaml зафиксировать единственный вариант — последовательный вызов `GetForecastData` после snapshot SQL (проще и без race).

### MINOR-5 — testcontainers infra status
- **Issue**: constraints §9 предполагает testcontainers Postgres. Проверить — есть ли он в проекте или его нужно добавить.
- **Resolution**: на этапе плана проверить `go.mod` и существующие тесты `internal/budget/`. Если нет — minimum смягчить до in-memory mocks для skill tests.

---

## Testability

### MINOR-6 — AC-A1/A2 (routing) — не testable без LLM
- **Issue**: routing зависит от LLM-выбора. Тест без реального LLM — это тест манифеста, не реального routing.
- **Resolution**: ограничиться unit-тестом который проверяет `Manifest().Description` содержит ключевые фразы из constraints §4. Manual verification на dev для реального routing.

### MINOR-7 — AC-I1 (latency p95) — нет infra для измерения
- **Issue**: SLO 4000ms p95 не измеряется автоматически.
- **Resolution**: убрать из CI-проверок, оставить как design goal. В plan.yaml указать «benchmark вручную после deploy, не блокер».

---

## Open questions resolved

| Open Q from proposal | Resolved in |
|----|----|
| Manifest description vs budget_skill | constraints §4 + verification MAJOR-2 |
| RUB conversion in question | requirements FR-4, AC-C1..3 |
| Low data fallback | constraints §7, verification MINOR-1 |
| Follow-up detection | requirements FR-5 (всегда полный) |
| Debt schema | constraints §1 (verified) |

---

## Action items before plan

| # | Severity | Action |
|---|----------|--------|
| 1 | MAJOR-1 | Дополнить constraints §6 — поведение при отсутствии курса (THB rate = blocker, прочие — skip+warn) |
| 2 | MAJOR-2 | Поправить AC-F1: фильтрация только для таблиц с chat_id |
| 3 | MAJOR-3 | Поправить requirements FR-6: «общий бюджет на весь инстанс» |
| 4 | MAJOR-4 | Зафиксировать в plan: один SQL CTE с явным комментом про асимметрию + последующий вызов GetForecastData |
| 5 | MINOR-1 | const `MinTxForConfidence = 5` в коде |
| 6 | MINOR-3 | Округление THB на финальном шаге |
| 7 | MINOR-5 | Проверить testcontainers в go.mod на этапе плана |
| 8 | MINOR-6 | Routing-тест = manifest content check |
| 9 | MINOR-7 | Latency SLO — design goal, не CI |

---

## Verdict

**Status: PASS WITH FIXES.** Нет BLOCKER'ов. 4 MAJOR требуют поправок в requirements и constraints перед `04-task-list.md`. MINOR-замечания учитываются при составлении plan.yaml.

Apply fixes → run verification once more (mental check) → proceed to plan.
