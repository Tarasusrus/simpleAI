// Package categories — постоянный модуль управления категориями (ADR-010): единая точка
// создания, слияние с алиасами и журналом, откат, дедупликатор из заменяемых частей.
package categories

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Category — категория бюджета.
type Category struct {
	ID        uuid.UUID
	Name      string
	Type      string // "income" | "expense"
	Icon      string
	SortOrder int
}

// Pair — кандидат на слияние: Source сливается в Target.
type Pair struct {
	Source, Target Category
	Reason         string
}

// Action — решение по паре.
type Action string

const (
	ActionMerge Action = "merge"
	ActionKeep  Action = "keep"
	ActionDefer Action = "defer"
)

// Decision — решение Decider по паре.
type Decision struct {
	Action  Action
	Decider string // 'rule' | 'manual' | 'llm-judge'
	Reason  string
}

// Source отдаёт текущие категории (БД в проде, фикстура в тестах).
type Source interface {
	List(ctx context.Context) ([]Category, error)
}

// SimilarityFinder находит пары похожих категорий. Стратегии заменяемы: строки, синонимы, модель.
type SimilarityFinder interface {
	Find(ctx context.Context, cats []Category) ([]Pair, error)
}

// Decider решает, что делать с парой. Правила сейчас, LLM-судья потом.
type Decider interface {
	Decide(ctx context.Context, p Pair) (Decision, error)
}

// Merger выполняет слияние одной транзакцией БД.
type Merger interface {
	Merge(ctx context.Context, req MergeRequest) (MergeResult, error)
}

// Usage считает, сколько строк переедет при слиянии (для dry-run).
type Usage interface {
	Usage(ctx context.Context, id uuid.UUID) (Counts, error)
}

// Event — что происходит с категориями, для оповещения.
type Event struct {
	Kind   string // "merged"
	Source string
	Target string
	Reason string
}

// Notifier оповещает владельца. Вызывается только после commit; его ошибка не откатывает слияние.
type Notifier interface {
	Notify(ctx context.Context, e Event) error
}

// NopNotifier ничего не делает.
type NopNotifier struct{}

func (NopNotifier) Notify(context.Context, Event) error { return nil }

// MergeRequest — запрос на слияние.
type MergeRequest struct {
	Source, Target uuid.UUID
	Decider        string
	Reason         string
}

// Counts — сколько строк ссылается на категорию в каждой таблице.
type Counts struct {
	Transactions, Planned, Recurring, Shares int
}

// MergeResult — итог слияния.
type MergeResult struct {
	MergeID uuid.UUID
	Moved   Counts
}

// Clean приводит имя к хранимому виду: обрезка и схлопывание пробелов. Регистр сравнивает БД
// через lower() — один источник истины с уникальным индексом (lower(name), type).
func Clean(name string) string {
	return strings.Join(strings.Fields(name), " ")
}
