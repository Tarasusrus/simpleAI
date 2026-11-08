-- +goose Up
-- +goose StatementBegin
-- ADR-021: окно плана — бизнес-сущность со своей таблицей. Окно появляется само (его открывает зарплата или аванс)
-- и в день начала сохраняется сюда; дальше его даты не зависят от поступлений. Владелец двигает начало и конец и
-- может удалить окно (deleted = true: на этих датах окно само больше не появится).
--
-- Перенос из plan_window_length (ADR-020): каждая правка длины — это окно, которое владелец уже поправил; оно
-- становится строкой с теми же датами. Остальные начавшиеся окна (и до, и после перенесённых) сохранит приложение при
-- первом чтении плана: окна до первого сохранённого считаются как раньше (ADR-021 §3).
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS plan_window (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id    BIGINT NOT NULL,
    start_date DATE NOT NULL,
    end_date   DATE NOT NULL,
    deleted    BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    CONSTRAINT chk_plan_window_length CHECK (end_date >= start_date AND end_date - start_date < 62)
);
-- Одно действующее окно на дату начала в чате: повторное сохранение того же окна безопасно.
CREATE UNIQUE INDEX IF NOT EXISTS uq_plan_window_active_start
    ON plan_window (chat_id, start_date) WHERE NOT deleted;

DO $$
BEGIN
    IF to_regclass('plan_window_length') IS NOT NULL THEN
        -- Как читала ADR-020: на одну дату начала — самая свежая правка любого чата области.
        INSERT INTO plan_window (chat_id, start_date, end_date)
        SELECT chat_id, window_start, window_start + (days - 1) FROM (
            SELECT DISTINCT ON (window_start) chat_id, window_start, days
            FROM plan_window_length ORDER BY window_start, updated_at DESC, chat_id
        ) latest
        ON CONFLICT DO NOTHING;
        DROP TABLE plan_window_length;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Откат возвращает таблицу правок длины (ADR-020) из окон нестандартной длины; удалённые окна и сдвиги начала
-- теряются, окна снова вычисляются из поступлений. Поступления, платежи и лимиты не затрагиваются.
CREATE TABLE IF NOT EXISTS plan_window_length (
    chat_id      BIGINT NOT NULL,
    window_start DATE NOT NULL,
    days         INT NOT NULL CHECK (days BETWEEN 1 AND 62),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, window_start)
);
INSERT INTO plan_window_length (chat_id, window_start, days)
SELECT chat_id, start_date, end_date - start_date + 1 FROM plan_window
WHERE NOT deleted AND end_date - start_date + 1 <> 14
ON CONFLICT DO NOTHING;
DROP TABLE IF EXISTS plan_window;
-- +goose StatementEnd
