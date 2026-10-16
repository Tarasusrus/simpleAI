-- Ручной откат миграции 00021 (план окна) ПОСЛЕ того, как веб начал писать данные.
-- Down самой миграции в таком случае отказывается (RAISE EXCEPTION), потому что
-- новые данные старая схема хранить не умеет. Этот скрипт запускает человек
-- осознанно, после бэкапа:
--
--   docker exec -i simpleai-postgres psql -U simpleai -d simpleai -v ON_ERROR_STOP=1 < deploy/rollback-00021.sql
--
-- ПОТЕРИ: плановые платежи без суммы и плановые поступления удаляются, пропущенные
-- сроки правил, added_by, scheduled_for и процент резерва пропадают.
BEGIN;
DELETE FROM budget_planned_expense WHERE amount IS NULL OR direction = 'income';
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
DELETE FROM goose_db_version WHERE version_id = 21;
COMMIT;
