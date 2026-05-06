-- +goose Up
-- +goose StatementBegin
CREATE TABLE budget_recurring (
    id               UUID PRIMARY KEY,
    chat_id          BIGINT NOT NULL,
    name             TEXT NOT NULL,
    type             TEXT NOT NULL DEFAULT 'expense' CHECK (type IN ('expense', 'income')),
    amount           NUMERIC(12, 2) NOT NULL CHECK (amount > 0),
    category_id      UUID REFERENCES budget_category(id) ON DELETE SET NULL,
    currency         TEXT NOT NULL DEFAULT 'RUB',
    recurrence_type  TEXT NOT NULL CHECK (recurrence_type IN ('monthly', 'weekly', 'daily')),
    day_of_month     INT CHECK (day_of_month IS NULL OR (day_of_month >= 1 AND day_of_month <= 31)),
    next_date        DATE NOT NULL,
    enabled          BOOLEAN NOT NULL DEFAULT true,
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX idx_budget_recurring_next_date ON budget_recurring (next_date) WHERE enabled = true;
CREATE INDEX idx_budget_recurring_chat_id ON budget_recurring (chat_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS budget_recurring;
-- +goose StatementEnd
