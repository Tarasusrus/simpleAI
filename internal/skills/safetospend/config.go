package safetospend

import "time"

// Настроечные параметры skill'а — единый именованный блок (конвенция ADR-006:
// пороги/константы поведения не разбросаны по коду, а собраны и обоснованы).
// Горизонт по умолчанию берётся из budget.DefaultHorizonDays (общий домен).
const (
	// forecastMonths — окно истории для прогноза бытовых трат.
	forecastMonths = 3
	// prorationBaseDays — прогноз даётся месячным; делим на 30 при пересчёте к
	// длине периода (days/prorationBaseDays).
	prorationBaseDays = 30.0
	// regularIncomeTolerance — |отклонение| прихода от регулярного дохода, при
	// котором приход считается «регулярным» (±15%).
	regularIncomeTolerance = 0.15
	// categoriesTopN — сколько категорий показывать в разбивке «на что уйдут»,
	// остальные сворачиваются в «прочее».
	categoriesTopN = 5
	// defaultLLMTimeout — таймаут LLM-нарратива, независимый от Telegram-хендлера.
	defaultLLMTimeout = 45 * time.Second
)
