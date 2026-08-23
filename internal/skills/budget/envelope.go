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

	plan := safetospend.PlanEnvelope(safetospend.EnvelopePlanInput{
		IncomeTHB:  incomeTHB,
		Snapshot:   snap,
		PlannedTHB: plannedTHB,
		Forecast:   forecast,
		Rates:      rates,
		Days:       h.Days(),
		Overrides:  overrides,
		History:    history,
	})
	s.attachCategoryIDs(ctx, plan.Shares)

	envID, err := s.store.CreateEnvelopeWithShares(ctx, chatID, req.Amount, currency, h.From, h.To, plan.Shares)
	if err != nil {
		slog.Default().ErrorContext(ctx, "start_envelope: create envelope with shares", "err", err, "chat_id", chatID)
		return "Не удалось сохранить конверт — попробуй ещё раз.", nil
	}

	// «Вне конвертов» — факт с начала конверта по сегодня. Конец периода в
	// будущем, а факта из будущего не бывает: верхняя граница обрезается по now.
	outsideTo := time.Now()
	if outsideTo.After(h.To) {
		outsideTo = h.To
	}
	outsideTHB, err := s.store.SpentOutsideShares(ctx, h.From, outsideTo, rates)
	if err != nil {
		slog.Default().WarnContext(ctx, "start_envelope: outside shares (continuing with 0)", "err", err)
		outsideTHB = 0
	}

	slog.Default().InfoContext(ctx, "start_envelope",
		"chat_id", chatID, "envelope_id", envID, "amount", req.Amount, "currency", currency,
		"free_after_obl_thb", plan.Result.FreeAfterObligations, "shares", len(plan.Shares),
		"warnings", len(plan.Warnings))

	return safetospend.FormatEnvelopePlan(safetospend.EnvelopeReply{
		Plan:           plan,
		RubPerTHB:      rates["THB"],
		Period:         h.Label,
		From:           h.From,
		To:             h.To,
		IncomeAmount:   req.Amount,
		IncomeCurrency: currency,
		OutsideTHB:     outsideTHB,
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
	out := make(map[string]float64, len(list))
	for _, o := range list {
		thb, ok := budget.ToTHB(o.Amount, o.Currency, rates)
		if !ok {
			slog.Default().WarnContext(ctx, "start_envelope: override без курса — пропущен",
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
		key := strings.ToLower(strings.TrimSpace(c.Name))
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
			key := strings.ToLower(strings.TrimSpace(shares[i].Categories[j].CategoryName))
			if c, ok := byName[key]; ok {
				id := c.ID
				shares[i].Categories[j].CategoryID = &id
			}
		}
	}
}
