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
	if len(in.PrevShares) == 0 {
		return ApplyCarry(in.NextShares, nil)
	}

	prevRemaining := computeShareRemaining(in.PrevShares, in.PrevSpent, in.Rates)
	carried := make([]CarriedAmount, 0, len(prevRemaining))
	for pi, rem := range prevRemaining {
		if rem.Kind != budget.ShareKindSave || rem.Remaining <= 0 {
			continue
		}
		prev := in.PrevShares[pi]
		carried = append(carried, CarriedAmount{
			Name:       prev.Name,
			Amount:     rem.Remaining,
			Source:     prev.Source,
			Categories: prev.Categories,
		})
	}
	return ApplyCarry(in.NextShares, carried)
}

// CarriedAmount — накопленное, которое обязано пережить перезапись раскладки:
// сумма плюс всё, чем доля-носитель воссоздаётся, если свежей раскладке она
// больше не нужна.
type CarriedAmount struct {
	Name       string // отображаемое имя доли; ключ — его нормализованная форма
	Amount     float64
	Source     string
	Categories []budget.EnvelopeShareCategory
}

// ApplyCarry кладёт накопленное в новую раскладку — единственный путь, которым
// carried_in попадает в доли: и при заведении нового конверта (CarryOver), и при
// пересчёте под правку лимита. Отдельной ветки «перенести carried_in по именам»
// быть не должно: она теряет доли-носители, которых в свежей раскладке нет, —
// PlanEnvelope лишних save-долей не выдаёт никогда.
//
// Правила (ADR-008 §9):
//   - carried_in новых долей сначала обнуляется ЯВНО: доля, пришедшая откуда-то
//     ещё с ненулевым carried_in, молча удвоила бы деньги;
//   - имя нашлось — сумма кладётся в найденную долю;
//   - имени нет — заводится save-доля с allocated=0 и carried_in=сумма, иначе
//     накопленное пропало бы вместе с долей.
//
// Ключ — нормализованное имя доли (UNIQUE(envelope_id, name) для того и стоит).
// Position в ключ НЕ входит, в отличие от shareKey внутри одной раскладки:
// раскладка пересчитывается заново, и «еда» легко переезжает со строки на строку.
func ApplyCarry(nextShares []budget.EnvelopeShare, carried []CarriedAmount) []budget.EnvelopeShare {
	next := make([]budget.EnvelopeShare, len(nextShares))
	copy(next, nextShares)
	for i := range next {
		next[i].CarriedIn = 0
	}
	if len(carried) == 0 {
		return next
	}

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

	for _, c := range carried {
		if c.Amount <= 0 {
			continue
		}
		key := normalizeShareName(c.Name)
		if i, ok := idx[key]; ok {
			next[i].CarriedIn = c.Amount
			continue
		}
		// Доли с таким именем в новой раскладке нет — заводим её пустой, но с
		// накопленным. Категории копируются со старой доли, чтобы траты по ним
		// продолжали матчиться туда же.
		maxPos++
		next = append(next, budget.EnvelopeShare{
			Name:       c.Name,
			Kind:       budget.ShareKindSave,
			Allocated:  0,
			CarriedIn:  c.Amount,
			Source:     c.Source,
			Position:   maxPos,
			Categories: c.Categories,
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
