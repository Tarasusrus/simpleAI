// Package txedit — просмотр, правка и удаление любой транзакции со страницы «Транзакции» (ADR-018).
//
// Один путь для всех транзакций, независимо от того, кто их создал. Пакет переводит значения между страницей и
// хранилищем (деньги, даты, версия, курсор), проверяет вход и выбирает категорию; атомарность, версия строки и
// журнал tx_edit_log живут в хранилище (budget.Store).
package txedit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
	"simpleAI/internal/categories"
	"simpleAI/internal/plan"
)

var (
	// ErrUnknownCategory — категории с таким именем нет: страница выбирает из существующих, новых не создаёт.
	ErrUnknownCategory = errors.New("txedit: такой категории нет")
	// ErrCategoryRetired — категория вытеснена разделением («Еда», ADR-013): выбирать её нельзя.
	ErrCategoryRetired = errors.New("txedit: категория разделена на другие, выберите одну из них")
	// ErrBadID, ErrBadVersion, ErrBadCursor — id, версия или курсор не разобрались: запрос неверный, а не «нет данных».
	ErrBadID      = errors.New("txedit: неверный id транзакции")
	ErrBadVersion = errors.New("txedit: неверная версия строки")
	ErrBadCursor  = errors.New("txedit: неверный курсор")
	// ErrNotIncome — вид «открывает окно» есть только у прихода.
	ErrNotIncome = errors.New("txedit: вид «открывает окно» есть только у прихода")
	// ErrNotFound и ErrConflict — транзакции нет / строку изменили после чтения (из хранилища).
	ErrNotFound = budget.ErrTxNotFound
	ErrConflict = budget.ErrTxConflict
)

// DefaultPage — размер страницы списка; AroundBefore/AroundAfter — окно вокруг строки для ссылки focus.
const (
	DefaultPage  = 30
	MaxPage      = 100
	AroundBefore = 5
	AroundAfter  = 20
)

// Store — то, что сервис берёт у хранилища транзакций (реализует *budget.Store).
type Store interface {
	GetTxView(ctx context.Context, id uuid.UUID) (budget.TxRow, error)
	ListTx(ctx context.Context, q budget.TxListQuery) ([]budget.TxRow, error)
	AroundTx(ctx context.Context, id uuid.UUID, before, after int) ([]budget.TxRow, error)
	EditTx(ctx context.Context, id uuid.UUID, version time.Time, source string, c budget.TxChange) (budget.TxRow, error)
	DeleteTx(ctx context.Context, id uuid.UUID, version time.Time, source string) error
}

// Directory — то, что сервис берёт у каталога категорий (реализует *categories.Store).
type Directory interface {
	Resolve(ctx context.Context, name, typ string) (categories.Category, bool, error)
	SplitTargets(ctx context.Context, source uuid.UUID) ([]categories.SplitTarget, error)
}

// Tx — транзакция для страницы: деньги и дата в доменных типах, Version — непрозрачная версия строки,
// которую страница возвращает при правке и удалении.
type Tx struct {
	ID          string
	Kind        string // "income" | "expense"
	Amount      plan.Money
	Category    string
	Description string
	Date        plan.Date
	Version     string
	Linked      bool // закрывает плановую запись: удаление откроет её заново
	FromRule    bool // создана правилом повтора
	AddedBy     string
	// OpensWindow — только у прихода (Kind=income): зарплата/аванс открывают окно, обычный приход нет (ADR-019).
	OpensWindow bool
}

// Page — страница списка. Next — курсор следующей (более старой) страницы, пусто — конец.
type Page struct {
	Rows []Tx
	Next string
}

// Service просматривает, правит и удаляет транзакции.
type Service struct {
	store Store
	dir   Directory
}

func NewService(store Store, dir Directory) *Service { return &Service{store: store, dir: dir} }

// Input — правка. Version — версия, которую страница получила вместе со строкой. Category пустая — категория
// остаётся как есть. Сумма и валюта идут вместе: сменить валюту можно только с новой суммой.
type Input struct {
	ID          string
	Version     string
	Description string
	Amount      plan.Money
	Date        plan.Date
	Category    string
	// OpensWindow — вид прихода: nil — не менять. У расхода значение отклоняется: смысла у него нет.
	OpensWindow *bool
}

// ListQuery — фильтры и продолжение списка.
type ListQuery struct {
	Text, Category string
	From, To       *plan.Date
	Cursor         string
	Limit          int
}

// Get возвращает одну транзакцию.
func (s *Service) Get(ctx context.Context, id string) (Tx, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return Tx{}, ErrBadID
	}
	row, err := s.store.GetTxView(ctx, u)
	if err != nil {
		return Tx{}, err
	}
	return toTx(row), nil
}

// List возвращает страницу списка: дата по убыванию, при равных датах id по возрастанию.
func (s *Service) List(ctx context.Context, q ListQuery) (Page, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultPage
	}
	limit = min(limit, MaxPage)
	bq := budget.TxListQuery{
		Text: strings.TrimSpace(q.Text), Category: strings.TrimSpace(q.Category),
		From: dbDate(q.From), To: dbDate(q.To), Limit: limit + 1, // лишняя строка — признак следующей страницы
	}
	if q.Cursor != "" {
		d, id, err := parseCursor(q.Cursor)
		if err != nil {
			return Page{}, err
		}
		bq.AfterDate, bq.AfterID = &d, &id
	}
	rows, err := s.store.ListTx(ctx, bq)
	if err != nil {
		return Page{}, err
	}
	page := Page{}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.Next = formatCursor(last.Date, last.ID)
	}
	for _, r := range rows {
		page.Rows = append(page.Rows, toTx(r))
	}
	return page, nil
}

// Around возвращает окно вокруг транзакции для ссылки /transactions?focus=<id>.
func (s *Service) Around(ctx context.Context, id string) (Page, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return Page{}, ErrBadID
	}
	rows, err := s.store.AroundTx(ctx, u, AroundBefore, AroundAfter)
	if err != nil {
		return Page{}, err
	}
	page := Page{}
	for _, r := range rows {
		page.Rows = append(page.Rows, toTx(r))
	}
	// Более старые строки продолжаются после окна, если их набралось ровно AroundAfter: считаем от самой строки,
	// а не от длины окна, потому что у верха списка более новых строк меньше AroundBefore.
	for i, r := range rows {
		if r.ID == u && len(rows)-1-i == AroundAfter {
			last := rows[len(rows)-1]
			page.Next = formatCursor(last.Date, last.ID)
		}
	}
	return page, nil
}

// Edit проверяет значения, выбирает категорию и записывает правку; возвращает строку после правки.
func (s *Service) Edit(ctx context.Context, in Input) (Tx, error) {
	id, version, err := parseIDVersion(in.ID, in.Version)
	if err != nil {
		return Tx{}, err
	}
	if err := (plan.FactEdit{Name: in.Description, Amount: in.Amount, Date: in.Date}).Validate(); err != nil {
		return Tx{}, err
	}
	cur, err := s.store.GetTxView(ctx, id)
	if err != nil {
		return Tx{}, err
	}
	if in.OpensWindow != nil && cur.Type != "income" {
		return Tx{}, ErrNotIncome
	}
	var catID *uuid.UUID
	if name := strings.TrimSpace(in.Category); name != "" {
		c, err := s.pickCategory(ctx, name, cur.Type)
		if err != nil {
			return Tx{}, err
		}
		catID = &c
	}
	if _, err := s.store.EditTx(ctx, id, version, budget.TxSourceWeb, budget.TxChange{
		Description: strings.TrimSpace(in.Description),
		AmountMinor: in.Amount.Minor,
		Currency:    string(in.Amount.Cur),
		Date:        *dbDate(&in.Date),
		CategoryID:  catID,
		OpensWindow: in.OpensWindow,
	}); err != nil {
		return Tx{}, err
	}
	return s.Get(ctx, in.ID)
}

// Delete удаляет транзакцию; версия защищает от удаления строки, которую успели изменить.
func (s *Service) Delete(ctx context.Context, id, version string) error {
	u, v, err := parseIDVersion(id, version)
	if err != nil {
		return err
	}
	return s.store.DeleteTx(ctx, u, v, budget.TxSourceWeb)
}

func (s *Service) pickCategory(ctx context.Context, name, typ string) (uuid.UUID, error) {
	cat, found, err := s.dir.Resolve(ctx, name, typ)
	if err != nil {
		return uuid.Nil, fmt.Errorf("txedit: категория: %w", err)
	}
	if !found {
		return uuid.Nil, ErrUnknownCategory
	}
	targets, err := s.dir.SplitTargets(ctx, cat.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("txedit: категория: %w", err)
	}
	if len(targets) > 0 {
		return uuid.Nil, ErrCategoryRetired
	}
	return cat.ID, nil
}

func parseIDVersion(id, version string) (uuid.UUID, time.Time, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, time.Time{}, ErrBadID
	}
	v, err := time.Parse(time.RFC3339Nano, version)
	if err != nil {
		return uuid.Nil, time.Time{}, ErrBadVersion
	}
	return u, v, nil
}

func toTx(r budget.TxRow) Tx {
	return Tx{
		ID: r.ID.String(), Kind: r.Type,
		Amount:   plan.Money{Minor: r.AmountMinor, Cur: plan.Currency(r.Currency)},
		Category: r.CategoryName, Description: r.Description,
		Date:    plan.DateOf(r.Date, time.UTC),
		Version: r.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Linked:  r.PlannedID != nil, FromRule: r.RecurringID != nil, AddedBy: r.AddedBy, OpensWindow: r.OpensWindow,
	}
}

func dbDate(d *plan.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
	return &t
}

func formatCursor(d time.Time, id uuid.UUID) string {
	return d.Format("2006-01-02") + "_" + id.String()
}

func parseCursor(c string) (time.Time, uuid.UUID, error) {
	ds, is, ok := strings.Cut(c, "_")
	if !ok {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	d, err := time.Parse("2006-01-02", ds)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	id, err := uuid.Parse(is)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	return d, id, nil
}
