-- +goose Up
-- +goose StatementBegin
-- Номера уже записанных чеков (скрины оплаты кошельком: один и тот же чек приходит в двух видах — экран приложения
-- и сохранённый чек). Ключ — чат и номер транзакции с чека; вторая запись того же номера отклоняется до записи
-- расхода. Аддитивная и повторяемая.
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS receipt_seen (
    chat_id    BIGINT NOT NULL,
    reference  TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, reference)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Откат безопасен: пропадает только память о записанных чеках, расходы не затрагиваются.
DROP TABLE IF EXISTS receipt_seen;
-- +goose StatementEnd
