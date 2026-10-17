package plan

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

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

// NewItem — новая плановая запись: ожидаемое поступление или плановый платёж.
type NewItem struct {
	Kind Kind
	Name string
	// Amount nil допустим только у платежа («Права»: сумма пока неизвестна).
	Amount *Money
	// Date и WindowStart взаимоисключающие; у поступления обязательна Date.
	Date        *Date
	WindowStart *Date
	Category    string // только у платежа; пусто = без категории
}

// maxAmountMinor — потолок суммы записи: 1 000 000 000 в основных единицах. Без него
// опечатка в лишний десяток цифр дала бы переполнение при пересчёте, и экран падал
// бы на КАЖДОМ запросе, пока запись не удалят руками из базы.
const maxAmountMinor = 100_000_000_000

const (
	maxNameRunes     = 120
	maxCategoryRunes = 60
)

// Validate проверяет запись до сохранения: домен отказывает с понятной причиной,
// а не чинит данные молча.
func (n NewItem) Validate() error {
	name := strings.TrimSpace(n.Name)
	if name == "" {
		return ErrNameRequired
	}
	if utf8.RuneCountInString(name) > maxNameRunes || utf8.RuneCountInString(n.Category) > maxCategoryRunes {
		return ErrNameTooLong
	}
	if n.Date != nil && n.WindowStart != nil {
		return fmt.Errorf("%w: дата и окно вместе", ErrInvalidEvent)
	}
	if n.Kind == KindIncome && (n.WindowStart != nil || n.Category != "") {
		return fmt.Errorf("%w: у поступления нет окна и категории", ErrInvalidEvent)
	}
	if n.Amount != nil && n.Amount.Minor > maxAmountMinor {
		return ErrAmountTooLarge
	}
	return Event{
		Key: "new", Kind: n.Kind, State: StatePlanned, Name: name,
		Amount: n.Amount, Date: n.Date, WindowStart: n.WindowStart, Source: SourcePlanned,
	}.Validate()
}

// Writer пишет плановые записи. Каждая команда — одна транзакция БД. Область
// (чаты) принудительно ограничивает и запись, и удаление. Реализует internal/planrepo.
type Writer interface {
	// AddPlanned сохраняет запись и возвращает её id.
	AddPlanned(ctx context.Context, sc Scope, n NewItem) (string, error)
	// DeletePlanned удаляет плановую запись; чужой чат или нет записи — ErrNotFound.
	DeletePlanned(ctx context.Context, sc Scope, id string) error
	// SettlePlanned закрывает плановую запись фактом на дату date. actual задаёт
	// сумму, если у записи её не было, и заменяет плановую, если задана. Нет записи
	// или она уже закрыта — ErrNotFound.
	SettlePlanned(ctx context.Context, sc Scope, id string, actual *Money, date Date) error
	// SkipTerm добавляет срок правила повтора в пропущенные; чужое правило — ErrNotFound.
	SkipTerm(ctx context.Context, sc Scope, ruleID string, term Date) error
	// SetRate задаёт ручной курс «рублей за бат» в микроединицах.
	SetRate(ctx context.Context, rubPerThbMicro int64) error
	// ExpenseCategories — названия категорий расходов для выбора в форме.
	ExpenseCategories(ctx context.Context) ([]string, error)
}
