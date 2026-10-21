# ADR-002: LLM-reasoning skills как отдельные plugin'ы

**Дата:** 2026-05-04
**Статус:** Accepted
**Авторы:** @architect (via spec/advisor)
**Связанные ADR:** ADR-001 (Forecast Architecture)

---

## Context

После Stage 1 forecast (ADR-001) проект движется в сторону советующих фич: financial advisor (#43), debt manager (#44), planner и подобные. У этих фич общий профиль:

- **Не пишут в БД** — только читают.
- **Потребляют несколько источников** — transactions, debt, recurring, forecast — одновременно.
- **Используют LLM как ядро** — структурированный prompt → JSON-ответ → форматирование для пользователя.
- **Один action на skill** — нет CRUD-разнообразия.

Текущая практика, заданная ADR-001 для forecast, — добавлять action **внутрь BudgetSkill** ради «no new dependencies». Этот аргумент работает для агрегатора одной сущности (forecast читает только `budget_transaction`). Для LLM-reasoning skill'ов он перестаёт работать:

- BudgetSkill уже > 1100 LOC и продолжает расти на каждый CRUD-action.
- LLM-reasoning skill приносит prompt template, JSON-парсинг, форматирование — это другая ось ответственности, не «ещё один SQL aggregate».
- Отсутствие явной границы будущих skill'ов приводит к тому, что BudgetSkill становится мегафайлом, а тестируемость падает.

Нужно зафиксировать границу: что идёт **внутрь** BudgetSkill, что — **наружу как отдельный plugin**.

---

## Decision

LLM-reasoning skills реализуются как **отдельные plugin'ы** в `internal/skills/`, потребляющие BudgetSkill через `budget.Store` и существующие методы.

### Граница

| Профиль | Где живёт |
|---------|-----------|
| Пишет в БД (CRUD над budget-сущностью) | Action внутри `BudgetSkill` |
| Агрегирует одну сущность через SQL | Action внутри `BudgetSkill` (как forecast в ADR-001) |
| Читает 2+ источника + использует LLM для рекомендации | Отдельный plugin (`AdvisorSkill`, `DebtManagerSkill`, ...) |

### Контракт plugin'а

- Файл: `internal/skills/<name>_skill.go` (один skill — один файл).
- Struct: `<Name>Skill` с полями `store *budget.Store`, `llm llm.Client`, `logger *slog.Logger` (зависимости через конструктор).
- Методы: `Manifest() plugin.Manifest`, `Run(ctx, payload) (string, error)`.
- Регистрация: в `cmd/app/main.go` через `plugin.Registry.Register`.
- **Состояние не хранится** — никаких cache-полей в struct, каждый Run независим (соответствует stateless agent).
- **Не вызывает другие skill'ы** — только Store и LLM. Никакого agent loop изнутри plugin'а.
- **Manifest содержит явный negative-prompt**, отделяющий skill от смежных (например advisor от `budget.summary`/`budget.forecast`).

### Переиспользование

Существующие aggregator-методы в `Store` (например `GetForecastData`) — точка переиспользования. Если LLM-reasoning skill нуждается в новом срезе, сначала добавляем метод в `Store`, потом потребляем из skill'а. Это сохраняет SQL в одном пакете и облегчает тестирование данных отдельно от LLM-логики.

### Когда пересмотреть

Если у LLM-reasoning skill'ов появятся общие зависимости (например shared prompt templates, общий JSON-схема валидатор) — рассмотреть выделение в `internal/skills/llm/` подпакет с общей инфраструктурой. Преждевременно не делать.

---

## Consequences

**Плюсы:**
- BudgetSkill не растёт от добавления каждой советующей фичи.
- Тесты LLM-reasoning skill'а изолированы от CRUD BudgetSkill.
- Манифест каждого LLM-skill настраивается независимо — лучшее routing-разрешение.
- Декомпозиция предсказуема для будущих фич (debt manager, planner).

**Минусы:**
- Чуть больше boilerplate (Manifest/Run на каждом skill'е).
- Risk дублирования: если два skill'а нуждаются в одном snapshot — нужно вытаскивать в Store, иначе будет копипаста.
- Manifest description'ы могут конкурировать за routing — требуется явный negative-prompt и smoke-тест.

**Действия:**
- AdvisorSkill (spec/advisor) — первый skill по этому контракту. Реализуется отдельным plugin'ом.
- ADR-001 не меняется: forecast остаётся внутри BudgetSkill (он соответствует профилю «агрегирует одну сущность»).
- Будущие LLM-reasoning skill'ы (DebtManagerSkill #44 и др.) следуют этому ADR.

---

## Links

- ADR-001 — Forecast Architecture (предшествующий прецедент)
- spec/advisor/ — первая реализация по этому контракту
- Issues: #43 (advisor), #44 (debt manager)
