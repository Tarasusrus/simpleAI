package plan

import (
	"math"
	"math/bits"
)

// Currency — валюта суммы. Поддерживаются только баты и рубли: остальные валюты
// владельцу в плане не нужны, а молчаливый пересчёт «1:1» исказил бы итог.
type Currency string

const (
	CurTHB Currency = "THB"
	CurRUB Currency = "RUB"
)

// Supported сообщает, что валюту умеет считать домен.
func (c Currency) Supported() bool { return c == CurTHB || c == CurRUB }

// Money — сумма в минимальных единицах валюты (сатанги, копейки). Целые числа,
// чтобы сумма строк экрана всегда сходилась с итогом.
type Money struct {
	Minor int64
	Cur   Currency
}

// Rate — курс «рублей за 1 бат» в микроединицах: 2,5 ₽/฿ = 2_500_000.
type Rate struct{ RubPerThbMicro int64 }

// Convert переводит сумму в целые единицы валюты показа (рубли или баты).
//
// Округление ровно одно, half-away-from-zero (и для отрицательных), на целом
// делении num/den: два деления подряд усекли бы результат. Курс нужен только
// между разными валютами, но курс ≤ 0 — ошибка всегда: это признак битых данных.
func Convert(rate *Rate, m Money, to Currency) (int64, error) {
	if !m.Cur.Supported() || !to.Supported() {
		return 0, ErrUnsupportedCurrency
	}
	if rate != nil && rate.RubPerThbMicro <= 0 {
		return 0, ErrBadRate
	}
	abs := absMinor(m.Minor)
	var rateU uint64
	if rate != nil {
		rateU = uint64(rate.RubPerThbMicro) //nolint:gosec // G115: курс > 0 проверен выше
	}
	var hi, lo, den uint64
	switch {
	case m.Cur == to:
		lo, den = abs, 100
	case rate == nil:
		return 0, ErrNoRate
	case m.Cur == CurTHB: // ฿ → ₽: сатанги · курс / 1e8
		hi, lo = bits.Mul64(abs, rateU)
		den = 100_000_000
	default: // ₽ → ฿: копейки · 1e6 / (курс · 100)
		if rate.RubPerThbMicro > math.MaxInt64/100 {
			return 0, ErrOverflow
		}
		hi, lo = bits.Mul64(abs, 1_000_000)
		den = rateU * 100
	}
	if hi >= den { // частное не поместится в 64 бита
		return 0, ErrOverflow
	}
	q, r := bits.Div64(hi, lo, den)
	if r >= den-r { // остаток ≥ половины делителя → округляем от нуля
		if q == math.MaxUint64 {
			return 0, ErrOverflow
		}
		q++
	}
	if q > math.MaxInt64 {
		return 0, ErrOverflow
	}
	if m.Minor < 0 {
		return -int64(q), nil
	}
	return int64(q), nil
}

// absMinor — модуль без переполнения на math.MinInt64.
func absMinor(v int64) uint64 {
	if v < 0 {
		return uint64(-(v + 1)) + 1 //nolint:gosec // G115: -(v+1) ≥ 0 для v < 0
	}
	return uint64(v) //nolint:gosec // G115: v ≥ 0
}
