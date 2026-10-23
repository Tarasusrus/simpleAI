package plan

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limit — деньги, отложенные на категорию на одно окно плана (ADR-011). Остаток —
// max(лимит − потрачено, 0). Окна не хранятся: лимит привязан к дате начала окна.
type Limit struct {
	ID string
	// Category — имя категории, как в справочнике; события сопоставляются по CategoryKey.
	Category string
	Amount   Money
	// Repeat — действует на каждое окно, начиная с окна WindowStart; иначе только на него.
	Repeat      bool
	WindowStart Date
}

// CategoryKey — ключ сравнения имён категорий: обрезка пробелов и нижний регистр.
// Домен не вправе импортировать budget, поэтому это зеркало budget.NormalizeName
// (ADR-010); совпадение посимвольно проверяет тест в planrepo.
func CategoryKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NewLimit — новый лимит до сохранения.
type NewLimit struct {
	Category    string
	Amount      Money
	Repeat      bool
	WindowStart Date
}

// Validate проверяет лимит: лимита на «Без категории» нет.
func (n NewLimit) Validate() error {
	name := strings.TrimSpace(n.Category)
	if name == "" || CategoryKey(name) == CategoryKey(UncategorizedName) {
		return ErrLimitCategory
	}
	if utf8.RuneCountInString(name) > maxCategoryRunes {
		return ErrNameTooLong
	}
	if n.Amount.Minor <= 0 {
		return ErrAmountNonPositive
	}
	if !n.Amount.Cur.Supported() {
		return ErrUnsupportedCurrency
	}
	if n.Amount.Minor > maxAmountMinor {
		return ErrAmountTooLarge
	}
	if !n.WindowStart.Valid() {
		return fmt.Errorf("%w: %v", ErrBadDate, n.WindowStart)
	}
	return nil
}

// LimitEdit — новые значения существующего лимита. Категория не меняется: другая
// категория — это другой лимит.
type LimitEdit struct {
	Amount Money
	Repeat bool
	// WindowStart — новое окно привязки; nil — не менять. Сервис ставит его при переходе
	// «повторяющийся → разовый»: иначе лимит остался бы привязан к давно прошедшему окну.
	WindowStart *Date
}

// Validate проверяет правку.
func (e LimitEdit) Validate() error {
	if e.Amount.Minor <= 0 {
		return ErrAmountNonPositive
	}
	if !e.Amount.Cur.Supported() {
		return ErrUnsupportedCurrency
	}
	if e.Amount.Minor > maxAmountMinor {
		return ErrAmountTooLarge
	}
	return nil
}

// LimitView — лимит в окне, в валюте показа. Расчёт один (core), фронт только показывает.
type LimitView struct {
	ID       string
	Category string
	Repeat   bool
	// Amount — лимит; Spent — оплачено в категории; Planned — неоплаченные плановые
	// платежи категории (тоже съедают лимит); Left и Over — остаток и перерасход.
	Amount, Spent, Planned, Left, Over int64
	// LeftPct — остаток в процентах лимита, округлён вниз.
	LeftPct int
	// Attention — требует взгляда: перерасход, остаток меньше 30% или недавние траты.
	Attention bool
}

// Пороги «внимания» (макет E, F): остаток меньше 30% или трата за последние дни.
const (
	attentionLeftPct   = 30
	attentionRecentDay = 2
)

// limitApplies — действует ли лимит на окно с индексом idx: разовый — на окно,
// выбранное его датой; повторяющийся — на это окно и все после него.
func limitApplies(ws []Window, l Limit, idx int) bool {
	at, ok := ResolveWindow(ws, l.WindowStart)
	if !ok {
		return false
	}
	if l.Repeat {
		return idx >= at
	}
	return idx == at
}

// limitsFor выбирает лимиты, действующие на описываемое окно: разовый — на окно,
// выбранное его датой; повторяющийся — на это окно и все после него. Разовый
// перекрывает повторяющийся той же категории.
func (c calc) limitsFor() []Limit {
	byCat := map[string]Limit{}
	for _, l := range c.in.Limits {
		if !limitApplies(c.in.Windows, l, c.st.Index) {
			continue
		}
		k := CategoryKey(l.Category)
		if cur, dup := byCat[k]; dup && !cur.Repeat && l.Repeat {
			continue
		}
		byCat[k] = l
	}
	keys := make([]string, 0, len(byCat))
	for k := range byCat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Limit, 0, len(keys))
	for _, k := range keys {
		out = append(out, byCat[k])
	}
	return out
}

// limits считает лимиты окна и сумму удержанного (LimitHold). Платёж повторяющегося
// правила лимит не ест (он уже обязательство), траты в зазоре до окна — тоже.
func (c calc) limits() (views []LimitView, hold int64, err error) {
	ls := c.limitsFor()
	if len(ls) == 0 {
		return nil, 0, nil
	}
	idx := map[string]int{}
	for i, l := range ls {
		idx[CategoryKey(l.Category)] = i
	}
	views = make([]LimitView, len(ls))
	recent := make([]bool, len(ls))
	for i, l := range ls {
		n, cerr := c.money(&l.Amount)
		if cerr != nil {
			return nil, 0, cerr
		}
		views[i] = LimitView{ID: l.ID, Category: l.Category, Repeat: l.Repeat, Amount: n}
	}
	for _, e := range c.in.Events {
		if e.RuleID != "" || e.Amount == nil || e.Category == "" {
			continue
		}
		i, ok := idx[CategoryKey(e.Category)]
		if !ok {
			continue
		}
		switch {
		case e.State == StateDone:
			if e.Date == nil || !c.inWindowByDate(*e.Date) {
				continue
			}
			n, cerr := c.money(e.Amount)
			if cerr != nil {
				return nil, 0, cerr
			}
			if views[i].Spent, err = addI64(views[i].Spent, n); err != nil {
				return nil, 0, err
			}
			if DaysBetween(*e.Date, c.in.Today) <= attentionRecentDay && e.Date.Compare(c.in.Today) <= 0 {
				recent[i] = true
			}
		case c.plannedInThisWindow(e):
			n, cerr := c.money(e.Amount)
			if cerr != nil {
				return nil, 0, cerr
			}
			if views[i].Planned, err = addI64(views[i].Planned, n); err != nil {
				return nil, 0, err
			}
		}
	}
	for i := range views {
		v := &views[i]
		used, uerr := addI64(v.Spent, v.Planned)
		if uerr != nil {
			return nil, 0, uerr
		}
		v.Left, v.Over = max(v.Amount-used, 0), max(used-v.Amount, 0)
		if v.Amount > 0 {
			v.LeftPct = int(v.Left * 100 / v.Amount) //nolint:gosec // G115: 0..100
		}
		v.Attention = v.Over > 0 || v.LeftPct < attentionLeftPct || recent[i]
		if hold, err = addI64(hold, v.Left); err != nil {
			return nil, 0, err
		}
	}
	return views, hold, nil
}

// UnplannedRow — категория без действующего лимита с оплаченными тратами окна (ADR-015 §3).
type UnplannedRow struct {
	Category string
	Spent    int64
	// Count — число оплаченных трат категории в окне (для «Это уже k-я, обычно N»).
	Count int
	// Prefill — сумма по умолчанию в форме «Запланировать» при малой истории: 2 × потрачено
	// (ещё столько же на остаток окна). Равную потраченному предзаполнять нельзя: лимит L ≤ S
	// даёт LimitHold = 0, и «Свободно после плана» не менялось бы (ADR-015 §4).
	Prefill int64
}

// unplanned считает оплаченные траты окна в категориях без лимита, действующего на это окно
// (ADR-015 §2). Факты с RuleID (платежи правил — обязательства) и «Без категории» не входят.
// В формулу CanSpend сумма не входит: эти траты уже в Spent.
func (c calc) unplanned() (visible, hidden []UnplannedRow, total int64, err error) {
	var rows []UnplannedRow
	limited := map[string]bool{}
	for _, l := range c.limitsFor() {
		limited[CategoryKey(l.Category)] = true
	}
	idx := map[string]int{}
	for _, e := range c.in.Events {
		if e.State != StateDone || e.RuleID != "" || e.Amount == nil || e.Category == "" || e.Date == nil {
			continue
		}
		k := CategoryKey(e.Category)
		if limited[k] || !c.inWindowByDate(*e.Date) {
			continue
		}
		n, cerr := c.money(e.Amount)
		if cerr != nil {
			return nil, nil, 0, cerr
		}
		if n == 0 {
			continue // копейки, округлённые до нуля: пустая строка «0 ₽» экрану не нужна
		}
		i, ok := idx[k]
		if !ok {
			i = len(rows)
			idx[k] = i
			rows = append(rows, UnplannedRow{Category: strings.TrimSpace(e.Category)})
		}
		rows[i].Count++
		if rows[i].Spent, err = addI64(rows[i].Spent, n); err != nil {
			return nil, nil, 0, err
		}
		if total, err = addI64(total, n); err != nil {
			return nil, nil, 0, err
		}
	}
	for i := range rows {
		if rows[i].Prefill, err = addI64(rows[i].Spent, rows[i].Spent); err != nil {
			return nil, nil, 0, err
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		if rows[a].Spent != rows[b].Spent {
			return rows[a].Spent > rows[b].Spent
		}
		return CategoryKey(rows[a].Category) < CategoryKey(rows[b].Category)
	})
	dismissed := map[string]bool{}
	for _, n := range c.in.Dismissed {
		dismissed[CategoryKey(n)] = true
	}
	for _, r := range rows {
		if dismissed[CategoryKey(r.Category)] {
			hidden = append(hidden, r)
		} else {
			visible = append(visible, r)
		}
	}
	return visible, hidden, total, nil
}
