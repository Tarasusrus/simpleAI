package botlimits

import (
	"context"
	"errors"
	"fmt"
	"time"

	"simpleAI/internal/plan"
)

// Правка окна плана из бота (ADR-020). Скиллы видят только эти типы и не импортируют plan: команда «добавь день»
// проходит через то же ядро, что и кнопки веба.

// Window — окно плана глазами бота: первый и последний день (UTC-полночь), длина, сколько осталось, поправлено ли.
type Window struct {
	Start, End time.Time
	Days       int
	DaysLeft   int
	Adjusted   bool
}

// MinWindowDays и MaxWindowDays — допустимая длина окна (как в ядре плана).
const (
	MinWindowDays = plan.MinWindowDays
	MaxWindowDays = plan.MaxWindowDays
)

var (
	// ErrNoWindow — окна на экране нет (поступлений нет или последнее окно закончилось).
	ErrNoWindow = errors.New("botlimits: окна нет")
	// ErrBadWindowLength — длина вне 1..62 дней или конец раньше начала.
	ErrBadWindowLength = errors.New("botlimits: длина окна вне 1..62 дней")
	// ErrWindowEndPast — конец окна раньше сегодня.
	ErrWindowEndPast = errors.New("botlimits: конец окна раньше сегодня")
)

// WindowEditor читает и правит окно плана для чата. ok=false — чат вне области плана.
type WindowEditor interface {
	CurrentWindow(ctx context.Context, chatID int64) (w Window, ok bool, err error)
	// ResizeWindow продлевает (days > 0) или сокращает (days < 0) окно.
	ResizeWindow(ctx context.Context, chatID int64, days int) (w Window, ok bool, err error)
	// SetWindowEnd ставит последний день окна (дата без времени).
	SetWindowEnd(ctx context.Context, chatID int64, end time.Time) (w Window, ok bool, err error)
}

var _ WindowEditor = (*Plan)(nil)

func (p *Plan) CurrentWindow(ctx context.Context, chatID int64) (Window, bool, error) {
	return p.window(chatID, func(s *plan.Service) (plan.WindowFrame, error) { return s.CurrentWindow(ctx) })
}

func (p *Plan) ResizeWindow(ctx context.Context, chatID int64, days int) (Window, bool, error) {
	return p.window(chatID, func(s *plan.Service) (plan.WindowFrame, error) { return s.ResizeWindow(ctx, days) })
}

func (p *Plan) SetWindowEnd(ctx context.Context, chatID int64, end time.Time) (Window, bool, error) {
	d := plan.Date{Year: end.Year(), Month: int(end.Month()), Day: end.Day()}
	return p.window(chatID, func(s *plan.Service) (plan.WindowFrame, error) { return s.SetWindowEnd(ctx, d) })
}

// window — общий путь: область чата, вызов ядра, перевод ответа и ошибок в типы бота.
func (p *Plan) window(chatID int64, run func(*plan.Service) (plan.WindowFrame, error)) (Window, bool, error) {
	sc, in := p.scope(chatID)
	if !in {
		return Window{}, false, nil
	}
	f, err := run(p.svc.ForScope(sc))
	switch {
	case errors.Is(err, plan.ErrNoWindow):
		return Window{}, true, fmt.Errorf("%w: %w", ErrNoWindow, err)
	case errors.Is(err, plan.ErrBadWindowLength):
		return Window{}, true, fmt.Errorf("%w: %w", ErrBadWindowLength, err)
	case errors.Is(err, plan.ErrWindowEndPast):
		return Window{}, true, fmt.Errorf("%w: %w", ErrWindowEndPast, err)
	case err != nil:
		return Window{}, true, fmt.Errorf("botlimits: окно плана: %w", err)
	}
	return Window{Start: day(f.Start), End: day(f.End), Days: f.Days, DaysLeft: f.DaysLeft, Adjusted: f.Adjusted}, true, nil
}
