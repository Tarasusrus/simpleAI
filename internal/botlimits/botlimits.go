// Package botlimits — единственная дверь бота к лимитам и «Свободно» (ADR-016): скиллы читают и пишут
// лимиты через plan.Service, а не считают остаток сами. Скиллы видят только типы этого пакета и не
// импортируют plan: бот форматирует числа ядра, а не считает.
package botlimits

import (
	"context"
	"errors"
	"time"

	"simpleAI/internal/plan"
)

// Limit — лимит категории в окне плана; все суммы — целые единицы валюты Snapshot.Cur.
// Это ровно plan.LimitView: остаток Left не бывает меньше нуля, перерасход отдельно в Over.
type Limit struct {
	Category string
	Repeat   bool
	Amount   int64
	Spent    int64
	Planned  int64
	Left     int64
	Over     int64
}

// Snapshot — окно плана глазами бота для одного чата.
type Snapshot struct {
	// Cur — валюта чисел: THB или RUB.
	Cur string
	// WindowStart и WindowEnd — первый и последний день окна плана (UTC-полночь).
	WindowStart, WindowEnd time.Time
	DaysLeft               int
	// CanSpend — «Свободно до конца окна» (знаковое), PerDay — «на день»; nil, если дней не осталось или недостача.
	CanSpend int64
	PerDay   *int64
	// LimitHold — сумма остатков лимитов (уже вычтена из CanSpend).
	LimitHold int64
	Limits    []Limit
}

// ForCategory возвращает лимит категории (без учёта регистра и пробелов, как plan.CategoryKey).
func (s Snapshot) ForCategory(name string) (Limit, bool) {
	k := key(name)
	for _, l := range s.Limits {
		if key(l.Category) == k {
			return l, true
		}
	}
	return Limit{}, false
}

// Reader отдаёт окно плана для чата бота. ok=false — чат обслуживается старым путём (вне области
// плана или окна с приходом нет), а не «лимитов нет» (ADR-016 §2, §4).
type Reader interface {
	Snapshot(ctx context.Context, chatID int64, cur string) (snap Snapshot, ok bool, err error)
}

// ErrUnknownCategory — у лимита нет такой категории расходов (или это «Без категории»). Скиллы различают её через
// errors.Is, не импортируя plan.
var ErrUnknownCategory = errors.New("botlimits: нет такой категории расходов")

// Writer пишет лимиты категорий через ядро плана (ADR-016 §6).
type Writer interface {
	// SetLimit ставит или меняет лимит категории на текущее окно плана. amount в основных единицах
	// валюты currency (RUB|THB). ok=false — чат обслуживается старым путём.
	SetLimit(ctx context.Context, chatID int64, category string, amount float64, currency string) (ok bool, err error)
	// ClearLimit снимает лимит категории. found=false — лимита не было.
	ClearLimit(ctx context.Context, chatID int64, category string) (ok, found bool, err error)
}

func key(s string) string { return plan.CategoryKey(s) }
