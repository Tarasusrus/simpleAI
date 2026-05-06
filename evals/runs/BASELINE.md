# Baseline — routing eval

**Дата:** 2026-05-06
**Коммит:** ca3cc79
**Файл прогона:** `evals/runs/baseline-2026-05-06.jsonl`
**Prompt hash:** `a1079d769fd7…`
**Кейсов в golden_set:** 24

## LLM окружение

Прогон выполнен на **Ollama (qwen)** — fallback. `DEEPSEEK_API_KEY` и `GEMINI_API_KEY` не сконфигурированы в локальном `.env`.
Cloud-провайдеры в проде дадут другие цифры. Перебить baseline после первого прогона на cloud-LLM в CI.

## Метрики

| Метрика | Значение |
|---|---|
| Total | 24 |
| Pass | 20 |
| Fail | 4 |
| **Accuracy** | **0.83** |
| advisor accuracy | 0.83 |
| budget accuracy | 0.83 |

### Per-tag (выборочно)

| Тег | Accuracy |
|---|---|
| past_tense | 1.00 |
| summary | 1.00 |
| edge / mixed_intent / no_verb | 1.00 |
| no_amount | 1.00 |
| en | 1.00 |
| with_amount | 0.86 |
| future_purchase | 0.89 |
| ru | 0.82 |
| list_transactions | 0.67 |
| forecast | 0.50 |
| advice / saving_plan | 0.00 |

## Failed cases

| ID | Input | Expected | Actual | Класс |
|---|---|---|---|---|
| r002 | `планирую купить машину за 500000` | advisor/advice | `/` (parse fail) | LLM output garbage: корректный JSON + token salad `}leanor` |
| r016 | `сколько у меня останется к концу месяца` | budget/forecast | `/` (no tool call) | LLM ушёл в свободный текст с уточняющими вопросами |
| r017 | `покажи траты за неделю` | budget/list_transactions | budget/summary | unexpected fail — реальная routing-неточность |
| r023 | `сколько откладывать на машину` | advisor/advice | `/` (no tool call) | LLM ушёл в clarifying questions |

### Классификация

- **Expected fail (LLM-quality, не routing):** r002, r016, r023 — qwen на Ollama не всегда отдаёт tool call, иногда портит JSON. Ожидаемо для local-fallback модели.
- **Unexpected fail (routing weakness):** r017 — `list_transactions` vs `summary` на «покажи траты за неделю». Manifest не разделяет «показать список» vs «итог».

## Действия

- accuracy ≥ 80% — follow-up bug **не открывается** (DoD пройден).
- r001 (`хочу купить блендер`) **PASS** — регрессия simpleAI-q49 не воспроизводится.
- r017 — открыт **simpleAI-399** (P3) на уточнение manifest'а budget (разделить summary vs list_transactions триггеры). Не блокер.

## Как воспроизвести

```
go run ./evals/cmd/routing -input evals/golden_set.jsonl -out evals/runs
```

## Как сравнить с baseline

```
go run ./evals/cmd/routing -prev evals/runs/baseline-2026-05-06.jsonl
```

Регрессия (был pass → стал fail) — блокер мержа. Объяснение в PR description обязательно.
