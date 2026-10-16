# Constraints: План окна (v8.3)

MUST / SHOULD / MUST NOT, каждый пункт проверяем. Предыдущие версии: *-v1, *-v8.1. Решения: docs/adr/009-plan-web-ui.md. Принципы: SOLID, DRY, KISS, YAGNI.

## 0. Переиспользуем (DRY)
`budget.Store`, `budget_recurring` (type expense/income), `budget_planned_expense`, `GetExchangeRates`/`SetManualRate` (курс — один источник), `GetExecutedRecurringIDs`, `safetospend.FmtAmount/FmtRate`, `internal/rates`. Конверты ADR-007/008 в экране не используются.

## 1. Слои (SOLID)
Зависимости вниз: `web` и `telegram` → `plan.Service` → `plan` (домен) ← `planrepo` (адаптер к `budget.Store`).
- C1. `internal/plan` — чистый домен: MUST NOT импортировать pgx, net/http, telegram, `budget`, форматирование строк, `time.Now`. Время приходит параметром. Дата — тип `Date` (год-месяц-день без времени), не `time.Time`.
- C2. Порты `Reader` и `Writer` раздельные (ISP), объявлены в домене; реализует `internal/planrepo`.
- C3. `plan.Service` — единственная точка входа для веба и Telegram; зависимости (`Reader`, `Writer`, часы `func() time.Time`, зона) приходят через конструктор. MUST NOT: `var nowFn = time.Now` на уровне пакета, замена значений по умолчанию в сигнатурах для тестов.
- C4. `internal/web` — тонкий BFF: проверить токен → разобрать запрос → Service → JSON. MUST NOT считать деньги, ветвиться по бизнес-правилам, форматировать суммы.
- C5. Telegram — тонкий навык, вызывающий `plan.Service`; MUST NOT считать «можно тратить» сам.
- C6. Тест архитектуры: `plan` (не-тестовый код) импортирует только `errors, fmt (только ошибки), sort, slices, strings, time, math, math/bits`; AST-тест запрещает селектор `time.Now`; `time.Time` допустим только в `DateOf`; циклов `notify → plan` нет. Порты `Reader`/`Writer` создаются в задачах 2.2/2.3 (в фазе 1 их нет — YAGNI).

## 2. Время и деньги в домене
- C7. Зона `PLAN_TZ` (по умолчанию `Asia/Bangkok`). «Сегодня» = дата без времени в этой зоне; воркер, экран и бот берут её из одной функции. Воркер переходит с UTC на `PLAN_TZ` ОСОЗНАННО (изменение поведения, фиксируется в task-2.4).
- C7a. Навыки бота (`skills/budget/transactions.go`, `recurring.go`, `safetospend/remaining.go`) сегодня берут дату через `time.Now()` в UTC. Одна функция `clock.Today(PLAN_TZ)` MUST использоваться ими и воркером (задача 2.6); до тех пор расхождение дат бота и экрана ночью по Бангкоку — известный риск.
- C8. Деньги в домене — целые минимальные единицы (`int64`: сатанги/копейки). `float64` в `plan` запрещён. Курс применяется к каждому событию один раз; округление half-away-from-zero до целой единицы показа; итог = сумма уже округлённых слагаемых.
- C9. Валюты THB и RUB; прочие → ошибка 400. Курс ≤ 0 или отсутствует при THB-событиях → ошибка, не деление на ноль.
- C10. Не допускается `NaN`/`Inf`: `perDay` при `daysLeft=0` или `canSpend<0` — null.

## 3. Повторы (один движок)
- C11. `plan.Next(rule, after)` (строго после) и `plan.Occurrences(rule, from, to)` (обе границы включительно) используют ОДИН внутренний шаг; якорь — поле `Rule.Anchor` (отдельного аргумента нет). n-й срок = `Anchor + n·N` с клампингом на последний день короткого месяца. N ≥ 1; потолок 1 000 событий (`ErrTooMany`); `until` включительно. Сигнатуры — domain-types.md.
- C12. Воркер `internal/notify/recurring_worker.go` MUST перейти на `plan.Next` в режиме shadow (расхождение со старой `nextOccurrence` логируется Warn, наблюдение ≥ суток с одним срабатыванием); `nextOccurrence` удаляется ОТДЕЛЬНЫМ PR после этого. Тест эквивалентности живёт в `internal/notify` (там разрешён импорт `plan`) строит `Rule{Anchor: from, N: 1, Unit, DayOfMonth: r.DayOfMonth}` и сравнивает `nextOccurrence(r, from)` с `plan.Next(rule, from)` только когда `from.Day == *DayOfMonth` (или `DayOfMonth=nil` и `from.Day ≤ 28`), а также для weekly и daily. Для `DayOfMonth ≠ from.Day` и для дня якоря 29–31 расхождение — осознанное изменение (старая берёт следующий месяц цепочкой от «сегодня» воркера и дрейфует; новая даёт ближайший срок после `from` от якоря). В `plan` характеризационных тестов старой функции нет, только таблицы AC-2.3–2.5.
- C13. Идемпотентность: колонка `budget_transaction.scheduled_for date` + частичный уникальный индекс `(recurring_id, scheduled_for) WHERE scheduled_for IS NOT NULL` (старые строки с `NULL` не мешают; бэкфилл `scheduled_for` из `transaction_date` НЕ делается). Вставка воркера — `INSERT … ON CONFLICT (recurring_id, scheduled_for) WHERE scheduled_for IS NOT NULL DO NOTHING` (предикат совпадает с индексом; сигнатуры `CreateRecurringTransaction` и интерфейса `RecurringStore` принимают `scheduled_for`). Срок из `skipped_dates` факт не создаёт, но `next_date` двигается. Догон: сроки не раньше даты создания правила, не более 5 транзакций за тик на правило, одно сводное сообщение, срок старше 62 дней факт не создаёт (Warn + сообщение). Для сопоставления СТАРЫХ фактов с сроком (AC-2.7) используется пара `(recurring_id, transaction_date)`; после `DELETE` правила (`recurring_id` → NULL) факт остаётся, но сопоставление теряется — осознанно.
- C14. Повторяющееся **поступление** воркер НЕ исполняет: `GetDueRecurring` получает `AND type='expense'` В ТОМ ЖЕ деплое, что миграция 00021 (иначе старый воркер исполнит бот-созданное поступление). Утверждение «в проде повторяющихся поступлений нет» MUST быть подтверждено запросом к проду и вывод записан в bd notes task-2.0 (в спеке не принимается на веру). Факт создаёт «пришло» с `scheduled_for`. «Пропустить этот раз» — общее действие: срок добавляется в исключения, `Anchor` не двигается.
- C15. `budget/store.go AddRecurring` (строка 616) MUST писать обе группы колонок (старые `recurrence_type`/`day_of_month`/`next_date` и новые `interval_*`/`anchor_date`), иначе новые правила бота молча выпадут. Тест: правило, созданное ботом после миграции, исполняется ровно один раз. Владелец нового правила, созданного с экрана, — первый чат из `PLAN_CHAT_IDS`.

## 4. Источники и область
- C16. Транзакции общие (нет `chat_id`, как в боте). Chat-таблицы фильтруются по `PLAN_CHAT_IDS` (список, по умолчанию владелец); `Reader`/`Writer` принимают `Scope` параметром.
- C17. Факт-поступление/расход — `budget_transaction`; ожидаемое — `budget_recurring` и `budget_planned_expense`. Событие правила за срок с транзакцией показывается только фактом; `settled=true` не показывается как план.

## 5. Данные и миграции (задачи 2.0, 2.1a, 2.1b)
- C18. Миграция 00021 (номер подтвердить после слияния PR #63): `SET LOCAL lock_timeout='5s'`; все `ADD COLUMN IF NOT EXISTS`, `DROP CONSTRAINT IF EXISTS` (повторный прогон безопасен).
  - `budget_planned_expense`: `direction` DEFAULT 'expense', nullable `amount`, `category_id`, `window_start date`; `settled` остаётся источником правды. Взаимоисключение `due_date` и `window_start` (CHECK: не оба): установка даты обнуляет `window_start` и наоборот.
  - `budget_recurring`: `interval_n`, `interval_unit`, `until`, **`anchor_date`**, `skipped_dates date[]` (исключения). Бэкфилл: monthly → (1 мес; `anchor_date` = `next_date` с днём `day_of_month`, если задан); weekly → (1 нед; якорь = `next_date`); daily → (1 дн; якорь = `next_date`); старые колонки остаются заполненными.
  - `budget_transaction`: `added_by`, `scheduled_for` + индекс (C13).
  - Настройки плана: процент резерва в таблице настроек на область (одна строка).
- C19. Читатели бота MUST фильтровать: `PlannedExpensesTHB`, `ListPlannedExpenses` → `direction='expense' AND amount IS NOT NULL`. В task-2.0 MUST быть выполнен поиск всех SQL по `budget_planned_expense`, результат записан.
- C20. Down: `RAISE EXCEPTION`, если есть строки с `amount IS NULL` или `direction='income'`; иначе откат колонок (без `DELETE`). Тесты обоих сценариев.
- C21. Раскатка «расширить → использовать»: деплой 1 = миграция + фильтры (откат бинаря безопасен, пока веб ничего не пишет); после включения записи с веба — только roll-forward (`deploy/rollback-00021.sql` для ручного отката). Воркер (task-2.4a) — отдельным деплоем.
- C22. DoD task-2.0 и 2.1a: свежий бэкап через `workflow_dispatch` и ОДНО проверенное восстановление в пустую БД (результат в bd notes); проверена строка `goose_db_version version_id=1` на проде (из-за шага `DROP SCHEMA` в deploy.yml); прогон миграции на копии боевой структуры; golden-снимок ответов бота снят ДО миграции.
- C23. Окна и KPI вычисляются, не хранятся. Тестовые поступления (10 ₽) удаляются чисткой данных ДО включения веба (порогов нет).

## 6. Контракт и BFF
- C24. `GET /api/plan/window?cur=RUB` → `WindowView`: `v`, `meta{computedAt, ratesDate, ratesManual, anchorId}`, `window` (null допустим), `kpi{canSpend, perDay, payments, spent, reserve, expectedIncome, unpriced, undatedCount, undatedSum}`, `timeline[]`, `next{count,sum,items}`, `later[]` по месяцам, `inbox[]`, `categories[]`. Деньги уже в запрошенной валюте, округлены. Листания окон (`offset`) нет.
- C25. Команды: POST/PUT/DELETE `/api/plan/income`, `/payment`, `/payment/{id}/paid`, `/skip`, `/arrived`, `/reserve`, `/rate`; ответ — обновлённый `WindowView`. Каждая команда — одна транзакция БД.
- C26. Ошибки: 400 с русским текстом (причина), 404, 415, 429, 500 без деталей наружу.
- C27. Контракт `specs/income-plan-sheet/contract.md`; тест — golden на СТРУКТУРУ (ключи и типы) + отдельные тесты значений по блокам.
- C28. Самопроверка в `Service`: нарушение инварианта `canSpend = income − payments − spent − reserve` или NaN/Inf → лог Error + 500 с русским текстом.

## 7. Веб-слой (эксплуатация, безопасность)
- C29. Флаги: `PLAN_WEB_ENABLED` (по умолчанию false), `PLAN_WEB_ADDR` (по умолчанию `127.0.0.1:8090`; не loopback — отказ запуска без `PLAN_WEB_ALLOW_NONLOOPBACK=true`). Режима READONLY нет (YAGNI): первые дни сверка вручную с ботом.
- C30. Токен: `Authorization: Bearer`, ссылка владельцу `http://127.0.0.1:<порт>/#t=<token>` (фрагмент не уходит на сервер и в логи; JS кладёт в `sessionStorage` и стирает `history.replaceState`). Токен: ≥ 43 символов base64url (32 байта энтропии; hex только от 64 символов) из `PLAN_WEB_TOKEN`; пустой/короткий/недопустимый → веб НЕ запускается (fail-closed), Warn без значения. Заголовок `Authorization` требуется только на `/api/*`; статика и `/healthz` публичны и данных не содержат. `sessionStorage` не переживает новую вкладку — ссылка с `#t=` открывается заново (runbook). Сравнение: sha256 обеих сторон + `ConstantTimeCompare`.
- C31. Rate limit: считает ТОЛЬКО неверные попытки, ключ глобальный (токен-бакет ~10/мин), верный токен лимитом не блокируется; `X-Forwarded-For` не используется.
- C32. Заголовки: `Referrer-Policy: no-referrer`, `Cache-Control: no-store`, `X-Robots-Tag: noindex`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'; base-uri 'none'`. POST только `application/json` (иначе 415), CORS не отдаётся, `Host` сравнивается по имени без порта (список: `127.0.0.1`, `localhost`, имя Tailscale).
- C33. Сервер: `http.Server{ReadHeaderTimeout, ReadTimeout, WriteTimeout, IdleTimeout}`, `MaxBytesReader` 64 КБ, `Shutdown(5s)`, семафор параллельных запросов 4, таймаут контекста запроса, `recover`-middleware без URL в логе. Запуск в своей горутине, ошибки только в `logger.Error` (паттерн `runMCP`), падение веба не убивает бота.
- C34. Логи: одна строка на запрос (метод, шаблон маршрута, статус, мс), никогда `r.URL`/query; `Server.ErrorLog` через тот же редактор; в `tools.NewLogger` добавить `ReplaceAttr`, маскирующий `PLAN_WEB_TOKEN` и `TELEGRAM_BOT_TOKEN` в строках и ошибках (закрывает и существующую утечку Telegram).
- C35. Деплой: `PLAN_WEB_TOKEN`, `PLAN_WEB_ENABLED`, `PLAN_CHAT_IDS`, `PLAN_TZ` и т. д. добавить в `.github/workflows/deploy.yml` в ТРИ места (`env:`, `envs:`, блок `echo` в `.env`), секреты — `gh secret set`; `workflow_dispatch:` для деплоя; после restart `sleep 10`, `is-active`, `journalctl -n 20`, `curl 127.0.0.1:<порт>/healthz`; при неуспехе — шаг падает и шлёт уведомление.
- C36. Runbook в CONTEXT.md: ротация токена (`gh secret set` → `gh workflow run deploy` → проверка старого и нового); честно: ротация перезапускает бота на несколько секунд; аварийный выключатель `PLAN_WEB_ENABLED=false` + restart. Tailscale — только `serve`, `funnel` запрещён (фаза 5).
- C37. Запись с экрана: лог `added_by=web`, тип команды, id (лимита 100/час нет — YAGNI).

## 8. Фронт
- C38. Статика в бинаре (`embed`), ES-модули, один файл на блок, реестр блоков; без сборщика и внешних CDN; ни inline-скриптов, ни `style=""`/`<style>` в разметке (CSS в отдельном файле, CSP без `unsafe-inline`; проверка в тесте).
- C39. Модуль блока — чистая функция `render(часть модели) → узлы`; тексты через `textContent`/один `esc()`; тест `<img onerror>` в названии для каждого блока.
- C40. Деньги форматируются одним форматтером по готовым числам. Макет — эталон вёрстки, не логики (см. requirements, шапка).

## 9. Тесты
- C41. Домен: табличные и property-тесты без базы (окна, повторы, деньги, границы парами, перестановка входа). Property-инструмент — `pgregory.net/rapid` (прямая зависимость в go.mod). Golden рубли и баты (AC-4.1, 4.1b, 4.1c, 4.2).
- C42. DB-тесты (AC-2.6, 6.4, 8.5, 8.9) идут на отдельной базе с `-race`; пропущенный тест не считается зелёным в CI.
- C43. Каждый новый тест проходит мутацию: сломать логику — тест краснеет; проверять аргумент вызова (спай), а не «не упало».
- C44. Поведение бота до/после (`/envelope`, «остаток») — golden-снимок до миграции.

## 10. Стиль
- C45. `slog`, ошибки `%w`, русские комментарии «зачем», `gofmt`, линтер чистый. MUST NOT: глобальное состояние, `init()` с побочными эффектами, дублирование формул, LLM в расчёте; MUST NOT менять модели LLM, таймауты, секреты (CLAUDE.md проекта).

## 11. Решения и компромиссы
D1 Окно вычисляется, не хранится. D2 Чистый домен + порты. D3 Фронт без сборщика, ES-модули + embed. D4 Одно «событие денег» вместо трёх сущностей. D5 Расширяем существующие таблицы (DRY), риск снят фильтрами и тестом. D6 Нет листания окон и ориентиров. D7 Остаток не переносится (A1). D8 Токен в заголовке, ссылка с фрагментом: защита от утечки в логи и Referer. D9 Факт повтора создаёт воркер (расходы) / владелец (поступления), «пропустить» вместо «привязать к существующей». D10 YAGNI: нет поиска, колонки «остаётся», порога `PLAN_MIN_INCOME`, долгов, оплаты частями.
