package planwindow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/botlimits"
)

type fakeWindows struct {
	cur     botlimits.Window
	resized []int
	ends    []time.Time
	outside bool
	err     error
}

func (f *fakeWindows) result() (botlimits.Window, bool, error) { return f.cur, !f.outside, f.err }

func (f *fakeWindows) CurrentWindow(context.Context, int64) (botlimits.Window, bool, error) {
	return f.result()
}

func (f *fakeWindows) ResizeWindow(_ context.Context, _ int64, days int) (botlimits.Window, bool, error) {
	f.resized = append(f.resized, days)
	return f.result()
}

func (f *fakeWindows) SetWindowEnd(_ context.Context, _ int64, end time.Time) (botlimits.Window, bool, error) {
	f.ends = append(f.ends, end)
	return f.result()
}

type fakeNumbers struct{ snap botlimits.Snapshot }

func (f fakeNumbers) Snapshot(context.Context, int64, string) (botlimits.Snapshot, bool, error) {
	return f.snap, true, nil
}

func utc(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func chatCtx() context.Context {
	return context.WithValue(context.Background(), agent.ChatIDKey{}, int64(42))
}

func window() botlimits.Window {
	return botlimits.Window{Start: utc(2026, 10, 5), End: utc(2026, 10, 19), Days: 15, DaysLeft: 10, Adjusted: true}
}

// «добавь день» → extend без days = +1; «убери 2 дня» → shrink 2 = −2; знак days от LLM не путает направление.
func TestRun_ExtendShrinkDirection(t *testing.T) {
	f := &fakeWindows{cur: window()}
	s := New(f, nil)
	for _, in := range []string{`{"action":"extend"}`, `{"action":"shrink","days":2}`, `{"action":"shrink","days":-3}`, `{"action":"extend","days":4}`} {
		if _, err := s.Run(chatCtx(), in); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
	}
	want := []int{1, -2, -3, 4}
	if len(f.resized) != len(want) {
		t.Fatalf("resized=%v", f.resized)
	}
	for i := range want {
		if f.resized[i] != want[i] {
			t.Fatalf("resized=%v, ждали %v", f.resized, want)
		}
	}
}

// Пробелы вокруг действия от LLM не теряют подтверждение в ответе.
func TestRun_ActionTrimmed(t *testing.T) {
	out, err := New(&fakeWindows{cur: window()}, nil).Run(chatCtx(), `{"action":" extend "}`)
	if err != nil || !strings.Contains(out, "Окно продлено") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestRun_ReplyHasDatesAndNumbers(t *testing.T) {
	per := int64(1200)
	s := New(&fakeWindows{cur: window()}, fakeNumbers{snap: botlimits.Snapshot{Cur: "THB", CanSpend: 12000, PerDay: &per}})
	out, err := s.Run(chatCtx(), `{"action":"extend"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Окно продлено", "05.10 — 19.10", "15 дней", "изменено", "осталось 10 дней", "на день"} {
		if !strings.Contains(out, want) {
			t.Errorf("в ответе нет %q: %s", want, out)
		}
	}
}

func TestRun_SetEndParsesDates(t *testing.T) {
	cases := map[string]time.Time{
		"2026-10-25": utc(2026, 10, 25),
		"25.10.2026": utc(2026, 10, 25),
		"25.10":      utc(2026, 10, 25),
		"3.1":        utc(2027, 1, 3), // без года и раньше начала окна — следующий год
	}
	for in, want := range cases {
		f := &fakeWindows{cur: window()}
		if _, err := New(f, nil).Run(chatCtx(), `{"action":"set_end","end":"`+in+`"}`); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if len(f.ends) != 1 || !f.ends[0].Equal(want) {
			t.Errorf("%s → %v, ждали %v", in, f.ends, want)
		}
	}
	f := &fakeWindows{cur: window()}
	out, err := New(f, nil).Run(chatCtx(), `{"action":"set_end","end":"двадцатое"}`)
	if err != nil || len(f.ends) != 0 || !strings.Contains(out, "Не понял дату") {
		t.Fatalf("неразборчивая дата: out=%q err=%v ends=%v", out, err, f.ends)
	}
}

func TestRun_Show(t *testing.T) {
	f := &fakeWindows{cur: botlimits.Window{Start: utc(2026, 10, 5), End: utc(2026, 10, 18), Days: 14, DaysLeft: 9}}
	out, err := New(f, nil).Run(chatCtx(), `{"action":"show"}`)
	if err != nil || !strings.Contains(out, "05.10 — 18.10") || strings.Contains(out, "изменено") || len(f.resized)+len(f.ends) != 0 {
		t.Fatalf("show: %q err=%v", out, err)
	}
}

func TestRun_FriendlyRefusals(t *testing.T) {
	cases := []struct {
		f    *fakeWindows
		want string
	}{
		{&fakeWindows{outside: true}, "не включён"},
		{&fakeWindows{err: botlimits.ErrNoWindow}, "Открытого окна нет"},
		{&fakeWindows{err: botlimits.ErrBadWindowLength}, "от 1 до 62 дней"},
		{&fakeWindows{err: botlimits.ErrWindowEndPast}, "раньше сегодня"},
	}
	for _, c := range cases {
		out, err := New(c.f, nil).Run(chatCtx(), `{"action":"extend"}`)
		if err != nil || !strings.Contains(out, c.want) {
			t.Errorf("ждали %q: out=%q err=%v", c.want, out, err)
		}
	}
	if _, err := New(&fakeWindows{err: errors.New("база")}, nil).Run(chatCtx(), `{"action":"extend"}`); err == nil {
		t.Error("внутренняя ошибка должна уйти наверх")
	}
	if _, err := New(&fakeWindows{}, nil).Run(chatCtx(), `{"action":"fly"}`); err == nil {
		t.Error("неизвестное действие — ошибка")
	}
	if _, err := New(&fakeWindows{}, nil).Run(context.Background(), `{"action":"show"}`); err == nil {
		t.Error("без чата — ошибка")
	}
}

// Каждое действие из схемы обрабатывается, и только они.
func TestManifest_ActionsMatchHandlers(t *testing.T) {
	s := New(&fakeWindows{cur: window()}, nil)
	props, ok := s.Manifest().InputSchema.JSON["properties"].(map[string]any)
	if !ok {
		t.Fatal("нет properties")
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatal("нет action")
	}
	enum, ok := action["enum"].([]string)
	if !ok || len(enum) != 4 {
		t.Fatalf("enum=%v", action["enum"])
	}
	for _, a := range enum {
		if _, err := s.Run(chatCtx(), `{"action":"`+a+`","end":"25.10"}`); err != nil {
			t.Errorf("действие %q из схемы не обработано: %v", a, err)
		}
	}
}
