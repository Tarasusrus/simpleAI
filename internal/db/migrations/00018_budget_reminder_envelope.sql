-- +goose Up
-- +goose StatementBegin
ALTER TABLE budget_reminder
    ADD COLUMN IF NOT EXISTS envelope_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS envelope_hour    INT NOT NULL DEFAULT 8 CHECK (envelope_hour >= 0 AND envelope_hour <= 23),
    ADD COLUMN IF NOT EXISTS envelope_minute  INT NOT NULL DEFAULT 0 CHECK (envelope_minute >= 0 AND envelope_minute <= 59);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE budget_reminder
    DROP COLUMN IF EXISTS envelope_enabled,
    DROP COLUMN IF EXISTS envelope_hour,
    DROP COLUMN IF EXISTS envelope_minute;
-- +goose StatementEnd
