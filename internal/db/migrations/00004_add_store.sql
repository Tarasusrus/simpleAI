-- +goose Up
-- +goose StatementBegin
CREATE TABLE store (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

ALTER TABLE receipt
    ADD COLUMN store_id UUID REFERENCES store(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_store_name ON store (name);
CREATE INDEX idx_receipt_store_id ON receipt (store_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_receipt_store_id;
DROP INDEX IF EXISTS idx_store_name;
ALTER TABLE receipt DROP COLUMN IF EXISTS store_id;
DROP TABLE IF EXISTS store;
-- +goose StatementEnd
