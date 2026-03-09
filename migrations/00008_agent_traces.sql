-- +goose Up
CREATE TABLE agent_trace (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  UUID NOT NULL,
    chat_id     BIGINT,
    user_input  TEXT NOT NULL,
    iteration   INT NOT NULL,
    skill       TEXT,
    skill_input JSONB,
    skill_result TEXT,
    llm_response TEXT NOT NULL,
    is_final    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_trace_chat_id    ON agent_trace (chat_id);
CREATE INDEX idx_agent_trace_session_id ON agent_trace (session_id);
CREATE INDEX idx_agent_trace_created_at ON agent_trace (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS agent_trace;
