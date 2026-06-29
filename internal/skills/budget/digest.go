package budgetskill

import (
	"context"
	"fmt"
	"time"

	"simpleAI/internal/budget"
)

// nowFunc — источник текущего времени, подменяется в тестах для проверки
// границы таймзоны.
var nowFunc = time.Now

// digestStore — узкий контракт стора для дайджеста (тестируемость).
// *budget.Store его удовлетворяет.
type digestStore interface {
	GetSummary(ctx context.Context, p budget.Period) (*budget.Summary, error)
	GetExchangeRates(ctx context.Context) (map[string]float64, error)
}

// DigestProvider строит текстовый дайджест трат за вчера для напоминаний.
// Реализует notify.DigestSource структурно (метод YesterdayDigest), что держит
// пакет notify развязанным: рендер и конверсия валют живут здесь (budget/skills),
// notify знает только сигнатуру интерфейса.
type DigestProvider struct {
	store digestStore
}

// NewDigestProvider создаёт провайдер дайджеста.
func NewDigestProvider(store digestStore) *DigestProvider {
	return &DigestProvider{store: store}
}

// YesterdayDigest возвращает однострочный дайджест трат за вчера в THB-экв.
//
// chatID НЕ участвует в выборке данных: бюджет — глобальный single-household
// ledger (budget_transaction без chat_id, store.go GetSummary). chatID и loc
// определяют только календарный день «вчера» в таймзоне получателя.
//
// Пустой день (нет трат) → "" (вызывающий не показывает блок).
func (d *DigestProvider) YesterdayDigest(ctx context.Context, chatID int64, loc *time.Location) (string, error) {
	if loc == nil {
		loc = time.UTC
	}
	_ = chatID // global ledger: chatID только для адресации, не фильтр данных

	// Календарный день «вчера» берём в loc, диапазон якорим UTC-midnight —
	// transaction_date это DATE, важна только дата (консистентно с query.go).
	y := nowFunc().In(loc).AddDate(0, 0, -1)
	p := budget.Period{
		From: time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, time.UTC),
		To:   time.Date(y.Year(), y.Month(), y.Day(), 23, 59, 59, 0, time.UTC),
	}

	sum, err := d.store.GetSummary(ctx, p)
	if err != nil {
		return "", fmt.Errorf("digest: get summary: %w", err)
	}

	totalTHB := summaryTotalTHB(sum, d.rates(ctx))
	if totalTHB <= 0 {
		return "", nil
	}
	return fmt.Sprintf("💸 Вчера потрачено: ~%.0f ฿", totalTHB), nil
}

// rates возвращает курсы из стора с фолбэком на rubRates (как в callback.go).
func (d *DigestProvider) rates(ctx context.Context) map[string]float64 {
	rates, err := d.store.GetExchangeRates(ctx)
	if err != nil || len(rates) == 0 {
		return rubRates
	}
	for k, v := range rubRates {
		if _, ok := rates[k]; !ok {
			rates[k] = v
		}
	}
	return rates
}

// summaryTotalTHB сводит расходы сводки к THB-экв.
// Все валюты → RUB (summaryTotalRUB), затем делёж на курс THB→RUB.
// Курс THB берётся из rates с фолбэком на rubRates; защита от нуля.
func summaryTotalTHB(s *budget.Summary, rates map[string]float64) float64 {
	thbRate := rates["THB"]
	if thbRate <= 0 {
		thbRate = rubRates["THB"]
	}
	return summaryTotalRUB(s, rates) / thbRate
}
