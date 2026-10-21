-- +goose Up
-- +goose StatementBegin
-- Разделение легаси-категории на несколько целей (ADR-013 §2): «Еда» → «Продукты» | «Рестораны».
-- Алиас ведёт на одну цель, поэтому категория, исторически смешавшая две, описывается этой таблицей.
-- Аддитивная миграция: одна новая пустая таблица, существующие читатели её не знают.
-- Повторный прогон безопасен (IF NOT EXISTS).
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS category_split_target (
    source_id  UUID NOT NULL REFERENCES budget_category(id) ON DELETE CASCADE,
    target_id  UUID NOT NULL REFERENCES budget_category(id) ON DELETE CASCADE,
    is_default BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (source_id, target_id),
    CHECK (source_id <> target_id)
);

-- Не больше одной цели по умолчанию на источник.
CREATE UNIQUE INDEX IF NOT EXISTS uq_category_split_default
    ON category_split_target (source_id) WHERE is_default;
CREATE INDEX IF NOT EXISTS idx_category_split_target
    ON category_split_target (target_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Строки таблицы — решение владельца о разделении: откат отказывается, если они есть.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM category_split_target) THEN
        RAISE EXCEPTION 'откат 00025 невозможен: в category_split_target есть строки; удали их вручную';
    END IF;
END
$$;

DROP TABLE IF EXISTS category_split_target;
-- +goose StatementEnd
