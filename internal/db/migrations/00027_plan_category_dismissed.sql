-- +goose Up
-- +goose StatementBegin
-- «Не планировать» (ADR-015 §3): категория без лимита, которую владелец скрыл из блока
-- «Не запланировано». Скрытие — свойство чата, не окна. Аддитивная миграция, повторный прогон безопасен.
-- ON DELETE CASCADE: слияние категорий удаляет источник вместе с флагом скрытия (флаг источника
-- теряется, решение зафиксировано в ADR-015 §3) и не блокирует cmd/categories.
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS plan_category_dismissed (
    chat_id     BIGINT NOT NULL,
    category_id UUID NOT NULL REFERENCES budget_category(id) ON DELETE CASCADE,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    CONSTRAINT uq_plan_category_dismissed UNIQUE (chat_id, category_id)
);
CREATE INDEX IF NOT EXISTS idx_plan_category_dismissed_category
    ON plan_category_dismissed (category_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Флаги скрытия некритичны: откат стирает таблицу без защиты.
DROP TABLE IF EXISTS plan_category_dismissed;
-- +goose StatementEnd
