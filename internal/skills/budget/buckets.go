package budgetskill

import "fmt"

// Bucket описывает корзину расходов.
type Bucket struct {
	ID         string   // идентификатор для callback_data
	Name       string   // отображаемое название
	Icon       string   // emoji
	Categories []string // категории, входящие в корзину (lowercase)
	Default    bool     // fallback для категорий без явного mapping
}

// BucketConfig — упорядоченный список корзин.
// Ровно одна корзина должна иметь Default=true.
type BucketConfig []Bucket

// defaultBuckets возвращает дефолтную конфигурацию корзин.
// Редактируй только здесь — логика не меняется.
func defaultBuckets() BucketConfig {
	return BucketConfig{
		{
			ID:         "obligations",
			Name:       "Обязательства",
			Icon:       "🏠",
			Categories: []string{"жильё", "кредит", "водоснабжение", "переводы"},
		},
		{
			ID:         "goals",
			Name:       "Цели",
			Icon:       "🎯",
			Categories: []string{"инвестиции", "накопления"},
		},
		{
			ID:      "life",
			Name:    "Жизнь и радость",
			Icon:    "✨",
			Default: true,
		},
	}
}

// Validate проверяет что ровно одна корзина Default=true.
func (bc BucketConfig) Validate() error {
	defaults := 0
	for _, b := range bc {
		if b.Default {
			defaults++
		}
	}
	if defaults != 1 {
		return fmt.Errorf("BucketConfig: expected exactly 1 default bucket, got %d", defaults)
	}
	return nil
}

// BucketTotal содержит сумму по одной корзине.
type BucketTotal struct {
	Bucket   Bucket
	RUBTotal float64
	// категории внутри корзины с суммами
	Categories map[string]float64
}

// GroupByBuckets распределяет категории по корзинам согласно конфигу.
func GroupByBuckets(cats map[string]float64, cfg BucketConfig) []BucketTotal {
	totals := make([]BucketTotal, len(cfg))
	for i, b := range cfg {
		totals[i] = BucketTotal{
			Bucket:     b,
			Categories: make(map[string]float64),
		}
	}

	// индекс category→bucket index
	catIndex := make(map[string]int)
	defaultIdx := -1
	for i, b := range cfg {
		if b.Default {
			defaultIdx = i
		}
		for _, c := range b.Categories {
			catIndex[c] = i
		}
	}

	for catName, amount := range cats {
		idx, ok := catIndex[catName]
		if !ok {
			idx = defaultIdx
		}
		if idx < 0 {
			continue
		}
		totals[idx].RUBTotal += amount
		totals[idx].Categories[catName] += amount
	}

	return totals
}
