-- +goose Up
-- +goose StatementBegin
-- ADR-022: окно само дотягивается до следующего поступления, если его конец не поставил владелец. end_fixed = true —
-- конец поставил владелец (правка конца или длины), окно в зазор не растёт.
--
-- Окна нестандартной длины к этому моменту — правки владельца (перенесены из plan_window_length или сделаны после
-- ADR-021): их конец считаем поставленным владельцем. Окна, сохранённые приложением, — 14 дней.
SET LOCAL lock_timeout = '5s';

ALTER TABLE plan_window ADD COLUMN IF NOT EXISTS end_fixed BOOLEAN NOT NULL DEFAULT false;
UPDATE plan_window SET end_fixed = true WHERE end_date - start_date + 1 <> 14 AND NOT end_fixed;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Откат: окна снова не растут в зазоры; даты окон не меняются.
ALTER TABLE plan_window DROP COLUMN IF EXISTS end_fixed;
-- +goose StatementEnd
