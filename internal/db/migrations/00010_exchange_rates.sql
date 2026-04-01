-- +goose Up
-- +goose StatementBegin
CREATE TABLE exchange_rate (
    currency    TEXT PRIMARY KEY,
    rate_to_rub FLOAT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS exchange_rate;
-- +goose StatementEnd
