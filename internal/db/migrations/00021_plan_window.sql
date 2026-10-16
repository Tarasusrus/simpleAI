-- +goose Up
-- +goose StatementBegin
-- План окна (эпик simpleAI-766e, ADR-009): расширяем схему, ничего не ломая.
-- Раскатка «расширить → использовать»: все колонки добавляются с безопасными
-- значениями по умолчанию, старые читатели бота продолжают работать (им
-- добавлены фильтры в том же деплое).
--
-- Повторный прогон безопасен: IF NOT EXISTS / DROP CONSTRAINT IF EXISTS.
SET LOCAL lock_timeout = '5s';

-- Плановые платежи: направление, необязательная сумма («Права»: сумма неизвестна),
-- категория и выбранное окно для платежа без даты.
ALTER TABLE budget_planned_expense
    ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'expense';
ALTER TABLE budget_planned_expense
    ADD COLUMN IF NOT EXISTS category_id UUID REFERENCES budget_category(id) ON DELETE SET NULL;
ALTER TABLE budget_planned_expense
    ADD COLUMN IF NOT EXISTS window_start DATE;
ALTER TABLE budget_planned_expense
    ALTER COLUMN amount DROP NOT NULL;

ALTER TABLE budget_planned_expense
    DROP CONSTRAINT IF EXISTS budget_planned_expense_direction_check;
ALTER TABLE budget_planned_expense
    ADD CONSTRAINT budget_planned_expense_direction_check
    CHECK (direction IN ('expense', 'income'));

-- Сумма, если задана, положительная (раньше её не проверял никто).
ALTER TABLE budget_planned_expense
    DROP CONSTRAINT IF EXISTS budget_planned_expense_amount_positive;
ALTER TABLE budget_planned_expense
    ADD CONSTRAINT budget_planned_expense_amount_positive
    CHECK (amount IS NULL OR amount > 0);

-- Дата и выбранное окно взаимоисключают друг друга: установка даты обнуляет
-- window_start и наоборот (это делает писатель, CHECK страхует).
ALTER TABLE budget_planned_expense
    DROP CONSTRAINT IF EXISTS budget_planned_expense_due_or_window;
ALTER TABLE budget_planned_expense
    ADD CONSTRAINT budget_planned_expense_due_or_window
    CHECK (due_date IS NULL OR window_start IS NULL);

-- Повторяющиеся правила: шаг «каждые N единиц» от якоря, конец, пропущенные сроки.
-- Старые recurrence_type / day_of_month / next_date остаются заполненными: ими
-- пользуются бот и воркер до перехода на plan.Next.
ALTER TABLE budget_recurring
    ADD COLUMN IF NOT EXISTS interval_n INT NOT NULL DEFAULT 1;
ALTER TABLE budget_recurring
    ADD COLUMN IF NOT EXISTS interval_unit TEXT;
ALTER TABLE budget_recurring
    ADD COLUMN IF NOT EXISTS until DATE;
ALTER TABLE budget_recurring
    ADD COLUMN IF NOT EXISTS anchor_date DATE;
ALTER TABLE budget_recurring
    ADD COLUMN IF NOT EXISTS skipped_dates DATE[] NOT NULL DEFAULT '{}';

-- Бэкфилл. weekly/daily: шаг 1, якорь = next_date. monthly: якорь = next_date, а
-- если задан day_of_month — тот день этого месяца, прижатый к его концу (день
-- 31 в феврале даёт 28/29; сам день остаётся в day_of_month, и plan.Next берёт
-- его оттуда). Строки, уже заполненные новым кодом, не трогаем.
UPDATE budget_recurring
SET interval_unit = CASE recurrence_type
        WHEN 'weekly' THEN 'week'
        WHEN 'daily'  THEN 'day'
        ELSE 'month'
    END,
    anchor_date = CASE
        WHEN recurrence_type = 'monthly' AND day_of_month IS NOT NULL THEN
            make_date(
                EXTRACT(YEAR FROM next_date)::int,
                EXTRACT(MONTH FROM next_date)::int,
                LEAST(
                    day_of_month,
                    EXTRACT(DAY FROM (date_trunc('month', next_date) + interval '1 month - 1 day'))::int
                )
            )
        ELSE next_date
    END
WHERE interval_unit IS NULL OR anchor_date IS NULL;

ALTER TABLE budget_recurring
    DROP CONSTRAINT IF EXISTS budget_recurring_interval_n_check;
ALTER TABLE budget_recurring
    ADD CONSTRAINT budget_recurring_interval_n_check CHECK (interval_n >= 1);

ALTER TABLE budget_recurring
    DROP CONSTRAINT IF EXISTS budget_recurring_interval_unit_check;
ALTER TABLE budget_recurring
    ADD CONSTRAINT budget_recurring_interval_unit_check
    CHECK (interval_unit IS NULL OR interval_unit IN ('day', 'week', 'month', 'year'));

-- Транзакции: кто добавил и к какому сроку правила относится факт. Пара
-- (recurring_id, scheduled_for) делает исполнение срока идемпотентным: воркер
-- вставляет с ON CONFLICT DO NOTHING. Старые строки (NULL) индексу не мешают.
ALTER TABLE budget_transaction
    ADD COLUMN IF NOT EXISTS added_by TEXT;
ALTER TABLE budget_transaction
    ADD COLUMN IF NOT EXISTS scheduled_for DATE;

CREATE UNIQUE INDEX IF NOT EXISTS uq_budget_transaction_recurring_term
    ON budget_transaction (recurring_id, scheduled_for)
    WHERE scheduled_for IS NOT NULL;

-- Настройки плана: одна строка (процент резерва от поступлений окна).
CREATE TABLE IF NOT EXISTS plan_settings (
    id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    reserve_pct INT NOT NULL DEFAULT 0 CHECK (reserve_pct BETWEEN 0 AND 100),
    updated_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);
INSERT INTO plan_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Откат отказывается, если после миграции появились данные, которые старая
-- схема не умеет хранить: иначе DROP/SET NOT NULL молча уничтожил бы или
-- сломал их. Сначала такие строки нужно убрать вручную.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM budget_planned_expense WHERE amount IS NULL) THEN
        RAISE EXCEPTION 'откат 00021 невозможен: есть плановые платежи без суммы (amount IS NULL); удали или заполни их';
    END IF;
    IF EXISTS (SELECT 1 FROM budget_planned_expense WHERE direction = 'income') THEN
        RAISE EXCEPTION 'откат 00021 невозможен: есть плановые поступления (direction = income); удали их';
    END IF;
END
$$;

DROP TABLE IF EXISTS plan_settings;

DROP INDEX IF EXISTS uq_budget_transaction_recurring_term;
ALTER TABLE budget_transaction DROP COLUMN IF EXISTS scheduled_for;
ALTER TABLE budget_transaction DROP COLUMN IF EXISTS added_by;

ALTER TABLE budget_recurring DROP CONSTRAINT IF EXISTS budget_recurring_interval_unit_check;
ALTER TABLE budget_recurring DROP CONSTRAINT IF EXISTS budget_recurring_interval_n_check;
ALTER TABLE budget_recurring DROP COLUMN IF EXISTS skipped_dates;
ALTER TABLE budget_recurring DROP COLUMN IF EXISTS anchor_date;
ALTER TABLE budget_recurring DROP COLUMN IF EXISTS until;
ALTER TABLE budget_recurring DROP COLUMN IF EXISTS interval_unit;
ALTER TABLE budget_recurring DROP COLUMN IF EXISTS interval_n;

ALTER TABLE budget_planned_expense DROP CONSTRAINT IF EXISTS budget_planned_expense_due_or_window;
ALTER TABLE budget_planned_expense DROP CONSTRAINT IF EXISTS budget_planned_expense_amount_positive;
ALTER TABLE budget_planned_expense DROP CONSTRAINT IF EXISTS budget_planned_expense_direction_check;
ALTER TABLE budget_planned_expense ALTER COLUMN amount SET NOT NULL;
ALTER TABLE budget_planned_expense DROP COLUMN IF EXISTS window_start;
ALTER TABLE budget_planned_expense DROP COLUMN IF EXISTS category_id;
ALTER TABLE budget_planned_expense DROP COLUMN IF EXISTS direction;
-- +goose StatementEnd
