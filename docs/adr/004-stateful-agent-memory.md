## ADR-004: Stateful agent memory — short-term + long-term

**Дата:** 2026-05-06
**Статус:** Proposed
**Авторы:** @architect (via stateful epic)
**Связанные ADR:** ADR-002 (LLM-reasoning skills), ADR-003 (Eval suite convention)

---

## Context

Сервис сейчас stateless. Каждый `agent.Service.AskWithMeta` создаёт новый `sessionID := uuid.New()`, `currentPrompt` инициализируется только текущим `input`, история turn'ов в LLM не передаётся. Skill'ы тоже stateless по ADR-002 («Состояние не хранится — никаких cache-полей в struct, каждый Run независим»).

Последствия:
- Пользователь повторяет контекст в каждом сообщении («Я живу в Бангкоке…», «У меня бюджет 100k THB…»).
- Reasoning-фичи (advisor, planner) не помнят предпочтений и предыдущих решений.
- Multi-turn диалоги невозможны: «А если в рассрочку?» после «Купить байк?» — второй вопрос отрабатывается без контекста первого.

Существующая `agent_trace` таблица (`session_id, chat_id, user_input, llm_response, is_final, created_at`) — фактически write-only лог истории. Память можно построить поверх неё для short-term без новой схемы.

ADR-002 фиксирует stateless для skill'ов. Этот ADR расширяет границу: **stateless касается уровня skill, agent service может быть stateful**. Это уточнение, не отмена ADR-002.

---

## Decision

Перевести `agent.Service` в stateful с двумя уровнями памяти:

1. **Short-term (within session):** последние N turn'ов из `agent_trace` за окно времени T для данного `chat_id`. Без новой схемы. Подмешивается в system prompt отдельной секцией.
2. **Long-term (between sessions):** новая таблица `agent_memory` с **явными** фактами от LLM через skill `memory` (`remember/recall/forget`). Без авто-summarizer'а на старте.

### Границы

| Уровень | Хранилище | Запись | Чтение |
|---------|-----------|--------|--------|
| Short-term | `agent_trace` (существует) | Уже пишется trace.Store | `Store.RecentTurns(chatID, since, limit)` |
| Long-term | `agent_memory` (новая) | Skill `memory.remember` | Skill `memory.recall` + автоинъекция top-K в prompt |

Skill'ы остаются stateless. Memory читается/пишется только в `agent.Service` (короткая память) и через специальный `MemorySkill` (долгая). Другие skill'ы (advisor, budget) **не** обращаются к memory напрямую — они получают расширенный prompt от agent.Service.

### Точка инжекции

`agent.Service.AskWithMeta`, до первого `client.AskWithSystem`:

1. Собрать short-term context (RecentTurns за T минут, до N записей, обрезка по M символам).
2. Собрать long-term context (top-K записей с `kind='profile'` всегда, остальные по запросу).
3. Добавить в `toolSystemPrompt` секцию `## Контекст предыдущих сообщений` и `## Что знаю о пользователе`.
4. Передать расширенный prompt в LLM.

Логические сессии (long-term) идентифицируются по `chat_id` + временному окну. Технический `sessionID := uuid.New()` остаётся для observability в Langfuse и для трассировки одного `Ask`.

### Storage

PostgreSQL. Без pgvector — преждевременно для текущего объёма (низкий QPS, малое число пользователей). Пересмотреть, когда long-term превысит ~1k записей на пользователя или появится потребность семантического поиска вместо `kind/key`.

### Schema (long-term)

```sql
CREATE TABLE agent_memory (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id            BIGINT NOT NULL,
    kind               TEXT NOT NULL,        -- 'profile', 'preference', 'goal', 'fact', ...
    key                TEXT NOT NULL,        -- идемпотентный upsert по (chat_id, kind, key)
    content            TEXT NOT NULL,
    source_session_id  UUID,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ,           -- NULL = бессрочно
    UNIQUE (chat_id, kind, key)
);
CREATE INDEX idx_agent_memory_chat_kind ON agent_memory (chat_id, kind);
CREATE INDEX idx_agent_memory_expires   ON agent_memory (expires_at) WHERE expires_at IS NOT NULL;
```

### Privacy

- **Изоляция по `chat_id`** — обязательный test-invariant. Запись пользователя A не должна возвращаться при чтении для B.
- **Команда `/forget`** в Telegram — удаляет всё long-term для `chat_id`, с подтверждением.
- **Retention worker** — фоновый job (как существующие reminder/recurring workers в `cmd/app/main.go`) удаляет записи где `expires_at <= now()`.
- **Только явная запись** на старте: записывается то что LLM явно вызвал через `memory.remember`. Никакого авто-тегирования всех turn'ов — это привело бы к мусору и privacy leak'ам.
- **Чувствительные категории** (advisor суммы, mail-content) фильтруются в Manifest skill `memory` через явный negative-prompt: «не сохраняй платёжные данные, точные суммы, содержимое писем».

### Feature-flag

Конфигурация:
```
agent.memory.short_term.enabled   (bool, default off в проде, on в dev)
agent.memory.short_term.window    (duration, default 30m)
agent.memory.short_term.max_turns (int, default 5)
agent.memory.short_term.max_chars (int, default 4000)
agent.memory.long_term.enabled    (bool, default off в проде)
agent.memory.long_term.top_k      (int, default 5)
```

Раскатка: dev → `on`, прод → `off` на первом релизе, включается после прохождения eval-сьюта и ручной проверки изоляции.

---

## Alternatives Considered

1. **pgvector + RAG-style retrieval** — отложено. Объём не оправдывает сложность.
2. **Авто-summarizer над `agent_trace`** — отложено. Риск мусора и непредсказуемой памяти. Можно добавить позже отдельным ADR.
3. **Memory внутри каждого skill'а** — отвергнуто. Нарушает ADR-002 stateless skills и приводит к дублированию.
4. **Только short-term, без long-term** — отвергнуто. Не решает «помни мой профиль/цели между сессиями».
5. **Только long-term, без short-term** — отвергнуто. Multi-turn внутри сессии — самый частый сценарий.

---

## Consequences

**Плюсы:**
- Multi-turn диалоги работают.
- Reasoning-фичи (advisor) получают профиль пользователя без повторного ввода.
- Skill'ы остаются stateless (ADR-002 сохраняется).
- Использование `agent_trace` для short-term — без новой схемы для уровня 1.

**Минусы / риски:**
- **Token bloat:** prompt растёт на размер истории — нужны жёсткие лимиты (N turns, M chars) и контроль стоимости.
- **Privacy leak across chats** — обязательный тест-инвариант на изоляцию по `chat_id`.
- **Stale state:** долгая память противоречит новым фактам — нужна стратегия overwrite-by-key (`UNIQUE (chat_id, kind, key)`) и `expires_at`.
- **Eval determinism (ADR-003):** stateful агент сложнее тестировать — eval-сьют должен явно сбрасывать память между сценариями.
- **Семантика `agent_trace.session_id`** — техническая (один Ask), пользовательская «сессия» определяется временным окном. Расхождение нужно явно зафиксировать в Langfuse-аналитике.
- **Расширение скоупа `MemorySkill`** — LLM может злоупотреблять `remember`. Mitigation: явный negative-prompt в Manifest, лимит на запись (rate-limit или max-records-per-chat).

**Действия:**
- Создать epic «Stateful agent memory» с 6 задачами (см. декомпозицию).
- Ввести ADR-002 amendment: явный пункт «stateless касается skill Run, agent service может быть stateful».
- Ввести eval-сьют по конвенции ADR-003: short-term recall, long-term recall через сессию, isolation chat A≠B, forget эффективен.

---

## Decomposition (для epic)

1. **Memory module skeleton + short-term reader** — `internal/memory/` пакет, `Store.RecentTurns` поверх `agent_trace`, юнит-тесты.
2. **Inject short-term в agent loop** — `agent.Service.AskWithMeta`, лимиты, feature-flag, eval «помнит факт из предыдущего turn'а».
3. **Long-term storage** — миграция `agent_memory`, `Store.Remember/Recall/Forget`, тест на изоляцию по chat_id.
4. **MemorySkill + long-term injection** — skill `memory` (remember/recall/forget), top-K в prompt, eval «факт переживает новую сессию».
5. **Privacy controls** — `/forget` в Telegram, retention worker, явный negative-prompt против записи sensitive данных.
6. **ADR-002 amendment + eval-сьют** — правка ADR-002, новый `evals/memory/` по конвенции ADR-003.

---

## Links

- ADR-002 — LLM-reasoning skills (требует amendment про stateless boundary)
- ADR-003 — Eval suite convention (memory-сценарии добавляются по той же конвенции)
- `internal/agent/service.go` — точка инжекции
- `internal/trace/store.go` — источник short-term
- Beads epic: создаётся после Accepted этого ADR
