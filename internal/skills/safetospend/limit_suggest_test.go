package safetospend

import (
	"testing"

	"simpleAI/internal/budget"
)

var sugRates = map[string]float64{"THB": 2.5, "RUB": 1}

func TestWindowAmountTHBMinor(t *testing.T) {
	fc := []budget.CategoryForecast{
		{CategoryName: "Еда", Currency: "THB", ForecastAmount: 9000},
		{CategoryName: "Кафе", Currency: "THB", ForecastAmount: 1000},
		{CategoryName: "Такси", Currency: "RUB", ForecastAmount: 2500}, // = 1000 THB
	}
	cases := []struct {
		cat  string
		days int
		want int64
		ok   bool
	}{
		{"Еда", 14, 420_000, true}, // 9000·14/30 = 4200 ฿ = 420 000 сатангов
		{"  еДа ", 30, 900_000, true},
		{"Еда", 7, 210_000, true},
		{"Такси", 30, 100_000, true}, // рубли переведены в баты
		{"Нет такой", 14, 0, false},
		{"Еда", 0, 0, false},
	}
	for _, c := range cases {
		got, ok := WindowAmountTHBMinor(fc, c.cat, c.days, sugRates)
		if got != c.want || ok != c.ok {
			t.Errorf("%q/%d: %d,%v; ждали %d,%v", c.cat, c.days, got, ok, c.want, c.ok)
		}
	}
	if _, ok := WindowAmountTHBMinor(fc, "Такси", 14, map[string]float64{"THB": 2.5}); ok {
		t.Error("нет курса валюты — подсказки быть не должно")
	}
}
