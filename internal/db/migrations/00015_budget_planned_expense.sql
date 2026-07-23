-- +goose Up
-- +goose StatementBegin
-- Ручные разовые плановые траты (ADR-007 §Действия задача 6): пользователь
-- заранее фиксирует «в этом периоде будет трата X на Y», чтобы safe_to_spend
-- вычитал их из свободных денег. chat-scoped (ADR-004 изоляция).
CREATE TABLE budget_planned_expense (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id     BIGINT NOT NULL,
    amount      NUMERIC(12,2) NOT NULL,
    currency    TEXT NOT NULL DEFAULT 'RUB',
    description TEXT NOT NULL DEFAULT '',
    due_date    DATE,
    settled     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX idx_budget_planned_expense_chat
    ON budget_planned_expense(chat_id)
    WHERE settled = false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS budget_planned_expense;
-- +goose StatementEnd
