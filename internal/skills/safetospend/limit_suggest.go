package safetospend

import (
	"math"

	"simpleAI/internal/budget"
)

// WindowAmountTHBMinor пересчитывает месячный прогноз категории на период в days дней
// (ForecastAmount · days / 30) и отдаёт сатанги. Сопоставление имени — по
// budget.NormalizeName. ok=false: категории в прогнозе нет или для её валюты нет курса.
// Чистая функция: подсказка суммы лимита плана (ADR-011) считается здесь, а не в SQL.
func WindowAmountTHBMinor(fc []budget.CategoryForecast, category string, days int, rates map[string]float64) (int64, bool) {
	key := budget.NormalizeName(category)
	var sum float64
	found := false
	for _, f := range fc {
		if budget.NormalizeName(f.CategoryName) != key {
			continue
		}
		thb, ok := budget.ToTHB(f.ForecastAmount, f.Currency, rates)
		if !ok {
			continue
		}
		sum += thb
		found = true
	}
	if !found || days <= 0 {
		return 0, false
	}
	return int64(math.Round(sum * 100 * float64(days) / prorationBaseDays)), true
}
