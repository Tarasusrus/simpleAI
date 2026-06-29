package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type stubReminderStore struct {
	reminders []budget.Reminder
}

func (s *stubReminderStore) ListActiveReminders(_ context.Context) ([]budget.Reminder, error) {
	return s.reminders, nil
}

type captureSender struct {
	sent []string
}

func (c *captureSender) SendToChatID(_ context.Context, _ int64, text string) error {
	c.sent = append(c.sent, text)
	return nil
}

type stubDigest struct {
	out string
	err error
}

func (s stubDigest) YesterdayDigest(_ context.Context, _ int64, _ *time.Location) (string, error) {
	return s.out, s.err
}

// now, попадающее в окно напоминания (09:00 UTC).
func windowNow() time.Time { return time.Date(2026, 6, 29, 9, 0, 0, 0, time.UTC) }

func workerWith(digest DigestSource) (*ReminderWorker, *captureSender) {
	store := &stubReminderStore{reminders: []budget.Reminder{
		{ChatID: 1, Enabled: true, NotifyHour: 9, NotifyMinute: 0, Timezone: "UTC"},
	}}
	sender := &captureSender{}
	return NewReminderWorker(store, sender, digest, discardLogger()), sender
}

// Ошибка дайджеста не блокирует доставку: напоминание уходит без дайджеста.
func TestCheck_DigestError_StillSendsReminder(t *testing.T) {
	w, sender := workerWith(stubDigest{err: errors.New("boom")})
	w.check(context.Background(), windowNow())

	if len(sender.sent) != 1 {
		t.Fatalf("want 1 message sent, got %d", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0], "Не забудь") {
		t.Fatalf("reminder text missing: %q", sender.sent[0])
	}
	if strings.Contains(sender.sent[0], "Вчера потрачено") {
		t.Fatalf("digest must be absent on error: %q", sender.sent[0])
	}
}

// Непустой дайджест добавляется к напоминанию.
func TestCheck_DigestNonEmpty_Appended(t *testing.T) {
	w, sender := workerWith(stubDigest{out: "💸 Вчера потрачено: ~1000 ฿"})
	w.check(context.Background(), windowNow())

	if len(sender.sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0], "Не забудь") || !strings.Contains(sender.sent[0], "1000 ฿") {
		t.Fatalf("want reminder + digest, got %q", sender.sent[0])
	}
}

// Пустой дайджест → только напоминание, без висящего переноса строки.
func TestCheck_DigestEmpty_NoTrailingNewline(t *testing.T) {
	w, sender := workerWith(stubDigest{out: ""})
	w.check(context.Background(), windowNow())

	if len(sender.sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(sender.sent))
	}
	if strings.HasSuffix(sender.sent[0], "\n") {
		t.Fatalf("empty digest must not append newline: %q", sender.sent[0])
	}
}

// nil digest (источник не сконфигурирован) → только напоминание.
func TestCheck_NilDigest_SendsReminder(t *testing.T) {
	w, sender := workerWith(nil)
	w.check(context.Background(), windowNow())

	if len(sender.sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(sender.sent))
	}
}

// Вне окна напоминания ничего не отправляется.
func TestCheck_OutsideWindow_NoSend(t *testing.T) {
	w, sender := workerWith(stubDigest{out: "x"})
	w.check(context.Background(), time.Date(2026, 6, 29, 10, 30, 0, 0, time.UTC))

	if len(sender.sent) != 0 {
		t.Fatalf("want no messages outside window, got %d", len(sender.sent))
	}
}
