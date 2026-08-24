package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

type stubEnvelopes struct {
	out    string
	err    error
	calls  int
	lastTZ string
}

func (s *stubEnvelopes) MorningEnvelopes(_ context.Context, _ int64, loc *time.Location) (string, error) {
	s.calls++
	s.lastTZ = loc.String()
	return s.out, s.err
}

func envelopeWorker(r budget.Reminder, src EnvelopeSource) (*ReminderWorker, *captureSender) {
	store := &stubReminderStore{reminders: []budget.Reminder{r}}
	sender := &captureSender{}
	w := NewReminderWorker(store, sender, nil, discardLogger()).WithEnvelopes(src)
	return w, sender
}

// Бангкокское утро наступает в 01:00 UTC — воркер обязан считать час в
// часовом поясе оператора, а не в UTC.
func TestCheck_EnvelopeTimezoneBoundary_Bangkok(t *testing.T) {
	r := budget.Reminder{
		ChatID: 1, Enabled: false,
		EnvelopeEnabled: true, EnvelopeHour: 8, EnvelopeMinute: 0,
		Timezone: "Asia/Bangkok",
	}
	src := &stubEnvelopes{out: "🍚 Еда: 5000 ฿"}
	w, sender := envelopeWorker(r, src)

	// 01:00 UTC = 08:00 в Бангкоке → пуш есть.
	w.check(context.Background(), time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC))
	if len(sender.sent) != 1 {
		t.Fatalf("want 1 push at 01:00 UTC (08:00 Bangkok), got %d", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0], "Еда") {
		t.Fatalf("envelope body missing: %q", sender.sent[0])
	}
	if src.lastTZ != "Asia/Bangkok" {
		t.Fatalf("want source called in Asia/Bangkok, got %q", src.lastTZ)
	}

	// 08:00 UTC = 15:00 в Бангкоке → пуша быть не должно.
	sender.sent = nil
	w.check(context.Background(), time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC))
	if len(sender.sent) != 0 {
		t.Fatalf("want no push at 08:00 UTC (15:00 Bangkok), got %d", len(sender.sent))
	}
}

// Нет активного конверта (пустое тело) → пуша нет.
func TestCheck_EnvelopeNoActiveEnvelope_NoPush(t *testing.T) {
	r := budget.Reminder{
		ChatID: 1, EnvelopeEnabled: true, EnvelopeHour: 8, Timezone: "Asia/Bangkok",
	}
	src := &stubEnvelopes{out: ""}
	w, sender := envelopeWorker(r, src)

	w.check(context.Background(), time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC))
	if src.calls != 1 {
		t.Fatalf("want source consulted once, got %d", src.calls)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("want no push without active envelope, got %d: %q", len(sender.sent), sender.sent)
	}
}

// Ошибка источника — не повод слать пустой пуш.
func TestCheck_EnvelopeSourceError_NoPush(t *testing.T) {
	r := budget.Reminder{ChatID: 1, EnvelopeEnabled: true, EnvelopeHour: 8, Timezone: "Asia/Bangkok"}
	w, sender := envelopeWorker(r, &stubEnvelopes{err: errors.New("boom")})

	w.check(context.Background(), time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC))
	if len(sender.sent) != 0 {
		t.Fatalf("want no push on source error, got %d", len(sender.sent))
	}
}

// Выключенная утренняя рассылка не шлётся, даже если вечернее напоминание включено.
func TestCheck_EnvelopeDisabled_NoPush(t *testing.T) {
	r := budget.Reminder{
		ChatID: 1, Enabled: true, NotifyHour: 21, Timezone: "Asia/Bangkok",
		EnvelopeEnabled: false, EnvelopeHour: 8,
	}
	src := &stubEnvelopes{out: "🍚 Еда: 5000 ฿"}
	w, sender := envelopeWorker(r, src)

	w.check(context.Background(), time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC))
	if src.calls != 0 {
		t.Fatalf("disabled morning push must not consult source, got %d calls", src.calls)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("want no push when morning push disabled, got %d", len(sender.sent))
	}
}

// Вечернее напоминание выключено (enabled=false) — оно не должно уходить,
// даже когда строка попала в выборку ради утренних конвертов.
func TestCheck_ReminderDisabled_OnlyEnvelopePush(t *testing.T) {
	r := budget.Reminder{
		ChatID: 1, Enabled: false, NotifyHour: 8, NotifyMinute: 0,
		EnvelopeEnabled: true, EnvelopeHour: 8, EnvelopeMinute: 0,
		Timezone: "UTC",
	}
	w, sender := envelopeWorker(r, &stubEnvelopes{out: "🍚 Еда: 5000 ฿"})

	w.check(context.Background(), time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC))
	if len(sender.sent) != 1 {
		t.Fatalf("want exactly 1 message (envelope only), got %d: %q", len(sender.sent), sender.sent)
	}
	if strings.Contains(sender.sent[0], "Не забудь") {
		t.Fatalf("disabled evening reminder leaked: %q", sender.sent[0])
	}
}
