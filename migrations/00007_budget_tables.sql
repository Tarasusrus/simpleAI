-- +goose Up
-- +goose StatementBegin
CREATE TABLE budget_category (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('income', 'expense')),
    icon TEXT NOT NULL DEFAULT '',
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_budget_category_name_type ON budget_category (name, type);

CREATE TABLE budget_transaction (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('income', 'expense')),
    amount NUMERIC(12, 2) NOT NULL CHECK (amount > 0),
    category_id UUID REFERENCES budget_category(id) ON DELETE SET NULL,
    description TEXT NOT NULL DEFAULT '',
    transaction_date DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX idx_budget_transaction_date ON budget_transaction (transaction_date);
CREATE INDEX idx_budget_transaction_category ON budget_transaction (category_id);
CREATE INDEX idx_budget_transaction_type ON budget_transaction (type);

CREATE TABLE budget_goal (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    target_amount NUMERIC(12, 2) NOT NULL CHECK (target_amount > 0),
    current_amount NUMERIC(12, 2) NOT NULL DEFAULT 0 CHECK (current_amount >= 0),
    deadline DATE,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'cancelled')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX idx_budget_goal_status ON budget_goal (status);

CREATE TABLE budget_debt (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    total_amount NUMERIC(12, 2) NOT NULL CHECK (total_amount > 0),
    paid_amount NUMERIC(12, 2) NOT NULL DEFAULT 0 CHECK (paid_amount >= 0),
    monthly_payment NUMERIC(12, 2) CHECK (monthly_payment IS NULL OR monthly_payment > 0),
    direction TEXT NOT NULL CHECK (direction IN ('owe', 'owed')),
    counterparty TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paid')),
    due_date DATE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE INDEX idx_budget_debt_status ON budget_debt (status);

-- Seed: expense categories
INSERT INTO budget_category (id, name, type, icon, sort_order) VALUES
    ('b0000001-0000-0000-0000-000000000001', 'Еда', 'expense', '🍔', 1),
    ('b0000001-0000-0000-0000-000000000002', 'Транспорт', 'expense', '🚕', 2),
    ('b0000001-0000-0000-0000-000000000003', 'Жильё', 'expense', '🏠', 3),
    ('b0000001-0000-0000-0000-000000000004', 'Здоровье', 'expense', '💊', 4),
    ('b0000001-0000-0000-0000-000000000005', 'Развлечения', 'expense', '🎮', 5),
    ('b0000001-0000-0000-0000-000000000006', 'Подписки', 'expense', '📱', 6),
    ('b0000001-0000-0000-0000-000000000007', 'Одежда', 'expense', '👕', 7),
    ('b0000001-0000-0000-0000-000000000008', 'Переводы', 'expense', '💸', 8),
    ('b0000001-0000-0000-0000-000000000009', 'Прочее', 'expense', '📦', 9);

-- Seed: income categories
INSERT INTO budget_category (id, name, type, icon, sort_order) VALUES
    ('b0000002-0000-0000-0000-000000000001', 'Зарплата', 'income', '💰', 1),
    ('b0000002-0000-0000-0000-000000000002', 'Фриланс', 'income', '💻', 2),
    ('b0000002-0000-0000-0000-000000000003', 'Прочее', 'income', '📥', 3);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS budget_debt;
DROP TABLE IF EXISTS budget_goal;
DROP TABLE IF EXISTS budget_transaction;
DROP TABLE IF EXISTS budget_category;
-- +goose StatementEnd
