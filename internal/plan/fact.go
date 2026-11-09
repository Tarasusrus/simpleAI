package plan

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Правка и возврат закрытых записей (simpleAI-1inl, ADR-009).
//
// Закрытая запись — факт-транзакция (Source=SourceTransaction). Что с ней можно
// делать, решает ОДНО место — Event.Actions: адаптер сообщает происхождение факта
// (FactOrigin), домен выводит из него права. Фронт и BFF права не выводят, а берут
// готовые флаги строки; новое действие или новое происхождение добавляются здесь и
// в таблице ниже, не трогая остальной код.

// transactionKeyPrefix — префикс ключа факта-транзакции. Ключ строит адаптер
// (TransactionKey), разбирает домен (factID): строка живёт в одном месте.
const transactionKeyPrefix = "transaction:"

// TransactionKey — ключ строки для факта-транзакции с данным id.
func TransactionKey(id string) string { return transactionKeyPrefix + id }

// factID достаёт id факта из ключа; false — это не ключ факта.
func factID(key string) (string, bool) { return strings.CutPrefix(key, transactionKeyPrefix) }

// FactOrigin — откуда взялся факт. Нулевое значение — внешний факт, который экран не
// трогает, поэтому забытая в адаптере пометка не открывает лишних прав.
type FactOrigin string

const (
	// FactExternal — бот, воркер, импорт: экран эти факты не правит и не возвращает.
	FactExternal FactOrigin = ""
	// FactWebLoose — создан экраном «оплачено» до появления связи с плановой записью
	// (миграция 00023). Править можно, вернуть в план нельзя: какую запись он закрыл,
	// неизвестно, а угадывать по описанию и сумме можно развязать не ту.
	FactWebLoose FactOrigin = "web"
	// FactSettled — факт, закрывший плановую запись; она известна по связи, поэтому
	// его можно и править, и вернуть в план.
	FactSettled FactOrigin = "settled"
)

// Actions — что экран может сделать со строкой.
type Actions struct {
	Edit   bool // «изменить»
	Reopen bool // «вернуть в план»
}

// actionsByOrigin — права факта по происхождению. Факт правила повтора сюда не
// попадает: Actions отсекает его раньше (воркер пересоздал бы удалённый срок).
var actionsByOrigin = map[FactOrigin]Actions{
	FactWebLoose: {Edit: true},
	FactSettled:  {Edit: true, Reopen: true},
}

// Actions — права строки. Плановые записи и правила правятся (закрываются, переносятся
// и пропускаются отдельными командами), факты — по происхождению.
func (e Event) Actions() Actions {
	switch e.Source {
	case SourcePlanned, SourceRecurring:
		return Actions{Edit: true}
	case SourceTransaction:
		if e.IsRecurring() {
			return Actions{}
		}
		return actionsByOrigin[e.Origin]
	default:
		return Actions{}
	}
}

var (
	// ErrNotReopenable — факт нельзя вернуть в план (старый, чужой или факт правила).
	ErrNotReopenable = fmt.Errorf("%w: вернуть в план можно только оплату, закрытую с экрана", ErrInvalidEvent)
	// ErrCurrencyLocked — у факта пытались сменить валюту.
	ErrCurrencyLocked = fmt.Errorf("%w: валюта закрытой записи не меняется", ErrInvalidEvent)
	// ErrFactDateRequired — у закрытой записи нужна дата оплаты.
	ErrFactDateRequired = fmt.Errorf("%w: у закрытой записи нужна дата", ErrInvalidEvent)
	// ErrNoFactWriter — запись фактов не подключена.
	ErrNoFactWriter = fmt.Errorf("%w: правка закрытых записей отключена", ErrReadOnly)
)

// FactEdit — проверенные новые значения закрытой записи. Валюта, вид и категория
// остаются как в базе: поэтому их здесь нет.
type FactEdit struct {
	Name   string
	Amount Money // та же валюта, что у факта
	Date   Date
}

// FactWriter — запись закрытых записей. Отдельный узкий порт: писателю плановых
// записей он не нужен, а сервису без него остаётся ErrNoFactWriter. Реализует
// internal/planrepo.
type FactWriter interface {
	// UpdateFact меняет факт, созданный экраном; чужой, факт правила или нет факта —
	// ErrNotFound.
	UpdateFact(ctx context.Context, id string, e FactEdit) error
	// UnsettlePlanned удаляет факт и снова открывает плановую запись, которую он
	// закрыл; запись должна лежать в области sc. Нет факта, он без связи или чужой —
	// ErrNotFound.
	UnsettlePlanned(ctx context.Context, sc Scope, factID string) error
}

// Option настраивает Service необязательными зависимостями, не меняя NewService.
type Option func(*Service)

// WithFactWriter подключает правку и возврат закрытых записей.
func WithFactWriter(fw FactWriter) Option { return func(s *Service) { s.facts = fw } }

// Validate проверяет значения закрытой записи: название, сумма, дата.
func (e FactEdit) Validate() error {
	name := strings.TrimSpace(e.Name)
	switch {
	case name == "":
		return ErrNameRequired
	case utf8.RuneCountInString(name) > maxNameRunes:
		return ErrNameTooLong
	case e.Amount.Minor <= 0:
		return ErrAmountNonPositive
	case e.Amount.Minor > maxAmountMinor:
		return ErrAmountTooLarge
	case !e.Amount.Cur.Supported():
		return ErrUnsupportedCurrency
	case !e.Date.Valid():
		return fmt.Errorf("%w: %v", ErrBadDate, e.Date)
	}
	return nil
}

// editFact правит закрытую запись. Валюту, категорию и вид не меняет: сумма
// приходит в валюте факта, категория из формы игнорируется (форма её не показывает).
func (s *Service) editFact(ctx context.Context, e Event, in EditInput) error {
	if !e.Actions().Edit {
		return ErrNotEditable
	}
	if s.facts == nil {
		return ErrNoFactWriter
	}
	if in.Amount == nil {
		return ErrAmountRequired
	}
	if in.Amount.Cur != e.Amount.Cur {
		return ErrCurrencyLocked
	}
	if in.Place == nil || in.Place.Date == nil {
		return ErrFactDateRequired
	}
	if in.Place.Window != WindowNone {
		return fmt.Errorf("%w: у закрытой записи окно не выбирается", ErrInvalidEvent)
	}
	fe := FactEdit{Name: strings.TrimSpace(in.Name), Amount: *in.Amount, Date: *in.Place.Date}
	if err := fe.Validate(); err != nil {
		return err
	}
	return s.facts.UpdateFact(ctx, mustFactID(e.Key), fe)
}

// Reopen возвращает закрытую запись в план: ключ "transaction:<id>".
func (s *Service) Reopen(ctx context.Context, key string) error {
	snap, err := s.reader.Read(ctx, s.scope)
	if err != nil {
		return err
	}
	e, err := findFact(snap, key)
	if err != nil {
		return err
	}
	if !e.Actions().Reopen {
		return ErrNotReopenable
	}
	if s.facts == nil {
		return ErrNoFactWriter
	}
	return s.facts.UnsettlePlanned(ctx, s.scope, mustFactID(e.Key))
}

// findFact ищет факт по ключу строки.
func findFact(snap Snapshot, key string) (Event, error) {
	if _, ok := factID(key); !ok {
		return Event{}, ErrNotEditable
	}
	for _, list := range [][]Event{snap.Payments, snap.Incomes} {
		for _, e := range list {
			if e.Key == key && e.Source == SourceTransaction {
				return e, nil
			}
		}
	}
	return Event{}, ErrNotFound
}

// mustFactID — id факта из ключа, найденного findFact: префикс там уже проверен.
func mustFactID(key string) string {
	id, _ := factID(key)
	return id
}
