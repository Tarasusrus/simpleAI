-- +goose Up
-- +goose StatementBegin
-- Раскладка прихода по конвертам-долям (ADR-008). Живой конверт (00016) хранит
-- САМ приход и горизонт; здесь — на какие доли этот приход разложен.
--
-- ВАЖНО: все денежные суммы в этих таблицах хранятся в THB — валюта расчётной
-- базы (история трат агрегируется в THB). Приход в budget_envelope может быть
-- в любой валюте (income_currency), доли — всегда THB.
CREATE TABLE budget_envelope_share (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    envelope_id UUID NOT NULL REFERENCES budget_envelope(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('spend', 'save')),
    allocated   NUMERIC(12,2) NOT NULL,              -- THB
    carried_in  NUMERIC(12,2) NOT NULL DEFAULT 0,    -- THB, перенос с прошлого прихода
    source      TEXT NOT NULL CHECK (source IN ('auto', 'override')),
    position    INT NOT NULL,
    -- Имя доли — ключ переноса накоплений между приходами: остаток конверта
    -- «Отпуск» из прошлого периода ищется в новом ПО ИМЕНИ. Два конверта с
    -- одинаковым именем в одном приходе делают перенос неоднозначным, поэтому
    -- уникальность — структурная, а не проверка в коде.
    UNIQUE (envelope_id, name)
);

CREATE INDEX idx_budget_envelope_share_envelope
    ON budget_envelope_share(envelope_id);

-- Категории, которые попадают в долю. Двойной ключ (id + нормализованное имя) —
-- потому что фактический пайплайн трат name-keyed (getMonthlyExpenses группирует
-- по COALESCE(c.name,'Прочее')), но уникальный индекс категорий регистрозависим
-- по (name,type), а у части транзакций category_id IS NULL. Матчинг: сначала по
-- category_id, если он есть; иначе по lower(category_name).
CREATE TABLE budget_envelope_share_category (
    share_id      UUID NOT NULL REFERENCES budget_envelope_share(id) ON DELETE CASCADE,
    category_id   UUID REFERENCES budget_category(id),
    category_name TEXT NOT NULL,  -- всегда в нижнем регистре
    PRIMARY KEY (share_id, category_name)
);

CREATE INDEX idx_budget_envelope_share_category_cat
    ON budget_envelope_share_category(category_id)
    WHERE category_id IS NOT NULL;

-- Ручные лимиты, которые оператор поправил словами. Живут МЕЖДУ приходами и к
-- конкретному конверту не привязаны: новый приход раскладывается уже с их
-- учётом (source='override' у соответствующей доли). chat-scoped (ADR-004).
CREATE TABLE budget_envelope_limit_override (
    chat_id    BIGINT NOT NULL,
    share_name TEXT NOT NULL,
    amount     NUMERIC(12,2) NOT NULL,
    currency   TEXT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, share_name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS budget_envelope_limit_override;
DROP TABLE IF EXISTS budget_envelope_share_category;
DROP TABLE IF EXISTS budget_envelope_share;
-- +goose StatementEnd
