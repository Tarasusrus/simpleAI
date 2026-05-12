-- +goose Up
-- +goose StatementBegin
ALTER TABLE budget_transaction
    ADD COLUMN recurring_id UUID NULL;

ALTER TABLE budget_transaction
    ADD CONSTRAINT fk_budget_transaction_recurring
    FOREIGN KEY (recurring_id) REFERENCES budget_recurring(id) ON DELETE SET NULL;

CREATE INDEX idx_budget_transaction_recurring_id
    ON budget_transaction(recurring_id)
    WHERE recurring_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_budget_transaction_recurring_id;

ALTER TABLE budget_transaction
    DROP CONSTRAINT IF EXISTS fk_budget_transaction_recurring;

ALTER TABLE budget_transaction
    DROP COLUMN IF EXISTS recurring_id;
-- +goose StatementEnd
