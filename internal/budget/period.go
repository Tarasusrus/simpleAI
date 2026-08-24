package budget

import (
	"fmt"
	"strings"
	"time"
)

// DefaultHorizonDays — горизонт расчёта по умолчанию, когда период не задан:
// интервал между приходами ≤ 2 недель (safe-to-spend / живой конверт).
const DefaultHorizonDays = 14

// Horizon — период расчёта плюс человекочитаемая метка. Единый источник границ
// для safe_to_spend и конверта (устраняет дублирование резолверов периода).
type Horizon struct {
	Period
	Label string
}

// Days — число дней в периоде включительно (минимум 1).
func (h Horizon) Days() int {
	d := int(h.To.Sub(h.From).Hours()/24) + 1
	if d < 1 {
		d = 1
	}
	return d
}

// ResolveHorizon разрешает строку периода в границы и метку:
//   - ”/'2weeks'/'2w' → [now, now+defaultDays−1], то есть РОВНО defaultDays дней
//   - 'month'          → [now, конец текущего месяца]
//   - 'YYYY-MM'        → весь тот месяц
//
// defaultDays делает горизонт по умолчанию параметром, а не хардкодом.
//
// Граница включительная (Days() считает оба конца), поэтому конец периода —
// now+defaultDays−1: с now+defaultDays «две недели» длились пятнадцать дней, и
// дневной лимит делился на 15 вместо 14 (simpleAI-faeq.11).
func ResolveHorizon(period string, now time.Time, defaultDays int) Horizon {
	switch strings.TrimSpace(strings.ToLower(period)) {
	case "", "2weeks", "2w":
		return Horizon{Period{From: now, To: now.AddDate(0, 0, defaultDays-1)}, humanDays(defaultDays)}
	case "month":
		end := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
		return Horizon{Period{From: now, To: end}, "до конца месяца"}
	}
	if t, err := time.Parse("2006-01", period); err == nil {
		start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		return Horizon{Period{From: start, To: start.AddDate(0, 1, -1)}, period}
	}
	return Horizon{Period{From: now, To: now.AddDate(0, 0, defaultDays-1)}, humanDays(defaultDays)}
}

// humanDays форматирует горизонт по умолчанию человекочитаемо.
func humanDays(days int) string {
	if days%7 != 0 {
		return fmt.Sprintf("ближайшие %d дней", days)
	}
	weeks := days / 7
	if weeks == 1 {
		return "ближайшую неделю"
	}
	return fmt.Sprintf("ближайшие %d недели", weeks)
}
