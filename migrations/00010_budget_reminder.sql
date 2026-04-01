-- +goose Up
-- +goose StatementBegin
CREATE TABLE budget_reminder (
    chat_id      BIGINT PRIMARY KEY,
    enabled      BOOLEAN NOT NULL DEFAULT true,
    notify_hour  INT NOT NULL DEFAULT 21 CHECK (notify_hour >= 0 AND notify_hour <= 23),
    notify_minute INT NOT NULL DEFAULT 0 CHECK (notify_minute >= 0 AND notify_minute <= 59),
    timezone     TEXT NOT NULL DEFAULT 'UTC',
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS budget_reminder;
-- +goose StatementEnd
