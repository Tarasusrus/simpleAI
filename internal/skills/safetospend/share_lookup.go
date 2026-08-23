package safetospend

import (
	"github.com/google/uuid"

	"simpleAI/internal/budget"
)

// ShareRemainingFor — остаток ТОЙ доли конверта, которой принадлежит категория
// траты (ADR-008 §6, §8).
//
// Экспортируется ради предупреждения в момент траты (budget.add_expense):
// формула остатка живёт в одном месте — computeShareRemaining, — и повторно её
// нигде не пишут. Здесь только выбор нужной строки из уже посчитанной раскладки.
//
// Матчинг тот же двухступенчатый ResolveShare: category_id → lower(name) →
// fallback-доля «прочее». Категория без своей доли попадает в «прочее», и
// предупреждение считается по ней — иначе часть факта пропала бы молча.
//
// Второе значение false, если долей нет вовсе или падать некуда (нет даже
// fallback-доли).
func ShareRemainingFor(
	shares []budget.EnvelopeShare,
	spentByCategory []budget.CategorySpentRow,
	rates map[string]float64,
	categoryID *uuid.UUID,
	categoryName string,
) (ShareRemaining, bool) {
	target := budget.ResolveShare(shares, categoryID, categoryName)
	if target == nil {
		return ShareRemaining{}, false
	}
	// ResolveShare возвращает указатель ВНУТРЬ shares, а computeShareRemaining
	// сохраняет порядок входа — значит индекс доли и индекс её остатка совпадают.
	// Сопоставление по индексу, а не по имени: имена долей приходят от раскладки
	// и сравнивать их строками значило бы завести второй, расходящийся ключ.
	out := computeShareRemaining(shares, spentByCategory, rates)
	for i := range shares {
		if &shares[i] == target {
			if i < len(out) {
				return out[i], true
			}
			break
		}
	}
	return ShareRemaining{}, false
}
