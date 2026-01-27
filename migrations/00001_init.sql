-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE receipt (
    id UUID PRIMARY KEY,
    source TEXT NOT NULL,
    source_ref TEXT NOT NULL,
    purchase_ts TIMESTAMP WITH TIME ZONE,
    currency TEXT,
    total_amount NUMERIC(12, 2) CHECK (total_amount IS NULL OR total_amount >= 0),
    raw_text TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE receipt_item (
    id UUID PRIMARY KEY,
    receipt_id UUID NOT NULL REFERENCES receipt(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    quantity NUMERIC(12, 3) CHECK (quantity IS NULL OR quantity >= 0),
    unit_price NUMERIC(12, 2) CHECK (unit_price IS NULL OR unit_price >= 0),
    amount NUMERIC(12, 2) CHECK (amount IS NULL OR amount >= 0),
    category_id UUID,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE category (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    parent_id UUID REFERENCES category(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

ALTER TABLE receipt_item
    ADD CONSTRAINT fk_receipt_item_category
    FOREIGN KEY (category_id) REFERENCES category(id) ON DELETE SET NULL;

CREATE TABLE rag_document (
    id UUID PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id UUID NOT NULL,
    content TEXT NOT NULL,
    embedding vector(1536),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_receipt_source_ref ON receipt (source, source_ref);
CREATE INDEX idx_receipt_item_receipt_id ON receipt_item (receipt_id);
CREATE INDEX idx_category_parent_id ON category (parent_id);
CREATE INDEX idx_rag_document_source ON rag_document (source_type, source_id);
CREATE INDEX idx_rag_document_metadata ON rag_document USING gin (metadata);
CREATE INDEX idx_rag_document_embedding ON rag_document USING hnsw (embedding vector_l2_ops);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS rag_document;
DROP TABLE IF EXISTS receipt_item;
DROP TABLE IF EXISTS receipt;
DROP EXTENSION IF EXISTS vector;
-- +goose StatementEnd
