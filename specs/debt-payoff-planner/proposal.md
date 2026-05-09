# Proposal: Debt Payoff Planner

## Problem
Существующая работа с долгами в боте знает только тело и фиксированный месячный платёж: нет процента, нет прогноза даты закрытия, нет сравнения «минимальный платёж vs агрессивный», нет связки со свободным cashflow. Пользователь не понимает реальную стоимость долга и не может выбрать комфортную траекторию закрытия.

## Users
| Role | Current pain | Change |
|------|--------------|--------|
| Владелец финансов (single-user telegram-бот) | Видит только сумму долга и платёж; не понимает когда и какой ценой долг закроется; не знает можно ли платить больше из текущего cashflow | Получает план: «при min — N мес, переплата X; при max — M мес, переплата Y», max-предложение опирается на forecast свободных денег; может зафиксировать выбранный план на Debt |

## Solution
Расширить модуль `internal/budget` и budget skill сценариями работы с долгами: учёт месячного % от остатка, расчёт плана min vs max платежа для одного долга, портфельный план (avalanche/snowball) для всех active debts, и команду фиксации выбранного плана на запись Debt. Расчёт max-платежа берёт «свободные деньги» из существующего forecast на текущий месяц. Диалог stateless: пользователь сам передаёт min/max в одном запросе либо просит подсказать; отдельным действием закрепляет план в БД.

## Scope

**In scope (MVP):**
- Новые поля на `Debt`: `interest_rate` (месячный %, decimal), `planned_monthly`, `target_payoff_date`
- Расчёт прогноза закрытия одного долга при заданном monthly_payment с учётом месячного % от остатка (flat-модель)
- Action `debt_plan` (для одного долга или портфеля): сравнение min vs max платежа → месяцы до закрытия, переплата, итог
- Авто-предложение max-платежа из `forecast` текущего месяца (свободные деньги = прогноз дохода − прогноз обязательств)
- Action `debt_fix_plan`: сохраняет выбранный `planned_monthly` и пересчитанный `target_payoff_date` на Debt
- Портфельный режим: avalanche (макс % первым) и snowball (минимальный остаток первым), распределение свободного cashflow между active debts
- Action `debt_progress`: текущий остаток vs план (опережение/отставание по target_payoff_date)
- Все долги в одной валюте с основным cashflow

**Out of scope:**
- Напоминания о платежах (отдельный epic, через cron/notify)
- Авто-создание `transaction` при фиксации платежа по долгу
- Аннуитетная амортизация (точный банковский график тело/% по месяцам) — только flat % от остатка
- Мультивалютные долги с конвертацией по курсу
- Stateful многошаговый диалог («какой min?» → «какой max?» с сохранением сессии) — только stateless single-shot
- Telegram UI поверх actions (кнопки/inline) — пока всё через NL → LLM-роутинг → action

## User Flow
```
Linear (single debt):
  add_debt (existing) — указать name, total, interest_rate, [monthly_payment]
    │
    ▼
  user: «покажи план по карте Тинькофф min 10к max 30к»
    │  LLM-routing → action=debt_plan, debt_name, min, max
    ▼
  bot: «min 10000: закроется через 24 мес, переплата 18.5k
        max 30000: закроется через 7 мес, переплата 4.2k
        свободные в этом месяце по forecast: 35000»
    │
    ▼
  user: «фиксируй максимум»
    │  LLM-routing → action=debt_fix_plan, debt_name, monthly=30000
    ▼
  bot: «план зафиксирован, цель закрыть к 2026-12-XX»

Portfolio:
  user: «как закрывать все долги быстрее всего»
    │  action=debt_plan (без debt_id) → strategy=avalanche
    ▼
  bot: «свободно 35k/мес; стратегия avalanche:
        долг A (24%) — 25k, закроется через 5 мес
        долг B (12%) — 5k минимум, потом перенаправим в B
        итого все закрыты к 2026-XX, переплата 8k»
    │
    ▼
  user: «фиксируй» → debt_fix_plan для каждого долга
```

## Tech Context
- **Stack:** Go 1.24+, single-user telegram-бот, sqlite (`internal/db/migrations`), MCP gateway
- **Key entities:** `internal/budget.Debt` (расширяем), `Transaction`, `Forecast`/`MonthlyCategoryExpense` в `internal/budget/forecast*.go`
- **Existing mechanisms to reuse:**
  - `internal/budget/store.go` — debt CRUD; добавить методы под новые поля
  - `internal/budget/forecast.go` — источник «свободных денег» текущего месяца (доход − известные обязательства)
  - `internal/skills/budget_skill.go` — Manifest LLM-роутинга, добавить actions `debt_plan`, `debt_fix_plan`, `debt_progress` рядом с существующими `add_debt`/`pay_debt`/`debt_status`
  - `internal/db/migrations` — новая миграция: ALTER TABLE debts ADD COLUMN interest_rate, planned_monthly, target_payoff_date
  - Beads epic-родитель: `simpleAI-f94` (Бюджет-трекер: персональный учёт финансов) — новая фича логически принадлежит сюда
- **Interest model:** flat % от текущего остатка раз в месяц. Формула шага: `balance_{n+1} = balance_n * (1 + rate) − payment`. Закрытие при `balance ≤ 0`. Если `payment ≤ balance * rate` — план не сходится, возвращаем ошибку «платёж меньше начислений».

## Open Questions
| # | Question | Answer |
|---|----------|--------|
| 1 | Минимальный платёж по умолчанию если у пользователя нет в голове цифры — брать существующий `MonthlyPayment` Debt или предлагать `balance * rate + 1₽`? | Открыто; решается на этапе constraints |
| 2 | Что делать если `forecast` свободных денег отрицателен (расходы > доходы)? Показать предупреждение и не считать max? | Открыто |
| 3 | Округление переплаты/платежа — до целых единиц валюты или до копеек? | Открыто (по аналогии с существующим budget — посмотреть в `02-architect-constraints`) |
| 4 | Snowball vs avalanche — один из них дефолт или всегда обе альтернативы в `debt_plan`? | Открыто |
| 5 | Что считать «свободным» в forecast: `прогноз_дохода − прогноз_расхода − сумма_min_платежей_по_всем_долгам`? | Открыто; уточнить в constraints |
