-- Ручной откат миграции 00023 (связь факта с плановой записью). Down самой миграции
-- безопасен и тоже годится; этот скрипт для осознанного отката человеком после бэкапа:
--
--   docker exec -i simpleai-postgres psql -U simpleai -d simpleai -v ON_ERROR_STOP=1 < deploy/rollback-00023.sql
--
-- ПОТЕРИ: пропадает только ссылка planned_id у фактов («вернуть в план» перестанет
-- работать). Сами транзакции и плановые записи не трогаются.
BEGIN;
DROP INDEX IF EXISTS idx_budget_transaction_planned;
ALTER TABLE budget_transaction DROP COLUMN IF EXISTS planned_id;
DELETE FROM goose_db_version WHERE version_id = 23;
COMMIT;
