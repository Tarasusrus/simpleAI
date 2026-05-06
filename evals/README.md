# Evals — golden-set + harness

Регрессионная защита для routing- и advisor-поведения. Без evals рефакторинг тихо ломает LLM-логику: компиляция и юнит-тесты зелёные, а accuracy на реальных кейсах падает.

## Структура

```
evals/
├── README.md           # этот файл
├── golden_set.jsonl    # эталонные кейсы (один JSON-объект на строку)
└── runs/               # отчёты прогонов (накопительно, gitignored кроме .gitkeep)
```

## Формат кейса

См. ADR-003 (`docs/adr/003-eval-suite-convention.md`) — авторитетный источник схемы.

Минимум:
```jsonl
{"id":"r001","input":"хочу купить блендер","expected":{"skill":"advisor","action":"advice"},"tags":["future_purchase","no_amount"]}
```

## Как добавить кейс

1. Подобрать вход — реальная фраза пользователя, не синтетика.
2. Зафиксировать ожидание (`expected.skill` + `expected.action`).
3. Добавить теги для срезов (past_tense, no_amount, low_data, ...).
4. `id` — короткий, уникальный в пределах файла. Префикс по типу: `r###` routing, `a###` advisor.
5. Append-only — старые кейсы не удалять, помечать `"deprecated":true` если перестал быть валидным.

## Как прогнать

Harness ещё не реализован — см. simpleAI-85c. После реализации:
```
go run ./cmd/eval -set evals/golden_set.jsonl -out evals/runs/<timestamp>.json
```

## Baseline

Первый baseline-отчёт фиксируется в `evals/runs/baseline-YYYY-MM-DD.json` после прогона на main. Любой PR с регрессией accuracy относительно baseline блокируется до объяснения.
