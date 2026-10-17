-- +goose Up
-- +goose StatementBegin
-- Модуль категорий (эпик simpleAI-nebf, ADR-010): алиасы, очередь «ждёт решения»,
-- журнал решений, расширение category_merge_log, уникальный индекс lower(name),type.
--
-- Повторный прогон безопасен. category_merge_log на проде могла быть создана вручную
-- (скрипт слияния 2026-10-17, без merge_id), поэтому CREATE IF NOT EXISTS + ADD COLUMN IF NOT EXISTS.
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS category_merge_log (
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    table_name      TEXT NOT NULL,
    row_id          TEXT NOT NULL,
    old_category_id UUID NOT NULL,
    new_category_id UUID NOT NULL,
    old_category_name TEXT NOT NULL
);
ALTER TABLE category_merge_log ADD COLUMN IF NOT EXISTS merge_id UUID;
ALTER TABLE category_merge_log ADD COLUMN IF NOT EXISTS new_category_name TEXT;
ALTER TABLE category_merge_log ADD COLUMN IF NOT EXISTS source_category_type TEXT;
ALTER TABLE category_merge_log ADD COLUMN IF NOT EXISTS source_category_icon TEXT;
ALTER TABLE category_merge_log ADD COLUMN IF NOT EXISTS source_category_sort INT;
ALTER TABLE category_merge_log ADD COLUMN IF NOT EXISTS old_share_category_name TEXT;
ALTER TABLE category_merge_log ADD COLUMN IF NOT EXISTS rolled_back_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_category_merge_log_merge ON category_merge_log (merge_id);
CREATE INDEX IF NOT EXISTS idx_category_merge_log_row ON category_merge_log (table_name, row_id);

-- Синонимы: старое имя ведёт на живую категорию. Цепочек нет (Merge перенаправляет алиасы).
CREATE TABLE IF NOT EXISTS budget_category_alias (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alias_lower TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('income', 'expense')),
    category_id UUID NOT NULL REFERENCES budget_category(id) ON DELETE CASCADE,
    source      TEXT NOT NULL CHECK (source IN ('merge', 'manual', 'judge')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (alias_lower, type)
);

-- Очередь решений: сомнительные пары дедупликатора, позже предложения классификатора.
CREATE TABLE IF NOT EXISTS category_review_queue (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT NOT NULL CHECK (type IN ('income', 'expense')),
    name            TEXT NOT NULL,
    name_lower      TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('proposal', 'duplicate_candidate')),
    payload         JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'in_progress', 'done', 'failed', 'rejected')),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_category_review_queue_status ON category_review_queue (status, next_attempt_at);

-- Журнал решений (кто, что, почему). Без FK на категории: источник слияния удаляется.
CREATE TABLE IF NOT EXISTS category_decision_log (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    decided_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    decider            TEXT NOT NULL CHECK (decider IN ('rule', 'manual', 'llm-judge')),
    action             TEXT NOT NULL CHECK (action IN ('merge', 'keep', 'create', 'reject', 'rollback')),
    source_category_id UUID,
    target_category_id UUID,
    source_name        TEXT NOT NULL,
    target_name        TEXT,
    reason             TEXT,
    confidence         NUMERIC,
    merge_id           UUID
);

-- Дубли, различающиеся только регистром, мешают уникальному индексу: сливаем их здесь же
-- (единственный допустимый одноразовый SQL, ADR-010 §7). Остаётся самая старая категория.
DO $$
DECLARE
    g RECORD;
    keeper UUID;
    keeper_name TEXT;
    dup RECORD;
    mid UUID;
BEGIN
    FOR g IN
        SELECT lower(name) AS k, type FROM budget_category GROUP BY lower(name), type HAVING count(*) > 1
    LOOP
        SELECT id, name INTO keeper, keeper_name FROM budget_category
         WHERE lower(name) = g.k AND type = g.type ORDER BY created_at, id LIMIT 1;
        FOR dup IN
            SELECT id, name, icon, sort_order FROM budget_category
             WHERE lower(name) = g.k AND type = g.type AND id <> keeper
        LOOP
            mid := gen_random_uuid();
            INSERT INTO category_merge_log (table_name, row_id, old_category_id, new_category_id, old_category_name,
                    merge_id, new_category_name, source_category_type, source_category_icon, source_category_sort)
                SELECT 'budget_transaction', t.id::text, dup.id, keeper, dup.name, mid, keeper_name, g.type, dup.icon, dup.sort_order
                  FROM budget_transaction t WHERE t.category_id = dup.id;
            INSERT INTO category_merge_log (table_name, row_id, old_category_id, new_category_id, old_category_name,
                    merge_id, new_category_name, source_category_type, source_category_icon, source_category_sort)
                SELECT 'budget_planned_expense', t.id::text, dup.id, keeper, dup.name, mid, keeper_name, g.type, dup.icon, dup.sort_order
                  FROM budget_planned_expense t WHERE t.category_id = dup.id;
            INSERT INTO category_merge_log (table_name, row_id, old_category_id, new_category_id, old_category_name,
                    merge_id, new_category_name, source_category_type, source_category_icon, source_category_sort)
                SELECT 'budget_recurring', t.id::text, dup.id, keeper, dup.name, mid, keeper_name, g.type, dup.icon, dup.sort_order
                  FROM budget_recurring t WHERE t.category_id = dup.id;
            INSERT INTO category_merge_log (table_name, row_id, old_category_id, new_category_id, old_category_name,
                    merge_id, new_category_name, source_category_type, source_category_icon, source_category_sort, old_share_category_name)
                SELECT 'budget_envelope_share_category', t.share_id::text || '/' || t.category_name, dup.id, keeper, dup.name,
                       mid, keeper_name, g.type, dup.icon, dup.sort_order, t.category_name
                  FROM budget_envelope_share_category t WHERE t.category_id = dup.id;
            UPDATE budget_transaction SET category_id = keeper WHERE category_id = dup.id;
            UPDATE budget_planned_expense SET category_id = keeper WHERE category_id = dup.id;
            UPDATE budget_recurring SET category_id = keeper WHERE category_id = dup.id;
            UPDATE budget_envelope_share_category SET category_id = keeper WHERE category_id = dup.id;
            INSERT INTO category_decision_log (decider, action, source_category_id, target_category_id,
                    source_name, target_name, reason, merge_id)
                VALUES ('rule', 'merge', dup.id, keeper, dup.name, keeper_name, 'дубль по регистру (миграция 00022)', mid);
            DELETE FROM budget_category WHERE id = dup.id;
        END LOOP;
    END LOOP;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_budget_category_lower_name_type ON budget_category (lower(name), type);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- category_merge_log не дропаем: в ней может быть журнал для отката. Слитые дубли по регистру
-- миграцией не восстанавливаются.
DROP INDEX IF EXISTS idx_budget_category_lower_name_type;
DROP TABLE IF EXISTS category_decision_log;
DROP TABLE IF EXISTS category_review_queue;
DROP TABLE IF EXISTS budget_category_alias;
-- +goose StatementEnd
