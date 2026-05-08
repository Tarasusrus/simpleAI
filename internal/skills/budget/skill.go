package budgetskill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"simpleAI/internal/budget"
	"simpleAI/internal/plugin"
)

// BudgetSkill управляет личными финансами: транзакции, сводка, цели, долги.
type BudgetSkill struct {
	store *budget.Store
}

// NewBudgetSkill создаёт BudgetSkill.
func NewBudgetSkill(store *budget.Store) *BudgetSkill {
	return &BudgetSkill{store: store}
}

// Manifest возвращает описание скилла для registry и LLM.
func (s *BudgetSkill) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "budget",
		Name:        "Budget Tracker",
		Description: "Personal finance TRACKER: record COMPLETED expenses/income (past tense: 'купил', 'потратил', 'заплатил', 'получил'), get spending summaries, manage savings goals, debts, and recurring payments. " +
			"Use when user RECORDS a transaction or asks to SHOW data (summary, list, forecast, debt status). " +
			"action='summary' shows aggregate totals for a period. Pass transaction_type='income' when user asks ONLY about income ('покажи доходы за <период>', 'сколько я заработал'); pass transaction_type='expense' when user asks ONLY about expenses ('покажи траты / расходы за <период>'); omit transaction_type for a general overview ('итоги за <период>', 'сводка'). " +
			"action='list_transactions' shows individual transaction entries; pass transaction_type='income' or 'expense' when the user explicitly asks to LIST income or expense entries ('перечисли все доходы за апрель'). " +
			"Do NOT use for purchase advice / affordability questions / planning a future purchase ('планирую купить', 'хочу купить', 'стоит ли', 'можем ли позволить', 'хватит ли денег') — use the advisor skill for those. " +
			"Do NOT use for free-form spending analysis / anomalies / trends / savings advice ('проанализируй траты', 'найди аномалии', 'обзор трат', 'дай советы по экономии') — use advisor.analyze. " +
			"Use budget.summary for plain numerical totals only.",
		Version: "1.0.0",
		InputSchema: &plugin.Schema{
			Name:    "BudgetInput",
			Version: "1.0.0",
			JSON: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "Action to perform: add_expense, add_income, summary, list_transactions, edit_transaction, add_goal, update_goal, goal_status, add_debt, pay_debt, debt_status, set_reminder, get_reminder, add_recurring, list_recurring, disable_recurring, forecast",
					},
					"amount": map[string]any{
						"type":        "number",
						"description": "Transaction amount (positive number)",
					},
					"category": map[string]any{
						"type":        "string",
						"description": "Expense or income category. Standard expense categories: еда (food), транспорт (transport), жильё (housing), здоровье (health), красота (beauty), развлечения (entertainment), подписки (subscriptions), одежда (clothes), переводы (transfers), прочее (other). Standard income categories: зарплата (salary), фриланс (freelance), прочее (other). If the user explicitly named a category — pass it as-is, do not replace with 'прочее'. The system will create a new category automatically if no match is found.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Transaction description or note. For category='прочее' always fill from the user's message text (item or service name).",
					},
					"period": map[string]any{
						"type":        "string",
						"description": "Period for summary/list_transactions: 'month' (current month, default) or 'YYYY-MM' (specific month). Do NOT use for a specific day — use the 'date' field instead.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Name of a savings goal, debt, or recurring payment",
					},
					"target_amount": map[string]any{
						"type":        "number",
						"description": "Target amount for a savings goal",
					},
					"deadline": map[string]any{
						"type":        "string",
						"description": "Goal deadline in YYYY-MM-DD format",
					},
					"goal_id": map[string]any{
						"type":        "string",
						"description": "UUID of the savings goal to top up (used in update_goal)",
					},
					"debt_id": map[string]any{
						"type":        "string",
						"description": "UUID of the debt to pay (used in pay_debt)",
					},
					"total": map[string]any{
						"type":        "number",
						"description": "Total debt amount",
					},
					"monthly": map[string]any{
						"type":        "number",
						"description": "Monthly payment amount for a debt",
					},
					"counterparty": map[string]any{
						"type":        "string",
						"description": "Who owes whom (person or organization name)",
					},
					"direction": map[string]any{
						"type":        "string",
						"description": "'owe' — I owe someone, 'owed' — someone owes me",
					},
					"currency": map[string]any{
						"type":        "string",
						"description": "Transaction currency: RUB (default), USD, EUR, THB, etc. (ISO 4217)",
					},
					"transaction_id": map[string]any{
						"type":        "string",
						"description": "Short transaction ID (first 8 characters from list) for edit_transaction. IMPORTANT: before calling edit_transaction always show the found transaction and proposed changes to the user and wait for confirmation.",
					},
					"keyword": map[string]any{
						"type":        "string",
						"description": "Keyword to search transactions by description (used in list_transactions)",
					},
					"date": map[string]any{
						"type":        "string",
						"description": "Specific date in YYYY-MM-DD or DD.MM.YYYY format. For add_expense, add_income, edit_transaction — the operation date. For list_transactions — filter by a specific day (e.g. 'March 8' → '2026-03-08'). Always use 'date' for a specific day, never 'period'.",
					},
					"date_from": map[string]any{
						"type":        "string",
						"description": "Start of an arbitrary date range for list_transactions (YYYY-MM-DD or DD.MM.YYYY). Use together with date_from/date_to when the user asks for a custom range like '1–10 апреля' or 'с 5 по 20 марта'. Do NOT use 'period' for custom ranges. Inclusive.",
					},
					"date_to": map[string]any{
						"type":        "string",
						"description": "End of an arbitrary date range for list_transactions (YYYY-MM-DD or DD.MM.YYYY). Pair with date_from. Inclusive.",
					},
					"reminder_enabled": map[string]any{
						"type":        "boolean",
						"description": "Enable (true) or disable (false) daily reminder. Used in set_reminder.",
					},
					"reminder_hour": map[string]any{
						"type":        "integer",
						"description": "Hour to send the daily reminder (0–23). Used in set_reminder.",
					},
					"reminder_minute": map[string]any{
						"type":        "integer",
						"description": "Minute to send the daily reminder (0–59), default 0. Used in set_reminder.",
					},
					"reminder_timezone": map[string]any{
						"type":        "string",
						"description": "User's timezone in IANA format (e.g. Asia/Bangkok, Europe/Moscow). Used in set_reminder.",
					},
					"recurring_id": map[string]any{
						"type":        "string",
						"description": "Short recurring payment ID (first 8 characters from list_recurring output). Used in disable_recurring.",
					},
					"day_of_month": map[string]any{
						"type":        "integer",
						"description": "Day of month (1–31) for monthly recurring payment. Used in add_recurring.",
					},
					"transaction_type": map[string]any{
						"type":        "string",
						"description": "Transaction type filter. For add_recurring: 'expense' (default) or 'income' (e.g. monthly salary). For list_transactions: pass 'income' to list ONLY income transactions, 'expense' to list ONLY expenses; omit to list all. Use when user explicitly asks to LIST income/expense entries (e.g. 'перечисли все доходы за апрель').",
					},
					"months": map[string]any{
						"type":        "integer",
						"description": "Number of past months to use for forecast calculation (default: all available). Used in forecast action.",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

// budgetInput — входные данные от LLM.
type budgetInput struct {
	Action           string  `json:"action"`
	Amount           float64 `json:"amount,omitempty"`
	Category         string  `json:"category,omitempty"`
	Description      string  `json:"description,omitempty"`
	Period           string  `json:"period,omitempty"`
	Name             string  `json:"name,omitempty"`
	TargetAmount     float64 `json:"target_amount,omitempty"`
	Deadline         string  `json:"deadline,omitempty"`
	GoalID           string  `json:"goal_id,omitempty"`
	DebtID           string  `json:"debt_id,omitempty"`
	Total            float64 `json:"total,omitempty"`
	Monthly          float64 `json:"monthly,omitempty"`
	Counterparty     string  `json:"counterparty,omitempty"`
	Direction        string  `json:"direction,omitempty"`
	Currency         string  `json:"currency,omitempty"`
	TransactionID    string  `json:"transaction_id,omitempty"`
	Keyword          string  `json:"keyword,omitempty"`
	Date             string  `json:"date,omitempty"`
	DateFrom         string  `json:"date_from,omitempty"`
	DateTo           string  `json:"date_to,omitempty"`
	ReminderEnabled  *bool   `json:"reminder_enabled,omitempty"`
	ReminderHour     *int    `json:"reminder_hour,omitempty"`
	ReminderMinute   *int    `json:"reminder_minute,omitempty"`
	ReminderTimezone string  `json:"reminder_timezone,omitempty"`
	RecurringID      string  `json:"recurring_id,omitempty"`
	DayOfMonth       *int    `json:"day_of_month,omitempty"`
	TransactionType  string  `json:"transaction_type,omitempty"`
	Months           int     `json:"months,omitempty"`
}

// Run выполняет действие и возвращает текстовый ответ.
func (s *BudgetSkill) Run(ctx context.Context, input string) (string, error) {
	var req budgetInput
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	slog.Default().InfoContext(ctx, "budget action",
		"action", req.Action,
		"amount", req.Amount,
		"category", req.Category,
		"date", req.Date,
		"period", req.Period,
		"keyword", req.Keyword,
	)

	switch req.Action {
	case "add_expense":
		return s.addTransaction(ctx, req, "expense")
	case "add_income":
		return s.addTransaction(ctx, req, "income")
	case "summary":
		return s.summary(ctx, req)
	case "list_transactions":
		return s.listTransactions(ctx, req)
	case "edit_transaction":
		return s.editTransaction(ctx, req)
	case "add_goal":
		return s.addGoal(ctx, req)
	case "update_goal":
		return s.updateGoal(ctx, req)
	case "goal_status":
		return s.goalStatus(ctx)
	case "add_debt":
		return s.addDebt(ctx, req)
	case "pay_debt":
		return s.payDebt(ctx, req)
	case "debt_status":
		return s.debtStatus(ctx)
	case "set_reminder":
		return s.setReminder(ctx, req)
	case "get_reminder":
		return s.getReminder(ctx)
	case "add_recurring":
		return s.addRecurring(ctx, req)
	case "list_recurring":
		return s.listRecurring(ctx)
	case "disable_recurring":
		return s.disableRecurring(ctx, req)
	case "forecast":
		return s.forecastAction(ctx, req)
	default:
		return "", fmt.Errorf("unknown action: %s", req.Action)
	}
}
