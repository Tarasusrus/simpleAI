-- +goose Up
-- +goose StatementBegin
CREATE TABLE receipt_artifact (
    id UUID PRIMARY KEY,
    receipt_id UUID NOT NULL REFERENCES receipt(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    content_type TEXT,
    size_bytes BIGINT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX idx_receipt_artifact_receipt_id ON receipt_artifact (receipt_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS receipt_artifact;
-- +goose StatementEnd
