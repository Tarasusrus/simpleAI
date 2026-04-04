package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"simpleAI/internal/budget"

	"github.com/google/uuid"
)

// RecurringStore — интерфейс для работы с повторяющимися платежами.
type RecurringStore interface {
	GetDueRecurring(ctx context.Context, date time.Time) ([]budget.RecurringPayment, error)
	CreateRecurringTransaction(ctx context.Context, t budget.Transaction, recurringID uuid.UUID, nextDate time.Time) error
}

// RecurringWorker каждую минуту создаёт транзакции для наступивших повторяющихся платежей.
type RecurringWorker struct {
	store  RecurringStore
	sender ReminderSender
	logger *slog.Logger
}

// NewRecurringWorker создаёт воркер повторяющихся платежей.
func NewRecurringWorker(store RecurringStore, sender ReminderSender, logger *slog.Logger) *RecurringWorker {
	return &RecurringWorker{store: store, sender: sender, logger: logger}
}

// Run запускает цикл опроса. Блокируется до отмены ctx.
func (w *RecurringWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	// Проверяем сразу при старте.
	w.process(ctx, time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			w.process(ctx, t)
		}
	}
}

func (w *RecurringWorker) process(ctx context.Context, now time.Time) {
	today := now.UTC().Truncate(24 * time.Hour)

	due, err := w.store.GetDueRecurring(ctx, today)
	if err != nil {
		w.logger.Error("recurring worker: get due failed", "err", err)
		return
	}

	for _, r := range due {
		if err := w.handleOne(ctx, r, today); err != nil {
			w.logger.Error("recurring worker: handle failed", "id", r.ID, "name", r.Name, "err", err)
		}
	}
}

func (w *RecurringWorker) handleOne(ctx context.Context, r budget.RecurringPayment, today time.Time) error {
	tx := budget.Transaction{
		Type:        r.Type,
		Amount:      r.Amount,
		Currency:    r.Currency,
		CategoryID:  r.CategoryID,
		Description: r.Name,
		Date:        today,
	}

	nextDate := nextOccurrence(r, today)
	if err := w.store.CreateRecurringTransaction(ctx, tx, r.ID, nextDate); err != nil {
		return fmt.Errorf("create recurring transaction: %w", err)
	}

	text := fmt.Sprintf("🔄 Автоматически добавлено: %s — %.2f %s", r.Name, r.Amount, r.Currency)
	if err := w.sender.SendToChatID(ctx, r.ChatID, text); err != nil {
		w.logger.Warn("recurring worker: notify failed", "chat_id", r.ChatID, "err", err)
	}

	w.logger.Info("recurring worker: processed", "id", r.ID, "name", r.Name, "next_date", nextDate)
	return nil
}

// nextOccurrence вычисляет следующую дату срабатывания.
func nextOccurrence(r budget.RecurringPayment, from time.Time) time.Time {
	switch r.RecurrenceType {
	case "weekly":
		return from.AddDate(0, 0, 7)
	case "daily":
		return from.AddDate(0, 0, 1)
	default: // monthly
		next := from.AddDate(0, 1, 0)
		if r.DayOfMonth != nil {
			// Ставим конкретный день месяца, защищая от переполнения (28 февраля и т.д.).
			day := *r.DayOfMonth
			lastDay := time.Date(next.Year(), next.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
			if day > lastDay {
				day = lastDay
			}
			next = time.Date(next.Year(), next.Month(), day, 0, 0, 0, 0, time.UTC)
		}
		return next
	}
}

