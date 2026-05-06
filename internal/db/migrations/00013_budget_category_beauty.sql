-- +goose Up
-- +goose StatementBegin
INSERT INTO budget_category (id, name, type, icon, sort_order)
VALUES ('b0000001-0000-0000-0000-000000000010', 'Красота', 'expense', '💄', 10)
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM budget_category WHERE id = 'b0000001-0000-0000-0000-000000000010';
-- +goose StatementEnd
