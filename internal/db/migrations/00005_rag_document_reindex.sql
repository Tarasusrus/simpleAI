-- +goose Up
-- +goose StatementBegin
ALTER TABLE rag_document
    ADD COLUMN content_hash TEXT,
    ADD COLUMN updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    ADD COLUMN embedding_updated_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX idx_rag_document_content_hash ON rag_document (content_hash);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_rag_document_content_hash;
ALTER TABLE rag_document
    DROP COLUMN IF EXISTS embedding_updated_at,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS content_hash;
-- +goose StatementEnd
