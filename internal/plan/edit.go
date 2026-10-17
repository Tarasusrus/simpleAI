package plan

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Правка записей плана (issue #81): разовой плановой записи («planned:<id>») и
// правила повтора целиком («recurring:<ruleID>@срок» — правится правило, а не один
// срок).
//
// У правила правятся название, сумма, валюта и категория, но не расписание: бот и
// воркер ещё считают сроки по старым колонкам, план — по якорю и шагу, и правка
// одной группы развела бы их (отдельная задача после перевода воркера на Next).
// «Только этот срок» — это «пропустить этот раз» плюс разовая запись. Новая сумма
// правила ложится на все сроки без факта, в том числе просроченные: факты — отдельные
// транзакции, прошлые не меняются (R3c).

// UnnamedName — подпись записи без описания и категории. В форму правки она не
// идёт: иначе сохранение записало бы заглушку в описание как настоящее название.
const UnnamedName = "Без названия"

var (
	// ErrNotEditable — ключ не плановой записи и не правила (например, факт).
	ErrNotEditable = fmt.Errorf("%w: изменить можно только плановую запись или повтор", ErrInvalidEvent)
	// ErrRuleSchedule — у повтора попросили поменять дату или окно.
	ErrRuleSchedule = fmt.Errorf("%w: у повтора срок не правится", ErrInvalidEvent)
)

// EditableItem — запись в том виде, в каком её правят: сумма в исходной валюте, а не
// в валюте показа.
type EditableItem struct {
	// Key — "planned:<id>" или "recurring:<ruleID>" (без срока: правится правило).
	Key       string
	Kind      Kind
	Recurring bool
	Name      string
	Amount    *Money // nil — суммы нет (только у разового платежа)
	Category  string
	// Date и WindowStart — размещение разовой записи; у правила nil.
	Date, WindowStart *Date
	// Rule — расписание правила, только для подписи в форме; у разовой nil.
	Rule *Rule
}

// EditInput — новые значения записи. Название, сумма и категория заменяются
// целиком; Place nil — дата и окно не меняются (у правила Place запрещён).
type EditInput struct {
	Name     string
	Amount   *Money
	Category string
	Place    *MoveInput
}

// PlannedEdit — проверенная разовая запись для писателя. Item несёт итоговые дату и
// окно, но пишутся они только при SetPlace: иначе правка откатила бы перенос,
// сделанный между чтением формы и сохранением.
type PlannedEdit struct {
	Item     NewItem
	SetPlace bool
}

// RuleEdit — новые название, сумма и категория правила. Kind — вид правила из базы:
// писатель меняет правило, только если вид совпал.
type RuleEdit struct {
	Kind     Kind
	Name     string
	Amount   Money
	Category string // у поступления всегда пусто: его категория не меняется
}

// Item отдаёт запись для формы «изменить». Записи и правила, не вошедшие в снимок
// (чужой чат, закрытые, валюта не бат и не рубль), — ErrNotFound.
func (s *Service) Item(ctx context.Context, key string) (EditableItem, error) {
	if err := s.canWrite(); err != nil {
		return EditableItem{}, err
	}
	snap, err := s.reader.Read(ctx, s.scope)
	if err != nil {
		return EditableItem{}, err
	}
	return findItem(snap, key)
}

// Edit меняет запись одной командой писателя. Вид записи и её текущее размещение
// берутся из базы, а не от клиента.
func (s *Service) Edit(ctx context.Context, key string, in EditInput) error {
	if err := s.canWrite(); err != nil {
		return err
	}
	snap, err := s.reader.Read(ctx, s.scope)
	if err != nil {
		return err
	}
	it, err := findItem(snap, key)
	if err != nil {
		return err
	}
	if it.Recurring {
		return s.editRule(ctx, it, in)
	}
	return s.editPlanned(ctx, it, in)
}

func (s *Service) editPlanned(ctx context.Context, it EditableItem, in EditInput) error {
	n := NewItem{
		Kind: it.Kind, Name: in.Name, Amount: in.Amount, Category: strings.TrimSpace(in.Category),
		Date: it.Date, WindowStart: it.WindowStart,
	}
	if in.Place != nil {
		date, ws, err := s.placement(ctx, in.Place.Date, in.Place.Window)
		if err != nil {
			return err
		}
		n.Date, n.WindowStart = date, ws
	}
	if err := n.Validate(); err != nil {
		return err
	}
	n.Name = strings.TrimSpace(n.Name)
	return s.writer.UpdatePlanned(ctx, s.scope, strings.TrimPrefix(it.Key, "planned:"), PlannedEdit{Item: n, SetPlace: in.Place != nil})
}

func (s *Service) editRule(ctx context.Context, it EditableItem, in EditInput) error {
	if in.Place != nil {
		return ErrRuleSchedule
	}
	name, cat := strings.TrimSpace(in.Name), strings.TrimSpace(in.Category)
	if name == "" {
		return ErrNameRequired
	}
	if utf8.RuneCountInString(name) > maxNameRunes || utf8.RuneCountInString(cat) > maxCategoryRunes {
		return ErrNameTooLong
	}
	if it.Kind == KindIncome && cat != "" {
		return fmt.Errorf("%w: у поступления категория не правится", ErrInvalidEvent)
	}
	if in.Amount == nil {
		return ErrAmountRequired
	}
	if in.Amount.Minor > maxAmountMinor {
		return ErrAmountTooLarge
	}
	// Та же проверка, что у события повтора при расчёте: сумма > 0, валюта бат или рубль.
	// Остальное в правиле не меняется и уже прошло проверку при чтении.
	if err := (Event{Key: it.Key, Kind: it.Kind, State: StatePlanned, Name: name, Amount: in.Amount,
		Date: &it.Rule.Anchor, RuleID: strings.TrimPrefix(it.Key, "recurring:")}).Validate(); err != nil {
		return err
	}
	return s.writer.UpdateRule(ctx, s.scope, strings.TrimPrefix(it.Key, "recurring:"),
		RuleEdit{Kind: it.Kind, Name: name, Amount: *in.Amount, Category: cat})
}

// findItem ищет запись по ключу строки экрана. У правила ключ срока
// "recurring:<ruleID>@YYYY-MM-DD" ведёт к самому правилу.
func findItem(snap Snapshot, key string) (EditableItem, error) {
	if _, ok := strings.CutPrefix(key, "planned:"); ok {
		for _, list := range [][]Event{snap.Payments, snap.Incomes} {
			for _, e := range list {
				if e.Key == key && e.Source == SourcePlanned {
					name := e.Name
					if name == UnnamedName {
						name = ""
					}
					return EditableItem{
						Key: e.Key, Kind: e.Kind, Name: name, Amount: e.Amount, Category: editCategory(e.Kind, e.Category),
						Date: e.Date, WindowStart: e.WindowStart,
					}, nil
				}
			}
		}
		return EditableItem{}, ErrNotFound
	}
	if rest, ok := strings.CutPrefix(key, "recurring:"); ok {
		ruleID, _, _ := strings.Cut(rest, "@")
		if ruleID == "" {
			return EditableItem{}, ErrNotEditable
		}
		for _, r := range snap.Rules {
			if r.ID == ruleID {
				amount, rule := r.Rule.Amount, r.Rule
				return EditableItem{
					Key: "recurring:" + r.ID, Kind: r.Kind, Recurring: true, Name: r.Name,
					Amount: &amount, Category: editCategory(r.Kind, r.Category), Rule: &rule,
				}, nil
			}
		}
		return EditableItem{}, ErrNotFound
	}
	return EditableItem{}, ErrNotEditable
}

// editCategory — категория для формы. У поступления её нет: правка категорию
// поступления не меняет (её сохраняет писатель), и форма, отправленная обратно как
// есть, не должна получить отказ «у поступления нет категории».
func editCategory(k Kind, category string) string {
	if k == KindIncome {
		return ""
	}
	return category
}
