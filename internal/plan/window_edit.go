package plan

import (
	"context"
	"fmt"
)

// Окно — бизнес-сущность (ADR-021): хранится в базе со своими датами. Окно появляется само: его открывает
// поступление-зарплата (или аванс), а в день начала оно сохраняется и дальше от поступлений не зависит. Владелец
// двигает начало и конец окна, на экране и в Telegram, и может удалить лишнее окно. Отдельного «вернуть 14 дней»
// нет: 14 — длина нового окна по умолчанию, а не точка возврата.

const (
	// MinWindowDays и MaxWindowDays — допустимая длина окна. Верхняя граница совпадает с горизонтом догона
	// повторов (lookbackDays): окно длиннее уже не про «до следующей зарплаты».
	MinWindowDays = 1
	MaxWindowDays = lookbackDays
)

// WindowStore хранит окна. Отдельный порт, а не часть Writer (ISP): окно — самостоятельная сущность со своей
// таблицей. Запись идёт в первый чат области, правка и удаление — по всей области. Реализует internal/planrepo.
type WindowStore interface {
	// CreateWindow сохраняет окно и возвращает его id. Окно с тем же началом уже есть — возвращает его id
	// (повтор безопасен: окно может сохранить и экран, и бот одновременно). fixedEnd — конец поставил владелец:
	// окно само не растёт до следующего (ADR-022).
	CreateWindow(ctx context.Context, sc Scope, start, end Date, fixedEnd bool) (string, error)
	// UpdateWindow меняет даты окна. Нет окна в области — ErrNotFound.
	UpdateWindow(ctx context.Context, sc Scope, id string, start, end Date, fixedEnd bool) error
	// DeleteWindow помечает окно удалённым. Нет окна в области — ErrNotFound.
	DeleteWindow(ctx context.Context, sc Scope, id string) error
}

// WindowFrame — окно на экране глазами правки: границы, длина, сколько осталось и признак нестандартной длины.
type WindowFrame struct {
	Start, End Date
	Days       int
	DaysLeft   int
	Adjusted   bool
	// Ongoing — окно закончилось, а зарплаты нет: оно идёт до следующего поступления (ADR-022).
	Ongoing bool
}

// WithWindowStore подключает хранение окон и возвращает сервис. Без него окна только считаются (как до ADR-021),
// а команды правки отвечают ErrReadOnly.
func (s *Service) WithWindowStore(w WindowStore) *Service {
	s.windows = w
	return s
}

// CurrentWindow — окно, которое владелец видит на экране (текущее, а в зазоре — следующее). Окна нет или
// последнее закончилось — ErrNoWindow.
func (s *Service) CurrentWindow(ctx context.Context) (WindowFrame, error) {
	ld, err := s.load(ctx)
	if err != nil {
		return WindowFrame{}, err
	}
	w, err := ld.focusWindow()
	if err != nil {
		return WindowFrame{}, err
	}
	return ld.frameOf(w.Start)
}

// ResizeWindow продлевает (days > 0) или сокращает (days < 0) окно на экране, двигая его конец. Конец считается
// от того, что видно на экране (с автоматическим ростом), и дальше считается поставленным владельцем.
func (s *Service) ResizeWindow(ctx context.Context, days int) (WindowFrame, error) {
	if days == 0 {
		return WindowFrame{}, fmt.Errorf("%w: изменение на 0 дней", ErrBadWindowLength)
	}
	return s.editWindow(ctx, func(w Window) (Date, Date, bool) { return w.Start, w.End().AddDays(days), true })
}

// SetWindowEnd ставит окну на экране последний день end (включительно).
func (s *Service) SetWindowEnd(ctx context.Context, end Date) (WindowFrame, error) {
	if !end.Valid() {
		return WindowFrame{}, fmt.Errorf("%w: %v", ErrBadDate, end)
	}
	return s.editWindow(ctx, func(w Window) (Date, Date, bool) { return w.Start, end, true })
}

// SetWindowStart ставит окну на экране первый день start. Поступления, оказавшиеся вне окна, попадают в соседние
// окна по дате. Заданный конец и признак «конец поставил владелец» не меняются: окно, которое росло само, растёт и
// дальше.
func (s *Service) SetWindowStart(ctx context.Context, start Date) (WindowFrame, error) {
	if !start.Valid() {
		return WindowFrame{}, fmt.Errorf("%w: %v", ErrBadDate, start)
	}
	return s.editWindow(ctx, func(w Window) (Date, Date, bool) {
		// У растущего окна заданный конец может быть уже в прошлом (окно идёт или выросло в зазор): он остаётся, а рост
		// досчитается сам. Начало позже видимого конца — длина меньше дня, checkBounds откажет.
		if start.Compare(w.End()) > 0 {
			return start, w.End(), w.FixedEnd
		}
		end := w.BaseEnd()
		if !w.FixedEnd && end.Compare(start) < 0 {
			end = start
		}
		return start, end, w.FixedEnd
	})
}

// DeleteWindow удаляет окно на экране. Нельзя удалить окно, если после этого на экране не останется окна (единственное
// окно, или остальные уже закончились): править было бы нечего, а вернуть окно — нечем. Возвращает окно, которое
// теперь на экране.
func (s *Service) DeleteWindow(ctx context.Context) (WindowFrame, error) {
	if s.windows == nil {
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
	if !ld.screenAfterDelete(w) {
		return WindowFrame{}, ErrLastWindow
	}
	id, err := s.ensureStored(ctx, w)
	if err != nil {
		return WindowFrame{}, err
	}
	if err := s.windows.DeleteWindow(ctx, s.scope, id); err != nil {
		return WindowFrame{}, err
	}
	return s.CurrentWindow(ctx)
}

// screenAfterDelete — останется ли окно на экране, если удалить w: примеряем удаление на тех же поступлениях.
func (st state) screenAfterDelete(w Window) bool {
	var stored []StoredWindow
	for _, sw := range st.snap.Windows {
		if sw.Deleted {
			stored = append(stored, sw)
		}
	}
	for _, o := range st.windows {
		if o.Stored() || o.Start == w.Start {
			stored = append(stored, StoredWindow{ID: o.ID, Start: o.Start, End: o.BaseEnd(), FixedEnd: o.FixedEnd, Deleted: o.Start == w.Start})
		}
	}
	ws := BuildGrowingWindows(st.incomes, stored...)
	ExtendLast(ws, st.today)
	k := Locate(ws, st.today).Kind
	return k != StateEnded && k != StateNone
}

// editWindow — общий путь правки дат: найти окно на экране, посчитать новые границы, проверить, сохранить (окно,
// ещё не сохранённое, сохраняется), перечитать.
func (s *Service) editWindow(ctx context.Context, bounds func(Window) (start, end Date, fixedEnd bool)) (WindowFrame, error) {
	if s.windows == nil {
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
	start, end, fixed := bounds(w)
	if err := ld.checkBounds(w, start, end, fixed); err != nil {
		return WindowFrame{}, err
	}
	// Расчётное окно сохраняется и сразу получает новые даты. Правка идёт отдельной записью даже после создания:
	// если окно с тем же началом успели сохранить параллельно (экран и бот), создание вернёт его id, и правка не
	// потеряется.
	id := w.ID
	if !w.Stored() {
		if id, err = s.windows.CreateWindow(ctx, s.scope, start, end, fixed); err != nil {
			return WindowFrame{}, err
		}
	}
	if err := s.windows.UpdateWindow(ctx, s.scope, id, start, end, fixed); err != nil {
		return WindowFrame{}, err
	}
	ld, err = s.load(ctx)
	if err != nil {
		return WindowFrame{}, err
	}
	return ld.frameOf(start)
}

// checkBounds — правила дат окна: длина 1..62 дня; конец не раньше сегодня (иначе окно пропало бы с экрана,
// следующая команда правила бы уже другое окно, и правку нельзя было бы откатить); не пересекаться с другими
// сохранёнными окнами по их заданным датам (автоматический рост соседа не мешает: он пересчитается сам; расчётные
// соседи тоже пересчитаются).
func (st state) checkBounds(w Window, start, end Date, fixed bool) error {
	days := DaysBetween(start, end) + 1
	// Окно, которое само выросло длиннее предела (долго без зарплаты), можно сокращать; удлинять сверх предела — нет.
	if days < MinWindowDays || days > MaxWindowDays && days > w.Days() {
		return fmt.Errorf("%w: %d", ErrBadWindowLength, days)
	}
	// Растущему окну (конец не владельца) заданный конец в прошлом не страшен: рост дотянет окно до следующего
	// поступления или до сегодня (ADR-022).
	if fixed && end.Compare(st.today) < 0 {
		return fmt.Errorf("%w: конец окна раньше сегодня", ErrWindowEndPast)
	}
	for _, o := range st.windows {
		if !o.Stored() || o.Start == w.Start {
			continue
		}
		if start.Compare(o.BaseEnd()) <= 0 && end.Compare(o.Start) >= 0 {
			return fmt.Errorf("%w: окно %v–%v", ErrWindowOverlap, o.Start, o.BaseEnd())
		}
	}
	return nil
}

// ensureStored возвращает id окна, сохраняя расчётное окно, если оно ещё не в базе.
func (s *Service) ensureStored(ctx context.Context, w Window) (string, error) {
	if w.Stored() {
		return w.ID, nil
	}
	return s.windows.CreateWindow(ctx, s.scope, w.Start, w.BaseEnd(), w.FixedEnd)
}

// materialize сохраняет начавшиеся расчётные окна (ADR-021): с первого дня окно живёт само и от поступлений больше
// не зависит. Будущие расчётные окна не сохраняются: их открыла ожидаемая зарплата, и правка правила повтора должна
// их двигать. Без хранилища окна только считаются.
func (s *Service) materialize(ctx context.Context, ws []Window, today Date) error {
	if s.windows == nil {
		return nil
	}
	for i := range ws {
		if ws[i].Stored() && ws[i].shortened {
			// Окно закрыто ранней зарплатой: переписываем его конец, чтобы в базе окна не пересекались.
			if err := s.windows.UpdateWindow(ctx, s.scope, ws[i].ID, ws[i].Start, ws[i].BaseEnd(), ws[i].FixedEnd); err != nil {
				return fmt.Errorf("plan: обрезать окно %v: %w", ws[i].Start, err)
			}
			ws[i].shortened = false
		}
		if ws[i].Stored() || ws[i].Start.Compare(today) > 0 {
			continue
		}
		id, err := s.windows.CreateWindow(ctx, s.scope, ws[i].Start, ws[i].BaseEnd(), false) // рост не храним
		if err != nil {
			return fmt.Errorf("plan: сохранить окно %v: %w", ws[i].Start, err)
		}
		ws[i].ID = id
	}
	return nil
}

// focusWindow — окно на экране; закончившееся последнее окно и отсутствие окон — ErrNoWindow.
func (st state) focusWindow() (Window, error) {
	if len(st.windows) == 0 || st.st.Kind == StateEnded || st.st.Kind == StateNone {
		return Window{}, ErrNoWindow
	}
	idx, _, _ := st.focus()
	return st.windows[idx], nil
}

// frameOf описывает окно, начинающееся в start. После правки окно может перестать быть «на экране», поэтому ищем по
// началу, а не по focus.
func (st state) frameOf(start Date) (WindowFrame, error) {
	for _, w := range st.windows {
		if w.Start != start {
			continue
		}
		f := WindowFrame{Start: w.Start, End: w.End(), Days: w.Days(), Adjusted: w.Adjusted(), Ongoing: w.Ongoing}
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
