package safetospend

import (
	"simpleAI/internal/budget"
)

// Перенос накопленного между конвертами (ADR-008 §9). Чистая функция: остаток
// прошлого конверта считается ТЕМ ЖЕ computeShareRemaining, что и ответ «сколько
// осталось» — второй формулы остатка в проекте быть не должно, иначе перенос и
// показ разойдутся на глазах у оператора.

// CarryInput — вход переноса.
//
// PrevSpent — сырой факт за период ЗАКРЫВАЕМОГО конверта (уже без recurring).
// Пустой факт допустим и означает «за конверт не потрачено ничего» — так же
// выглядит конверт нулевой длины, факт по которому считать нельзя (ADR-008 §10).
type CarryInput struct {
	PrevShares []budget.EnvelopeShare
	PrevSpent  []budget.CategorySpentRow
	Rates      map[string]float64
	NextShares []budget.EnvelopeShare
}

// CarryOver проставляет новым долям carried_in по остаткам старых.
//
// Правила (ADR-008 §9):
//   - kind='save' — остаток переносится в одноимённую новую долю;
//   - kind='spend' — остаток сгорает, carried_in = 0 ЯВНО (новая раскладка
//     приходит из PlanEnvelope с нулями, но полагаться на это нельзя: доля,
//     пришедшая откуда-то ещё с ненулевым carried_in, молча удвоила бы деньги);
//   - save-доля, исчезнувшая из новой раскладки, создаётся принудительно с
//     allocated=0 и carried_in=остаток — иначе накопленное пропало бы вместе с
//     долей.
//
// Ключ переноса — нормализованное имя доли (UNIQUE(envelope_id, name) для того
// и стоит). Position в ключ НЕ входит, в отличие от shareKey внутри одной
// раскладки: между двумя конвертами позиции своей истории не имеют — раскладка
// пересчитывается заново, и «еда» легко переезжает с первой строки на третью.
//
// Отрицательный остаток (пробитая save-доля) не переносится: в carried_in это
// был бы долг, вычитаемый из нового прихода молча, без единой строки в ответе.
func CarryOver(in CarryInput) []budget.EnvelopeShare {
	next := make([]budget.EnvelopeShare, len(in.NextShares))
	copy(next, in.NextShares)
	for i := range next {
		next[i].CarriedIn = 0
	}
	if len(in.PrevShares) == 0 {
		return next
	}

	prevRemaining := computeShareRemaining(in.PrevShares, in.PrevSpent, in.Rates)

	// Индекс новых долей по нормализованному имени. Дубли имён внутри одной
	// раскладки невозможны (UNIQUE), первый выигрывает.
	idx := make(map[string]int, len(next))
	for i := range next {
		key := normalizeShareName(next[i].Name)
		if _, exists := idx[key]; !exists {
			idx[key] = i
		}
	}

	maxPos := 0
	for _, sh := range next {
		if sh.Position > maxPos {
			maxPos = sh.Position
		}
	}

	for pi, rem := range prevRemaining {
		if rem.Kind != budget.ShareKindSave || rem.Remaining <= 0 {
			continue
		}
		key := normalizeShareName(rem.Name)
		if i, ok := idx[key]; ok {
			next[i].CarriedIn = rem.Remaining
			continue
		}
		// Доли с таким именем в новой раскладке нет — заводим её пустой, но с
		// накопленным (ADR-008 §9). Категории копируются со старой доли, чтобы
		// траты по ним продолжали матчиться туда же.
		prev := in.PrevShares[pi]
		maxPos++
		next = append(next, budget.EnvelopeShare{
			Name:       prev.Name,
			Kind:       budget.ShareKindSave,
			Allocated:  0,
			CarriedIn:  rem.Remaining,
			Source:     prev.Source,
			Position:   maxPos,
			Categories: prev.Categories,
		})
		idx[key] = len(next) - 1
	}
	return next
}

// TotalCarriedIn — сколько всего перенесено с прошлого конверта. Отдельная
// функция, а не суммирование в форматтере: строка «перенесено с прошлого раза»
// обязана показывать то же число, которое легло в carried_in долей.
func TotalCarriedIn(shares []budget.EnvelopeShare) float64 {
	var total float64
	for _, sh := range shares {
		total += sh.CarriedIn
	}
	return total
}
