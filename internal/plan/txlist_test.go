package plan

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type fakeTxLister struct {
	items []TxItem
	got   []TxQuery
	err   error
}

func (f *fakeTxLister) CategoryTransactions(_ context.Context, q TxQuery) ([]TxItem, error) {
	f.got = append(f.got, q)
	n := min(q.Limit, len(f.items))
	return f.items[:n], f.err
}

func txSvc(snap Snapshot, f *fakeTxLister) *Service {
	return limSvc(snap, nil).WithTxLister(f)
}

func TestCategoryTx_WindowLimitConversionAndPaging(t *testing.T) {
	f := &fakeTxLister{}
	for i := 0; i < TxPageSize+5; i++ {
		f.items = append(f.items, TxItem{ID: fmt.Sprintf("id%02d", i), Date: d(2026, 3, 9), Name: "такси", Amount: Money{Minor: 25000, Cur: CurTHB}})
	}
	page, err := txSvc(baseSnap(), f).CategoryTransactions(context.Background(), CurRUB, " Такси ", "")
	if err != nil {
		t.Fatal(err)
	}
	// запрос: окно сервера, не больше страницы+1, категория без пробелов, без курсора
	q := f.got[0]
	if q.Category != "Такси" || q.From != d(2026, 3, 5) || q.To != d(2026, 3, 18) || q.Limit != TxPageSize+1 || q.After != nil {
		t.Fatalf("запрос %+v", q)
	}
	if len(page.Rows) != TxPageSize || !page.HasMore || page.Next != "2026-03-09|id19" {
		t.Fatalf("rows=%d hasMore=%v next=%q", len(page.Rows), page.HasMore, page.Next)
	}
	// 250 ฿ × 2,5 = 625 ₽
	if r := page.Rows[0]; r.Amount != 625 || r.Key != "transaction:id00" || r.Date != d(2026, 3, 9) {
		t.Fatalf("строка %+v", r)
	}
	if page.Start != d(2026, 3, 5) || page.End != d(2026, 3, 18) {
		t.Fatalf("окно %v–%v", page.Start, page.End)
	}
	// вторая страница: курсор доходит до запроса, «Впереди» только на первой
	f.items = f.items[TxPageSize:]
	withPlanned := baseSnap()
	withPlanned.Payments = append(withPlanned.Payments,
		Event{Key: "planned:a", Kind: KindPayment, State: StatePlanned, Name: "впереди", Amount: rub(900), Date: dp(2026, 3, 12), Category: "Такси", Source: SourcePlanned})
	if first, err := txSvc(withPlanned, &fakeTxLister{}).CategoryTransactions(context.Background(), CurRUB, "Такси", ""); err != nil || len(first.Ahead) != 1 {
		t.Fatalf("первая страница без «Впереди»: %+v %v", first.Ahead, err)
	}
	p2, err := txSvc(withPlanned, f).CategoryTransactions(context.Background(), CurRUB, "Такси", page.Next)
	if err != nil {
		t.Fatal(err)
	}
	if a := f.got[1].After; a == nil || a.Date != d(2026, 3, 9) || a.ID != "id19" {
		t.Fatalf("курсор не дошёл: %+v", f.got[1].After)
	}
	if p2.HasMore || p2.Next != "" || len(p2.Rows) != 5 || p2.Ahead != nil {
		t.Fatalf("вторая страница: %+v", p2)
	}
}

// Сумма строк страницы = Spent строки категории (ADR-015 §6) — на тех же фактах.
func TestCategoryTx_SumMatchesLimitSpent(t *testing.T) {
	snap := baseSnap()
	snap.Payments = nil
	items := make([]TxItem, 0, 3)
	for i, c := range []Money{{Minor: 12345, Cur: CurTHB}, {Minor: 777, Cur: CurRUB}, {Minor: 99999, Cur: CurTHB}} {
		id := fmt.Sprintf("t%d", i)
		dt := d(2026, 3, 6+i)
		items = append(items, TxItem{ID: id, Date: dt, Name: "x", Amount: c})
		cc := c
		snap.Payments = append(snap.Payments, Event{Key: TransactionKey(id), Kind: KindPayment, State: StateDone, Name: "x",
			Amount: &cc, Date: &dt, Category: "Такси", Source: SourceTransaction})
	}
	snap.Limits = []Limit{{ID: "l", Category: "Такси", Amount: *rub(100000), WindowStart: d(2026, 3, 5)}}
	for _, cur := range []Currency{CurRUB, CurTHB} {
		s := txSvc(snap, &fakeTxLister{items: items})
		page, err := s.CategoryTransactions(context.Background(), cur, "Такси", "")
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.View(context.Background(), cur)
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, r := range page.Rows {
			sum += r.Amount
		}
		if len(res.View.Limits) != 1 || sum != res.View.Limits[0].Spent || sum == 0 {
			t.Fatalf("%s: Σстрок=%d, Spent лимита=%+v", cur, sum, res.View.Limits)
		}
	}
}

func TestCategoryTx_AheadIncludesRecurringAndSkipsOthers(t *testing.T) {
	snap := baseSnap()
	snap.Payments = append(snap.Payments,
		Event{Key: "transaction:paid", Kind: KindPayment, State: StateDone, Name: "уже оплачено", Amount: rub(100), Date: dp(2026, 3, 8), Category: "Такси", Source: SourceTransaction},
		Event{Key: "planned:a", Kind: KindPayment, State: StatePlanned, Name: "такси в аэропорт", Amount: rub(900), Date: dp(2026, 3, 12), Category: "такси", Source: SourcePlanned},
		Event{Key: "planned:b", Kind: KindPayment, State: StatePlanned, Name: "кино", Amount: rub(500), Date: dp(2026, 3, 12), Category: "Кино", Source: SourcePlanned},
		Event{Key: "planned:c", Kind: KindPayment, State: StatePlanned, Name: "такси потом", Amount: rub(1), Date: dp(2026, 4, 30), Category: "Такси", Source: SourcePlanned},
	)
	snap.Rules = append(snap.Rules, RuleWithExceptions{ID: "tx", Name: "абонемент такси", Category: "Такси", Kind: KindPayment,
		Rule: Rule{Anchor: d(2026, 3, 14), N: 1, Unit: UnitMonth, Amount: *rub(200)}})
	page, err := txSvc(snap, &fakeTxLister{}).CategoryTransactions(context.Background(), CurRUB, "Такси", "")
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(page.Ahead))
	for _, r := range page.Ahead {
		keys = append(keys, r.Name)
	}
	if len(keys) != 2 || keys[0] != "такси в аэропорт" || keys[1] != "абонемент такси" {
		t.Fatalf("Ahead=%v, ждали платёж 12.03 и правило 14.03 (без «Кино» и платежа за окном)", keys)
	}
}

func TestCategoryTx_Errors(t *testing.T) {
	ctx := context.Background()
	if _, err := limSvc(baseSnap(), nil).CategoryTransactions(ctx, CurRUB, "Такси", ""); !errors.Is(err, ErrReadOnly) {
		t.Errorf("без TxLister: %v", err)
	}
	f := &fakeTxLister{}
	s := txSvc(baseSnap(), f)
	for _, c := range []string{"", "  ", UncategorizedName} {
		if _, err := s.CategoryTransactions(ctx, CurRUB, c, ""); !errors.Is(err, ErrLimitCategory) {
			t.Errorf("категория %q: %v", c, err)
		}
	}
	for _, cur := range []string{"nope", "2026-03-09", "2026-13-40|x", "|x"} {
		if _, err := s.CategoryTransactions(ctx, CurRUB, "Такси", cur); !errors.Is(err, ErrBadCursor) {
			t.Errorf("курсор %q: %v", cur, err)
		}
	}
	if _, err := s.CategoryTransactions(ctx, "XXX", "Такси", ""); !errors.Is(err, ErrUnsupportedCurrency) {
		t.Errorf("валюта: %v", err)
	}
	if len(f.got) != 0 {
		t.Fatalf("плохой запрос дошёл до источника: %+v", f.got)
	}
	f.err = errors.New("boom")
	if _, err := s.CategoryTransactions(ctx, CurRUB, "Такси", ""); err == nil {
		t.Error("ошибка источника проглочена")
	}
}

func TestCategoryTx_NoWindow_EmptyPage(t *testing.T) {
	f := &fakeTxLister{items: []TxItem{{ID: "x"}}}
	s := limSvc(Snapshot{Rate: &Rate{RubPerThbMicro: 2_500_000}}, nil).WithTxLister(f)
	page, err := s.CategoryTransactions(context.Background(), CurRUB, "Такси", "")
	if err != nil || len(page.Rows) != 0 || len(f.got) != 0 {
		t.Fatalf("page=%+v err=%v запросов=%d", page, err, len(f.got))
	}
}

// Окно закончилось и экран его скрывает: страницы нет и в источник не ходим.
func TestCategoryTx_EndedWindow_EmptyPage(t *testing.T) {
	f := &fakeTxLister{items: []TxItem{{ID: "x"}}}
	s := NewService(&fakeReader{snap: baseSnap()}, nil, func() Date { return d(2026, 6, 1) }, Scope{ChatIDs: []int64{42}}).WithTxLister(f)
	page, err := s.CategoryTransactions(context.Background(), CurRUB, "Такси", "")
	if err != nil || len(page.Rows) != 0 || len(f.got) != 0 {
		t.Fatalf("page=%+v err=%v запросов=%d", page, err, len(f.got))
	}
}
