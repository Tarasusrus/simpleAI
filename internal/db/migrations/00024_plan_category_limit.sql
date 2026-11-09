-- +goose Up
-- +goose StatementBegin
-- Лимиты категорий на экране плана (ADR-011): деньги, отложенные на категорию на окно.
-- Аддитивная миграция: одна новая таблица, существующие читатели её не знают.
-- Повторный прогон безопасен (IF NOT EXISTS).
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS plan_category_limit (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id      BIGINT NOT NULL,
    category_id  UUID NOT NULL REFERENCES budget_category(id) ON DELETE RESTRICT,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency     TEXT NOT NULL CHECK (currency IN ('THB', 'RUB')),
    repeat       BOOLEAN NOT NULL,
    -- Для повторяющегося — окно, с которого лимит действует; для разового — его окно.
    window_start DATE NOT NULL,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

-- Один повторяющийся лимит на категорию; один разовый на категорию и окно.
CREATE UNIQUE INDEX IF NOT EXISTS uq_plan_category_limit_repeat
    ON plan_category_limit (chat_id, category_id) WHERE repeat;
CREATE UNIQUE INDEX IF NOT EXISTS uq_plan_category_limit_once
    ON plan_category_limit (chat_id, category_id, window_start) WHERE NOT repeat;
CREATE INDEX IF NOT EXISTS idx_plan_category_limit_category
    ON plan_category_limit (category_id);

-- Журнал слияний категорий (ADR-010) учится помнить лимит, не пережившего слияние:
-- снимок строки нужен, чтобы откат вернул его целиком.
ALTER TABLE category_merge_log ADD COLUMN IF NOT EXISTS row_snapshot JSONB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Лимиты и снимки лимитов в журнале слияний — данные владельца: откат отказывается, если они
-- есть, а не стирает молча.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM plan_category_limit) THEN
        RAISE EXCEPTION 'откат 00024 невозможен: в plan_category_limit есть лимиты; удали их вручную';
    END IF;
    IF EXISTS (SELECT 1 FROM category_merge_log WHERE row_snapshot IS NOT NULL AND rolled_back_at IS NULL) THEN
        RAISE EXCEPTION 'откат 00024 невозможен: в category_merge_log есть снимки лимитов для отката слияний (row_snapshot); сначала откати эти слияния';
    END IF;
END
$$;

DROP TABLE IF EXISTS plan_category_limit;
ALTER TABLE category_merge_log DROP COLUMN IF EXISTS row_snapshot;
-- +goose StatementEnd
