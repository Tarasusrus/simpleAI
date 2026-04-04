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
	Currency     string // ISO 4217: RUB, USD, EUR, THB, ...
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

// Summary — сводка доходов/расходов за период, сгруппированная по валютам.
type Summary struct {
	Currencies []CurrencyGroup
	Period     Period
}

// CurrencyGroup — итоги по одной валюте.
type CurrencyGroup struct {
	Currency     string
	TotalIncome  float64
	TotalExpense float64
	Balance      float64
	ByCategory   []CategoryTotal
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
	Keyword    string // поиск по description (ILIKE)
	Limit      int
}

// Reminder — настройки ежедневного напоминания для пользователя.
type Reminder struct {
	ChatID       int64
	Enabled      bool
	NotifyHour   int // 0–23
	NotifyMinute int // 0–59
	Timezone     string
}

// RecurringPayment — повторяющийся платёж, создающий транзакцию автоматически по расписанию.
type RecurringPayment struct {
	ID              uuid.UUID
	ChatID          int64
	Name            string
	Type            string    // "expense" | "income"
	Amount          float64
	CategoryID      *uuid.UUID
	CategoryName    string
	Currency        string    // ISO 4217
	RecurrenceType  string    // "monthly" | "weekly" | "daily"
	DayOfMonth      *int      // для monthly: день месяца (1–31)
	NextDate        time.Time // следующая дата срабатывания
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TransactionPatch — поля для частичного обновления транзакции.
// Нулевое значение означает «не менять».
type TransactionPatch struct {
	Amount      float64
	Currency    string
	CategoryID  *uuid.UUID // uuid.Nil = очистить категорию
	Description *string    // pointer: nil = не трогать, "" = очистить
	Date        *time.Time
}
