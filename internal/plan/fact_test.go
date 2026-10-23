package plan

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type factCall struct {
	id string
	e  FactEdit
}

type fakeFactWriter struct {
	updated  []factCall
	reopened []string
	scopes   []Scope
	err      error
}

func (f *fakeFactWriter) UpdateFact(_ context.Context, id string, e FactEdit) error {
	f.updated = append(f.updated, factCall{id, e})
	return f.err
}

func (f *fakeFactWriter) UnsettlePlanned(_ context.Context, sc Scope, id string) error {
	f.reopened = append(f.reopened, id)
	f.scopes = append(f.scopes, sc)
	return f.err
}

func factEvent(key string, origin FactOrigin, ruleID string) Event {
	return Event{Key: key, Kind: KindPayment, State: StateDone, Name: "сотовая связь", Amount: thb(806),
		Date: dp(2026, 3, 8), Source: SourceTransaction, Origin: origin, RuleID: ruleID}
}

func factSvc(fw FactWriter, facts ...Event) *Service {
	snap := baseSnap()
	snap.Payments = append(snap.Payments, facts...)
	return NewService(&fakeReader{snap: snap}, &fakeWriter{}, func() Date { return d(2026, 3, 10) },
		Scope{ChatIDs: []int64{7}}, WithFactWriter(fw))
}

// Права строки выводит одно место, Event.Actions, из вида записи и происхождения факта.
//
// Мутации: убрать проверку IsRecurring у фактов, дать FactWebLoose Reopen или сделать
// нулевое происхождение правимым — соответствующая строка таблицы краснеет.
func TestEvent_Actions(t *testing.T) {
	cases := []struct {
		name string
		e    Event
		want Actions
	}{
		{"плановая запись", Event{Source: SourcePlanned}, Actions{Edit: true}},
		{"срок повтора", Event{Source: SourceRecurring, RuleID: "r"}, Actions{Edit: true}},
		{"факт, закрывший плановую", Event{Source: SourceTransaction, Origin: FactSettled}, Actions{Edit: true, Reopen: true}},
		{"факт экрана без связи", Event{Source: SourceTransaction, Origin: FactWebLoose}, Actions{Edit: true}},
		{"внешний факт", Event{Source: SourceTransaction}, Actions{}},
		{"факт правила повтора", Event{Source: SourceTransaction, Origin: FactSettled, RuleID: "r"}, Actions{}},
		{"неизвестный источник", Event{Source: "x", Origin: FactSettled}, Actions{}},
	}
	for _, c := range cases {
		if got := c.e.Actions(); got != c.want {
			t.Errorf("%s: %+v, ждали %+v", c.name, got, c.want)
		}
	}
}

// Правка закрытой записи доходит до писателя с проверенными значениями; валюта,
// категория и вид не едут. Всё невалидное писателя не достигает.
//
// Мутации: убрать проверку валюты, даты, Actions().Edit или Validate — краснеет
// соответствующий кейс; не обрезать название — краснеет проверка значения.
func TestService_EditFact(t *testing.T) {
	ctx := context.Background()
	okIn := EditInput{Name: "  сотовая связь  ", Amount: thb(900), Place: &MoveInput{Date: dp(2026, 3, 9)}}

	fw := &fakeFactWriter{}
	svc := factSvc(fw, factEvent("transaction:f1", FactSettled, ""))
	if err := svc.Edit(ctx, "transaction:f1", okIn); err != nil {
		t.Fatalf("правка: %v", err)
	}
	want := factCall{"f1", FactEdit{Name: "сотовая связь", Amount: *thb(900), Date: d(2026, 3, 9)}}
	if len(fw.updated) != 1 || fw.updated[0] != want {
		t.Fatalf("писателю ушло %+v, ждали %+v", fw.updated, want)
	}

	cases := []struct {
		name string
		key  string
		in   EditInput
		want error
	}{
		{"внешний факт", "transaction:ext", okIn, ErrNotEditable},
		{"факт правила", "transaction:rule", okIn, ErrNotEditable},
		{"нет факта", "transaction:нет", okIn, ErrNotFound},
		{"не факт", "planned:p1", EditInput{Name: "x", Amount: rub(1)}, ErrNotFound},
		{"нет суммы", "transaction:f1", EditInput{Name: "x", Place: okIn.Place}, ErrAmountRequired},
		{"другая валюта", "transaction:f1", EditInput{Name: "x", Amount: rub(900), Place: okIn.Place}, ErrCurrencyLocked},
		{"нет даты", "transaction:f1", EditInput{Name: "x", Amount: thb(1)}, ErrFactDateRequired},
		{"дата пустая", "transaction:f1", EditInput{Name: "x", Amount: thb(1), Place: &MoveInput{}}, ErrFactDateRequired},
		{"окно вместо даты", "transaction:f1", EditInput{Name: "x", Amount: thb(1), Place: &MoveInput{Date: dp(2026, 3, 9), Window: WindowNext}}, ErrInvalidEvent},
		{"битая дата", "transaction:f1", EditInput{Name: "x", Amount: thb(1), Place: &MoveInput{Date: dp(2026, 2, 30)}}, ErrBadDate},
		{"без названия", "transaction:f1", EditInput{Name: " ", Amount: thb(1), Place: okIn.Place}, ErrNameRequired},
		{"длинное название", "transaction:f1", EditInput{Name: strings.Repeat("я", maxNameRunes+1), Amount: thb(1), Place: okIn.Place}, ErrNameTooLong},
		{"нулевая сумма", "transaction:f1", EditInput{Name: "x", Amount: &Money{Cur: CurTHB}, Place: okIn.Place}, ErrAmountNonPositive},
		{"лишние цифры", "transaction:f1", EditInput{Name: "x", Amount: &Money{Minor: maxAmountMinor + 1, Cur: CurTHB}, Place: okIn.Place}, ErrAmountTooLarge},
	}
	for _, c := range cases {
		fw := &fakeFactWriter{}
		svc := factSvc(fw, factEvent("transaction:f1", FactSettled, ""), factEvent("transaction:ext", FactExternal, ""),
			factEvent("transaction:rule", FactSettled, "rent"))
		if err := svc.Edit(ctx, c.key, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, ждали %v", c.name, err, c.want)
		}
		if len(fw.updated) != 0 {
			t.Errorf("%s: невалидная правка дошла до писателя", c.name)
		}
	}

	// Ошибка писателя доходит до вызывающего без подмены.
	fw = &fakeFactWriter{err: ErrNotFound}
	if err := factSvc(fw, factEvent("transaction:f1", FactSettled, "")).Edit(ctx, "transaction:f1", okIn); !errors.Is(err, ErrNotFound) {
		t.Errorf("ответ писателя потерян: %v", err)
	}
	// Без подключённого писателя фактов правка отключена, а не падает.
	noFW := NewService(&fakeReader{snap: Snapshot{Payments: []Event{factEvent("transaction:f1", FactSettled, "")}}}, &fakeWriter{},
		func() Date { return d(2026, 3, 10) }, Scope{ChatIDs: []int64{7}})
	if err := noFW.Edit(ctx, "transaction:f1", okIn); !errors.Is(err, ErrNoFactWriter) {
		t.Errorf("без писателя фактов: %v", err)
	}
}

// Форма «изменить» у закрытой записи получает значения факта в его валюте.
func TestService_ItemFact(t *testing.T) {
	svc := factSvc(&fakeFactWriter{}, factEvent("transaction:f1", FactWebLoose, ""), factEvent("transaction:ext", FactExternal, ""))
	it, err := svc.Item(context.Background(), "transaction:f1")
	if err != nil {
		t.Fatalf("Item: %v", err)
	}
	if !it.Fact || it.Name != "сотовая связь" || it.Amount == nil || *it.Amount != *thb(806) || it.Date == nil || *it.Date != d(2026, 3, 8) {
		t.Errorf("форма факта: %+v", it)
	}
	if _, err := svc.Item(context.Background(), "transaction:ext"); !errors.Is(err, ErrNotEditable) {
		t.Errorf("внешний факт открылся для правки: %v", err)
	}
}

// Возврат в план: только факт, закрывший плановую запись; писатель получает область
// чатов сервиса и id без префикса.
//
// Мутации: убрать проверку Actions().Reopen, не передавать область или не отрезать
// префикс — краснеет соответствующий кейс.
func TestService_Reopen(t *testing.T) {
	ctx := context.Background()
	fw := &fakeFactWriter{}
	svc := factSvc(fw, factEvent("transaction:f1", FactSettled, ""), factEvent("transaction:loose", FactWebLoose, ""),
		factEvent("transaction:ext", FactExternal, ""), factEvent("transaction:rule", FactSettled, "rent"))
	if err := svc.Reopen(ctx, "transaction:f1"); err != nil {
		t.Fatalf("возврат: %v", err)
	}
	if len(fw.reopened) != 1 || fw.reopened[0] != "f1" || fw.scopes[0].ChatIDs[0] != 7 {
		t.Errorf("писателю ушло %v в области %v", fw.reopened, fw.scopes)
	}
	for key, want := range map[string]error{
		"transaction:loose": ErrNotReopenable, "transaction:ext": ErrNotReopenable, "transaction:rule": ErrNotReopenable,
		"transaction:нет": ErrNotFound, "planned:p1": ErrNotEditable,
	} {
		fw := &fakeFactWriter{}
		svc := factSvc(fw, factEvent("transaction:loose", FactWebLoose, ""), factEvent("transaction:ext", FactExternal, ""),
			factEvent("transaction:rule", FactSettled, "rent"))
		if err := svc.Reopen(ctx, key); !errors.Is(err, want) {
			t.Errorf("%s: %v, ждали %v", key, err, want)
		}
		if len(fw.reopened) != 0 {
			t.Errorf("%s: отказ дошёл до писателя", key)
		}
	}
	noFW := NewService(&fakeReader{snap: Snapshot{Payments: []Event{factEvent("transaction:f1", FactSettled, "")}}}, &fakeWriter{},
		func() Date { return d(2026, 3, 10) }, Scope{ChatIDs: []int64{7}})
	if err := noFW.Reopen(ctx, "transaction:f1"); !errors.Is(err, ErrNoFactWriter) {
		t.Errorf("без писателя фактов: %v", err)
	}
}

// Строки экрана несут права, выведенные доменом: фронт ничего не угадывает по ключу.
func TestView_RowsCarryActions(t *testing.T) {
	snap := baseSnap()
	settled := factEvent("transaction:settled", FactSettled, "")
	ext := factEvent("transaction:ext", FactExternal, "")
	// Закрытые в прошлые дни в ленту не идут: берём факты сегодняшнего числа.
	settled.Date, ext.Date = dp(2026, 3, 10), dp(2026, 3, 10)
	snap.Payments = append(snap.Payments, settled, ext)
	res, err := NewService(&fakeReader{snap: snap}, nil, func() Date { return d(2026, 3, 10) }, Scope{ChatIDs: []int64{7}}).View(context.Background(), CurTHB)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, r := range res.View.Timeline {
		switch r.Key {
		case "transaction:settled":
			seen++
			if !r.Editable || !r.Reopenable {
				t.Errorf("закрытая с планом: %+v", r)
			}
		case "transaction:ext":
			seen++
			if r.Editable || r.Reopenable {
				t.Errorf("внешний факт получил права: %+v", r)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("в ленте найдено %d из 2 фактов", seen)
	}
}

func TestFactEdit_Validate(t *testing.T) {
	good := FactEdit{Name: "x", Amount: *thb(1), Date: d(2026, 3, 9)}
	if err := good.Validate(); err != nil {
		t.Fatalf("валидная правка: %v", err)
	}
	bad := good
	bad.Amount.Cur = "USD"
	if err := bad.Validate(); !errors.Is(err, ErrUnsupportedCurrency) {
		t.Errorf("валюта: %v", err)
	}
}
