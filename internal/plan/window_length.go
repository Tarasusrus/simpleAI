package plan

import (
	"context"
	"fmt"
)

// Правка окна (ADR-020): владелец продлевает или сокращает окно, которое видит на экране. Отдельного «вернуть 14 дней»
// нет: 14 — длина нового окна по умолчанию, а не точка возврата; к ней приходят теми же ±день и «до даты». Начало окна задаёт
// открывающее поступление и правкой не двигается; меняется только длина, а значит конец. Длина хранится отдельно
// от поступлений, ключ — дата начала окна: окна по-прежнему вычисляются, а не хранятся.

const (
	// MinWindowDays и MaxWindowDays — допустимая длина окна. Верхняя граница совпадает с горизонтом догона
	// повторов (lookbackDays): окно длиннее уже не про «до следующей зарплаты».
	MinWindowDays = 1
	MaxWindowDays = lookbackDays
)

// WindowLength — поправленная длина окна, начинающегося в Start.
type WindowLength struct {
	Start Date
	Days  int
}

// Validate проверяет правку до сохранения.
func (l WindowLength) Validate() error {
	if !l.Start.Valid() {
		return fmt.Errorf("%w: %v", ErrBadDate, l.Start)
	}
	if l.Days < MinWindowDays || l.Days > MaxWindowDays {
		return fmt.Errorf("%w: %d", ErrBadWindowLength, l.Days)
	}
	return nil
}

// WindowLengthWriter хранит правки длины окон. Отдельный порт, а не часть Writer (ISP): правка окна —
// самостоятельная сущность со своей таблицей. Запись идёт в первый чат области, сброс — по всей области.
// Реализует internal/planrepo.
type WindowLengthWriter interface {
	// SetWindowLength сохраняет длину окна, начинающегося в start; повтор перезаписывает.
	SetWindowLength(ctx context.Context, sc Scope, l WindowLength) error
	// ResetWindowLength возвращает окну стандартную длину; правки не было — не ошибка.
	ResetWindowLength(ctx context.Context, sc Scope, start Date) error
}

// WindowFrame — окно на экране глазами правки: границы, длина и признак поправленной длины.
type WindowFrame struct {
	Start, End Date
	Days       int
	DaysLeft   int
	Adjusted   bool
}

// WithWindowLengths подключает правку окна и возвращает сервис. Без неё команды правки отвечают ErrReadOnly,
// а чтение окна работает.
func (s *Service) WithWindowLengths(w WindowLengthWriter) *Service {
	s.lengths = w
	return s
}

// CurrentWindow — окно, которое владелец видит на экране (текущее, а в зазоре — следующее). Окна нет или
// последнее закончилось — ErrNoWindow.
func (s *Service) CurrentWindow(ctx context.Context) (WindowFrame, error) {
	ld, err := s.load(ctx)
	if err != nil {
		return WindowFrame{}, err
	}
	return ld.frame()
}

// ResizeWindow продлевает (days > 0) или сокращает (days < 0) окно на экране.
func (s *Service) ResizeWindow(ctx context.Context, days int) (WindowFrame, error) {
	return s.editWindow(ctx, func(w Window) (int, error) {
		if days == 0 {
			return 0, fmt.Errorf("%w: изменение на 0 дней", ErrBadWindowLength)
		}
		return w.Days() + days, nil
	})
}

// SetWindowEnd ставит окну на экране последний день end (включительно).
func (s *Service) SetWindowEnd(ctx context.Context, end Date) (WindowFrame, error) {
	if !end.Valid() {
		return WindowFrame{}, fmt.Errorf("%w: %v", ErrBadDate, end)
	}
	return s.editWindow(ctx, func(w Window) (int, error) {
		return DaysBetween(w.Start, end) + 1, nil
	})
}

// editWindow — общий путь правки: найти окно на экране, посчитать новую длину, сохранить, перечитать.
// Стандартная длина хранится как отсутствие правки, а не как запись «14».
func (s *Service) editWindow(ctx context.Context, newDays func(Window) (int, error)) (WindowFrame, error) {
	if s.lengths == nil {
		return WindowFrame{}, ErrReadOnly
	}
	ld, err := s.load(ctx)
	if err != nil {
		return WindowFrame{}, err
	}
	w, err := ld.focusWindow()
	if err != nil {
		return WindowFrame{}, err
	}
	days, err := newDays(w)
	if err != nil {
		return WindowFrame{}, err
	}
	l := WindowLength{Start: w.Start, Days: days}
	if err := l.Validate(); err != nil {
		return WindowFrame{}, err
	}
	// Конец раньше сегодня запрещён: окно на экране исчезло бы (стало бы «закончилось» или «в зазоре»), следующая
	// команда правила бы уже другое окно, а это — не вернуть.
	if w.Start.AddDays(days-1).Compare(ld.today) < 0 {
		return WindowFrame{}, fmt.Errorf("%w: конец окна раньше сегодня", ErrWindowEndPast)
	}
	if days == windowDays {
		err = s.lengths.ResetWindowLength(ctx, s.scope, w.Start)
	} else {
		err = s.lengths.SetWindowLength(ctx, s.scope, l)
	}
	if err != nil {
		return WindowFrame{}, err
	}
	ld, err = s.load(ctx)
	if err != nil {
		return WindowFrame{}, err
	}
	return ld.frameOf(w.Start)
}

// focusWindow — окно на экране; закончившееся последнее окно и отсутствие окон — ErrNoWindow.
func (st state) focusWindow() (Window, error) {
	if len(st.windows) == 0 || st.st.Kind == StateEnded || st.st.Kind == StateNone {
		return Window{}, ErrNoWindow
	}
	idx, _, _ := st.focus()
	return st.windows[idx], nil
}

func (st state) frame() (WindowFrame, error) {
	w, err := st.focusWindow()
	if err != nil {
		return WindowFrame{}, err
	}
	return st.frameOf(w.Start)
}

// frameOf описывает окно, начинающееся в start. После правки окно может перестать быть «на экране»
// (например, сокращено так, что сегодня оказалось в зазоре), поэтому ищем по началу, а не по focus.
func (st state) frameOf(start Date) (WindowFrame, error) {
	for _, w := range st.windows {
		if w.Start != start {
			continue
		}
		f := WindowFrame{Start: w.Start, End: w.End(), Days: w.Days(), Adjusted: w.Adjusted()}
		switch {
		case st.today.Compare(w.Start) < 0:
			f.DaysLeft = w.Days()
		case st.today.Compare(w.End()) <= 0:
			f.DaysLeft = DaysBetween(st.today, w.End()) + 1
		}
		return f, nil
	}
	return WindowFrame{}, ErrNoWindow
}
