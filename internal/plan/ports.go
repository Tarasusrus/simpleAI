package plan

import "context"

// Scope — чьи данные читать. Транзакции общие и области не подчиняются (как в
// боте); chat-таблицы (правила, плановые) фильтруются списком чатов. Граница
// области — адаптер, а не домен.
type Scope struct{ ChatIDs []int64 }

// Snapshot — всё, что нужно расчёту, одним чтением. Reader отдаёт ВСЕ поступления
// с начала: цепочка окон зависит от точки отсечки истории.
//
// Факты повторяющихся правил лежат среди Incomes/Payments с RuleID и
// ScheduledFor, и поэтому на каждый срок правила приходит либо плановое событие
// (его разворачивает Expand из Rules), либо факт, но не оба: оба с одним
// (RuleID, ScheduledFor) Compute отвергает как ErrDuplicateTerm.
type Snapshot struct {
	// Incomes — факты-поступления и разовые ожидаемые поступления.
	Incomes []Event
	// Payments — факты-расходы и разовые плановые платежи (в том числе без суммы).
	Payments []Event
	// Rules — включённые повторяющиеся правила, и расходы, и поступления.
	Rules []RuleWithExceptions
	// Rate — курс; nil, если для бата курса нет.
	Rate       *Rate
	RateManual bool
	ReservePct int
	// Skipped — сколько записей не вошло в план: валюта не бат и не рубль, либо
	// ожидаемое поступление без даты. Молча их терять нельзя, экран скажет об этом.
	Skipped int
}

// Reader читает данные для плана. Реализует internal/planrepo.
type Reader interface {
	Read(ctx context.Context, sc Scope) (Snapshot, error)
}
