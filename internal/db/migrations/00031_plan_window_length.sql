-- +goose Up
-- +goose StatementBegin
-- ADR-020: правка длины окна плана. Окна по-прежнему вычисляются из поступлений; здесь только исключения —
-- окна, чью длину владелец поправил (продлил или сократил). Ключ — дата начала окна: её задаёт открывающее
-- поступление, правка начало не двигает. Окно стандартной длины (14 дней) строки не имеет.
--
-- Аддитивная и повторяемая: IF NOT EXISTS.
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS plan_window_length (
    chat_id      BIGINT NOT NULL,
    window_start DATE NOT NULL,
    days         INT NOT NULL CHECK (days BETWEEN 1 AND 62),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, window_start)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Откат возвращает всем окнам 14 дней; поступления, платежи и лимиты не затрагиваются.
DROP TABLE IF EXISTS plan_window_length;
-- +goose StatementEnd
