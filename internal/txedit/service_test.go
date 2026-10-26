package txedit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/categories"
	"simpleAI/internal/plan"
)

type fakeStore struct {
	row     budget.TxRow
	rows    []budget.TxRow
	gotList *budget.TxListQuery
	gotEdit *budget.TxChange
	gotVer  time.Time
	deleted bool
}

func (f *fakeStore) GetTxView(context.Context, uuid.UUID) (budget.TxRow, error) { return f.row, nil }
func (f *fakeStore) ListTx(_ context.Context, q budget.TxListQuery) ([]budget.TxRow, error) {
	f.gotList = &q
	return f.rows, nil
}
func (f *fakeStore) AroundTx(context.Context, uuid.UUID, int, int) ([]budget.TxRow, error) {
	return f.rows, nil
}
func (f *fakeStore) EditTx(_ context.Context, _ uuid.UUID, v time.Time, _ string, c budget.TxChange) (budget.TxRow, error) {
	f.gotEdit, f.gotVer = &c, v
	return f.row, nil
}
func (f *fakeStore) DeleteTx(_ context.Context, _ uuid.UUID, v time.Time, _ string) error {
	f.deleted, f.gotVer = true, v
	return nil
}

type fakeDir struct {
	cats    map[string]categories.Category
	retired map[uuid.UUID]bool
	gotTyp  string
}

func (d *fakeDir) Resolve(_ context.Context, name, typ string) (categories.Category, bool, error) {
	d.gotTyp = typ
	c, ok := d.cats[name]
	return c, ok, nil
}
func (d *fakeDir) SplitTargets(_ context.Context, id uuid.UUID) ([]categories.SplitTarget, error) {
	if d.retired[id] {
		return []categories.SplitTarget{{}}, nil
	}
	return nil, nil
}

var testVersion = time.Date(2026, 10, 26, 12, 0, 0, 123456000, time.UTC)

func valid(cat string) Input {
	return Input{
		ID: uuid.New().String(), Version: testVersion.Format(time.RFC3339Nano), Description: " шлем ",
		Amount: plan.Money{Minor: 157000, Cur: plan.CurTHB}, Date: plan.Date{Year: 2026, Month: 10, Day: 23}, Category: cat,
	}
}

func setup() (*Service, *fakeStore, *fakeDir) {
	food, retired := uuid.New(), uuid.New()
	st := &fakeStore{row: budget.TxRow{Type: "expense"}}
	dir := &fakeDir{
		cats:    map[string]categories.Category{"Одежда": {ID: food, Name: "Одежда"}, "Еда": {ID: retired, Name: "Еда"}},
		retired: map[uuid.UUID]bool{retired: true},
	}
	return NewService(st, dir), st, dir
}

// Правка передаёт хранилищу ИМЕННО то, что ввёл владелец: валюту, сумму, дату, обрезанное название и версию
// строки. Категория берётся из каталога по типу самой транзакции.
//
// Мутации: не передавать Currency / Version / тип транзакции в Resolve — краснеет соответствующая проверка.
func TestEdit_PassesEverythingToStore(t *testing.T) {
	s, st, dir := setup()
	in := valid("Одежда")
	if _, err := s.Edit(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	c := st.gotEdit
	if c == nil || c.Description != "шлем" || c.AmountMinor != 157000 || c.Currency != "THB" ||
		c.Date.Format("2006-01-02") != "2026-10-23" || c.CategoryID == nil {
		t.Fatalf("правка в хранилище: %+v", c)
	}
	if !st.gotVer.Equal(testVersion) {
		t.Errorf("версия не дошла до хранилища: %v", st.gotVer)
	}
	if dir.gotTyp != "expense" {
		t.Errorf("категория искалась по типу %q, ждали тип транзакции", dir.gotTyp)
	}
}

func TestEdit_EmptyCategoryKeepsCurrent(t *testing.T) {
	s, st, _ := setup()
	if _, err := s.Edit(context.Background(), valid("  ")); err != nil {
		t.Fatal(err)
	}
	if st.gotEdit == nil || st.gotEdit.CategoryID != nil {
		t.Errorf("пустая категория должна оставить прежнюю: %+v", st.gotEdit)
	}
}

// Разделённая категория («Еда») и несуществующая отклоняются до записи.
// Мутация: убрать проверку SplitTargets — краснеет «Еда».
func TestEdit_RejectsRetiredAndUnknownCategory(t *testing.T) {
	s, st, _ := setup()
	for cat, want := range map[string]error{"Еда": ErrCategoryRetired, "Нет такой": ErrUnknownCategory} {
		if _, err := s.Edit(context.Background(), valid(cat)); !errors.Is(err, want) {
			t.Errorf("%q: %v, ждали %v", cat, err, want)
		}
	}
	if st.gotEdit != nil {
		t.Error("отклонённая правка дошла до хранилища")
	}
}

// Мутация: убрать Validate — краснеют пустое название, нулевая сумма, неподдерживаемая валюта.
func TestEdit_Validates(t *testing.T) {
	s, st, _ := setup()
	for name, mutate := range map[string]func(*Input){
		"пустое название": func(i *Input) { i.Description = "  " },
		"нулевая сумма":   func(i *Input) { i.Amount.Minor = 0 },
		"чужая валюта":    func(i *Input) { i.Amount.Cur = "USD" },
		"неверная дата":   func(i *Input) { i.Date = plan.Date{Year: 2026, Month: 13, Day: 1} },
	} {
		in := valid("")
		mutate(&in)
		if _, err := s.Edit(context.Background(), in); err == nil {
			t.Errorf("%s: правка принята", name)
		}
	}
	if st.gotEdit != nil {
		t.Error("невалидная правка дошла до хранилища")
	}
}

func TestDelete_PassesVersion(t *testing.T) {
	s, st, _ := setup()
	v := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	if err := s.Delete(context.Background(), uuid.New().String(), v.Format(time.RFC3339Nano)); err != nil || !st.deleted || !st.gotVer.Equal(v) {
		t.Errorf("удаление: err=%v deleted=%v ver=%v", err, st.deleted, st.gotVer)
	}
}

// Курсор и страница: лишняя строка даёт Next, курсор возвращается в запрос как (дата, id); плохие id, версия и курсор —
// свои ошибки, до хранилища дело не доходит.
//
// Мутации: не запрашивать Limit+1 — пропадает Next; не передавать курсор в запрос — краснеет проверка AfterID.
func TestList_PagingAndBadInput(t *testing.T) {
	s, st, _ := setup()
	mk := func() budget.TxRow {
		return budget.TxRow{ID: uuid.New(), Type: "expense", Currency: "THB", AmountMinor: 100, Date: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), UpdatedAt: testVersion}
	}
	st.rows = []budget.TxRow{mk(), mk(), mk()}
	page, err := s.List(context.Background(), ListQuery{Limit: 2})
	if err != nil || len(page.Rows) != 2 || page.Next == "" {
		t.Fatalf("первая страница: %d строк, next=%q, %v", len(page.Rows), page.Next, err)
	}
	if st.gotList.Limit != 3 {
		t.Errorf("запрошено %d строк, ждали Limit+1=3", st.gotList.Limit)
	}
	if page.Rows[0].Version != testVersion.Format(time.RFC3339Nano) || page.Rows[0].Date != (plan.Date{Year: 2026, Month: 10, Day: 5}) {
		t.Errorf("перевод строки: %+v", page.Rows[0])
	}
	if _, err := s.List(context.Background(), ListQuery{Cursor: page.Next}); err != nil || st.gotList.AfterID == nil || st.gotList.AfterDate == nil {
		t.Errorf("курсор не дошёл до запроса: %v %+v", err, st.gotList)
	}
	if _, err := s.List(context.Background(), ListQuery{Cursor: "мусор"}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("плохой курсор: %v", err)
	}
	in := valid("")
	in.ID = "не-uuid"
	if _, err := s.Edit(context.Background(), in); !errors.Is(err, ErrBadID) {
		t.Errorf("плохой id: %v", err)
	}
	in = valid("")
	in.Version = "вчера"
	if _, err := s.Edit(context.Background(), in); !errors.Is(err, ErrBadVersion) {
		t.Errorf("плохая версия: %v", err)
	}
	if st.gotEdit != nil {
		t.Error("плохой вход дошёл до хранилища")
	}
}
