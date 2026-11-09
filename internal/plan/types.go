package plan

// Kind — направление денег.
type Kind string

const (
	KindIncome  Kind = "income"
	KindPayment Kind = "payment"
)

// State — состояние события: запланировано или уже случилось.
type State string

const (
	StatePlanned State = "planned"
	StateDone    State = "done"
)

// Source — откуда событие пришло (для ключа и разбора дублей).
type Source string

const (
	SourceTransaction Source = "transaction"
	SourceRecurring   Source = "recurring"
	SourcePlanned     Source = "planned"
)

// Event — одно событие денег на дату. «Расхода» как отдельной сущности нет:
// оплаченный платёж — это событие вида payment в состоянии done.
type Event struct {
	// Key уникален между источниками: "transaction:<id>", "planned:<id>",
	// "recurring:<ruleID>@YYYY-MM-DD".
	Key   string
	Kind  Kind
	State State
	Name  string
	// Amount nil допустим только у планового разового платежа («Права», «Тоник»).
	Amount *Money
	// Date nil допустим только у планового платежа (лежит в «Без даты»).
	Date *Date
	// WindowStart учитывается только при Date == nil: окно, выбранное владельцем.
	WindowStart *Date
	Category    string // пусто = «Без категории»
	Source      Source
	// JoinsWindow — только у поступлений: обычный приход (продажа вещи и т.п.) не открывает окно, а попадает в
	// существующее по дате (ADR-019). Поле инвертировано относительно спеки (OpensWindow), чтобы нулевое значение
	// оставалось прежним поведением: зарплата, аванс и всё, что адаптер не пометил, окно открывают.
	JoinsWindow bool
	// RuleID пуст у разовых. Это единственный признак повтора: второго поля нет,
	// чтобы они не могли разойтись.
	RuleID       string
	ScheduledFor *Date // срок правила, к которому относится плановое событие или факт
	// Origin — происхождение факта (только Source=SourceTransaction); от него зависят
	// права строки, см. Actions. Нулевое значение — внешний факт без прав.
	Origin FactOrigin
}

// IsRecurring — событие порождено правилом повтора.
func (e Event) IsRecurring() bool { return e.RuleID != "" }
