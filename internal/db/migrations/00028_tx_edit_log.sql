-- +goose Up
-- +goose StatementBegin
-- ADR-018 (simpleAI-93la): журнал правок и удалений транзакций и версия строки для оптимистичной блокировки.
--
-- tx_id без внешнего ключа: запись об удалении обязана пережить саму транзакцию. before — снимок строки целиком
-- (по нему восстанавливают удалённое), after — значения после правки, NULL у удаления.
--
-- Аддитивная и повторяемая: IF NOT EXISTS. updated_at у существующих строк = момент миграции; версия нужна только
-- для сравнения «менялась ли строка с тех пор, как её прочитали».
SET LOCAL lock_timeout = '5s';

ALTER TABLE budget_transaction
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS tx_edit_log (
    id         UUID PRIMARY KEY,
    tx_id      UUID NOT NULL,
    action     TEXT NOT NULL CHECK (action IN ('edit', 'delete')),
    before     JSONB NOT NULL,
    after      JSONB,
    source     TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    CHECK ((action = 'edit' AND after IS NOT NULL) OR (action = 'delete' AND after IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_tx_edit_log_tx ON tx_edit_log (tx_id, created_at);

-- Версия обязана расти при ЛЮБОМ UPDATE, а не только в коде страницы: бот и воркер повторов пишут в эту же
-- таблицу своими запросами, и без триггера оптимистичная блокировка их правок не увидит.
CREATE OR REPLACE FUNCTION budget_transaction_touch() RETURNS trigger AS $fn$
BEGIN
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$fn$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_budget_transaction_touch ON budget_transaction;
CREATE TRIGGER trg_budget_transaction_touch
    BEFORE UPDATE ON budget_transaction
    FOR EACH ROW EXECUTE FUNCTION budget_transaction_touch();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Откат теряет журнал: снимки удалённых транзакций пропадут вместе с таблицей. Сами транзакции не затрагиваются.
DROP TRIGGER IF EXISTS trg_budget_transaction_touch ON budget_transaction;
DROP FUNCTION IF EXISTS budget_transaction_touch();
DROP INDEX IF EXISTS idx_tx_edit_log_tx;
DROP TABLE IF EXISTS tx_edit_log;
ALTER TABLE budget_transaction DROP COLUMN IF EXISTS updated_at;
-- +goose StatementEnd
