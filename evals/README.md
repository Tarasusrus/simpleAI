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

```bash
# полный прогон
go run ./evals/cmd/routing

# срез по тегу
go run ./evals/cmd/routing -tag future_purchase

# smoke (первые N кейсов)
go run ./evals/cmd/routing -limit 5

# diff vs предыдущий прогон
go run ./evals/cmd/routing -prev evals/runs/baseline-2026-05-06.jsonl

# проверить только загрузку без вызова LLM
go run ./evals/cmd/routing -dry-run
```

Output:
- jsonl-файл в `evals/runs/<timestamp>_<promptHash>.jsonl` (одна строка на кейс + summary)
- stdout: total/pass/fail/accuracy + per-skill + per-tag + failed_ids
- exit code: 0 если все pass, 1 если есть fail, 2 при ошибке загрузки/конфига

Конфиг LLM читается из env как и в прод-боте (см. `config/config.go`).

## Baseline

Первый baseline-отчёт фиксируется в `evals/runs/baseline-YYYY-MM-DD.json` после прогона на main. Любой PR с регрессией accuracy относительно baseline блокируется до объяснения.
