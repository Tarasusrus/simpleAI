// Package budget реализует учёт личных финансов: транзакции, категории, цели и долги.
package budget

import (
	"time"

	"github.com/google/uuid"
)

// Category — категория дохода или расхода.
type Category struct {
	ID        uuid.UUID
	Name      string
	Type      string // "income" | "expense"
	Icon      string
	SortOrder int
}

// Transaction — одна финансовая операция (доход или расход).
type Transaction struct {
	ID           uuid.UUID
	Type         string // "income" | "expense"
	Amount       float64
	CategoryID   *uuid.UUID
	CategoryName string
	Description  string
	Date         time.Time
	CreatedAt    time.Time
}

// Goal — цель накопления.
type Goal struct {
	ID            uuid.UUID
	Name          string
	TargetAmount  float64
	CurrentAmount float64
	Deadline      *time.Time
	Status        string // "active" | "completed" | "cancelled"
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Debt — долг или кредит.
type Debt struct {
	ID             uuid.UUID
	Name           string
	TotalAmount    float64
	PaidAmount     float64
	MonthlyPayment *float64
	Direction      string // "owe" (я должен) | "owed" (мне должны)
	Counterparty   string
	Status         string // "active" | "paid"
	DueDate        *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Summary — сводка доходов/расходов за период.
type Summary struct {
	TotalIncome  float64
	TotalExpense float64
	Balance      float64
	ByCategory   []CategoryTotal
	Period       Period
}

// CategoryTotal — итог по одной категории.
type CategoryTotal struct {
	CategoryID   uuid.UUID
	CategoryName string
	Icon         string
	Total        float64
}

// Period — временной диапазон.
type Period struct {
	From time.Time
	To   time.Time
}

// TransactionFilter — фильтр для выборки транзакций.
type TransactionFilter struct {
	Period     *Period
	CategoryID *uuid.UUID
	Type       string // "" | "income" | "expense"
	Limit      int
}
