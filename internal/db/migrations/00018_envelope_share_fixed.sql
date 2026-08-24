-- +goose Up
-- +goose StatementBegin
-- Регулярные платежи становятся ВИДИМЫМИ конвертами (simpleAI-faeq.11).
--
-- До этого обязательства вычитались из прихода ДО раскладки и в ответе бота
-- жили одной сводной строкой «обязательства 12332». Оператор такой ответ
-- разнёс: приход визуально не сходится, а деление трат на «обязательные» и
-- «на жизнь» он отверг прямо («есть мне тоже надо, или ты считаешь что еда
-- необязательна?»). Ресёрч подтверждает: YNAB держит обязательные платежи
-- первыми КАТЕГОРИЯМИ плана, а не скрытым вычетом.
--
-- kind='fixed' — доля под конкретный платёж из budget_recurring: сумма и дата
-- известны заранее, тратить из неё нельзя, и в дневной лимит она не входит.
-- Категорий у такой доли нет намеренно: факт по ней — сам recurring-платёж,
-- а транзакции с recurring_id в факт долей не попадают (ADR-008 §5).
ALTER TABLE budget_envelope_share
    DROP CONSTRAINT budget_envelope_share_kind_check;

ALTER TABLE budget_envelope_share
    ADD CONSTRAINT budget_envelope_share_kind_check
    CHECK (kind IN ('spend', 'save', 'fixed'));

-- Дата платежа. NULL у гибких долей и накоплений — у них даты нет.
-- Может лежать ЗА границей периода конверта: аренда платится 10.09, период
-- кончается 06.09, а отложить деньги надо сейчас.
ALTER TABLE budget_envelope_share
    ADD COLUMN due_date DATE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM budget_envelope_share WHERE kind = 'fixed';

ALTER TABLE budget_envelope_share DROP COLUMN due_date;

ALTER TABLE budget_envelope_share
    DROP CONSTRAINT budget_envelope_share_kind_check;

ALTER TABLE budget_envelope_share
    ADD CONSTRAINT budget_envelope_share_kind_check
    CHECK (kind IN ('spend', 'save'));
-- +goose StatementEnd
