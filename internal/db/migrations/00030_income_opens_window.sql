-- +goose Up
-- +goose StatementBegin
-- ADR-019 (simpleAI-49aa): признак «приход открывает окно» (зарплата, аванс = true; обычный приход = false).
--
-- Раскатка «расширить → использовать»: колонка везде NOT NULL DEFAULT true, поэтому все старые строки и все
-- нынешние писатели (бот, воркер повторов) продолжают работать как раньше: каждый приход открывает окно.
-- Колонка есть и у расходов (значение там не читается): так одна схема на все три таблицы, без частичных индексов
-- и CHECK по направлению.
--
-- Аддитивная и повторяемая: IF NOT EXISTS.
SET LOCAL lock_timeout = '5s';

ALTER TABLE budget_transaction      ADD COLUMN IF NOT EXISTS opens_window BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE budget_planned_expense  ADD COLUMN IF NOT EXISTS opens_window BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE budget_recurring        ADD COLUMN IF NOT EXISTS opens_window BOOLEAN NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Откат теряет флаги: после него каждый приход снова открывает окно. Сами суммы и даты не затрагиваются.
ALTER TABLE budget_recurring        DROP COLUMN IF EXISTS opens_window;
ALTER TABLE budget_planned_expense  DROP COLUMN IF EXISTS opens_window;
ALTER TABLE budget_transaction      DROP COLUMN IF EXISTS opens_window;
-- +goose StatementEnd
