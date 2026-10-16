# Контракт экрана плана окна (v1)

`GET /api/plan/window?cur=RUB|THB`, заголовок `Authorization: Bearer <токен>`. Деньги — целые единицы валюты показа (рубли или баты), даты — `YYYY-MM-DD`. Сервер ничего не форматирует. Тест структуры: `internal/web/server_test.go` (`TestWindow_ContractShape`).

Ответ `WindowView`:
- `v` — версия контракта (1);
- `meta` — `today`, `cur`, `ratesManual`, `hasRate`, `skipped` (записей, не вошедших в план);
- `state` — `current | upcoming | between | ended | none`;
- `window` — `{start, end, daysLeft, anchorPlanned}` или `null` при `none`;
- `kpi` — `canSpend` (знаковое), `perDay` (`null` при недостаче или 0 днях), `payments`, `spent`, `reserve`, `received`, `expectedIncome`, `unpriced`, `undatedCount`, `undatedSum`;
- `timeline[]` — полная лента окна (режет фронт);
- `next` — `{count, sum, items[]}` или `null`;
- `later[]` — по месяцам `{year, month, count, sum, items[]}`;
- `inbox[]` — платежи без даты (в расчёт не входят);
- `categories[]` — `{name, last, now, recent[]}`.

Строка (`row`): `key, date|null, name, kind (income|payment), amount|null, overdueDays, expected, notArrived, noAmount, windowMissing, windowPassed`.

Массивы всегда массивы (пустые — `[]`, не `null`). Ошибки: `{"error": "текст по-русски"}`; 400 — понятная причина (нет курса, неверная валюта), 401 — нет токена, 429 — много неверных попыток, 500 — без деталей.

Запись (POST/PUT/DELETE) в версию 1 не входит.
