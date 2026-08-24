package notify

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"simpleAI/internal/budget"
)

// ReminderStore — интерфейс для получения активных напоминаний.
type ReminderStore interface {
	ListActiveReminders(ctx context.Context) ([]budget.Reminder, error)
}

// ReminderSender — интерфейс для отправки сообщений по chat_id.
type ReminderSender interface {
	SendToChatID(ctx context.Context, chatID int64, text string) error
}

// DigestSource строит дайджест трат за вчера для напоминания.
// Реализуется в budget/skills (DigestProvider); notify знает только контракт.
// Пустая строка = показывать нечего. Проводка в воркер — задача simpleAI-c2ud.
type DigestSource interface {
	YesterdayDigest(ctx context.Context, chatID int64, loc *time.Location) (string, error)
}

// EnvelopeSource строит утренний пуш с конвертами и дневным лимитом.
// Реализуется на стороне скиллов; notify знает только контракт.
// Пустая строка = активного конверта нет, слать нечего.
type EnvelopeSource interface {
	MorningEnvelopes(ctx context.Context, chatID int64, loc *time.Location) (string, error)
}

// ReminderWorker каждую минуту проверяет, кому нужно отправить напоминание.
// Один воркер обслуживает оба расписания чата: вечернее напоминание и
// утренний пуш с конвертами — у них общий пояс и общая строка budget_reminder.
type ReminderWorker struct {
	store     ReminderStore
	sender    ReminderSender
	digest    DigestSource
	envelopes EnvelopeSource
	logger    *slog.Logger
}

// WithEnvelopes подключает источник утренних конвертов. Без него утренний
// пуш не отправляется вовсе — это делает зависимость опциональной для
// вызывающих, которым нужны только напоминания.
func (w *ReminderWorker) WithEnvelopes(src EnvelopeSource) *ReminderWorker {
	w.envelopes = src
	return w
}

// NewReminderWorker создаёт воркер напоминаний.
// digest опционален (nil допустим) — без него шлётся только напоминание.
func NewReminderWorker(store ReminderStore, sender ReminderSender, digest DigestSource, logger *slog.Logger) *ReminderWorker {
	return &ReminderWorker{store: store, sender: sender, digest: digest, logger: logger}
}

// Run запускает цикл опроса. Блокируется до отмены ctx.
func (w *ReminderWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			w.check(ctx, t)
		}
	}
}

func (w *ReminderWorker) check(ctx context.Context, now time.Time) {
	reminders, err := w.store.ListActiveReminders(ctx)
	if err != nil {
		w.logger.Error("reminder worker: list reminders failed", "err", err)
		return
	}

	for _, r := range reminders {
		loc, err := time.LoadLocation(r.Timezone)
		if err != nil {
			loc = time.UTC
		}
		local := now.In(loc)
		w.checkEnvelope(ctx, r, local, loc)
		if r.Enabled && local.Hour() == r.NotifyHour && local.Minute() == r.NotifyMinute {
			text := "👋 Привет! Не забудь внести сегодняшние покупки и траты."
			// Дайджест за вчера — non-fatal: ошибка/пусто не блокирует напоминание.
			if w.digest != nil {
				d, err := w.digest.YesterdayDigest(ctx, r.ChatID, loc)
				if err != nil {
					w.logger.Warn("reminder worker: digest failed", "chat_id", r.ChatID, "err", err)
				} else if d != "" {
					text += "\n\n" + d
				}
			}
			if err := w.sender.SendToChatID(ctx, r.ChatID, text); err != nil {
				w.logger.Error("reminder worker: send failed", "chat_id", r.ChatID, "err", err)
			}
		}
	}
}

// checkEnvelope отправляет утренний пуш с конвертами, если для чата он включён
// и время совпало. Тело берётся у EnvelopeSource: пусто (активного конверта
// нет) или ошибка — пуша не будет, пустое сообщение оператору бесполезно.
func (w *ReminderWorker) checkEnvelope(ctx context.Context, r budget.Reminder, local time.Time, loc *time.Location) {
	if w.envelopes == nil || !r.EnvelopeEnabled {
		return
	}
	if local.Hour() != r.EnvelopeHour || local.Minute() != r.EnvelopeMinute {
		return
	}

	body, err := w.envelopes.MorningEnvelopes(ctx, r.ChatID, loc)
	if err != nil {
		w.logger.Warn("reminder worker: envelopes failed", "chat_id", r.ChatID, "err", err)
		return
	}
	if strings.TrimSpace(body) == "" {
		w.logger.Info("reminder worker: no active envelope, skip morning push", "chat_id", r.ChatID)
		return
	}

	if err := w.sender.SendToChatID(ctx, r.ChatID, body); err != nil {
		w.logger.Error("reminder worker: envelope push failed", "chat_id", r.ChatID, "err", err)
	}
}
