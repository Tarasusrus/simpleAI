-- +goose Up
-- +goose StatementBegin
-- Таксономия еды, этап 1 (ADR-013 §3-5): «Еда» делится на «Продукты» и «Рестораны».
-- История остаётся в «Еде» (её разберёт этап 2, отдельным решением с dry-run); с этой миграции
-- новые траты в «Еду» не попадают: Resolver отдаёт цели из category_split_target.
--
-- Что делает, в одной транзакции:
--  1. заводит «Продукты» и «Рестораны», если их нет;
--  2. пишет таблицу разделения «Еда» → Продукты (по умолчанию) | Рестораны;
--  3. переназначает алиасы слитых дублей: продукт → Продукты, ресторан и «кафе / ресторан» →
--     Рестораны, добавляет кафе, кофейня, доставка еды → Рестораны. Алиас, совпадающий с именем
--     живой категории («продукты»), удаляется: имя теперь принадлежит самой категории;
--  4. доли конверта ADR-008: к каждой доле, владеющей «едой», добавляются «продукты» и «рестораны»,
--     «еда» остаётся (история по-прежнему считается в той же доле, иначе остаток конверта прыгнул бы);
--  5. лимит плана ADR-011 с «Еды» переезжает на «Продукты» (Рестораны без лимита); если у «Продуктов»
--     уже есть лимит с тем же ключом, лимит «Еды» остаётся на месте, ничего не стирается.
-- Если «Еды» нет (чистая база), миграция ничего не меняет. Повторный прогон безопасен.
SET LOCAL lock_timeout = '5s';

DO $$
DECLARE
    eda  UUID;
    prod UUID;
    rest UUID;
BEGIN
    SELECT id INTO eda FROM budget_category WHERE lower(name) = 'еда' AND type = 'expense';
    IF eda IS NULL THEN
        RETURN;
    END IF;

    INSERT INTO budget_category (id, name, type, icon, sort_order)
        SELECT gen_random_uuid(), 'Продукты', 'expense', '🛒', sort_order FROM budget_category WHERE id = eda
        ON CONFLICT DO NOTHING;
    INSERT INTO budget_category (id, name, type, icon, sort_order)
        SELECT gen_random_uuid(), 'Рестораны', 'expense', '🍽️', sort_order FROM budget_category WHERE id = eda
        ON CONFLICT DO NOTHING;
    SELECT id INTO prod FROM budget_category WHERE lower(name) = 'продукты' AND type = 'expense';
    SELECT id INTO rest FROM budget_category WHERE lower(name) = 'рестораны' AND type = 'expense';

    INSERT INTO category_split_target (source_id, target_id, is_default) VALUES (eda, prod, true), (eda, rest, false)
        ON CONFLICT DO NOTHING;

    -- Алиасы: имя живой категории не может быть алиасом.
    DELETE FROM budget_category_alias a
        WHERE a.type = 'expense' AND EXISTS (SELECT 1 FROM budget_category c WHERE lower(c.name) = a.alias_lower AND c.type = 'expense');
    UPDATE budget_category_alias SET category_id = prod
        WHERE type = 'expense' AND category_id = eda AND alias_lower IN ('продукт', 'продукты');
    UPDATE budget_category_alias SET category_id = rest
        WHERE type = 'expense' AND category_id = eda AND alias_lower IN ('ресторан', 'кафе / ресторан');
    INSERT INTO budget_category_alias (alias_lower, type, category_id, source)
        SELECT v, 'expense', rest, 'manual' FROM unnest(ARRAY['кафе', 'кофейня', 'доставка еды']) AS v
        WHERE NOT EXISTS (SELECT 1 FROM budget_category c WHERE lower(c.name) = v AND c.type = 'expense')
        ON CONFLICT DO NOTHING;

    -- Доли конверта: «продукты» и «рестораны» живут в тех же долях, что и «еда».
    INSERT INTO budget_envelope_share_category (share_id, category_id, category_name)
        SELECT s.share_id, prod, 'продукты' FROM budget_envelope_share_category s WHERE s.category_id = eda
        ON CONFLICT DO NOTHING;
    INSERT INTO budget_envelope_share_category (share_id, category_id, category_name)
        SELECT s.share_id, rest, 'рестораны' FROM budget_envelope_share_category s WHERE s.category_id = eda
        ON CONFLICT DO NOTHING;

    -- Лимит плана: «Еда» → «Продукты», кроме ключей, уже занятых у «Продуктов».
    UPDATE plan_category_limit l SET category_id = prod
        WHERE l.category_id = eda
          AND NOT EXISTS (
              SELECT 1 FROM plan_category_limit o
              WHERE o.category_id = prod AND o.chat_id = l.chat_id AND o.repeat = l.repeat
                AND (l.repeat OR o.window_start = l.window_start));
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Возвращает лимит и алиасы на «Еду» и убирает разделение. Категории «Продукты» и «Рестораны» не
-- удаляются (на них могут быть траты), доли «продукты» и «рестораны» тоже остаются. Если «Продукты»
-- или «Рестораны» уже получили траты, часть их уже не вернуть «Еде» без этапа 2: откат отказывает.
DO $$
DECLARE
    eda  UUID;
    prod UUID;
    rest UUID;
BEGIN
    SELECT id INTO eda  FROM budget_category WHERE lower(name) = 'еда' AND type = 'expense';
    SELECT id INTO prod FROM budget_category WHERE lower(name) = 'продукты' AND type = 'expense';
    SELECT id INTO rest FROM budget_category WHERE lower(name) = 'рестораны' AND type = 'expense';
    IF eda IS NULL OR prod IS NULL OR rest IS NULL THEN
        RETURN;
    END IF;
    IF EXISTS (SELECT 1 FROM budget_transaction WHERE category_id IN (prod, rest)) THEN
        RAISE EXCEPTION 'откат 00026 невозможен: в «Продуктах»/«Ресторанах» уже есть траты; сначала перенеси их (этап 2 ADR-013)';
    END IF;
    UPDATE plan_category_limit l SET category_id = eda
        WHERE l.category_id = prod
          AND NOT EXISTS (
              SELECT 1 FROM plan_category_limit o
              WHERE o.category_id = eda AND o.chat_id = l.chat_id AND o.repeat = l.repeat
                AND (l.repeat OR o.window_start = l.window_start));
    DELETE FROM budget_category_alias WHERE type = 'expense' AND category_id IN (prod, rest)
        AND alias_lower IN ('продукт', 'ресторан', 'кафе / ресторан', 'кафе', 'кофейня', 'доставка еды');
    INSERT INTO budget_category_alias (alias_lower, type, category_id, source) VALUES
        ('продукт', 'expense', eda, 'merge'), ('продукты', 'expense', eda, 'merge'),
        ('ресторан', 'expense', eda, 'merge'), ('кафе / ресторан', 'expense', eda, 'merge')
        ON CONFLICT DO NOTHING;
    DELETE FROM category_split_target WHERE source_id = eda;
END
$$;
-- +goose StatementEnd
