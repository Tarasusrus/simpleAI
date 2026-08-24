package budgetskill

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/skills/safetospend"
)

// Ручная правка лимита конверта словами (ADR-008 §2): «на еду хватит 15000».
//
// Почему это действие budget-скилла, а не safe_to_spend: правка ПИШЕТ в базу
// (budget_envelope_limit_override + перезапись долей активного конверта), а
// safe_to_spend по ADR-002 read-only.
//
// Правка живёт МЕЖДУ приходами: она сохраняется отдельной строкой override'а и
// применяется при каждой следующей раскладке, пока её не сняли. Поэтому
// «применить к текущему конверту» и «запомнить» — два разных шага, и первый без
// второго был бы правкой на один период (а оператор просил правило).

// setShareLimit сохраняет ручной лимит доли и пересчитывает активный конверт.
//
// Порядок именно такой: сначала запись override'а, потом пересчёт. Пересчёт
// читает override'ы из базы (общий путь с start_envelope — shareOverrides), так
// что несохранённый лимит в раскладку бы не попал; а если пересчёт упадёт,
// сохранённое правило всё равно применится при следующем приходе.
func (s *BudgetSkill) setShareLimit(ctx context.Context, req budgetInput) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok || chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}
	name := shareLimitName(req)
	if name == "" {
		return "На какой конверт поставить лимит? Назови категорию — например «на еду хватит 15000».", nil
	}
	if req.Amount <= 0 {
		return fmt.Sprintf("Сколько закладывать на «%s»? Скажи сумму — например «на %s хватит 15000».", name, name), nil
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		// Тот же дефолт, что у add_expense и start_envelope: НЕназванная валюта
		// суммы — рубли. Это про сумму в сообщении, а не про валюту показа:
		// показ по умолчанию батовый (display_currency), и путать их нельзя —
		// иначе «на еду хватит 15000» без валюты молча стало бы 15000 ฿.
		//
		// Названную валюту («5000 бат») сюда доносит поле currency, и дальше
		// она конвертируется РОВНО ОДИН раз — в shareOverrides при раскладке
		// (budget.ToTHB). Здесь сумма сохраняется как сказана, вместе с кодом
		// валюты: конвертировать и тут, и там значило бы задвоить курс.
		currency = "RUB"
	}

	if err := s.store.SetOverride(ctx, chatID, name, req.Amount, currency); err != nil {
		slog.Default().ErrorContext(ctx, "set_share_limit: save override", "err", err, "chat_id", chatID, "share", name)
		return "Не удалось сохранить лимит — попробуй ещё раз.", nil
	}
	slog.Default().InfoContext(ctx, "set_share_limit",
		"chat_id", chatID, "share", name, "amount", req.Amount, "currency", currency)

	head := fmt.Sprintf("📌 Лимит на «%s» — %.0f %s. Запомнил: применю и к следующим приходам, пока не скажешь «убери лимит на %s».",
		name, req.Amount, currency, name)
	return head + s.replanTail(ctx, chatID, req), nil
}

// clearShareLimit снимает ручной лимит: доля снова считается из истории трат.
// Идемпотентно — «убери лимит» на доле без override'а не ошибка, а no-op с тем
// же ответом: оператору важно состояние «лимита нет», а не факт удаления строки.
func (s *BudgetSkill) clearShareLimit(ctx context.Context, req budgetInput) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok || chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}
	name := shareLimitName(req)
	if name == "" {
		return "С какого конверта снять лимит? Назови категорию — например «убери лимит на еду».", nil
	}

	if err := s.store.DeleteOverride(ctx, chatID, name); err != nil {
		slog.Default().ErrorContext(ctx, "clear_share_limit: delete override", "err", err, "chat_id", chatID, "share", name)
		return "Не удалось снять лимит — попробуй ещё раз.", nil
	}
	slog.Default().InfoContext(ctx, "clear_share_limit", "chat_id", chatID, "share", name)

	head := fmt.Sprintf("🧹 Лимит на «%s» снят — снова считаю его по истории трат.", name)
	return head + s.replanTail(ctx, chatID, req), nil
}

// shareLimitName — имя доли из входа LLM. Смотрим и name, и category: доля
// называется по категории трат («еда»), и модель кладёт её то в одно поле, то в
// другое. Пустое имя — не ошибка выполнения, а переспрос (см. вызывающих).
func shareLimitName(req budgetInput) string {
	if n := strings.TrimSpace(req.Name); n != "" {
		return n
	}
	return strings.TrimSpace(req.Category)
}

// replanTail пересчитывает активный конверт и возвращает хвост ответа. Ошибка
// пересчёта НЕ роняет ответ: сам лимит уже сохранён и применится при следующем
// приходе, а отказ отвечать вместо этого спрятал бы успешную часть работы.
func (s *BudgetSkill) replanTail(ctx context.Context, chatID int64, req budgetInput) string {
	res, ok, err := s.replanActiveEnvelope(ctx, chatID, req)
	switch {
	case err != nil:
		slog.Default().ErrorContext(ctx, "share limit: replan active envelope", "err", err, "chat_id", chatID)
		return "\n\n⚠️ Текущий конверт пересчитать не удалось — правило применю к следующему приходу."
	case !ok:
		return "\n\nАктивного конверта сейчас нет — разложу с этим лимитом следующий приход."
	default:
		return "\n\n" + formatReplan(*res)
	}
}

// replanActiveEnvelope пересчитывает раскладку активного конверта и перезаписывает
// его доли. ok=false — активного конверта нет (это не ошибка).
//
// Приход и горизонт берутся из САМОГО конверта, а не из сообщения: пересчёт
// обязан делить ту же сумму на том же периоде, иначе правка одного лимита
// молча переписала бы весь конверт под сегодняшнюю дату.
func (s *BudgetSkill) replanActiveEnvelope(ctx context.Context, chatID int64, req budgetInput) (*replan, bool, error) {
	env, ok, err := s.store.GetActiveEnvelope(ctx, chatID)
	if err != nil {
		return nil, false, fmt.Errorf("активный конверт: %w", err)
	}
	if !ok {
		return nil, false, nil
	}

	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("курсы валют: %w", err)
	}
	if rates["THB"] == 0 {
		// Без курса THB раскладку считать нечем: доли хранятся в THB.
		return nil, false, fmt.Errorf("курс THB не задан — обнови /rates")
	}
	incomeTHB, ok := budget.ToTHB(env.IncomeAmount, env.IncomeCurrency, rates)
	if !ok {
		return nil, false, fmt.Errorf("нет курса валюты конверта %s", env.IncomeCurrency)
	}

	h := budget.Horizon{Period: budget.Period{From: env.PeriodStart, To: env.PeriodEnd}}
	snap, err := s.store.GetPeriodSnapshot(ctx, chatID, h.From, h.To, rates)
	if err != nil {
		return nil, false, fmt.Errorf("снимок периода: %w", err)
	}
	plannedTHB, _, err := s.store.PlannedExpensesTHB(ctx, chatID, rates)
	if err != nil {
		slog.Default().WarnContext(ctx, "replan: planned (continuing with 0)", "err", err)
		plannedTHB = 0
	}
	forecast, err := s.store.GetForecastData(ctx, envelopeForecastMonths, rates)
	if err != nil {
		return nil, false, fmt.Errorf("прогноз трат: %w", err)
	}
	history, err := s.store.CategoryHistoryMonths(ctx, envelopeForecastMonths)
	if err != nil {
		return nil, false, fmt.Errorf("глубина истории: %w", err)
	}

	plan := safetospend.PlanEnvelope(safetospend.EnvelopePlanInput{
		IncomeTHB:  incomeTHB,
		Snapshot:   snap,
		PlannedTHB: plannedTHB,
		Forecast:   forecast,
		Rates:      rates,
		Days:       h.Days(),
		Overrides:  s.shareOverrides(ctx, chatID, rates),
		History:    history,
	})
	s.attachCategoryIDs(ctx, plan.Shares)
	plan.Shares, err = s.keepCarriedIn(ctx, chatID, env.ID, plan.Shares)
	if err != nil {
		return nil, false, err
	}

	if err := s.store.ReplaceShares(ctx, chatID, env.ID, plan.Shares); err != nil {
		return nil, false, fmt.Errorf("перезапись долей: %w", err)
	}
	slog.Default().InfoContext(ctx, "share limit: envelope replanned",
		"chat_id", chatID, "envelope_id", env.ID, "shares", len(plan.Shares),
		"free_after_obl_thb", plan.Result.FreeAfterObligations)
	return &replan{plan: plan, display: envelopeDisplay(req, rates)}, true, nil
}

// keepCarriedIn сохраняет уже перенесённое (carried_in) в пересчитанной
// раскладке. Перенос с прошлого конверта — не результат раскладки, а факт
// прошлого периода (ADR-008 §9): пересчёт лимита его не считает заново и не
// имеет права обнулить.
//
// Кладёт его тот же safetospend.ApplyCarry, что и при заведении нового конверта.
// Своей ветки «расставить carried_in по именам» здесь быть не может: доля-
// носитель переноса («отпуск», выпавший из авто-раскладки) в plan.Shares не
// приходит — PlanEnvelope лишних save-долей не выдаёт, — и ReplaceShares молча
// вынес бы её вместе с деньгами. ApplyCarry такую долю воссоздаёт.
//
// Ошибка чтения старых долей НЕ проглатывается, в отличие от override'ов и
// категорий: пересчёт без них затрёт накопленное. Дешевле не пересчитать —
// сам лимит уже сохранён и применится при следующем приходе.
func (s *BudgetSkill) keepCarriedIn(
	ctx context.Context,
	chatID int64,
	envelopeID uuid.UUID,
	shares []budget.EnvelopeShare,
) ([]budget.EnvelopeShare, error) {
	old, err := s.store.ListShares(ctx, chatID, envelopeID)
	if err != nil {
		return nil, fmt.Errorf("доли текущего конверта: %w", err)
	}
	carried := make([]safetospend.CarriedAmount, 0, len(old))
	for _, sh := range old {
		if sh.CarriedIn <= 0 {
			continue
		}
		carried = append(carried, safetospend.CarriedAmount{
			Name:       sh.Name,
			Amount:     sh.CarriedIn,
			Source:     sh.Source,
			Categories: sh.Categories,
		})
	}
	return safetospend.ApplyCarry(shares, carried), nil
}

// replan — результат пересчёта: сама раскладка плюс валюта, которой её
// печатать. Валюта показа идёт рядом с планом, а не берётся форматтером заново:
// печать обязана показывать ТЕ ЖЕ деньги, по которым посчитаны доли.
type replan struct {
	plan    safetospend.EnvelopePlan
	display safetospend.Display
}

// formatReplan печатает пересчитанную раскладку. Форматирование локальное и
// короткое: полный разбор конверта (приход → обязательства → доли) оператор уже
// видел при start_envelope, здесь нужен только результат правки.
func formatReplan(r replan) string {
	m := r.display
	var b strings.Builder
	b.WriteString("Пересчитал текущий конверт:\n")
	for _, sh := range safetospend.SpendShares(r.plan.Shares) {
		mark := ""
		if sh.Source == budget.ShareSourceOverride {
			mark = " (вручную)"
		}
		fmt.Fprintf(&b, "   • %s — %s%s\n", sh.Name, m.Fmt(sh.Allocated), mark)
	}
	for _, sh := range safetospend.SaveShares(r.plan.Shares) {
		fmt.Fprintf(&b, "   💰 %s — %s\n", sh.Name, m.Fmt(sh.Allocated))
	}
	for _, w := range r.plan.Warnings {
		fmt.Fprintf(&b, "⚠️ %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n")
}
