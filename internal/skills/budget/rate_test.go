package budgetskill

import "testing"

// Граница правдоподобия курса бата: опечатки в обе стороны не должны доходить
// до базы, иначе все конверты искажаются в десять раз правдоподобными цифрами.
func TestCheckRate(t *testing.T) {
	cases := []struct {
		rate float64
		want rateVerdict
	}{
		{0.27, rateTooLow},
		{0.999, rateTooLow},
		{1.0, rateOK},
		{2.7, rateOK},
		{10.0, rateOK},
		{10.01, rateTooHigh},
		{27, rateTooHigh},
	}
	for _, c := range cases {
		if got := checkRate(c.rate); got != c.want {
			t.Errorf("checkRate(%v) = %v, ожидалось %v", c.rate, got, c.want)
		}
	}
}
