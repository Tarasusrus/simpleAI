package plan

import "context"

// WindowDays — длина окна плана в днях; подсказка суммы лимита пересчитывается на неё.
const WindowDays = windowDays

// LimitSuggestion — подсказка суммы лимита: сколько обычно уходит на категорию за окно.
// Enough=false — истории мало (или нет курса): суммы нет, экран пишет «данных мало».
type LimitSuggestion struct {
	Enough bool
	// Months — сколько полных месяцев истории у категории.
	Months int
	// AmountTHBMinor — сатанги на окно длиной days.
	AmountTHBMinor int64
	// AvgCount — сколько трат категории обычно приходится на окно длиной days (округлено,
	// не меньше 1, если история есть); 0 — истории меньше MinMonths. Нужен для текста
	// «Это трата 1 из примерно N» (ADR-015 §4).
	AvgCount int
}

// LimitSuggester считает подсказку суммы. Реализует internal/planrepo.
type LimitSuggester interface {
	Suggest(ctx context.Context, category string, days int) (LimitSuggestion, error)
}

// LimitWriter пишет лимиты категорий (ADR-011). Отдельный порт, а не часть Writer:
// лимиты — самостоятельная сущность плана со своей таблицей. Область (чаты)
// принудительно ограничивает и запись, и правку, и удаление.
type LimitWriter interface {
	// AddLimit сохраняет лимит и возвращает его id. Нет такой категории расходов —
	// ErrLimitCategory; ключ уже занят — ErrLimitExists.
	AddLimit(ctx context.Context, sc Scope, n NewLimit) (string, error)
	// UpdateLimit меняет сумму, валюту и повтор. Нет лимита в области — ErrNotFound.
	UpdateLimit(ctx context.Context, sc Scope, id string, e LimitEdit) error
	// DeleteLimit удаляет лимит. Нет лимита в области — ErrNotFound.
	DeleteLimit(ctx context.Context, sc Scope, id string) error
}
