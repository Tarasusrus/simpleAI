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
CREATE INDEX idx_receipt_artifact_receipt_id ON receipt_artifact (receipt_id);
CREATE INDEX idx_rag_document_source ON rag_document (source_type, source_id);
CREATE INDEX idx_rag_document_embedding ON rag_document USING hnsw (embedding vector_l2_ops);
CREATE INDEX idx_rag_document_content_hash ON rag_document (content_hash);

-- Budget Tracker

CREATE TABLE budget_category (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('income', 'expense')),
    icon TEXT NOT NULL DEFAULT '',
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE budget_transaction (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('income', 'expense')),
    amount NUMERIC(12, 2) NOT NULL CHECK (amount > 0),
    category_id UUID REFERENCES budget_category(id) ON DELETE SET NULL,
    description TEXT NOT NULL DEFAULT '',
    transaction_date DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE budget_goal (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    target_amount NUMERIC(12, 2) NOT NULL CHECK (target_amount > 0),
    current_amount NUMERIC(12, 2) NOT NULL DEFAULT 0 CHECK (current_amount >= 0),
    deadline DATE,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'cancelled')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE budget_debt (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    total_amount NUMERIC(12, 2) NOT NULL CHECK (total_amount > 0),
    paid_amount NUMERIC(12, 2) NOT NULL DEFAULT 0 CHECK (paid_amount >= 0),
    monthly_payment NUMERIC(12, 2) CHECK (monthly_payment IS NULL OR monthly_payment > 0),
    direction TEXT NOT NULL CHECK (direction IN ('owe', 'owed')),
    counterparty TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paid')),
    due_date DATE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_budget_category_name_type ON budget_category (name, type);
CREATE INDEX idx_budget_transaction_date ON budget_transaction (transaction_date);
CREATE INDEX idx_budget_transaction_category ON budget_transaction (category_id);
CREATE INDEX idx_budget_transaction_type ON budget_transaction (type);
CREATE INDEX idx_budget_goal_status ON budget_goal (status);
CREATE INDEX idx_budget_debt_status ON budget_debt (status);
