-- +goose Up
-- +goose StatementBegin
-- Живой конверт (ADR-007 H3): сохранённое событие прихода + горизонт. Остаток
-- НЕ хранится — вычисляется на лету из фактических транзакций за период
-- (структурная защита от двойного учёта). chat-scoped (ADR-004).
CREATE TABLE budget_envelope (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id         BIGINT NOT NULL,
    income_amount   NUMERIC(12,2) NOT NULL,
    income_currency TEXT NOT NULL DEFAULT 'RUB',
    period_start    DATE NOT NULL,
    period_end      DATE NOT NULL,
    active          BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

-- Не более одного активного конверта на chat.
CREATE UNIQUE INDEX idx_budget_envelope_active_chat
    ON budget_envelope(chat_id)
    WHERE active;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS budget_envelope;
-- +goose StatementEnd
