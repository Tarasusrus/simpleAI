-- +goose Up
ALTER TABLE budget_transaction ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT 'RUB';

-- +goose Down
ALTER TABLE budget_transaction DROP COLUMN IF EXISTS currency;
