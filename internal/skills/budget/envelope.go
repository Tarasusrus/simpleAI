package budgetskill

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/skills/safetospend"
)

// envelopeForecastMonths — окно истории, из которого считаются лимиты долей.
// Совпадает с окном прогноза в safe_to_spend: лимит и допуск к нему (глубина
// истории) обязаны меряться по одной и той же выборке.
const envelopeForecastMonths = 3

// startEnvelope заводит «живой конверт» (ADR-007 H3) СРАЗУ с раскладкой прихода
// по категорийным долям (ADR-008).
//
// Порядок продиктован ADR-008 §3: сначала снимаются обязательства, делится
// только FreeAfterObligations. Раскладать сырой приход нельзя — доли начали бы
// конкурировать с уже известными регулярными платежами и суммарно обещали бы
// больше денег, чем есть.
//
// Конверт и его доли пишутся ОДНОЙ транзакцией: конверт без раскладки — это
// состояние, в котором трате некуда падать (budget.ResolveShare вернёт nil), а
// на вопрос «сколько осталось в конвертах» отвечать нечем.
func (s *BudgetSkill) startEnvelope(ctx context.Context, req budgetInput) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok || chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "RUB"
	}

	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil || rates["THB"] == 0 {
		slog.Default().ErrorContext(ctx, "start_envelope: rates", "err", err)
		return "Не могу разложить приход — обнови курс валют командой /rates.", nil
	}
	incomeTHB, ok := budget.ToTHB(req.Amount, currency, rates)
	if !ok {
		return fmt.Sprintf("Не знаю курс валюты %s — обнови /rates.", currency), nil
	}

	h := budget.ResolveHorizon(req.Period, time.Now(), budget.DefaultHorizonDays)

	// Обязательства — за весь горизонт конверта (та же семантика, что в
	// safe_to_spend: recurring.next_date / debt.due_date <= конец периода).
	snap, err := s.store.GetPeriodSnapshot(ctx, chatID, h.From, h.To, rates)
	if err != nil {
		slog.Default().ErrorContext(ctx, "start_envelope: period snapshot", "err", err, "chat_id", chatID)
		return "Временная ошибка при сборе финансового снимка — попробуй позже.", nil
	}
	plannedTHB, _, err := s.store.PlannedExpensesTHB(ctx, chatID, rates)
	if err != nil {
		slog.Default().WarnContext(ctx, "start_envelope: planned (continuing with 0)", "err", err)
		plannedTHB = 0
	}

	forecast, err := s.store.GetForecastData(ctx, envelopeForecastMonths, rates)
	if err != nil {
		slog.Default().ErrorContext(ctx, "start_envelope: forecast", "err", err)
		return "Не удалось поднять историю трат — без неё раскладка была бы выдумана. Попробуй позже.", nil
	}
	// Глубина истории — из БД, а не из длины прогноза: allocateShares по пустой
	// карте не назначит НИ ОДНОГО лимита и уведёт весь приход в накопления.
	history, err := s.store.CategoryHistoryMonths(ctx, envelopeForecastMonths)
	if err != nil {
		slog.Default().ErrorContext(ctx, "start_envelope: history depth", "err", err)
		return "Не удалось оценить глубину истории трат — попробуй позже.", nil
	}
	overrides := s.shareOverrides(ctx, chatID, rates)
	// Регулярные платежи становятся видимыми конвертами (simpleAI-faeq.11).
	// Ошибка чтения НЕ проглатывается: без них раскладка разделит между едой и
	// накоплениями деньги, которые уже обещаны аренде и кредиту.
	recurring, err := s.store.ListRecurring(ctx, chatID)
	if err != nil {
		slog.Default().ErrorContext(ctx, "start_envelope: recurring", "err", err, "chat_id", chatID)
		return "Не удалось поднять регулярные платежи — без них раскладка обещала бы деньги, которые уже расписаны. Попробуй позже.", nil
	}

	plan := safetospend.PlanEnvelope(safetospend.EnvelopePlanInput{
		IncomeTHB:  incomeTHB,
		Snapshot:   snap,
		PlannedTHB: plannedTHB,
		Forecast:   forecast,
		Rates:      rates,
		Days:       h.Days(),
		Overrides:  overrides,
		History:    history,
		Recurring:  recurring,
		From:       h.From,
	})
	s.attachCategoryIDs(ctx, plan.Shares)

	// Перенос накопленного с прошлого конверта — ДО записи нового: доли пишутся
	// одной транзакцией с конвертом, и carried_in должен быть уже в них.
	now := time.Now()
	carried, err := s.carryFromPrevious(ctx, chatID, rates, now, plan.Shares)
	if err != nil {
		slog.Default().ErrorContext(ctx, "start_envelope: carry over", "err", err, "chat_id", chatID)
		return "Не удалось поднять накопления с прошлого конверта — новый конверт не заведён, чтобы они не потерялись. Попробуй ещё раз.", nil
	}
	plan.Shares = carried

	envID, err := s.store.CreateEnvelopeWithShares(ctx, chatID, req.Amount, currency, h.From, h.To, plan.Shares, now)
	if err != nil {
		slog.Default().ErrorContext(ctx, "start_envelope: create envelope with shares", "err", err, "chat_id", chatID)
		return "Не удалось сохранить конверт — попробуй ещё раз.", nil
	}

	slog.Default().InfoContext(ctx, "start_envelope",
		"chat_id", chatID, "envelope_id", envID, "amount", req.Amount, "currency", currency,
		"free_after_obl_thb", plan.Result.FreeAfterObligations, "shares", len(plan.Shares),
		"warnings", len(plan.Warnings), "display", envelopeDisplay(req, rates).Code)

	// Строки «вне конвертов» больше нет: она показывала 0 ฿ при аренде 18 000 и
	// уборке 2 500 за период (simpleAI-faeq.10, баг 4) — потому что считала
	// ФАКТ прошедших трат, а оператор читал её как «что ещё предстоит». Теперь
	// каждый предстоящий платёж стоит своей строкой с датой, и объяснять
	// расхождение отдельной сводкой больше нечем.

	return safetospend.FormatEnvelopePlan(safetospend.EnvelopeReply{
		Plan:           plan,
		RubPerTHB:      rates["THB"],
		Display:        envelopeDisplay(req, rates),
		Period:         h.Label,
		From:           h.From,
		To:             h.To,
		IncomeAmount:   req.Amount,
		IncomeCurrency: currency,
	}), nil
}

// envelopeDisplay — валюта, которой печатать конверты: то, что попросил
// оператор, иначе дефолт THB (safetospend.NewDisplay). Одна точка на весь
// budget-скилл, чтобы раскладка, пересчёт лимита и предупреждение о пробитом
// конверте не разъехались по валютам.
func envelopeDisplay(req budgetInput, rates map[string]float64) safetospend.Display {
	return safetospend.NewDisplay(displayCode(req), rates["THB"])
}

// displayCode — код валюты показа из входа LLM, со страховкой разбором текста:
// модель кладёт просьбу «в рублях» то в display_currency, то в description, то
// не кладёт никуда. Пусто → NewDisplay сам возьмёт дефолт (THB).
func displayCode(req budgetInput) string {
	if c := strings.TrimSpace(req.DisplayCurrency); c != "" {
		return c
	}
	return safetospend.ParseDisplayCurrency(req.Description)
}

// carryFromPrevious переносит накопления прошлого активного конверта в новую
// раскладку (ADR-008 §9). Остаток считается computeShareRemaining внутри
// safetospend.CarryOver — второй формулы остатка в проекте нет.
//
// Ошибка здесь НЕ проглатывается, в отличие от override'ов и категорий: без
// раскладки лимиты просто станут авто-лимитами, а без переноса накопленные
// деньги молча исчезнут вместе со старым конвертом. Дешевле не завести конверт.
func (s *BudgetSkill) carryFromPrevious(
	ctx context.Context,
	chatID int64,
	rates map[string]float64,
	now time.Time,
	next []budget.EnvelopeShare,
) ([]budget.EnvelopeShare, error) {
	prev, ok, err := s.store.GetActiveEnvelope(ctx, chatID)
	if err != nil {
		return nil, fmt.Errorf("прошлый конверт: %w", err)
	}
	if !ok {
		// Первый приход: переносить нечего, но carried_in всё равно обнуляем
		// явно — этим занимается CarryOver с пустой прошлой раскладкой.
		return safetospend.CarryOver(safetospend.CarryInput{Rates: rates, NextShares: next}), nil
	}
	prevShares, err := s.store.ListShares(ctx, chatID, prev.ID)
	if err != nil {
		return nil, fmt.Errorf("доли прошлого конверта: %w", err)
	}
	if len(prevShares) == 0 {
		return safetospend.CarryOver(safetospend.CarryInput{Rates: rates, NextShares: next}), nil
	}

	// Факт считается за тот период, которым конверт СЕЙЧАС и закроется:
	// period_end := now − 1 день, обрезанный сверху своим period_end и снизу
	// period_start (ADR-008 §10). Иначе перенос учёл бы траты дня переключения,
	// которые по факту достанутся уже новому конверту.
	to := now.AddDate(0, 0, -1)
	if to.After(prev.PeriodEnd) {
		to = prev.PeriodEnd
	}
	var spent []budget.CategorySpentRow
	superseded := to.Before(prev.PeriodStart)
	if !superseded {
		spent, err = s.store.SpentByCategoryExcludingRecurring(ctx, prev.PeriodStart, to)
		if err != nil {
			return nil, fmt.Errorf("факт прошлого конверта: %w", err)
		}
	}
	// to < period_start — конверт заведён и закрыт в один день: он не прожил ни
	// дня и вытесняется повторной раскладкой. Переносить его накопления целиком
	// нельзя — они профинансированы тем же приходом (simpleAI-faeq.10, баг 1).

	return safetospend.CarryOver(safetospend.CarryInput{
		PrevShares:     prevShares,
		PrevSpent:      spent,
		Rates:          rates,
		NextShares:     next,
		PrevSuperseded: superseded,
	}), nil
}

// shareOverrides — ручные лимиты долей, приведённые к THB (доли хранятся в THB,
// а override оператор мог задать в рублях). Ключ — уже нормализованное имя из
// стора. Ошибка чтения не валит раскладку: без override'ов она просто вернётся
// к авто-лимитам, а отказ отвечать хуже.
func (s *BudgetSkill) shareOverrides(ctx context.Context, chatID int64, rates map[string]float64) map[string]float64 {
	list, err := s.store.ListOverrides(ctx, chatID)
	if err != nil {
		slog.Default().WarnContext(ctx, "start_envelope: overrides (continuing without)", "err", err)
		return nil
	}
	return overridesToTHB(list, rates)
}

// overridesToTHB — единственное место, где ручной лимит переводится в THB.
//
// Вынесено чистой функцией нарочно: «лимит в рублях и лимит в батах при одном
// курсе дают одну и ту же долю» — утверждение про конвертацию, и проверять его
// надо там, где она происходит, а не через живой стор. Лимит хранится как
// сказан (сумма + код валюты), курс применяется ровно один раз — здесь; если
// бы конвертация была ещё и на записи, курс задвоился бы.
func overridesToTHB(list []budget.EnvelopeOverride, rates map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(list))
	for _, o := range list {
		thb, ok := budget.ToTHB(o.Amount, o.Currency, rates)
		if !ok {
			slog.Default().Warn("start_envelope: override без курса — пропущен",
				"share", o.ShareName, "currency", o.Currency)
			continue
		}
		out[o.ShareName] = thb
	}
	return out
}

// attachCategoryIDs проставляет долям category_id по справочнику категорий.
// Аллокатор знает только имена (прогноз name-keyed), но матчинг траты к доле
// идёт СНАЧАЛА по id (budget.ResolveShare): без id доля не поймает транзакцию,
// у которой имя категории отличается регистром от прогнозного.
// Best-effort: категории не прочитались — остаётся матчинг по имени.
func (s *BudgetSkill) attachCategoryIDs(ctx context.Context, shares []budget.EnvelopeShare) {
	cats, err := s.store.ListCategories(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "start_envelope: categories (матчинг только по имени)", "err", err)
		return
	}
	byName := make(map[string]budget.Category, len(cats))
	for _, c := range cats {
		if c.Type != "expense" {
			continue
		}
		key := budget.NormalizeName(c.Name)
		if _, exists := byName[key]; exists {
			// Регистровые дубли («Еда» и «еда» — разные строки с разными id):
			// какой из них «тот самый», неизвестно, поэтому не берём ни один —
			// матчинг по имени всё равно поймает оба.
			continue
		}
		byName[key] = c
	}
	for i := range shares {
		for j := range shares[i].Categories {
			key := budget.NormalizeName(shares[i].Categories[j].CategoryName)
			if c, ok := byName[key]; ok {
				id := c.ID
				shares[i].Categories[j].CategoryID = &id
			}
		}
	}
}
