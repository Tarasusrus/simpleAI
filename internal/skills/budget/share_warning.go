package budgetskill

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/agent"
	"simpleAI/internal/botlimits"
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
func (s *BudgetSkill) shareOverspendWarning(ctx context.Context, t budget.Transaction, req budgetInput) string {
	if warn, ok := s.planOverspendWarning(ctx, t, req); ok {
		return warn
	}
	st := s.shareStore
	if st == nil {
		return ""
	}
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok || chatID == 0 {
		return ""
	}

	// Трата, которой по построению нет места ни в одной доле, не может её
	// пробить. computeShareRemaining берёт в факт только ПЕРЕМЕННЫЕ ежедневные
	// траты (ADR-008 §4) и только без recurring_id (§5, отсекает
	// SpentByCategoryExcludingRecurring). Без этой проверки «Переводы» или
	// платёж по recurring маршрутизировались бы в fallback-долю «прочее» и
	// печатали чужой пробой, ничего в этот конверт не внося.
	if t.RecurringID != nil || !budget.IsVariableDailyExpense(t.CategoryName) {
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
	return formatShareWarning(rem, envelopeDisplay(req, rates))
}

// formatShareWarning — текст предупреждения. Чистая функция: показывать нечего,
// пока доля не пробита. Валюта — та же, что и во всём ответе про конверты
// (по умолчанию баты); хранятся доли всё равно в THB (ADR-008 §7).
func formatShareWarning(rem safetospend.ShareRemaining, m safetospend.Display) string {
	if !rem.Overspent() {
		return ""
	}
	return fmt.Sprintf("\n\n⚠️ Конверт «%s» пробит на %s (лимит %s, потрачено %s).",
		rem.Name, m.Fmt(-rem.Remaining), m.Fmt(rem.LimitTHB), m.Fmt(rem.SpentTHB))
}

// WithLimits подключает лимиты из плана (ADR-016): при fromPlan предупреждение о трате считает ядро плана.
func (s *BudgetSkill) WithLimits(r botlimits.Reader, fromPlan bool) *BudgetSkill {
	s.limits, s.limitsFromPlan = r, fromPlan
	return s
}

// planOverspendWarning — предупреждение из плана (ADR-016 §6). ok=false — отвечать старым путём (флаг выключен,
// чат вне области плана, окна нет, ошибка плана). ok=true и пустая строка — предупреждать не о чем: у категории
// нет лимита (раньше трата падала в «прочее» и печатала чужой пробой) или лимит не пробит.
// Трата уже записана, функция только дополняет ответ; платежи повторяющихся правил лимит не едят.
func (s *BudgetSkill) planOverspendWarning(ctx context.Context, t budget.Transaction, req budgetInput) (string, bool) {
	if !s.limitsFromPlan || s.limits == nil {
		return "", false
	}
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok || chatID == 0 {
		return "", false
	}
	m := safetospend.NewDisplay(displayCode(req), 0)
	snap, ok, err := s.limits.Snapshot(ctx, chatID, m.Code)
	if err != nil {
		slog.Default().WarnContext(ctx, "add_expense: план недоступен, предупреждение по старому пути", "err", err, "chat_id", chatID)
		return "", false
	}
	if !ok {
		return "", false
	}
	if t.RecurringID != nil {
		return "", true
	}
	l, found := snap.ForCategory(t.CategoryName)
	if !found || l.Over <= 0 {
		return "", true
	}
	return formatPlanWarning(l, m), true
}

// formatPlanWarning — текст предупреждения плана; числа готовые, целые, в валюте показа.
func formatPlanWarning(l botlimits.Limit, m safetospend.Display) string {
	used := fmt.Sprintf("потрачено %s", m.Signed(int(l.Spent)))
	if l.Planned > 0 {
		used += fmt.Sprintf(", запланировано %s", m.Signed(int(l.Planned)))
	}
	return fmt.Sprintf("\n\n⚠️ Лимит «%s» пробит на %s (лимит %s, %s).",
		l.Category, m.Signed(int(l.Over)), m.Signed(int(l.Amount)), used)
}
