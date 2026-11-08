// Package planwindow — навык агента «правка окна плана» (ADR-020, ADR-021): «добавь день», «сократи окно на 2 дня»,
// «окно до 25.10», «окно с 3-го», «удали окно». Навык — только адаптер разговора: он разбирает ввод LLM и
// форматирует ответ, а окно правит ядро плана через botlimits.WindowEditor. Тот же путь, что у кнопок веба.
package planwindow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/botlimits"
	"simpleAI/internal/plugin"
	"simpleAI/internal/skills/safetospend"
)

// Skill правит окно плана для чата, из которого пришло сообщение.
type Skill struct {
	windows botlimits.WindowEditor
	// numbers — «Свободно» и «на день» после правки; nil — ответ без чисел.
	numbers botlimits.Reader
}

// New собирает навык. numbers может быть nil.
func New(w botlimits.WindowEditor, numbers botlimits.Reader) *Skill {
	return &Skill{windows: w, numbers: numbers}
}

var _ plugin.Skill = (*Skill)(nil)

const (
	actionShow     = "show"
	actionExtend   = "extend"
	actionShrink   = "shrink"
	actionSetEnd   = "set_end"
	actionSetStart = "set_start"
	actionDelete   = "delete"
)

// Manifest — описание для реестра, LLM и MCP.
func (s *Skill) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:   "plan_window",
		Name: "Plan Window",
		Description: "Shows and CHANGES the length of the current plan WINDOW (окно плана, by default 14 days from salary/advance). " +
			"action='extend' — make the window longer by `days` (default 1): 'добавь день', 'продли окно на 3 дня', 'плюс день к окну'. " +
			"action='shrink' — make it shorter by `days` (default 1): 'убери день', 'сократи окно на 2 дня', 'минус день'. " +
			"action='set_end' — set the LAST day of the window: 'окно до 25.10', 'пусть окно кончится 20-го', 'поправь окно до 1 ноября'. " +
			"action='set_start' — set the FIRST day of the window: 'окно с 3.10', 'пусть окно начнётся 3-го', 'начало окна 1 ноября'. " +
			"action='delete' — delete the current window (the money stays and joins the neighbouring window): 'удали окно', 'убери это окно'. " +
			"action='show' — current window dates: 'какое сейчас окно', 'до какого числа окно'. " +
			"A new window appears by itself when a salary or advance comes. Not an expense, not a limit.",
		Version: "1.0.0",
		InputSchema: &plugin.Schema{
			Name:    "PlanWindowInput",
			Version: "1.0.0",
			JSON: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{actionShow, actionExtend, actionShrink, actionSetEnd, actionSetStart, actionDelete},
						"description": "show | extend | shrink | set_end | set_start | delete",
					},
					"days": map[string]any{
						"type":        "integer",
						"description": "How many days to add (extend) or remove (shrink); positive number, default 1.",
					},
					"end": map[string]any{
						"type":        "string",
						"description": "Last day of the window for set_end: YYYY-MM-DD, DD.MM.YYYY or DD.MM (year is taken from the window).",
					},
					"start": map[string]any{
						"type":        "string",
						"description": "First day of the window for set_start: YYYY-MM-DD, DD.MM.YYYY or DD.MM (year is taken from the window).",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

type input struct {
	Action string `json:"action"`
	Days   int    `json:"days,omitempty"`
	End    string `json:"end,omitempty"`
	Start  string `json:"start,omitempty"`
}

// Run выполняет команду и возвращает ответ пользователю.
func (s *Skill) Run(ctx context.Context, raw string) (string, error) {
	var in input
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	in.Action = strings.TrimSpace(in.Action)
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok || chatID == 0 {
		return "", errors.New("plan_window: нет чата в контексте")
	}
	if s.windows == nil {
		return "План окна не подключён.", nil
	}
	w, inScope, err := s.do(ctx, chatID, in)
	if errors.Is(err, errUnknownAction) {
		return "", err
	}
	if !inScope {
		return "План окна для этого чата не включён.", nil
	}
	switch {
	case errors.Is(err, botlimits.ErrNoWindow):
		return "Открытого окна нет: сначала нужно поступление (зарплата или аванс).", nil
	case errors.Is(err, errBadEnd):
		return "Не понял дату. Напиши, например, «окно до 25.10» или «окно с 3.10».", nil
	case errors.Is(err, botlimits.ErrWindowOverlap):
		return "Так окна пересекутся: сначала поправь соседнее окно.", nil
	case errors.Is(err, botlimits.ErrLastWindow):
		return "Единственное окно удалить нельзя: поправь его даты.", nil
	case errors.Is(err, botlimits.ErrWindowEndPast):
		return "Конец окна не может быть раньше сегодня: окно пропало бы, и вернуть его было бы нельзя.", nil
	case errors.Is(err, botlimits.ErrBadWindowLength):
		return fmt.Sprintf("Так окно не поправить: длина от %d до %d дней, конец не раньше начала.",
			botlimits.MinWindowDays, botlimits.MaxWindowDays), nil
	case err != nil:
		return "", err
	}
	return s.reply(ctx, chatID, in, w), nil
}

var (
	errUnknownAction = errors.New("plan_window: неизвестное действие")
	errBadEnd        = errors.New("plan_window: не разобрал дату")
)

func (s *Skill) do(ctx context.Context, chatID int64, in input) (botlimits.Window, bool, error) {
	days := in.Days
	if days < 0 {
		days = -days
	}
	if days == 0 {
		days = 1
	}
	switch in.Action {
	case actionShow:
		return s.windows.CurrentWindow(ctx, chatID)
	case actionExtend:
		return s.windows.ResizeWindow(ctx, chatID, days)
	case actionShrink:
		return s.windows.ResizeWindow(ctx, chatID, -days)
	case actionSetEnd, actionSetStart:
		cur, ok, err := s.windows.CurrentWindow(ctx, chatID)
		if !ok || err != nil {
			return cur, ok, err
		}
		if in.Action == actionSetStart {
			start, err := parseDay(in.Start, cur.Start)
			if err != nil {
				return botlimits.Window{}, true, err
			}
			return s.windows.SetWindowStart(ctx, chatID, start)
		}
		end, err := parseDay(in.End, cur.End)
		if err != nil {
			return botlimits.Window{}, true, err
		}
		return s.windows.SetWindowEnd(ctx, chatID, end)
	case actionDelete:
		return s.windows.DeleteWindow(ctx, chatID)
	default:
		return botlimits.Window{}, true, fmt.Errorf("%w: %q", errUnknownAction, in.Action)
	}
}

// parseDay понимает YYYY-MM-DD, DD.MM.YYYY и DD.MM. Без года берётся ближайшая к опорной дате ref (начало или конец
// окна) дата с этим днём и месяцем: «окно до 03.11» в окне с 05.11 — это 3 ноября того же года (и честный отказ
// «конец раньше начала»), а «окно с 28.12» в окне с 3 января — прошлый год.
func parseDay(s string, ref time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "02.01.2006", "2.1.2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{"02.01", "2.1"} {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		var best time.Time
		for _, y := range []int{ref.Year() - 1, ref.Year(), ref.Year() + 1} {
			c := time.Date(y, t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			if best.IsZero() || absDur(c.Sub(ref)) < absDur(best.Sub(ref)) {
				best = c
			}
		}
		return best, nil
	}
	return time.Time{}, fmt.Errorf("%w: %q", errBadEnd, s)
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (s *Skill) reply(ctx context.Context, chatID int64, in input, w botlimits.Window) string {
	var b strings.Builder
	switch in.Action {
	case actionExtend:
		b.WriteString("Окно продлено. ")
	case actionShrink:
		b.WriteString("Окно сокращено. ")
	case actionSetEnd:
		b.WriteString("Конец окна изменён. ")
	case actionSetStart:
		b.WriteString("Начало окна изменено. ")
	case actionDelete:
		if w == (botlimits.Window{}) {
			return "Окно удалено. Открытого окна сейчас нет: новое откроется с зарплатой."
		}
		b.WriteString("Окно удалено. Теперь на экране: ")
	}
	if w.Ongoing {
		fmt.Fprintf(&b, "Окно с %s идёт до следующего поступления: зарплаты пока нет, окно растёт каждый день", w.Start.Format("02.01"))
	} else {
		fmt.Fprintf(&b, "Окно %s — %s (%d %s", w.Start.Format("02.01"), w.End.Format("02.01"), w.Days, safetospend.PluralDays(w.Days))
		if w.Adjusted {
			b.WriteString(", изменено")
		}
		b.WriteString(")")
		if w.DaysLeft > 0 {
			fmt.Fprintf(&b, ", осталось %d %s", w.DaysLeft, safetospend.PluralDays(w.DaysLeft))
		}
	}
	b.WriteString(".")
	if line := s.numbersLine(ctx, chatID); line != "" {
		b.WriteString("\n" + line)
	}
	return b.String()
}

// numbersLine — «Свободно» и «на день» по новому окну: ради них окно обычно и правят. Не получилось — без строки:
// правка уже сохранена, и ответ о ней важнее чисел.
func (s *Skill) numbersLine(ctx context.Context, chatID int64) string {
	if s.numbers == nil {
		return ""
	}
	snap, ok, err := s.numbers.Snapshot(ctx, chatID, "THB")
	if err != nil || !ok {
		return ""
	}
	m := safetospend.NewDisplay(snap.Cur, 0)
	per := "—"
	if snap.PerDay != nil {
		per = m.Signed(int(*snap.PerDay))
	}
	return fmt.Sprintf("Свободно до конца окна: %s · на день: %s", m.Signed(int(snap.CanSpend)), per)
}
