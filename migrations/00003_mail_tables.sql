-- +goose Up
-- +goose StatementBegin
CREATE TABLE mail_account (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL,
    email TEXT NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    client_id TEXT,
    client_secret TEXT,
    host TEXT,
    port INTEGER,
    use_tls BOOLEAN,
    labels TEXT[],
    folders TEXT[],
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE mail_message (
    id UUID PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES mail_account(id) ON DELETE CASCADE,
    message_id TEXT,
    provider_uid TEXT,
    from_email TEXT NOT NULL,
    subject TEXT NOT NULL,
    received_at TIMESTAMP WITH TIME ZONE,
    preview TEXT,
    category TEXT,
    importance TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE mail_checkpoint (
    id UUID PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES mail_account(id) ON DELETE CASCADE,
    cursor TEXT,
    last_uid TEXT,
    last_seen_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE mail_run (
    id UUID PRIMARY KEY,
    account_id UUID REFERENCES mail_account(id) ON DELETE SET NULL,
    status TEXT NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    finished_at TIMESTAMP WITH TIME ZONE,
    messages_count INTEGER NOT NULL DEFAULT 0,
    error_text TEXT
);

CREATE UNIQUE INDEX idx_mail_account_provider_email ON mail_account (provider, email);
CREATE UNIQUE INDEX idx_mail_message_account_message_id ON mail_message (account_id, message_id);
CREATE UNIQUE INDEX idx_mail_message_account_provider_uid ON mail_message (account_id, provider_uid);
CREATE UNIQUE INDEX idx_mail_checkpoint_account_id ON mail_checkpoint (account_id);
CREATE INDEX idx_mail_message_received_at ON mail_message (received_at);
CREATE INDEX idx_mail_message_metadata ON mail_message USING gin (metadata);
CREATE INDEX idx_mail_run_account_started_at ON mail_run (account_id, started_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS mail_run;
DROP TABLE IF EXISTS mail_checkpoint;
DROP TABLE IF EXISTS mail_message;
DROP TABLE IF EXISTS mail_account;
-- +goose StatementEnd
