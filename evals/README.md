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

## Eval классификатора категорий

Отдельный набор `evals/classifier_golden_set.jsonl` (36 описаний трат с ожидаемой категорией) и
харнес `evals/cmd/categories`. Прогон идёт на **реальном маршруте `standard`** прокси LiteLLM, не на
моке, через тот же `GuardedClassifier`, что у бота.

```bash
CATEGORY_LLM_BASE_URL=http://127.0.0.1:4444/v1 CATEGORY_LLM_API_KEY=... go run ./evals/cmd/categories
go run ./evals/cmd/categories -pause 4s -min-accuracy 0.8 -max-wrong 0.1 -max-errors 0.3
```

Метрики считаются **среди ответивших**: сбой вызова (лимит провайдера 429, таймаут, обрезанный
ответ) отделён от качества выбора. Исходы: `ok`, `wrong` (другая существующая категория),
`degraded` (none, create, низкая уверенность, ответ нарушил контракт: бот пишет в «Прочее»),
`errored` (вызов не дошёл). Код возврата: 1 — точность ниже порога или много неверных; 3 — сбоев
вызова больше `-max-errors`, прогон недействителен (повторить позже).

**Замер 2026-10-21** (локальный прокси, бесплатный маршрут): среди 22 ответивших верно 21 (95%),
неверно 1 (5%); сбоев вызова 14 из 36 (39%) из-за суточной квоты токенов (429) и таймаутов.
Модель один раз вернула `category_id` не из списка, несмотря на `enum` в схеме: такой ответ отсекает
`GuardedClassifier` (в проде запись уходит в «Прочее»). Вывод: качество выбора приемлемо, а
доступность бесплатного маршрута нет, поэтому деградация (default «Продукты» для «Еды», «Прочее» для
остальных) срабатывает часто; доля сбоев — основной показатель для наблюдения в проде.
