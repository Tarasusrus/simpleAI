-- Draft schema for expenses + RAG (Postgres + pgvector)

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE receipt (
    id UUID PRIMARY KEY,
    source TEXT NOT NULL, -- e.g., "telegram"
    source_ref TEXT NOT NULL, -- e.g., message_id or file_id
    purchase_ts TIMESTAMP WITH TIME ZONE,
    currency TEXT,
    total_amount NUMERIC(12, 2),
    store_id UUID,
    raw_text TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE store (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

ALTER TABLE receipt
    ADD CONSTRAINT fk_receipt_store
    FOREIGN KEY (store_id) REFERENCES store(id) ON DELETE SET NULL;

CREATE TABLE receipt_item (
    id UUID PRIMARY KEY,
    receipt_id UUID NOT NULL REFERENCES receipt(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    quantity NUMERIC(12, 3),
    unit_price NUMERIC(12, 2),
    amount NUMERIC(12, 2),
    category TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE rag_document (
    id UUID PRIMARY KEY,
    source_type TEXT NOT NULL, -- "receipt" | "item"
    source_id UUID NOT NULL,
    content TEXT NOT NULL,
    embedding vector(1536),
    content_hash TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    embedding_updated_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_receipt_source ON receipt (source, source_ref);
CREATE UNIQUE INDEX idx_store_name ON store (name);
CREATE INDEX idx_receipt_store_id ON receipt (store_id);
CREATE INDEX idx_rag_document_source ON rag_document (source_type, source_id);
CREATE INDEX idx_rag_document_embedding ON rag_document USING hnsw (embedding vector_l2_ops);
CREATE INDEX idx_rag_document_content_hash ON rag_document (content_hash);
