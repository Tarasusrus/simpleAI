package budgetskill

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/skills/safetospend"
)

// shareWarningStore — узкий read-only контракт для предупреждения о пробитом
// конверте. Отдельный интерфейс, а не *budget.Store, ровно ради одного
// инварианта: «ошибка расчёта не ломает запись траты» проверяется тестом с
// падающим стором, а не рассуждением.
type shareWarningStore interface {
	GetActiveEnvelope(ctx context.Context, chatID int64) (*budget.Envelope, bool, error)
	ListShares(ctx context.Context, chatID int64, envelopeID uuid.UUID) ([]budget.EnvelopeShare, error)
	GetExchangeRates(ctx context.Context) (map[string]float64, error)
	SpentByCategoryExcludingRecurring(ctx context.Context, from, to time.Time) ([]budget.CategorySpentRow, error)
}

// shareOverspendWarning — строка-предупреждение, что трата пробила долю
// активного конверта (ADR-008 §8).
//
// Контракт вызова жёсткий: функция ТОЛЬКО дополняет ответ. Транзакция уже
// записана, откатывать и блокировать её нельзя — конверт это план, а не
// разрешение тратить. Любая невозможность посчитать (нет конверта, нет курса,
// ошибка БД) даёт пустую строку и обычный ответ; ошибка при этом логируется,
// но наверх не поднимается.
//
// Остаток НЕ считается здесь заново: он берётся из safetospend.ShareRemainingFor
// поверх computeShareRemaining — единственного места, где живёт формула
// remaining = allocated + carried_in − факт (ADR-008 §8).
func (s *BudgetSkill) shareOverspendWarning(ctx context.Context, t budget.Transaction) string {
	st := s.shareStore
	if st == nil {
		return ""
	}
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok || chatID == 0 {
		return ""
	}

	env, found, err := st.GetActiveEnvelope(ctx, chatID)
	if err != nil {
		slog.Default().WarnContext(ctx, "add_expense: активный конверт не прочитан, предупреждение пропущено",
			"err", err, "chat_id", chatID)
		return ""
	}
	if !found || env == nil {
		return "" // конвертов нет — предупреждать не о чем
	}

	shares, err := st.ListShares(ctx, chatID, env.ID)
	if err != nil || len(shares) == 0 {
		if err != nil {
			slog.Default().WarnContext(ctx, "add_expense: доли конверта не прочитаны", "err", err, "chat_id", chatID)
		}
		return ""
	}

	rates, err := st.GetExchangeRates(ctx)
	if err != nil || rates["THB"] == 0 {
		slog.Default().WarnContext(ctx, "add_expense: нет курса, остаток доли не посчитан", "err", err)
		return ""
	}

	// Верхняя граница факта — по сегодня: конверт может кончаться в будущем, а
	// факта из будущего не бывает (та же обрезка, что в startEnvelope).
	to := time.Now()
	if to.After(env.PeriodEnd) {
		to = env.PeriodEnd
	}
	if to.Before(env.PeriodStart) {
		to = env.PeriodStart
	}
	spent, err := st.SpentByCategoryExcludingRecurring(ctx, env.PeriodStart, to)
	if err != nil {
		slog.Default().WarnContext(ctx, "add_expense: факт по категориям не прочитан", "err", err)
		return ""
	}

	rem, ok := safetospend.ShareRemainingFor(shares, spent, rates, t.CategoryID, t.CategoryName)
	if !ok {
		return "" // трате некуда падать — нет даже fallback-доли (ADR-008 §6)
	}
	return formatShareWarning(rem, rates["THB"])
}

// formatShareWarning — текст предупреждения. Чистая функция: показывать нечего,
// пока доля не пробита. Суммы в ₽ — той же валютой, что и весь ответ про
// конверты (доли хранятся в THB, ADR-008 §7).
func formatShareWarning(rem safetospend.ShareRemaining, rubPerTHB float64) string {
	if !rem.Overspent() {
		return ""
	}
	over := -rem.Remaining * rubPerTHB
	return fmt.Sprintf("\n\n⚠️ Конверт «%s» пробит на %.0f ₽ (лимит %.0f ₽, потрачено %.0f ₽).",
		rem.Name, over, rem.LimitTHB*rubPerTHB, rem.SpentTHB*rubPerTHB)
}
