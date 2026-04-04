package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/plugin"

	"github.com/google/uuid"
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
		Description: "Personal finance management: record expenses and income, get spending summaries, manage savings goals, debts, and recurring payments. Use this tool when the user talks about spending, money, budget, savings, or debts.",
		Version:     "1.0.0",
		InputSchema: &plugin.Schema{
			Name:    "BudgetInput",
			Version: "1.0.0",
			JSON: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "Action to perform: add_expense, add_income, summary, list_transactions, edit_transaction, add_goal, update_goal, goal_status, add_debt, pay_debt, debt_status, set_reminder, get_reminder, add_recurring, list_recurring, disable_recurring",
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
						"description": "Transaction type for add_recurring: 'expense' (default) or 'income' (e.g. monthly salary).",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

// budgetInput — входные данные от LLM.
type budgetInput struct {
	Action       string  `json:"action"`
	Amount       float64 `json:"amount,omitempty"`
	Category     string  `json:"category,omitempty"`
	Description  string  `json:"description,omitempty"`
	Period       string  `json:"period,omitempty"`
	Name         string  `json:"name,omitempty"`
	TargetAmount float64 `json:"target_amount,omitempty"`
	Deadline     string  `json:"deadline,omitempty"`
	GoalID       string  `json:"goal_id,omitempty"`
	DebtID       string  `json:"debt_id,omitempty"`
	Total        float64 `json:"total,omitempty"`
	Monthly      float64 `json:"monthly,omitempty"`
	Counterparty string  `json:"counterparty,omitempty"`
	Direction     string  `json:"direction,omitempty"`
	Currency      string  `json:"currency,omitempty"`
	TransactionID     string  `json:"transaction_id,omitempty"`
	Keyword           string  `json:"keyword,omitempty"`
	Date              string  `json:"date,omitempty"`
	ReminderEnabled   *bool   `json:"reminder_enabled,omitempty"`
	ReminderHour      *int    `json:"reminder_hour,omitempty"`
	ReminderMinute    *int    `json:"reminder_minute,omitempty"`
	ReminderTimezone  string  `json:"reminder_timezone,omitempty"`
	RecurringID       string  `json:"recurring_id,omitempty"`
	DayOfMonth        *int    `json:"day_of_month,omitempty"`
	TransactionType   string  `json:"transaction_type,omitempty"`
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
	default:
		return "", fmt.Errorf("unknown action: %s", req.Action)
	}
}

// --- Транзакции ---

func (s *BudgetSkill) addTransaction(ctx context.Context, req budgetInput, typ string) (string, error) {
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	date := time.Now()
	if req.Date != "" {
		for _, layout := range []string{"2006-01-02", "02.01.2006", "2006-01-02T15:04:05Z07:00"} {
			if d, err := time.Parse(layout, req.Date); err == nil {
				date = d
				break
			}
		}
	}

	t := budget.Transaction{
		ID:          uuid.New(),
		Type:        typ,
		Amount:      req.Amount,
		Currency:    req.Currency,
		Description: req.Description,
		Date:        date,
	}

	// Найти категорию по имени.
	if req.Category != "" {
		cat, err := s.store.FindCategoryByName(ctx, req.Category, typ)
		if err != nil {
			// Категория не найдена — попробовать создать.
			cat, err = s.store.AddCategory(ctx, req.Category, typ)
			if err != nil {
				return "", fmt.Errorf("create category: %w", err)
			}
		}
		t.CategoryID = &cat.ID
		t.CategoryName = cat.Name
	}

	if err := s.store.AddTransaction(ctx, t); err != nil {
		return "", fmt.Errorf("add transaction: %w", err)
	}

	// Сводка за текущий месяц.
	p := currentMonthPeriod()
	summary, err := s.store.GetSummary(ctx, p)
	if err != nil {
		// Транзакция записана, но сводка не получена — не критично.
		return formatTransaction(t, typ, nil), nil
	}

	return formatTransaction(t, typ, summary), nil
}

func (s *BudgetSkill) summary(ctx context.Context, req budgetInput) (string, error) {
	p := parsePeriod(req.Period)
	curr, err := s.store.GetSummary(ctx, p)
	if err != nil {
		return "", fmt.Errorf("get summary: %w", err)
	}
	prev, err := s.store.GetSummary(ctx, prevMonthPeriod(p))
	if err != nil {
		prev = nil // предыдущий месяц не критичен
	}

	// Загружаем актуальные курсы из БД; при ошибке используем хардкод-fallback.
	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil || len(rates) == 0 {
		rates = rubRates
	} else {
		// Дополняем хардкодом для валют, которых нет в БД.
		for k, v := range rubRates {
			if _, ok := rates[k]; !ok {
				rates[k] = v
			}
		}
	}

	return formatSummary(curr, prev, rates), nil
}

func (s *BudgetSkill) listTransactions(ctx context.Context, req budgetInput) (string, error) {
	var p budget.Period
	var periodLabel string

	// LLM иногда кладёт конкретную дату в period вместо date — исправляем.
	if req.Date == "" && req.Period != "" {
		for _, layout := range []string{"2006-01-02", "02.01.2006"} {
			if _, err := time.Parse(layout, req.Period); err == nil {
				req.Date = req.Period
				req.Period = ""
				break
			}
		}
	}

	if req.Date != "" {
		var day time.Time
		for _, layout := range []string{"2006-01-02", "02.01.2006"} {
			if d, err := time.Parse(layout, req.Date); err == nil {
				day = d
				break
			}
		}
		if day.IsZero() {
			return "", fmt.Errorf("неверный формат даты %q, используй YYYY-MM-DD или DD.MM.YYYY", req.Date)
		}
		p = budget.Period{
			From: time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC),
			To:   time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, time.UTC),
		}
		periodLabel = day.Format("02.01.2006")
	} else {
		p = parsePeriod(req.Period)
		periodLabel = formatPeriodName(p)
	}

	f := budget.TransactionFilter{
		Period:  &p,
		Keyword: req.Keyword,
		Limit:   20,
	}

	txs, err := s.store.ListTransactions(ctx, f)
	if err != nil {
		return "", fmt.Errorf("list transactions: %w", err)
	}

	if len(txs) == 0 {
		return "Транзакций за этот период нет.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Транзакции за %s:\n\n", periodLabel)
	for _, t := range txs {
		icon := "🔴"
		sign := "-"
		if t.Type == "income" {
			icon = "🟢"
			sign = "+"
		}
		cat := t.CategoryName
		if cat == "" {
			cat = "без категории"
		}
		desc := t.Description
		if desc != "" {
			desc = " — " + desc
		}
		fmt.Fprintf(&sb, "[%s] %s %s%.0f %s | %s%s | %s\n",
			t.ID.String()[:8], icon, sign, t.Amount, currencySymbol(t.Currency), cat, desc, t.Date.Format("02.01"))
	}
	return sb.String(), nil
}

// --- Цели ---

func (s *BudgetSkill) addGoal(ctx context.Context, req budgetInput) (string, error) {
	if req.Name == "" {
		return "", fmt.Errorf("name is required for goal")
	}
	if req.TargetAmount <= 0 {
		return "", fmt.Errorf("target_amount must be positive")
	}

	g := budget.Goal{
		ID:           uuid.New(),
		Name:         req.Name,
		TargetAmount: req.TargetAmount,
	}

	if req.Deadline != "" {
		if d, err := time.Parse("2006-01-02", req.Deadline); err == nil {
			g.Deadline = &d
		}
	}

	if err := s.store.AddGoal(ctx, g); err != nil {
		return "", fmt.Errorf("add goal: %w", err)
	}

	result := fmt.Sprintf("🎯 Цель создана: %s — %.0f ₽", g.Name, g.TargetAmount)
	if g.Deadline != nil {
		months := monthsUntil(*g.Deadline)
		if months > 0 {
			monthly := g.TargetAmount / float64(months)
			result += fmt.Sprintf("\nДедлайн: %s (нужно откладывать ~%.0f ₽/мес)",
				g.Deadline.Format("02.01.2006"), monthly)
		}
	}
	return result, nil
}

func (s *BudgetSkill) updateGoal(ctx context.Context, req budgetInput) (string, error) {
	if req.GoalID == "" {
		return "", fmt.Errorf("goal_id is required")
	}
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	id, err := uuid.Parse(req.GoalID)
	if err != nil {
		return "", fmt.Errorf("invalid goal_id: %w", err)
	}

	if err := s.store.UpdateGoalProgress(ctx, id, req.Amount); err != nil {
		return "", fmt.Errorf("update goal: %w", err)
	}

	return fmt.Sprintf("✅ Пополнение цели: +%.0f ₽", req.Amount), nil
}

func (s *BudgetSkill) goalStatus(ctx context.Context) (string, error) {
	goals, err := s.store.ListGoals(ctx)
	if err != nil {
		return "", fmt.Errorf("list goals: %w", err)
	}

	if len(goals) == 0 {
		return "Целей пока нет. Создайте цель, например: «хочу накопить 100000 на отпуск».", nil
	}

	var sb strings.Builder
	sb.WriteString("🎯 Цели накоплений:\n\n")
	for _, g := range goals {
		pct := 0.0
		if g.TargetAmount > 0 {
			pct = g.CurrentAmount / g.TargetAmount * 100
		}
		status := ""
		switch g.Status {
		case "completed":
			status = " ✅"
		case "cancelled":
			status = " ❌"
		}
		fmt.Fprintf(&sb, "• %s%s: %.0f / %.0f ₽ (%.0f%%)\n",
			g.Name, status, g.CurrentAmount, g.TargetAmount, pct)
		if g.Deadline != nil && g.Status == "active" {
			fmt.Fprintf(&sb, "  Дедлайн: %s\n", g.Deadline.Format("02.01.2006"))
		}
	}
	return sb.String(), nil
}

// --- Долги ---

func (s *BudgetSkill) addDebt(ctx context.Context, req budgetInput) (string, error) {
	if req.Name == "" {
		return "", fmt.Errorf("name is required for debt")
	}
	if req.Total <= 0 {
		return "", fmt.Errorf("total must be positive")
	}

	d := budget.Debt{
		ID:           uuid.New(),
		Name:         req.Name,
		TotalAmount:  req.Total,
		Direction:    req.Direction,
		Counterparty: req.Counterparty,
	}
	if d.Direction == "" {
		d.Direction = "owe"
	}
	if req.Monthly > 0 {
		d.MonthlyPayment = &req.Monthly
	}

	if err := s.store.AddDebt(ctx, d); err != nil {
		return "", fmt.Errorf("add debt: %w", err)
	}

	dir := "Я должен"
	if d.Direction == "owed" {
		dir = "Мне должны"
	}
	result := fmt.Sprintf("📋 Долг записан: %s — %.0f ₽ (%s)", d.Name, d.TotalAmount, dir)
	if d.Counterparty != "" {
		result += fmt.Sprintf("\nКонтрагент: %s", d.Counterparty)
	}
	return result, nil
}

func (s *BudgetSkill) payDebt(ctx context.Context, req budgetInput) (string, error) {
	if req.DebtID == "" {
		return "", fmt.Errorf("debt_id is required")
	}
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	id, err := uuid.Parse(req.DebtID)
	if err != nil {
		return "", fmt.Errorf("invalid debt_id: %w", err)
	}

	if err := s.store.PayDebt(ctx, id, req.Amount); err != nil {
		return "", fmt.Errorf("pay debt: %w", err)
	}

	return fmt.Sprintf("✅ Платёж по долгу: %.0f ₽", req.Amount), nil
}

func (s *BudgetSkill) debtStatus(ctx context.Context) (string, error) {
	debts, err := s.store.ListDebts(ctx)
	if err != nil {
		return "", fmt.Errorf("list debts: %w", err)
	}

	if len(debts) == 0 {
		return "Долгов нет 🎉", nil
	}

	var sb strings.Builder
	sb.WriteString("📋 Долги:\n\n")
	for _, d := range debts {
		remaining := d.TotalAmount - d.PaidAmount
		pct := 0.0
		if d.TotalAmount > 0 {
			pct = d.PaidAmount / d.TotalAmount * 100
		}
		dir := "я должен"
		if d.Direction == "owed" {
			dir = "мне должны"
		}
		status := ""
		if d.Status == "paid" {
			status = " ✅"
		}
		fmt.Fprintf(&sb, "• %s%s: осталось %.0f ₽ из %.0f ₽ (%.0f%% погашено) — %s\n",
			d.Name, status, remaining, d.TotalAmount, pct, dir)
	}
	return sb.String(), nil
}

// --- Редактирование транзакций ---

func (s *BudgetSkill) editTransaction(ctx context.Context, req budgetInput) (string, error) {
	if req.TransactionID == "" {
		return "", fmt.Errorf("transaction_id is required")
	}

	tx, err := s.store.GetTransactionByPrefix(ctx, req.TransactionID)
	if err != nil {
		return "", fmt.Errorf("find transaction: %w", err)
	}

	patch := budget.TransactionPatch{}

	if req.Amount > 0 {
		patch.Amount = req.Amount
	}
	if req.Currency != "" {
		patch.Currency = strings.ToUpper(req.Currency)
	}
	if req.Category != "" {
		cat, err := s.store.FindCategoryByName(ctx, req.Category, tx.Type)
		if err != nil {
			cat, err = s.store.AddCategory(ctx, req.Category, tx.Type)
			if err != nil {
				return "", fmt.Errorf("resolve category: %w", err)
			}
		}
		patch.CategoryID = &cat.ID
	}
	if req.Description != "" {
		patch.Description = &req.Description
	}
	if req.Date != "" {
		d, err := time.Parse("2006-01-02", req.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format, use YYYY-MM-DD: %w", err)
		}
		patch.Date = &d
	}

	updated, err := s.store.PatchTransaction(ctx, tx.ID, patch)
	if err != nil {
		return "", fmt.Errorf("edit transaction: %w", err)
	}

	cat := updated.CategoryName
	if cat == "" {
		cat = "без категории"
	}
	icon := "🔴"
	if updated.Type == "income" {
		icon = "🟢"
	}
	return fmt.Sprintf("✏️ Транзакция обновлена:\n%s %.0f %s | %s | %s\n(ID: %s)",
		icon, updated.Amount, currencySymbol(updated.Currency),
		cat, updated.Date.Format("02.01.2006"),
		updated.ID.String()[:8],
	), nil
}

// --- Форматирование ---

func formatTransaction(t budget.Transaction, typ string, summary *budget.Summary) string {
	icon := "🔴"
	label := "Расход"
	if typ == "income" {
		icon = "🟢"
		label = "Доход"
	}
	cat := t.CategoryName
	if cat == "" {
		cat = "без категории"
	}

	cur := currencySymbol(t.Currency)
	result := fmt.Sprintf("%s %s записан: %.0f %s → %s | %s", icon, label, t.Amount, cur, cat, t.Date.Format("02.01.2006"))
	if t.Description != "" {
		result += fmt.Sprintf(" (%s)", t.Description)
	}
	if summary != nil && len(summary.Currencies) > 0 {
		result += "\n"
		for _, cg := range summary.Currencies {
			sym := currencySymbol(cg.Currency)
			result += fmt.Sprintf("\nБаланс месяца (%s): %.0f %s (доходы %.0f, расходы %.0f)",
				cg.Currency, cg.Balance, sym, cg.TotalIncome, cg.TotalExpense)
		}
	}
	return result
}

// rubRates — приблизительные курсы к рублю для сводного отчёта.
var rubRates = map[string]float64{
	"RUB": 1.0,
	"THB": 2.5,
	"USD": 82.0,
	"EUR": 90.0,
}

func toRUB(amount float64, currency string, rates map[string]float64) float64 {
	if rate, ok := rates[currency]; ok {
		return amount * rate
	}
	return amount
}

// summaryTotalRUB считает суммарные расходы сводки в RUB-эквиваленте.
func summaryTotalRUB(s *budget.Summary, rates map[string]float64) float64 {
	var total float64
	for _, cg := range s.Currencies {
		total += toRUB(cg.TotalExpense, cg.Currency, rates)
	}
	return total
}

// summaryCategories собирает все категории сводки с конвертацией в RUB.
func summaryCategories(s *budget.Summary, rates map[string]float64) map[string]float64 {
	m := map[string]float64{}
	for _, cg := range s.Currencies {
		for _, c := range cg.ByCategory {
			m[c.CategoryName] += toRUB(c.Total, cg.Currency, rates)
		}
	}
	return m
}

func formatSummary(s *budget.Summary, prev *budget.Summary, rates map[string]float64) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — расходы\n", formatPeriodName(s.Period))

	if len(s.Currencies) == 0 {
		sb.WriteString("\nОпераций нет.")
		return sb.String()
	}

	totalRUB := summaryTotalRUB(s, rates)

	fmt.Fprintf(&sb, "\n💸 Всего потрачено: ~%.0f ₽ экв.\n", totalRUB)

	// Собираем категории из всех валют, конвертируем в RUB.
	type catEntry struct {
		name     string
		icon     string
		rubTotal float64
		origAmt  float64
		origCur  string // пусто если RUB
	}
	catMap := map[string]*catEntry{}
	for _, cg := range s.Currencies {
		for _, c := range cg.ByCategory {
			key := c.CategoryName
			rubAmt := toRUB(c.Total, cg.Currency, rates)
			if e, ok := catMap[key]; ok {
				e.rubTotal += rubAmt
				if cg.Currency != "RUB" {
					e.origAmt += c.Total
					e.origCur = cg.Currency
				}
			} else {
				orig := ""
				origAmt := 0.0
				if cg.Currency != "RUB" {
					orig = cg.Currency
					origAmt = c.Total
				}
				icon := c.Icon
				if icon == "" {
					icon = "•"
				}
				catMap[key] = &catEntry{
					name:     c.CategoryName,
					icon:     icon,
					rubTotal: rubAmt,
					origAmt:  origAmt,
					origCur:  orig,
				}
			}
		}
	}

	// Сортируем по убыванию суммы.
	cats := make([]*catEntry, 0, len(catMap))
	for _, e := range catMap {
		cats = append(cats, e)
	}
	sort.Slice(cats, func(i, j int) bool {
		return cats[i].rubTotal > cats[j].rubTotal
	})

	sb.WriteString("\nПо категориям:\n")
	for _, e := range cats {
		pct := 0.0
		if totalRUB > 0 {
			pct = e.rubTotal / totalRUB * 100
		}
		sym := currencySymbol(e.origCur)
		if e.origCur != "" && e.origAmt > 0 {
			fmt.Fprintf(&sb, "%s %-14s ~%6.0f ₽  %2.0f%%  (%.0f %s)\n",
				e.icon, e.name, e.rubTotal, pct, e.origAmt, sym)
		} else {
			fmt.Fprintf(&sb, "%s %-14s %7.0f ₽  %2.0f%%\n",
				e.icon, e.name, e.rubTotal, pct)
		}
	}

	// Блок сравнения с предыдущим месяцем.
	if prev != nil && len(prev.Currencies) > 0 {
		prevTotal := summaryTotalRUB(prev, rates)
		if prevTotal > 0 {
			diff := totalRUB - prevTotal
			sign := "+"
			if diff < 0 {
				sign = ""
			}
			pct := diff / prevTotal * 100
			prevName := formatPeriodName(prev.Period)
			fmt.Fprintf(&sb, "\nvs %s: %s%.0f ₽ (%s%.0f%%)\n", prevName, sign, diff, sign, pct)

			// Топ-2 изменения по категориям.
			prevCats := summaryCategories(prev, rates)
			type catDiff struct {
				name string
				pct  float64
			}
			var diffs []catDiff
			for _, e := range cats {
				if pv, ok := prevCats[e.name]; ok && pv > 0 {
					d := (e.rubTotal - pv) / pv * 100
					if d > 15 || d < -15 {
						diffs = append(diffs, catDiff{e.name, d})
					}
				} else if _, ok := prevCats[e.name]; !ok && e.rubTotal > 1000 {
					diffs = append(diffs, catDiff{e.name + " (новое)", 100})
				}
			}
			sort.Slice(diffs, func(i, j int) bool {
				ai, aj := diffs[i].pct, diffs[j].pct
				if ai < 0 {
					ai = -ai
				}
				if aj < 0 {
					aj = -aj
				}
				return ai > aj
			})
			for i, d := range diffs {
				if i >= 2 {
					break
				}
				arrow := "↑"
				if d.pct < 0 {
					arrow = "↓"
				}
				fmt.Fprintf(&sb, "  %s %s %+.0f%%\n", arrow, d.name, d.pct)
			}
		}
	}

	return sb.String()
}

func formatPeriodName(p budget.Period) string {
	months := []string{
		"", "январь", "февраль", "март", "апрель", "май", "июнь",
		"июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь",
	}
	m := int(p.From.Month())
	if m >= 1 && m <= 12 {
		return fmt.Sprintf("%s %d", months[m], p.From.Year())
	}
	return fmt.Sprintf("%s – %s", p.From.Format("02.01.2006"), p.To.Format("02.01.2006"))
}

// --- Утилиты ---

func prevMonthPeriod(p budget.Period) budget.Period {
	from := time.Date(p.From.Year(), p.From.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(p.From.Year(), p.From.Month(), 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)
	return budget.Period{From: from, To: to}
}

func currentMonthPeriod() budget.Period {
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)
	return budget.Period{From: from, To: to}
}

func parsePeriod(s string) budget.Period {
	if s == "" || s == "month" {
		return currentMonthPeriod()
	}
	// Формат YYYY-MM
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return currentMonthPeriod()
	}
	from := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)
	return budget.Period{From: from, To: to}
}

func currencySymbol(currency string) string {
	switch currency {
	case "USD":
		return "$"
	case "EUR":
		return "€"
	case "THB":
		return "฿"
	case "CNY":
		return "¥"
	default:
		return "₽"
	}
}

func monthsUntil(deadline time.Time) int {
	now := time.Now()
	months := (deadline.Year()-now.Year())*12 + int(deadline.Month()) - int(now.Month())
	if months < 1 {
		return 1
	}
	return months
}

// --- Напоминания ---

func (s *BudgetSkill) setReminder(ctx context.Context, req budgetInput) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	_ = ok
	if chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}

	r := budget.Reminder{
		ChatID:       chatID,
		Enabled:      true,
		NotifyHour:   21,
		NotifyMinute: 0,
		Timezone:     "UTC",
	}

	// Применяем явно переданные поля.
	if req.ReminderEnabled != nil {
		r.Enabled = *req.ReminderEnabled
	}
	if req.ReminderHour != nil {
		r.NotifyHour = *req.ReminderHour
	}
	if req.ReminderMinute != nil {
		r.NotifyMinute = *req.ReminderMinute
	}
	if req.ReminderTimezone != "" {
		r.Timezone = req.ReminderTimezone
	}

	if err := s.store.SetReminder(ctx, r); err != nil {
		return fmt.Sprintf("Не удалось сохранить напоминание (%v). Попробуй ещё раз.", err), nil
	}

	if !r.Enabled {
		return "🔕 Ежедневные напоминания отключены.", nil
	}
	return fmt.Sprintf("🔔 Напоминание настроено: каждый день в %02d:%02d (%s) бот напомнит внести покупки.", r.NotifyHour, r.NotifyMinute, r.Timezone), nil
}

func (s *BudgetSkill) getReminder(ctx context.Context) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	_ = ok
	if chatID == 0 {
		return "Напоминания не настроены. Скажи «включи напоминания в 21:00» чтобы настроить.", nil
	}

	r, err := s.store.GetReminder(ctx, chatID)
	if err != nil {
		return "Напоминания не настроены. Скажи «включи напоминания в 21:00» чтобы настроить.", nil
	}

	if !r.Enabled {
		return "🔕 Напоминания отключены.", nil
	}
	return fmt.Sprintf("🔔 Напоминание активно: каждый день в %02d:%02d (%s).", r.NotifyHour, r.NotifyMinute, r.Timezone), nil
}

// --- Повторяющиеся платежи ---

func (s *BudgetSkill) addRecurring(ctx context.Context, req budgetInput) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	_ = ok
	if chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}
	if req.Name == "" {
		return "", fmt.Errorf("name is required for recurring payment")
	}
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	currency := req.Currency
	if currency == "" {
		currency = "RUB"
	}

	txType := req.TransactionType
	if txType != "income" {
		txType = "expense"
	}

	var catID *uuid.UUID
	if req.Category != "" {
		cat, err := s.store.FindCategoryByName(ctx, req.Category, txType)
		if err != nil {
			cat, err = s.store.AddCategory(ctx, req.Category, txType)
		}
		if err == nil {
			catID = &cat.ID
		}
	}

	// Вычисляем первую дату срабатывания.
	now := time.Now().UTC()
	var nextDate time.Time
	dayOfMonth := 1
	if req.DayOfMonth != nil {
		dayOfMonth = *req.DayOfMonth
	}
	// Если этот день в текущем месяце ещё не прошёл — ставим его, иначе следующий месяц.
	candidate := time.Date(now.Year(), now.Month(), dayOfMonth, 0, 0, 0, 0, time.UTC)
	if !candidate.Before(now.Truncate(24 * time.Hour)) {
		nextDate = candidate
	} else {
		nextDate = candidate.AddDate(0, 1, 0)
	}

	r := budget.RecurringPayment{
		ChatID:         chatID,
		Name:           req.Name,
		Type:           txType,
		Amount:         req.Amount,
		CategoryID:     catID,
		Currency:       currency,
		RecurrenceType: "monthly",
		DayOfMonth:     &dayOfMonth,
		NextDate:       nextDate,
		Enabled:        true,
	}

	if err := s.store.AddRecurring(ctx, r); err != nil {
		return fmt.Sprintf("Не удалось сохранить повторяющийся платёж: %v", err), nil
	}

	return fmt.Sprintf("🔄 Повторяющийся платёж добавлен: *%s* — %.0f %s каждое %d-е число месяца. Первое списание: %s.",
		req.Name, req.Amount, currency, dayOfMonth, nextDate.Format("02.01.2006")), nil
}

func (s *BudgetSkill) listRecurring(ctx context.Context) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	_ = ok
	if chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}

	list, err := s.store.ListRecurring(ctx, chatID)
	if err != nil {
		return fmt.Sprintf("Не удалось загрузить список: %v", err), nil
	}
	if len(list) == 0 {
		return "Повторяющихся платежей нет. Чтобы добавить, скажи: «каждое 1-е число списывай аренду 30000 THB».", nil
	}

	var sb strings.Builder
	sb.WriteString("🔄 *Повторяющиеся платежи:*\n\n")
	for _, r := range list {
		status := "✅"
		if !r.Enabled {
			status = "⏸"
		}
		day := "?"
		if r.DayOfMonth != nil {
			day = fmt.Sprintf("%d-е", *r.DayOfMonth)
		}
		fmt.Fprintf(&sb, "%s %s — %.0f %s | каждое %s | след. %s | ID: %s\n",
			status, r.Name, r.Amount, r.Currency, day,
			r.NextDate.Format("02.01"), r.ID.String()[:8])
	}
	return sb.String(), nil
}

func (s *BudgetSkill) disableRecurring(ctx context.Context, req budgetInput) (string, error) {
	if req.RecurringID == "" {
		return "", fmt.Errorf("recurring_id is required")
	}

	if err := s.store.DisableRecurringByPrefix(ctx, req.RecurringID); err != nil {
		return fmt.Sprintf("Не удалось отключить: %v", err), nil
	}
	return "⏸ Повторяющийся платёж отключён.", nil
}
