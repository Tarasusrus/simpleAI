package safetospend

import (
	"math"
	"strings"
	"testing"

	"simpleAI/internal/budget"
)

// oneToOne — курсы 1:1, чтобы THB читались как рубли эталона и суммы сверялись
// напрямую (тот же приём, что в compute_test.go).
var oneToOne = map[string]float64{"RUB": 1.0, "THB": 1.0}

func fcRUB(name string, amount float64) budget.CategoryForecast {
	return budget.CategoryForecast{CategoryName: name, Currency: "RUB", ForecastAmount: amount}
}

func sumAllocated(shares []budget.EnvelopeShare) float64 {
	var s float64
	for _, sh := range shares {
		s += sh.Allocated
	}
	return s
}

func allocatedByName(shares []budget.EnvelopeShare) map[string]float64 {
	m := make(map[string]float64, len(shares))
	for _, sh := range shares {
		m[sh.Name] = sh.Allocated
	}
	return m
}

// checkConvergence — главный инвариант ADR-008: Σ Allocated + свободно = free,
// копейка в копейку. «Свободно» — непокрытая часть прихода: при free > 0 её
// забирают «накопления», поэтому Σ Allocated обязана сойтись ровно в free;
// при free <= 0 раскладывать нечего, Σ Allocated = 0, а «свободно» = free.
// Инвариант формулируется ТОЛЬКО на Allocated: RemainingTHB из compute.go —
// другая величина (там три слагаемых), сцеплять их нельзя.
func checkConvergence(t *testing.T, shares []budget.EnvelopeShare, free float64) {
	t.Helper()
	allocated := sumAllocated(shares)
	unallocated := free - allocated // «свободно»: то, что не попало ни в одну долю

	wantAllocated, wantUnallocated := free, 0.0
	if free <= 0 {
		wantAllocated, wantUnallocated = 0, free
	}
	if math.Abs(allocated-wantAllocated) > 0.005 {
		t.Errorf("Σ Allocated: want %.4f, got %.4f (расхождение %.4f)", wantAllocated, allocated, allocated-wantAllocated)
	}
	if math.Abs(unallocated-wantUnallocated) > 0.005 {
		t.Errorf("свободно: want %.4f, got %.4f", wantUnallocated, unallocated)
	}
}

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestAllocateShares(t *testing.T) {
	cases := []struct {
		name      string
		free      float64
		fc        []budget.CategoryForecast
		overrides map[string]float64
		history   map[string]int
		want      map[string]float64 // имя доли → лимит (проверяются только перечисленные)
		wantWarn  string
		wantSrc   map[string]string
	}{
		{
			name: "обычная раскладка: мелочь в «прочее», остаток в накопления",
			free: 27800,
			fc: []budget.CategoryForecast{
				fcRUB("Еда", 12000), fcRUB("Транспорт", 4000),
				fcRUB("Развлечения", 3000), fcRUB("Кафе", 400), // 400 < minShareMonthlyTHB=500 → в «прочее»
				fcRUB("Жильё", 50000), fcRUB("Переводы", 90000), // фикс и движение денег — вне раскладки
			},
			history: map[string]int{"еда": 3, "транспорт": 3, "развлечения": 3, "кафе": 3},
			want: map[string]float64{
				"Еда": 12000, "Транспорт": 4000, "Развлечения": 3000,
				budget.FallbackShareName: 400, savingsShareName: 8400,
			},
		},
		{
			name:    "нехватка остатка: авто-лимиты урезаны пропорционально",
			free:    10000,
			fc:      []budget.CategoryForecast{fcRUB("Еда", 12000), fcRUB("Транспорт", 4000)},
			history: map[string]int{"еда": 3, "транспорт": 3},
			// k = 10000/16000 = 0.625
			want:     map[string]float64{"Еда": 7500, "Транспорт": 2500, savingsShareName: 0},
			wantWarn: "не помещаются",
		},
		{
			name:      "override поверх авто: заменяет лимит, соседей не двигает",
			free:      27800,
			fc:        []budget.CategoryForecast{fcRUB("Еда", 12000), fcRUB("Транспорт", 4000)},
			overrides: map[string]float64{"еда": 15000},
			history:   map[string]int{"еда": 3, "транспорт": 3},
			want:      map[string]float64{"Еда": 15000, "Транспорт": 4000, savingsShareName: 8800},
			wantSrc:   map[string]string{"Еда": budget.ShareSourceOverride, "Транспорт": budget.ShareSourceAuto},
		},
		{
			name:      "override больше free: режутся сами override, авто в ноль",
			free:      10000,
			fc:        []budget.CategoryForecast{fcRUB("Еда", 12000), fcRUB("Транспорт", 4000), fcRUB("Развлечения", 3000)},
			overrides: map[string]float64{"еда": 9000, "транспорт": 6000},
			history:   map[string]int{"еда": 3, "транспорт": 3, "развлечения": 3},
			// override 15000 > 10000 → k = 2/3; авто-доля «Развлечения» обнуляется
			want:     map[string]float64{"Еда": 6000, "Транспорт": 4000, "Развлечения": 0, savingsShareName: 0},
			wantWarn: "ручные лимиты",
		},
		{
			name:    "мало истории: лимит не выдумываем, категория в «прочее»",
			free:    10000,
			fc:      []budget.CategoryForecast{fcRUB("Еда", 6000), fcRUB("Одежда", 2000)},
			history: map[string]int{"еда": 3, "одежда": 1},
			want: map[string]float64{
				"Еда": 6000, budget.FallbackShareName: 0, savingsShareName: 4000,
			},
			wantWarn: "Мало истории: Одежда",
		},
		{
			name:     "нулевой free: лимитов нет",
			free:     0,
			fc:       []budget.CategoryForecast{fcRUB("Еда", 6000)},
			history:  map[string]int{"еда": 3},
			want:     map[string]float64{"Еда": 0, budget.FallbackShareName: 0, savingsShareName: 0},
			wantWarn: "свободных денег нет",
		},
		{
			name:     "отрицательный free: лимитов нет, в минус не уходим",
			free:     -5000,
			fc:       []budget.CategoryForecast{fcRUB("Еда", 6000)},
			history:  map[string]int{"еда": 3},
			want:     map[string]float64{"Еда": 0, savingsShareName: 0},
			wantWarn: "свободных денег нет",
		},
		{
			name:    "только фиксированные категории: всё в накопления",
			free:    10000,
			fc:      []budget.CategoryForecast{fcRUB("Жильё", 50000), fcRUB("Переводы", 90000), fcRUB("Подписки", 2000)},
			history: map[string]int{"жильё": 6, "переводы": 6, "подписки": 6},
			want:    map[string]float64{budget.FallbackShareName: 0, savingsShareName: 10000},
		},
		{
			name:    "пустой прогноз: одна fallback-доля и накопления",
			free:    5000,
			fc:      nil,
			history: nil,
			want:    map[string]float64{budget.FallbackShareName: 0, savingsShareName: 5000},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shares, warnings := allocateShares(tc.free, tc.fc, oneToOne, 30, tc.overrides, tc.history)

			checkConvergence(t, shares, tc.free)

			got := allocatedByName(shares)
			for name, want := range tc.want {
				val, ok := got[name]
				if !ok {
					t.Fatalf("доля %q отсутствует; получено %v", name, got)
				}
				if !approx(val, want) {
					t.Errorf("доля %q: want %.2f, got %.2f", name, want, val)
				}
			}
			if tc.wantWarn != "" && !hasWarning(warnings, tc.wantWarn) {
				t.Errorf("ожидался warning со словами %q, получено %v", tc.wantWarn, warnings)
			}
			if tc.wantWarn == "" && len(warnings) > 0 {
				t.Errorf("warnings не ожидались, получено %v", warnings)
			}
			for name, wantSrc := range tc.wantSrc {
				for _, sh := range shares {
					if sh.Name == name && sh.Source != wantSrc {
						t.Errorf("доля %q: Source want %q, got %q", name, wantSrc, sh.Source)
					}
				}
			}
		})
	}
}

// Fallback-доля обязана существовать всегда: без неё budget.ResolveShare вернёт
// nil и трате в неизвестной категории будет некуда падать (ADR-008).
func TestAllocateShares_FallbackAlwaysRoutesUnknownSpend(t *testing.T) {
	for _, free := range []float64{27800, 0, -1000} {
		shares, _ := allocateShares(free, []budget.CategoryForecast{fcRUB("Еда", 12000)},
			oneToOne, 30, nil, map[string]int{"еда": 3})

		if budget.FallbackShare(shares) == nil {
			t.Fatalf("free=%.0f: доля «%s» не создана", free, budget.FallbackShareName)
		}
		if sh := budget.ResolveShare(shares, nil, "Неизвестная категория"); sh == nil {
			t.Errorf("free=%.0f: трата в неизвестной категории не маршрутизируется", free)
		}
		if sh := budget.ResolveShare(shares, nil, "Еда"); sh == nil || sh.Name != "Еда" {
			t.Errorf("free=%.0f: трата «Еда» ушла не в свою долю: %+v", free, sh)
		}
	}
}

// Виды и позиции: траты — spend, «накопления» — save и всегда последние.
func TestAllocateShares_KindsAndPositions(t *testing.T) {
	shares, _ := allocateShares(27800,
		[]budget.CategoryForecast{fcRUB("Еда", 12000), fcRUB("Транспорт", 4000)},
		oneToOne, 30, nil, map[string]int{"еда": 3, "транспорт": 3})

	if len(shares) != 4 { // Еда, Транспорт, прочее, накопления
		t.Fatalf("ожидалось 4 доли, got %d: %+v", len(shares), shares)
	}
	for i, sh := range shares {
		if sh.Position != i {
			t.Errorf("доля %q: Position want %d, got %d", sh.Name, i, sh.Position)
		}
	}
	if shares[0].Name != "Еда" || shares[1].Name != "Транспорт" {
		t.Errorf("ожидалась сортировка по убыванию лимита, got %q, %q", shares[0].Name, shares[1].Name)
	}
	last := shares[len(shares)-1]
	if last.Name != savingsShareName || last.Kind != budget.ShareKindSave {
		t.Errorf("последняя доля должна быть «%s» с Kind=%s, got %q/%s",
			savingsShareName, budget.ShareKindSave, last.Name, last.Kind)
	}
	for _, sh := range shares[:len(shares)-1] {
		if sh.Kind != budget.ShareKindSpend {
			t.Errorf("доля %q: Kind want %s, got %s", sh.Name, budget.ShareKindSpend, sh.Kind)
		}
	}
	// Категории хранятся нормализованными (двойной ключ ADR-008).
	if len(shares[0].Categories) != 1 || shares[0].Categories[0].CategoryName != "еда" {
		t.Errorf("категории доли «Еда»: want [еда], got %+v", shares[0].Categories)
	}
}

// Копейки при пропорциональном усечении: суммы долей округляются до копейки,
// а накопленный остаток округления оседает в «накоплениях» — сумма обязана
// сойтись в free точно, а не «примерно».
func TestAllocateShares_KopecksOnTruncation(t *testing.T) {
	const free = 10000.55
	shares, warnings := allocateShares(free,
		[]budget.CategoryForecast{fcRUB("Еда", 12345.67), fcRUB("Транспорт", 7654.33)},
		oneToOne, 30, nil, map[string]int{"еда": 3, "транспорт": 3})

	if !hasWarning(warnings, "не помещаются") {
		t.Errorf("ожидался warning об усечении, got %v", warnings)
	}
	got := sumAllocated(shares)
	if math.Abs(got-free) > 0.005 {
		t.Errorf("Σ Allocated want %.2f, got %.4f", free, got)
	}
	for _, sh := range shares {
		if sh.Allocated < 0 {
			t.Errorf("доля %q ушла в минус: %.4f", sh.Name, sh.Allocated)
		}
		if sh.Name == savingsShareName {
			continue // остаток намеренно точный, не округлённый
		}
		if math.Abs(sh.Allocated*100-math.Round(sh.Allocated*100)) > 1e-6 {
			t.Errorf("доля %q не округлена до копейки: %.6f", sh.Name, sh.Allocated)
		}
	}
}

// Сходимость при усечении на сетке «некруглых» сумм: проверяет, что остаток
// округления не теряется и не уводит доли в минус ни на одной из комбинаций,
// а не только на одной удачно подобранной. Без компенсации копеек часть этих
// прогонов даёт Σ Allocated > free.
func TestAllocateShares_KopecksAcrossRange(t *testing.T) {
	fc := []budget.CategoryForecast{
		fcRUB("Еда", 12345.67), fcRUB("Транспорт", 7654.33),
		fcRUB("Развлечения", 3333.33), fcRUB("Кафе", 1111.11),
	}
	history := map[string]int{"еда": 3, "транспорт": 3, "развлечения": 3, "кафе": 3}

	for i := 0; i < 300; i++ {
		free := 9000 + float64(i)*0.37 // некруглые суммы, усечение почти всегда срабатывает
		shares, _ := allocateShares(free, fc, oneToOne, 30, nil, history)

		if got := sumAllocated(shares); math.Abs(got-free) > 0.005 {
			t.Fatalf("free=%.2f: Σ Allocated %.4f, расхождение %.4f", free, got, got-free)
		}
		for _, sh := range shares {
			if sh.Allocated < 0 {
				t.Fatalf("free=%.2f: доля %q ушла в минус: %.4f", free, sh.Name, sh.Allocated)
			}
		}
	}
}

// TestAcceptance_Allocate_27800 — эталон ADR-007/ADR-008 в раскладке: приход
// 127000₽ минус обязательства 99200 = свободно 27800₽ (см.
// TestAcceptance_127k_to_27800), которые раскладываются по конвертам ровно.
// Единицы: THB==₽ (курс 1:1) для прямого сравнения.
func TestAcceptance_Allocate_27800(t *testing.T) {
	snap := &budget.AdvisorSnapshot{UpcomingRecurring: 28000}
	const planned = 36400 + 15600 + 6720 + 5200 + 3380 + 3900 // = 71200
	res := computeSafeToSpend(127000, snap, planned, 0)
	if !approx(res.FreeAfterObligations, 27800) {
		t.Fatalf("предпосылка эталона сломана: свободно want 27800, got %.2f", res.FreeAfterObligations)
	}

	fc := []budget.CategoryForecast{
		fcRUB("Еда", 12000), fcRUB("Транспорт", 4000), fcRUB("Развлечения", 3000),
		fcRUB("Кафе", 400),    // < minShareMonthlyTHB=500 → в «прочее»
		fcRUB("Одежда", 2000), // история 1 месяц → лимит не назначаем
		fcRUB("Жильё", 50000), fcRUB("Переводы", 90000),
	}
	history := map[string]int{"еда": 3, "транспорт": 3, "развлечения": 3, "кафе": 3, "одежда": 1}

	shares, warnings := allocateShares(res.FreeAfterObligations, fc, oneToOne, 30, nil, history)

	checkConvergence(t, shares, res.FreeAfterObligations)
	want := map[string]float64{
		"Еда": 12000, "Транспорт": 4000, "Развлечения": 3000,
		budget.FallbackShareName: 400, savingsShareName: 8400,
	}
	got := allocatedByName(shares)
	if len(got) != len(want) {
		t.Fatalf("состав раскладки: want %v, got %v", want, got)
	}
	for name, w := range want {
		if !approx(got[name], w) {
			t.Errorf("доля %q: want %.2f, got %.2f", name, w, got[name])
		}
	}
	if !hasWarning(warnings, "Мало истории: Одежда") {
		t.Errorf("ожидался warning про Одежду, got %v", warnings)
	}
}

// Override на «накопления» не применяется: они не лимит, а непокрытый остаток.
func TestAllocateShares_OverrideOnSavingsIgnored(t *testing.T) {
	shares, warnings := allocateShares(10000,
		[]budget.CategoryForecast{fcRUB("Еда", 6000)}, oneToOne, 30,
		map[string]float64{savingsShareName: 1000}, map[string]int{"еда": 3})

	checkConvergence(t, shares, 10000)
	if got := allocatedByName(shares)[savingsShareName]; !approx(got, 4000) {
		t.Errorf("накопления: want 4000 (остаток), got %.2f", got)
	}
	if !hasWarning(warnings, "считаются как остаток") {
		t.Errorf("ожидался warning про накопления, got %v", warnings)
	}
}

// Override на имя, которого в авто-раскладке нет, заводит новую долю: оператор
// мог назвать категорию, по которой прогноза ещё не было.
func TestAllocateShares_OverrideCreatesMissingShare(t *testing.T) {
	shares, _ := allocateShares(10000,
		[]budget.CategoryForecast{fcRUB("Еда", 6000)}, oneToOne, 30,
		map[string]float64{"спорт": 1500}, map[string]int{"еда": 3})

	checkConvergence(t, shares, 10000)
	var found *budget.EnvelopeShare
	for i := range shares {
		if shares[i].Name == "спорт" {
			found = &shares[i]
		}
	}
	if found == nil {
		t.Fatalf("доля «спорт» не создана: %+v", allocatedByName(shares))
	}
	if !approx(found.Allocated, 1500) || found.Source != budget.ShareSourceOverride {
		t.Errorf("доля «спорт»: want 1500/override, got %.2f/%s", found.Allocated, found.Source)
	}
	if sh := budget.ResolveShare(shares, nil, "Спорт"); sh == nil || sh.Name != "спорт" {
		t.Errorf("трата «Спорт» не маршрутизируется в свою долю: %+v", sh)
	}
}

// Раскладка детерминирована: одинаковый вход — одинаковый выход, включая
// порядок долей (обход карты override'ов отсортирован).
func TestAllocateShares_Deterministic(t *testing.T) {
	fc := []budget.CategoryForecast{fcRUB("Еда", 6000), fcRUB("Транспорт", 3000)}
	hist := map[string]int{"еда": 3, "транспорт": 3}
	ovr := map[string]float64{"спорт": 1200, "хобби": 900, "книги": 700}

	first, _ := allocateShares(20000, fc, oneToOne, 30, ovr, hist)
	for i := 0; i < 10; i++ {
		next, _ := allocateShares(20000, fc, oneToOne, 30, ovr, hist)
		if len(next) != len(first) {
			t.Fatalf("разное число долей: %d vs %d", len(next), len(first))
		}
		for j := range first {
			if next[j].Name != first[j].Name || !approx(next[j].Allocated, first[j].Allocated) {
				t.Fatalf("прогон %d, позиция %d: %q/%.2f vs %q/%.2f",
					i, j, next[j].Name, next[j].Allocated, first[j].Name, first[j].Allocated)
			}
		}
	}
}

// TestAllocateShares_DuplicateNamesMerge — регрессия на молча теряющийся лимит.
//
// Два источника одинакового имени доли: (1) категория, БУКВАЛЬНО названная
// «Прочее» — совпадает с именем fallback-доли; (2) регистровые дубли категорий
// («Еда» и «еда» — по ADR-008 §6 это разные строки budget_category с разными
// id, и прогноз, сгруппированный по имени, даёт их обе).
//
// До фикса каждый из них создавал ВТОРУЮ долю с тем же именем: на сборке одна
// затирала другую, но её сумма оставалась в Σ allocated — «накопления» получали
// заниженный остаток, и Σ allocated + свободно ≠ free. Ловится инвариантом, а
// не сравнением текста: расхождение вылезло на живом прогоне (127000 ₽, реплика
// с категориями «Прочее» и «clothes»), а не в юнит-фикстуре.
func TestAllocateShares_DuplicateNamesMerge(t *testing.T) {
	const free = 100000.0
	fc := []budget.CategoryForecast{
		fcRUB("Прочее", 20000),
		fcRUB("Еда", 30000),
		fcRUB("еда", 10000),
	}
	history := map[string]int{"прочее": 3, "еда": 3}

	shares, _ := allocateShares(free, fc, oneToOne, 30, nil, history)

	checkConvergence(t, shares, free)

	seen := map[string]int{}
	for _, sh := range shares {
		seen[normalizeShareName(sh.Name)]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("доля %q встречается %d раза — UNIQUE(envelope_id,name) отобьёт вторую", name, n)
		}
	}

	byName := allocatedByName(shares)
	// Регистровые дубли складываются, а не теряются.
	if got := byName["Еда"]; math.Abs(got-40000) > 0.01 {
		t.Errorf("«Еда» = %.2f, ожидали 40000 (30000 + дубль 10000)", got)
	}
	// Категория «Прочее» уходит в долю-приёмник вместе со своим лимитом.
	if got := byName[budget.FallbackShareName]; math.Abs(got-20000) > 0.01 {
		t.Errorf("«%s» = %.2f, ожидали 20000 (лимит категории «Прочее»)", budget.FallbackShareName, got)
	}
}
