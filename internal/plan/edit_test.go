package plan

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Заглушки, чтобы общий fakeWriter оставался Writer; запись правок — в editWriter.
func (f *fakeWriter) UpdatePlanned(context.Context, Scope, string, PlannedEdit) error { return f.err }
func (f *fakeWriter) UpdateRule(context.Context, Scope, string, RuleEdit) error       { return f.err }

type plannedCall struct {
	id string
	e  PlannedEdit
}

type ruleCall struct {
	id string
	e  RuleEdit
}

type editWriter struct {
	fakeWriter
	planned []plannedCall
	rules   []ruleCall
}

func (f *editWriter) UpdatePlanned(_ context.Context, _ Scope, id string, e PlannedEdit) error {
	f.planned = append(f.planned, plannedCall{id, e})
	return f.err
}

func (f *editWriter) UpdateRule(_ context.Context, _ Scope, id string, e RuleEdit) error {
	f.rules = append(f.rules, ruleCall{id, e})
	return f.err
}

func salaryRule() RuleWithExceptions {
	return RuleWithExceptions{
		ID: "salary", Name: "зарплата", Category: "Зарплата", Kind: KindIncome,
		Rule: Rule{Anchor: d(2026, 3, 25), N: 2, Unit: UnitWeek, Amount: *rub(50000)},
	}
}

// editSnap: окно 05.03–18.03; плановые платёж с датой, платёж в окне, поступление;
// правила аренды (расход, баты) и зарплаты (поступление).
func editSnap() Snapshot {
	s := baseSnap()
	s.Payments = append(s.Payments,
		Event{Key: "planned:p1", Kind: KindPayment, State: StatePlanned, Name: "врач", Amount: rub(500), Date: dp(2026, 3, 15), Category: "Здоровье", Source: SourcePlanned},
		Event{Key: "planned:p2", Kind: KindPayment, State: StatePlanned, Name: "права", WindowStart: dp(2026, 3, 5), Source: SourcePlanned},
	)
	s.Incomes = append(s.Incomes,
		Event{Key: "planned:i1", Kind: KindIncome, State: StatePlanned, Name: "аванс", Amount: rub(30000), Date: dp(2026, 3, 20), Source: SourcePlanned})
	s.Rules = append(s.Rules, salaryRule())
	return s
}

func editSvc(w Writer) *Service {
	return NewService(&fakeReader{snap: editSnap()}, w, func() Date { return d(2026, 3, 10) }, Scope{ChatIDs: []int64{42}})
}

// Форма получает запись в исходной валюте; ключ срока ведёт к правилу целиком.
//
// Мутация: в findItem не отрезать «@срок» у ключа повтора — тест краснеет.
func TestService_Item(t *testing.T) {
	s := editSvc(&editWriter{})
	ok := []struct {
		key, wantKey, name string
		recurring          bool
		amount             *Money
	}{
		{"planned:p1", "planned:p1", "врач", false, rub(500)},
		{"planned:p2", "planned:p2", "права", false, nil},
		{"planned:i1", "planned:i1", "аванс", false, rub(30000)},
		{"recurring:rent@2026-03-15", "recurring:rent", "аренда", true, thb(18000)},
		{"recurring:rent", "recurring:rent", "аренда", true, thb(18000)},
	}
	for _, c := range ok {
		it, err := s.Item(context.Background(), c.key)
		if err != nil {
			t.Fatalf("%s: %v", c.key, err)
		}
		if it.Key != c.wantKey || it.Name != c.name || it.Recurring != c.recurring || (it.Amount == nil) != (c.amount == nil) ||
			(it.Amount != nil && *it.Amount != *c.amount) || (it.Rule != nil) != c.recurring {
			t.Errorf("%s: %+v", c.key, it)
		}
	}
	if it, err := s.Item(context.Background(), "planned:p1"); err != nil || it.Category != "Здоровье" || it.Date == nil || *it.Date != d(2026, 3, 15) {
		t.Errorf("категория и дата: %+v %v", it, err)
	}
	bad := map[string]error{
		"transaction:food":       ErrNotEditable,
		"":                       ErrNotEditable,
		"recurring:@2026-03-15":  ErrNotEditable,
		"planned:нет":            ErrNotFound,
		"recurring:нет@2026-3-1": ErrNotFound,
		"planned:in":             ErrNotFound, // ключ факта под видом плановой записи
	}
	for key, want := range bad {
		if _, err := s.Item(context.Background(), key); !errors.Is(err, want) {
			t.Errorf("%q: %v, ждали %v", key, err, want)
		}
	}
	if _, err := svc(&fakeReader{snap: editSnap()}, d(2026, 3, 10)).Item(context.Background(), "planned:p1"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("без писателя: %v", err)
	}
}

// Форму можно отправить обратно как есть: заглушка имени не становится названием,
// категории поступления в форме нет (писатель её сохраняет).
//
// Мутации: убрать замену UnnamedName на пусто или editCategory — тест краснеет.
func TestService_Item_FormRoundTrip(t *testing.T) {
	snap := editSnap()
	snap.Payments = append(snap.Payments, Event{Key: "planned:p3", Kind: KindPayment, State: StatePlanned, Name: UnnamedName, Source: SourcePlanned})
	snap.Incomes = append(snap.Incomes, Event{Key: "planned:i2", Kind: KindIncome, State: StatePlanned, Name: "бонус", Amount: rub(100), Date: dp(2026, 3, 21), Category: "Зарплата", Source: SourcePlanned})
	s := NewService(&fakeReader{snap: snap}, &editWriter{}, func() Date { return d(2026, 3, 10) }, Scope{ChatIDs: []int64{42}})
	if it, err := s.Item(context.Background(), "planned:p3"); err != nil || it.Name != "" {
		t.Errorf("заглушка имени попала в форму: %+v %v", it, err)
	}
	for _, key := range []string{"planned:i2", "recurring:salary"} {
		it, err := s.Item(context.Background(), key)
		if err != nil || it.Category != "" {
			t.Fatalf("%s: категория поступления в форме: %+v %v", key, it, err)
		}
		if err := s.Edit(context.Background(), key, EditInput{Name: it.Name, Amount: it.Amount, Category: it.Category}); err != nil {
			t.Errorf("%s: форма как есть отклонена: %v", key, err)
		}
	}
}

// Правка разовой записи: вид и текущее размещение — из снимка; без Place дата и
// окно не пишутся (SetPlace=false), с Place — считаются тем же placement, что у Move.
//
// Мутация: SetPlace: true всегда, или Place не через placement (окно не в дату
// начала) — тест краснеет.
func TestService_EditPlanned(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		in       EditInput
		setPlace bool
		date, ws *Date
		kind     Kind
	}{
		{"переименовать, место не трогать", "planned:p1",
			EditInput{Name: "  стоматолог ", Amount: rub(700), Category: " Здоровье "}, false, dp(2026, 3, 15), nil, KindPayment},
		{"на другую дату", "planned:p1",
			EditInput{Name: "врач", Amount: rub(500), Place: &MoveInput{Date: dp(2026, 3, 16)}}, true, dp(2026, 3, 16), nil, KindPayment},
		{"в это окно", "planned:p1",
			EditInput{Name: "врач", Amount: thb(200), Place: &MoveInput{Window: WindowCurrent}}, true, nil, dp(2026, 3, 5), KindPayment},
		// Следующее окно открывает ожидаемый аванс 20.03.
		{"в следующее окно", "planned:p2",
			EditInput{Name: "права", Place: &MoveInput{Window: WindowNext}}, true, nil, dp(2026, 3, 20), KindPayment},
		{"без даты", "planned:p1",
			EditInput{Name: "врач", Place: &MoveInput{}}, true, nil, nil, KindPayment},
		{"сумму стереть — напоминание", "planned:p1",
			EditInput{Name: "врач"}, false, dp(2026, 3, 15), nil, KindPayment},
		{"поступление: сумма, дата та же", "planned:i1",
			EditInput{Name: "аванс", Amount: thb(12000)}, false, dp(2026, 3, 20), nil, KindIncome},
		{"поступление на другую дату", "planned:i1",
			EditInput{Name: "аванс", Amount: rub(30000), Place: &MoveInput{Date: dp(2026, 3, 21)}}, true, dp(2026, 3, 21), nil, KindIncome},
	}
	for _, c := range cases {
		w := &editWriter{}
		if err := editSvc(w).Edit(context.Background(), c.key, c.in); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(w.planned) != 1 || len(w.rules) != 0 {
			t.Fatalf("%s: вызовы %+v %+v", c.name, w.planned, w.rules)
		}
		got := w.planned[0]
		n := got.e.Item
		if got.id != strings.TrimPrefix(c.key, "planned:") || got.e.SetPlace != c.setPlace || n.Kind != c.kind ||
			n.Name != strings.TrimSpace(c.in.Name) || n.Category != strings.TrimSpace(c.in.Category) || n.Amount != c.in.Amount {
			t.Errorf("%s: %+v", c.name, got)
		}
		if !sameDate(n.Date, c.date) || !sameDate(n.WindowStart, c.ws) {
			t.Errorf("%s: дата %v окно %v, ждали %v %v", c.name, n.Date, n.WindowStart, c.date, c.ws)
		}
	}
}

func sameDate(a, b *Date) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// Невалидная правка до писателя не доходит; ответ писателя «нет записи» не теряется.
//
// Мутация: убрать n.Validate() в editPlanned — тест краснеет.
func TestService_EditPlanned_Rejects(t *testing.T) {
	cases := []struct {
		name string
		key  string
		in   EditInput
		want error
	}{
		{"поступление в окно", "planned:i1", EditInput{Name: "аванс", Amount: rub(1), Place: &MoveInput{Window: WindowCurrent}}, ErrInvalidEvent},
		{"поступление без даты", "planned:i1", EditInput{Name: "аванс", Amount: rub(1), Place: &MoveInput{}}, ErrIncomeNoDate},
		{"поступление без суммы", "planned:i1", EditInput{Name: "аванс"}, ErrAmountRequired},
		{"категория у поступления", "planned:i1", EditInput{Name: "аванс", Amount: rub(1), Category: "Еда"}, ErrInvalidEvent},
		{"без названия", "planned:p1", EditInput{Name: "  ", Amount: rub(1)}, ErrNameRequired},
		{"длинное название", "planned:p1", EditInput{Name: strings.Repeat("я", maxNameRunes+1)}, ErrNameTooLong},
		{"лишние цифры", "planned:p1", EditInput{Name: "x", Amount: &Money{Minor: maxAmountMinor + 1, Cur: CurRUB}}, ErrAmountTooLarge},
		{"нулевая сумма", "planned:p1", EditInput{Name: "x", Amount: &Money{Cur: CurRUB}}, ErrAmountNonPositive},
		{"чужая валюта", "planned:p1", EditInput{Name: "x", Amount: &Money{Minor: 1, Cur: "USD"}}, ErrUnsupportedCurrency},
		{"битая дата", "planned:p1", EditInput{Name: "x", Place: &MoveInput{Date: dp(2026, 2, 30)}}, ErrBadDate},
		{"неизвестное окно", "planned:p1", EditInput{Name: "x", Place: &MoveInput{Window: "past"}}, ErrInvalidEvent},
		{"дата и окно вместе", "planned:p1", EditInput{Name: "x", Place: &MoveInput{Date: dp(2026, 3, 16), Window: WindowNext}}, ErrInvalidEvent},
		{"факт", "transaction:food", EditInput{Name: "x", Amount: rub(1)}, ErrNotEditable},
		{"нет записи", "planned:нет", EditInput{Name: "x"}, ErrNotFound},
	}
	for _, c := range cases {
		w := &editWriter{}
		if err := editSvc(w).Edit(context.Background(), c.key, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, ждали %v", c.name, err, c.want)
		}
		if len(w.planned)+len(w.rules) != 0 {
			t.Errorf("%s: невалидная правка дошла до писателя", c.name)
		}
	}
	w := &editWriter{fakeWriter: fakeWriter{err: ErrNotFound}}
	if err := editSvc(w).Edit(context.Background(), "planned:p1", EditInput{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("ответ писателя потерян: %v", err)
	}
	if err := svc(&fakeReader{snap: editSnap()}, d(2026, 3, 10)).Edit(context.Background(), "planned:p1", EditInput{Name: "x"}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("без писателя: %v", err)
	}
}

// Правка правила: правится правило целиком (id без срока), вид — из снимка;
// категория поступления не трогается.
func TestService_EditRule(t *testing.T) {
	cases := []struct {
		name, key string
		in        EditInput
		want      RuleEdit
		id        string
	}{
		{"аренда по ключу срока", "recurring:rent@2026-03-15",
			EditInput{Name: " аренда дома ", Amount: thb(20000), Category: " Жильё "},
			RuleEdit{Kind: KindPayment, Name: "аренда дома", Amount: *thb(20000), Category: "Жильё"}, "rent"},
		{"зарплата в другой валюте", "recurring:salary@2026-03-25",
			EditInput{Name: "зарплата", Amount: thb(25000)},
			RuleEdit{Kind: KindIncome, Name: "зарплата", Amount: *thb(25000)}, "salary"},
	}
	for _, c := range cases {
		w := &editWriter{}
		if err := editSvc(w).Edit(context.Background(), c.key, c.in); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(w.rules) != 1 || len(w.planned) != 0 || w.rules[0].id != c.id || w.rules[0].e != c.want {
			t.Errorf("%s: %+v %+v", c.name, w.rules, w.planned)
		}
	}
}

// Мутация: убрать потолок суммы или запрет даты у правила — тест краснеет.
func TestService_EditRule_Rejects(t *testing.T) {
	cases := []struct {
		name, key string
		in        EditInput
		want      error
	}{
		{"дата у повтора", "recurring:rent@2026-03-15", EditInput{Name: "аренда", Amount: thb(1), Place: &MoveInput{Date: dp(2026, 3, 16)}}, ErrRuleSchedule},
		{"«без даты» у повтора", "recurring:rent", EditInput{Name: "аренда", Amount: thb(1), Place: &MoveInput{}}, ErrRuleSchedule},
		{"без суммы", "recurring:rent", EditInput{Name: "аренда"}, ErrAmountRequired},
		{"нулевая сумма", "recurring:rent", EditInput{Name: "аренда", Amount: &Money{Cur: CurTHB}}, ErrAmountNonPositive},
		{"лишние цифры", "recurring:rent", EditInput{Name: "аренда", Amount: &Money{Minor: maxAmountMinor + 1, Cur: CurTHB}}, ErrAmountTooLarge},
		{"чужая валюта", "recurring:rent", EditInput{Name: "аренда", Amount: &Money{Minor: 1, Cur: "USD"}}, ErrUnsupportedCurrency},
		{"без названия", "recurring:rent", EditInput{Name: " ", Amount: thb(1)}, ErrNameRequired},
		{"длинная категория", "recurring:rent", EditInput{Name: "x", Amount: thb(1), Category: strings.Repeat("я", maxCategoryRunes+1)}, ErrNameTooLong},
		{"категория у поступления", "recurring:salary", EditInput{Name: "x", Amount: rub(1), Category: "Еда"}, ErrInvalidEvent},
		{"нет правила", "recurring:нет@2026-03-15", EditInput{Name: "x", Amount: rub(1)}, ErrNotFound},
	}
	for _, c := range cases {
		w := &editWriter{}
		if err := editSvc(w).Edit(context.Background(), c.key, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, ждали %v", c.name, err, c.want)
		}
		if len(w.planned)+len(w.rules) != 0 {
			t.Errorf("%s: невалидная правка дошла до писателя", c.name)
		}
	}
}

// memPlan — снимок, который правка правила меняет на месте: проверка R3c от
// команды до расчёта.
type memPlan struct {
	editWriter
	snap Snapshot
}

func (m *memPlan) Read(context.Context, Scope) (Snapshot, error) { return m.snap, nil }

func (m *memPlan) UpdateRule(_ context.Context, _ Scope, id string, e RuleEdit) error {
	for i := range m.snap.Rules {
		if m.snap.Rules[i].ID == id {
			m.snap.Rules[i].Name, m.snap.Rules[i].Rule.Amount = e.Name, e.Amount
			return nil
		}
	}
	return ErrNotFound
}

// R3c: новая сумма правила ложится на неисполненный срок, даже прошедший (аренда
// 15.03 при «сегодня» 17.03), а факт прошлого срока остаётся прежним.
func TestService_EditRule_AppliesToUnexecutedTerms(t *testing.T) {
	m := &memPlan{snap: baseSnap()}
	fact := Event{Key: "transaction:rent-feb", Kind: KindPayment, State: StateDone, Name: "аренда", Amount: thb(18000),
		Date: dp(2026, 2, 15), Source: SourceTransaction, RuleID: "rent", ScheduledFor: dp(2026, 2, 15)}
	m.snap.Payments = append(m.snap.Payments, fact)
	s := NewService(m, m, func() Date { return d(2026, 3, 17) }, Scope{ChatIDs: []int64{42}})
	before, err := s.View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Edit(context.Background(), "recurring:rent@2026-03-15", EditInput{Name: "аренда", Amount: thb(20000)}); err != nil {
		t.Fatal(err)
	}
	after, err := s.View(context.Background(), CurRUB)
	if err != nil {
		t.Fatal(err)
	}
	if before.View.Payments != 45000 || after.View.Payments != 50000 {
		t.Errorf("платежи окна до %d после %d; ждали 45000 → 50000", before.View.Payments, after.View.Payments)
	}
	for _, e := range m.snap.Payments {
		if e.Key == fact.Key && *e.Amount != *thb(18000) {
			t.Errorf("факт прошлого срока изменился: %+v", e)
		}
	}
}

// Вид ожидаемого прихода (ADR-019): доходит до писателя как есть, nil — не менять; у расхода и у повтора отказ до
// писателя; форма получает текущий вид.
//
// Мутации: не передавать OpensWindow в PlannedEdit; убрать проверку Kind — краснеют проверки.
func TestService_EditPlanned_OpensWindow(t *testing.T) {
	f := false
	w := &editWriter{}
	if err := editSvc(w).Edit(context.Background(), "planned:i1", EditInput{Name: "байк", Amount: rub(100), OpensWindow: &f}); err != nil {
		t.Fatal(err)
	}
	if len(w.planned) != 1 || w.planned[0].e.OpensWindow == nil || *w.planned[0].e.OpensWindow {
		t.Fatalf("вид не дошёл до писателя: %+v", w.planned)
	}
	w = &editWriter{}
	if err := editSvc(w).Edit(context.Background(), "planned:i1", EditInput{Name: "аванс", Amount: rub(100)}); err != nil || w.planned[0].e.OpensWindow != nil {
		t.Errorf("без вида: %v %+v", err, w.planned)
	}
	w = &editWriter{}
	if err := editSvc(w).Edit(context.Background(), "planned:p1", EditInput{Name: "врач", OpensWindow: &f}); !errors.Is(err, ErrKindNotIncome) || len(w.planned) != 0 {
		t.Errorf("расход с видом прихода: %v, вызовов %d", err, len(w.planned))
	}
	if it, err := editSvc(&editWriter{}).Item(context.Background(), "planned:i1"); err != nil || !it.OpensWindow {
		t.Errorf("форма: OpensWindow=%v %v, ждали true (по умолчанию открывает)", it.OpensWindow, err)
	}
	// Обычный приход (JoinsWindow) в форме показывается как «не открывает окно».
	snap := editSnap()
	snap.Incomes = append(snap.Incomes, Event{Key: "planned:i9", Kind: KindIncome, State: StatePlanned, Name: "байк", Amount: rub(100),
		Date: dp(2026, 3, 21), Source: SourcePlanned, JoinsWindow: true})
	s2 := NewService(&fakeReader{snap: snap}, &editWriter{}, func() Date { return d(2026, 3, 10) }, Scope{ChatIDs: []int64{42}})
	if it, err := s2.Item(context.Background(), "planned:i9"); err != nil || it.OpensWindow {
		t.Errorf("обычный приход в форме: OpensWindow=%v %v, ждали false", it.OpensWindow, err)
	}
}
