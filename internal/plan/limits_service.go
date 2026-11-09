package plan

import (
	"context"
	"strings"
)

// WithLimitWriter подключает запись лимитов категорий (ADR-011) и возвращает сервис.
// Без неё команды лимитов отвечают ErrReadOnly, а предпросмотр работает: он ничего не пишет.
func (s *Service) WithLimitWriter(w LimitWriter) *Service {
	s.limits = w
	return s
}

// WithCategoryHider подключает «Не планировать» / «Вернуть» (ADR-015 §3) и возвращает сервис.
func (s *Service) WithCategoryHider(h CategoryHider) *Service {
	s.hider = h
	return s
}

// DismissCategory скрывает категорию в блоке «Не запланировано». «Без категории» скрывать нечего.
func (s *Service) DismissCategory(ctx context.Context, category string) error {
	if s.hider == nil {
		return ErrReadOnly
	}
	category = strings.TrimSpace(category)
	if category == "" || CategoryKey(category) == CategoryKey(UncategorizedName) {
		return ErrLimitCategory
	}
	return s.hider.Dismiss(ctx, s.scope, category)
}

// RestoreCategory возвращает скрытую категорию в блок.
func (s *Service) RestoreCategory(ctx context.Context, category string) error {
	if s.hider == nil {
		return ErrReadOnly
	}
	category = strings.TrimSpace(category)
	if category == "" {
		return ErrLimitCategory
	}
	return s.hider.Restore(ctx, s.scope, category)
}

func (s *Service) canWriteLimits() error {
	if s.limits == nil {
		return ErrReadOnly
	}
	return nil
}

// AddLimitInput — новый лимит на окно, которое сейчас на экране.
type AddLimitInput struct {
	Category string
	Amount   Money
	// Repeat — действует на каждое окно, начиная с этого.
	Repeat bool
}

// limitWindow — окно, на которое ставится лимит: то, что на экране.
func (st state) limitWindow() (idx int, start Date, err error) {
	start, err = st.chosenWindowStart(WindowCurrent)
	if err != nil {
		return 0, Date{}, err
	}
	idx, _, _ = st.focus()
	return idx, start, nil
}

func (st state) limitByID(id string) (Limit, bool) {
	for _, l := range st.limits {
		if l.ID == id {
			return l, true
		}
	}
	return Limit{}, false
}

// limitTaken — на категорию в окне idx уже действует другой лимит (повторяющийся и
// разовый вместе не живут), либо новый повторяющийся встречает старый повторяющийся.
func (st state) limitTaken(category string, repeat bool, idx int, exceptID string) bool {
	k := CategoryKey(category)
	for _, l := range st.limits {
		if l.ID == exceptID || CategoryKey(l.Category) != k {
			continue
		}
		if (repeat && l.Repeat) || limitApplies(st.windows, l, idx) {
			return true
		}
	}
	return false
}

// AddLimit сохраняет лимит на окно, которое на экране; возвращает id.
func (s *Service) AddLimit(ctx context.Context, in AddLimitInput) (string, error) {
	if err := s.canWriteLimits(); err != nil {
		return "", err
	}
	ld, err := s.load(ctx)
	if err != nil {
		return "", err
	}
	idx, start, err := ld.limitWindow()
	if err != nil {
		return "", err
	}
	n := NewLimit{Category: strings.TrimSpace(in.Category), Amount: in.Amount, Repeat: in.Repeat, WindowStart: start}
	if err := n.Validate(); err != nil {
		return "", err
	}
	if ld.limitTaken(n.Category, n.Repeat, idx, "") {
		return "", ErrLimitExists
	}
	return s.limits.AddLimit(ctx, s.scope, n)
}

// EditLimit меняет сумму и повтор лимита (категория и окно не меняются).
func (s *Service) EditLimit(ctx context.Context, id string, e LimitEdit) error {
	if err := s.canWriteLimits(); err != nil {
		return err
	}
	if err := e.Validate(); err != nil {
		return err
	}
	ld, err := s.load(ctx)
	if err != nil {
		return err
	}
	cur, ok := ld.limitByID(id)
	if !ok {
		return ErrNotFound
	}
	if !e.Repeat && cur.Repeat {
		_, start, err := ld.limitWindow()
		if err != nil {
			return err
		}
		e.WindowStart = &start
	}
	return s.limits.UpdateLimit(ctx, s.scope, id, e)
}

// DeleteLimit удаляет лимит.
func (s *Service) DeleteLimit(ctx context.Context, id string) error {
	if err := s.canWriteLimits(); err != nil {
		return err
	}
	return s.limits.DeleteLimit(ctx, s.scope, id)
}

// PreviewLimitInput — лимит «что если»: новый (ID пуст) или правка существующего.
type PreviewLimitInput struct {
	ID       string
	Category string // только у нового
	Amount   Money
	Repeat   bool
}

// LimitPreview — эффект лимита на экран, в валюте показа.
type LimitPreview struct {
	CanSpendBefore, CanSpendAfter   int64
	LimitHoldBefore, LimitHoldAfter int64
	ExceedsFree                     bool
	Limit                           *LimitView
}

// PreviewLimit считает окно с лимитом и без него. Ничего не пишет: те же данные,
// тот же Compute, что у экрана, поэтому предпросмотр не может разойтись с итогом.
func (s *Service) PreviewLimit(ctx context.Context, cur Currency, in PreviewLimitInput) (LimitPreview, error) {
	ld, err := s.load(ctx)
	if err != nil {
		return LimitPreview{}, err
	}
	idx, start, err := ld.limitWindow()
	if err != nil {
		return LimitPreview{}, err
	}
	next := make([]Limit, 0, len(ld.limits)+1)
	next = append(next, ld.limits...)
	probeID := in.ID
	if in.ID == "" {
		n := NewLimit{Category: strings.TrimSpace(in.Category), Amount: in.Amount, Repeat: in.Repeat, WindowStart: start}
		if err := n.Validate(); err != nil {
			return LimitPreview{}, err
		}
		if ld.limitTaken(n.Category, n.Repeat, idx, "") {
			return LimitPreview{}, ErrLimitExists
		}
		probeID = "preview:new"
		next = append(next, Limit{ID: probeID, Category: n.Category, Amount: n.Amount, Repeat: n.Repeat, WindowStart: start})
	} else {
		e := LimitEdit{Amount: in.Amount, Repeat: in.Repeat}
		if err := e.Validate(); err != nil {
			return LimitPreview{}, err
		}
		found := false
		for i := range next {
			if next[i].ID == in.ID {
				if !in.Repeat && next[i].Repeat {
					next[i].WindowStart = start // как в EditLimit: разовый привязывается к окну на экране
				}
				next[i].Amount, next[i].Repeat = in.Amount, in.Repeat
				found = true
			}
		}
		if !found {
			return LimitPreview{}, ErrNotFound
		}
	}
	before, err := viewOf(ld, cur, ViewOptions{})
	if err != nil {
		return LimitPreview{}, err
	}
	ld.limits = next
	after, err := viewOf(ld, cur, ViewOptions{})
	if err != nil {
		return LimitPreview{}, err
	}
	out := LimitPreview{
		CanSpendBefore: before.View.CanSpend, CanSpendAfter: after.View.CanSpend,
		LimitHoldBefore: before.View.LimitHold, LimitHoldAfter: after.View.LimitHold,
		ExceedsFree: after.View.LimitsExceedFree,
	}
	for i := range after.View.Limits {
		if after.View.Limits[i].ID == probeID {
			l := after.View.Limits[i]
			out.Limit = &l
		}
	}
	return out, nil
}
