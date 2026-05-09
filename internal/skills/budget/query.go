package budgetskill

import (
	"fmt"
	"strings"
	"time"

	"simpleAI/internal/budget"
)

// DisplayMeta — presentation-only метаданные запроса.
// Не участвует в repository contract; используется только renderer'ом.
type DisplayMeta struct {
	PeriodLabel string
}

// TransactionQuery — нормализованный запрос на выборку транзакций.
// Строится из LLM DTO через NormalizeBudgetInput; уже провалидирован.
type TransactionQuery struct {
	Filter  budget.TransactionFilter
	Display DisplayMeta
}

// NormalizeBudgetInput преобразует сырой LLM DTO в TransactionQuery.
// Вся грязь LLM (mutation period→date, flexible date formats, pagination policy,
// defaults) изолирована здесь. После успешного возврата Filter гарантированно
// проходит Filter.Validate().
func NormalizeBudgetInput(req budgetInput) (TransactionQuery, error) {
	// LLM иногда кладёт конкретную дату в поле period — исправляем.
	if req.Date == "" && req.Period != "" {
		for _, layout := range []string{"2006-01-02", "02.01.2006"} {
			if _, err := time.Parse(layout, req.Period); err == nil {
				req.Date = req.Period
				req.Period = ""
				break
			}
		}
	}

	var dateRange *budget.DateRange
	limit := 20

	switch {
	case req.Date != "":
		day := parseFlexibleDate(req.Date)
		if day.IsZero() {
			return TransactionQuery{}, fmt.Errorf("неверный формат даты %q, используй YYYY-MM-DD или DD.MM.YYYY", req.Date)
		}
		dateRange = &budget.DateRange{
			From: time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC),
			To:   time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, time.UTC),
		}

	case req.DateFrom != "" || req.DateTo != "":
		from := parseFlexibleDate(req.DateFrom)
		to := parseFlexibleDate(req.DateTo)
		if from.IsZero() || to.IsZero() {
			return TransactionQuery{}, fmt.Errorf("неверный формат date_from/date_to, используй YYYY-MM-DD или DD.MM.YYYY")
		}
		if to.Before(from) {
			from, to = to, from
		}
		dateRange = &budget.DateRange{
			From: time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC),
			To:   time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 0, time.UTC),
		}
		limit = 200

	case req.Period != "":
		p := parsePeriod(req.Period)
		dateRange = &budget.DateRange{From: p.From, To: p.To}

	// req.Period == "" && нет дат → DateRange = nil = all time
	}

	f := budget.TransactionFilter{
		DateRange:  dateRange,
		Pagination: budget.Pagination{Limit: limit},
	}

	if raw := strings.ToLower(strings.TrimSpace(req.TransactionType)); raw == "income" || raw == "expense" {
		f.Directions = []budget.TransactionDirection{budget.TransactionDirection(raw)}
	}

	if kw := strings.TrimSpace(req.Keyword); kw != "" {
		f.Search = &kw
	}

	if err := f.Validate(); err != nil {
		return TransactionQuery{}, err
	}

	return TransactionQuery{
		Filter:  f,
		Display: DisplayMeta{PeriodLabel: buildPeriodLabel(&f)},
	}, nil
}
