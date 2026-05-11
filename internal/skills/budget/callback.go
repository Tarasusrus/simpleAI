package budgetskill

import (
	"context"
	"fmt"
	"strings"
	"time"

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
func (s *BudgetSkill) HandleCallbackData(ctx context.Context, data string) (core.CallbackResult, error) {
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

	switch action {
	case "summary":
		avg := s.threeMonthAvg(ctx, p, rates)
		text, buttons := formatSummaryL1(curr, avg, rates, period)
		return core.CallbackResult{Text: text, Buttons: buttons}, nil

	case "buckets":
		text, buttons := formatBucketsL2(curr, rates, period, s.buckets)
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

// threeMonthAvg считает средний расход за 3 предыдущих месяца.
func (s *BudgetSkill) threeMonthAvg(ctx context.Context, p budget.Period, rates map[string]float64) float64 {
	var total float64
	for i := 1; i <= 3; i++ {
		from := time.Date(p.From.Year(), p.From.Month()-time.Month(i), 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(p.From.Year(), p.From.Month()-time.Month(i)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)
		sum, err := s.store.GetSummary(ctx, budget.Period{From: from, To: to})
		if err != nil {
			continue
		}
		total += summaryTotalRUB(sum, rates)
	}
	return total / 3
}
