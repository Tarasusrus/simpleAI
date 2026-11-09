-- +goose Up
-- +goose StatementBegin
-- Связь факта с плановой записью (задача simpleAI-rqrk, ADR-009): «оплачено» кладёт
-- транзакцию и закрывает плановую запись, но раньше не помнило, какую именно. Без
-- этой ссылки «вернуть в план» не знает, какую запись снова открыть. Старые факты
-- остаются с NULL: развязать их по описанию и сумме нельзя, можно открыть не ту.
--
-- Аддитивная и повторяемая: IF NOT EXISTS. Удаление плановой записи не удаляет факт,
-- а только обнуляет ссылку (ON DELETE SET NULL).
SET LOCAL lock_timeout = '5s';

ALTER TABLE budget_transaction
    ADD COLUMN IF NOT EXISTS planned_id UUID REFERENCES budget_planned_expense(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_budget_transaction_planned
    ON budget_transaction (planned_id)
    WHERE planned_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Откат безопасен: данные самих фактов не теряются, пропадает только связь с
-- плановой записью.
DROP INDEX IF EXISTS idx_budget_transaction_planned;
ALTER TABLE budget_transaction DROP COLUMN IF EXISTS planned_id;
-- +goose StatementEnd
