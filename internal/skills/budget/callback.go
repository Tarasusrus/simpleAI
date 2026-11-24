package budgetskill

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/core"
)

// CallbackPrefix — префикс для всех budget callback_data.
const CallbackPrefix = "budget:"

// HandleCallbackData обрабатывает callback_data вида:
//
//	budget:summary:YYYY-MM   → L1
//	budget:buckets:YYYY-MM   → L2
//	budget:detail:YYYY-MM:bucketID → L3
func (s *BudgetSkill) HandleCallbackData(ctx context.Context, chatID int64, data string) (core.CallbackResult, error) {
	parts := strings.SplitN(data, ":", 4)
	if len(parts) < 3 || parts[0] != "budget" {
		return core.CallbackResult{}, fmt.Errorf("unexpected callback data: %s", data)
	}

	action := parts[1]
	period := parts[2]

	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil || len(rates) == 0 {
		rates = rubRates
	} else {
		for k, v := range rubRates {
			if _, ok := rates[k]; !ok {
				rates[k] = v
			}
		}
	}

	p := parsePeriod(period)
	curr, err := s.store.GetSummary(ctx, p)
	if err != nil {
		return core.CallbackResult{}, fmt.Errorf("get summary: %w", err)
	}

	pending := s.pendingOrUnavailable(ctx, chatID, p, rates)

	switch action {
	case "summary":
		text, buttons := formatSummaryL1(curr, pending, rates, period)
		return core.CallbackResult{Text: text, Buttons: buttons}, nil

	case "buckets":
		text, buttons := formatBucketsL2(curr, pending, rates, period, s.buckets)
		return core.CallbackResult{Text: text, Buttons: buttons}, nil

	case "detail":
		if len(parts) < 4 {
			return core.CallbackResult{}, fmt.Errorf("missing bucket id in callback: %s", data)
		}
		bucketID := parts[3]
		text, buttons := formatBucketDetailL3(curr, rates, period, bucketID, s.buckets)
		return core.CallbackResult{Text: text, Buttons: buttons}, nil

	default:
		return core.CallbackResult{}, fmt.Errorf("unknown budget callback action: %s", action)
	}
}

// pendingForecast — агрегированные суммы ещё не исполненных recurring на текущий месяц.
type pendingForecast struct {
	Available  bool // false = ошибка при расчёте, данные ненадёжны
	IncomeRUB  float64
	ExpenseRUB float64
	// суммы по категориям (lowercase) для bucket-overlay
	ByCategory map[string]float64
}

// pendingOrUnavailable — прогноз ещё не исполненных recurring для итогов месяца. Один путь и для первого ответа
// «Итоги», и для кнопок: раньше первый ответ получал пустой прогноз и всегда писал «Прогноз временно недоступен».
// Ошибка не валит ответ: итоги показываются без прогноза.
func (s *BudgetSkill) pendingOrUnavailable(ctx context.Context, chatID int64, p budget.Period, rates map[string]float64) pendingForecast {
	if chatID == 0 {
		// Без чата прогноз не посчитать: ListRecurring(0) вернул бы пусто и выдал бы это за «ничего не ждём».
		return pendingForecast{Available: false}
	}
	fn := s.pendingFn
	if fn == nil {
		fn = s.pendingRecurring
	}
	pending, err := fn(ctx, chatID, p, rates)
	if err != nil {
		slog.WarnContext(ctx, "pending recurring forecast unavailable",
			"chat_id", chatID,
			"period", p.From.Format("2006-01"),
			"err", err,
		)
		return pendingForecast{Available: false}
	}
	return pending
}

// pendingRecurring вычисляет прогнозные суммы из enabled recurring,
// которые ещё не исполнились в периоде p.
func (s *BudgetSkill) pendingRecurring(ctx context.Context, chatID int64, p budget.Period, rates map[string]float64) (pendingForecast, error) {
	allRecurring, err := s.store.ListRecurring(ctx, chatID)
	if err != nil {
		return pendingForecast{}, fmt.Errorf("list recurring: %w", err)
	}

	endOfMonth := time.Date(p.From.Year(), p.From.Month()+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)

	// Кандидаты: enabled + next_date в пределах месяца.
	candidates := make([]uuid.UUID, 0, len(allRecurring))
	for _, r := range allRecurring {
		if r.Enabled && !r.NextDate.After(endOfMonth) {
			candidates = append(candidates, r.ID)
		}
	}

	executed, err := s.store.GetExecutedRecurringIDs(ctx, candidates, p)
	if err != nil {
		return pendingForecast{}, fmt.Errorf("get executed recurring ids: %w", err)
	}

	result := pendingForecast{Available: true, ByCategory: make(map[string]float64)}
	for _, r := range allRecurring {
		if !r.Enabled {
			continue
		}
		if r.NextDate.After(endOfMonth) {
			continue
		}
		if _, ok := executed[r.ID]; ok {
			continue
		}

		amountRUB := toRUB(r.Amount, r.Currency, rates)
		catKey := strings.ToLower(r.CategoryName)

		switch r.Type {
		case "income":
			result.IncomeRUB += amountRUB
		case "expense":
			result.ExpenseRUB += amountRUB
			result.ByCategory[catKey] += amountRUB
		}
	}

	return result, nil
}
