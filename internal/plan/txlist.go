package plan

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// TxPageSize — размер страницы трат категории (ADR-015 §6). Задаёт сервер, клиент его не меняет.
const TxPageSize = 20

// ErrBadCursor — курсор страницы трат повреждён.
var ErrBadCursor = fmt.Errorf("plan: некорректный курсор страницы")

// TxCursor — позиция keyset-пагинации: страница продолжается строго после (Date, ID)
// в порядке «дата по убыванию, id по возрастанию».
type TxCursor struct {
	Date Date
	ID   string
}

// String — вид курсора в API: «ГГГГ-ММ-ДД|id».
func (c TxCursor) String() string { return c.Date.String() + "|" + c.ID }

// ParseTxCursor разбирает непустой курсор.
func ParseTxCursor(s string) (*TxCursor, error) {
	ds, id, ok := strings.Cut(s, "|")
	if !ok || id == "" {
		return nil, ErrBadCursor
	}
	d, err := ParseDate(ds)
	if err != nil {
		return nil, ErrBadCursor
	}
	return &TxCursor{Date: d, ID: id}, nil
}

// TxQuery — запрос трат категории окна [From, To]. Limit — сколько строк вернуть не более.
type TxQuery struct {
	Category string
	From, To Date
	After    *TxCursor
	Limit    int
}

// TxItem — одна оплаченная трата в исходной валюте.
type TxItem struct {
	ID     string
	Date   Date
	Name   string
	Amount Money
}

// TxLister читает траты категории. Реализует internal/planrepo. Это ровно те факты, из которых
// собирается Spent строки категории: расходы окна без RuleID с этой категорией.
// Транзакции общие и области чатов не подчиняются (budget_transaction без chat_id), как и сам Spent.
type TxLister interface {
	CategoryTransactions(ctx context.Context, q TxQuery) ([]TxItem, error)
}

// WithTxLister подключает список трат категории и возвращает сервис.
func (s *Service) WithTxLister(l TxLister) *Service {
	s.txs = l
	return s
}

// TxRow — трата в валюте показа.
type TxRow struct {
	Key    string
	Date   Date
	Name   string
	Amount int64
}

// CategoryTxPage — страница трат категории окна и «Впереди в категории».
type CategoryTxPage struct {
	Start, End Date
	Rows       []TxRow
	HasMore    bool
	// Next — курсор следующей страницы; пуст, если HasMore=false.
	Next string
	// Ahead — неоплаченные плановые платежи категории в окне (с повторяющимися правилами);
	// только на первой странице. В Planned лимита они не входят (ADR-011 §3).
	Ahead []Row
}

// CategoryTransactions отдаёт траты категории в окне, которое видит экран. «Без категории» — ErrLimitCategory
// (у неё нет ключа категории). Окна на экране нет — пустая страница.
func (s *Service) CategoryTransactions(ctx context.Context, cur Currency, category, cursor string) (CategoryTxPage, error) {
	if s.txs == nil {
		return CategoryTxPage{}, ErrReadOnly
	}
	category = strings.TrimSpace(category)
	if category == "" || CategoryKey(category) == CategoryKey(UncategorizedName) {
		return CategoryTxPage{}, ErrLimitCategory
	}
	var after *TxCursor // пустой курсор — первая страница
	if cursor != "" {
		var err error
		if after, err = ParseTxCursor(cursor); err != nil {
			return CategoryTxPage{}, err
		}
	}
	if !cur.Supported() {
		return CategoryTxPage{}, ErrUnsupportedCurrency
	}
	ld, err := s.load(ctx)
	if err != nil {
		return CategoryTxPage{}, err
	}
	// Окно то же, что на экране (viewOf): закончившееся и скрытое окно экран не описывает.
	if len(ld.windows) == 0 || ld.st.Kind == StateNone || ld.st.Kind == StateEnded {
		return CategoryTxPage{}, nil
	}
	idx, asOf, _ := ld.focus()
	w := ld.windows[idx]
	page := CategoryTxPage{Start: w.Start, End: w.End()}
	items, err := s.txs.CategoryTransactions(ctx, TxQuery{Category: category, From: w.Start, To: w.End(), After: after, Limit: TxPageSize + 1})
	if err != nil {
		return CategoryTxPage{}, err
	}
	if len(items) > TxPageSize {
		items, page.HasMore = items[:TxPageSize], true
	}
	for _, it := range items {
		n, err := Convert(ld.snap.Rate, it.Amount, cur)
		if err != nil {
			return CategoryTxPage{}, err
		}
		page.Rows = append(page.Rows, TxRow{Key: TransactionKey(it.ID), Date: it.Date, Name: it.Name, Amount: n})
	}
	if page.HasMore {
		last := items[len(items)-1]
		page.Next = TxCursor{Date: last.Date, ID: last.ID}.String()
	}
	if after == nil {
		if page.Ahead, err = ld.aheadIn(cur, asOf, category); err != nil {
			return CategoryTxPage{}, err
		}
	}
	return page, nil
}

// aheadIn — неоплаченные плановые платежи категории в окне, тем же отбором, что и расчёт окна.
func (st state) aheadIn(cur Currency, asOf Date, category string) ([]Row, error) {
	c := calc{in: ComputeInput{Windows: st.windows, Events: st.events, Rate: st.snap.Rate, Cur: cur, Today: asOf}, hiddenLast: -1}
	c.st = Locate(st.windows, asOf)
	c.w = st.windows[c.st.Index]
	var out []Row
	key := CategoryKey(category)
	for _, e := range st.events {
		if e.State != StatePlanned || CategoryKey(e.Category) != key || !c.plannedInThisWindow(e) {
			continue
		}
		r, err := c.rowOfPayment(e)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sortRowsByDate(out)
	return out, nil
}

// sortRowsByDate — по дате, платежи без даты в конце; при равенстве по ключу.
func sortRowsByDate(rs []Row) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i].Date, rs[j].Date
		switch {
		case a == nil && b == nil:
			return rs[i].Key < rs[j].Key
		case a == nil:
			return false
		case b == nil:
			return true
		}
		if c := a.Compare(*b); c != 0 {
			return c < 0
		}
		return rs[i].Key < rs[j].Key
	})
}
