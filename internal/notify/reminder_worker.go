package notify

import (
	"context"
	"log/slog"
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

// ReminderWorker каждую минуту проверяет, кому нужно отправить напоминание.
type ReminderWorker struct {
	store  ReminderStore
	sender ReminderSender
	digest DigestSource
	logger *slog.Logger
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
		if local.Hour() == r.NotifyHour && local.Minute() == r.NotifyMinute {
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
