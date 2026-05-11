// Package budget реализует учёт личных финансов: транзакции, категории, цели и долги.
package budget

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// --- Строго-типизированные перечисления ---

// Currency — ISO 4217 код валюты.
type Currency string

// Поддерживаемые валюты (ISO 4217).
const (
	CurrencyRUB Currency = "RUB"
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
	CurrencyTHB Currency = "THB"
)

// TransactionDirection — направление транзакции.
type TransactionDirection string

// Допустимые направления транзакции.
const (
	DirectionIncome  TransactionDirection = "income"
	DirectionExpense TransactionDirection = "expense"
)

// TransactionSortField — поле для сортировки транзакций.
type TransactionSortField string

// Допустимые поля сортировки транзакций.
const (
	SortByDate      TransactionSortField = "transaction_date"
	SortByAmount    TransactionSortField = "amount"
	SortByCreatedAt TransactionSortField = "created_at"
)


// SortDirection — направление сортировки.
type SortDirection string

// Направления сортировки.
const (
	SortAsc  SortDirection = "ASC"
	SortDesc SortDirection = "DESC"
)

// --- Value-объекты фильтра ---

// AmountRange — диапазон суммы (оба края включительно).
type AmountRange struct {
	Min float64
	Max float64
}

// DateRange — временной диапазон (оба края включительно).
type DateRange struct {
	From time.Time
	To   time.Time
}

// Pagination — параметры пагинации. Cursor и Offset взаимоисключающие.
type Pagination struct {
	Limit  int
	Offset int
	Cursor *string
}

const maxPaginationLimit = 500

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

// TransactionFilter — строго-типизированный фильтр для выборки транзакций.
// Все поля опциональны кроме Pagination.Limit.
// Вызови Validate() перед передачей в Store.
type TransactionFilter struct {
	DateRange   *DateRange
	Directions  []TransactionDirection // пусто = оба направления
	CategoryIDs []uuid.UUID
	Currencies  []Currency
	AmountRange *AmountRange
	Search      *string
	SortBy      TransactionSortField
	SortDir     SortDirection
	Pagination  Pagination
}

// Validate проверяет инварианты фильтра. Возвращает ошибку с перечислением всех нарушений.
func (f *TransactionFilter) Validate() error {
	var errs []string

	if f.DateRange != nil && f.DateRange.From.After(f.DateRange.To) {
		errs = append(errs, "DateRange.From must be <= DateRange.To")
	}

	if f.AmountRange != nil {
		if f.AmountRange.Min < 0 {
			errs = append(errs, "AmountRange.Min must be >= 0")
		}
		if f.AmountRange.Min > f.AmountRange.Max {
			errs = append(errs, "AmountRange.Min must be <= AmountRange.Max")
		}
	}

	if f.Pagination.Limit <= 0 {
		errs = append(errs, "Pagination.Limit must be > 0")
	}
	if f.Pagination.Limit > maxPaginationLimit {
		errs = append(errs, fmt.Sprintf("Pagination.Limit must be <= %d", maxPaginationLimit))
	}
	if f.Pagination.Cursor != nil && f.Pagination.Offset != 0 {
		errs = append(errs, "Pagination.Cursor and Pagination.Offset are mutually exclusive")
	}

	switch f.SortBy {
	case "", SortByDate, SortByAmount, SortByCreatedAt:
		// ok
	default:
		errs = append(errs, fmt.Sprintf("SortBy %q is not allowed", f.SortBy))
	}

	switch f.SortDir {
	case "", SortAsc, SortDesc:
		// ok
	default:
		errs = append(errs, fmt.Sprintf("SortDir %q is not allowed", f.SortDir))
	}

	for _, d := range f.Directions {
		if d != DirectionIncome && d != DirectionExpense {
			errs = append(errs, fmt.Sprintf("Directions contains invalid value %q", d))
			break
		}
	}

	for _, id := range f.CategoryIDs {
		if id == uuid.Nil {
			errs = append(errs, "CategoryIDs contains nil UUID")
			break
		}
	}

	if f.Search != nil && strings.TrimSpace(*f.Search) == "" {
		errs = append(errs, "Search must not be blank (use nil to skip)")
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.New("TransactionFilter: " + strings.Join(errs, "; "))
}

// Reminder — настройки ежедневного напоминания для пользователя.
type Reminder struct {
	ChatID       int64
	Enabled      bool
	NotifyHour   int // 0–23
	NotifyMinute int // 0–59
	Timezone     string
}

// CategoryForecast — прогноз трат по одной категории в одной валюте на следующий период.
// Используется как граница интерфейса для будущего ForecastSkill (ADR-001).
type CategoryForecast struct {
	CategoryName    string
	Icon            string
	Currency        string
	ForecastAmount  float64 // среднее за последние N месяцев (Этап 1)
	TrendPct        float64 // (last_month - prev_month) / prev_month * 100
	HasTrend        bool    // false если данных только за 1 месяц
}

// MinTxForConfidence — минимум транзакций в текущем месяце для уверенного прогноза.
// Меньше — AdvisorSnapshot.LowData = true, AdvisorSkill отдаёт verdict='Условно'.
const MinTxForConfidence = 5

// AdvisorSnapshot — финансовый снимок для AdvisorSkill (ADR-002).
//
// Все суммы в THB. Собирается одним SQL CTE из budget_transaction (global, MTD),
// budget_debt (global, status='active', direction='owe'), budget_recurring
// (chat-scoped, enabled). ForecastRemaining заполняется skill'ом отдельным
// вызовом Store.GetForecastData (не входит в CTE).
type AdvisorSnapshot struct {
	BalanceMTD              float64            // доходы − расходы текущего месяца, THB
	IncomeMTD               float64            // доходы текущего месяца (до вычета расходов), THB
	SpentByCategory         map[string]float64 // расходы MTD по категориям, THB
	ForecastRemaining       float64            // прогнозный остаток до конца месяца, THB (заполняется skill'ом)
	UpcomingRecurring       float64            // сумма enabled expense recurring с next_date <= конец месяца, THB
	UpcomingRecurringIncome float64            // сумма enabled income recurring с next_date <= конец месяца, THB
	ActiveDebtDue           float64            // активные долги (owe) с due_date <= конец месяца, THB
	FreeCash                float64            // BalanceMTD − UpcomingRecurring − ActiveDebtDue, THB
	TxCount                 int                // число транзакций в текущем месяце
	LowData                 bool               // TxCount < MinTxForConfidence (порог в пакете skills)
}

// TopExpense — одна из топ-N самых дорогих расходных транзакций за период,
// сконвертированная в THB. Используется AdvisorSkill action='analyze' для
// передачи LLM детализированного среза трат.
type TopExpense struct {
	Date      time.Time
	Category  string
	AmountTHB float64
	OrigAmt   float64
	Currency  string
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
