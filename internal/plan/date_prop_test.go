package plan

import (
	"testing"

	"pgregory.net/rapid"
)

// Свойства арифметики дат на случайных входах: ловят то, что таблица случаев не
// перебирает (границы веков, отрицательные сдвиги, 29 февраля).
func genDate() *rapid.Generator[Date] {
	return rapid.Custom(func(t *rapid.T) Date {
		y := rapid.IntRange(1900, 2200).Draw(t, "год")
		m := rapid.IntRange(1, 12).Draw(t, "месяц")
		d := rapid.IntRange(1, daysInMonth(y, m)).Draw(t, "день")
		return Date{y, m, d}
	})
}

func TestProp_AddDaysThenDaysBetween(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := genDate().Draw(t, "дата")
		n := rapid.IntRange(-100000, 100000).Draw(t, "сдвиг")
		got := d.AddDays(n)
		if !got.Valid() {
			t.Fatalf("%v.AddDays(%d) = %v невалидна", d, n, got)
		}
		if DaysBetween(d, got) != n {
			t.Fatalf("DaysBetween(%v, %v) = %d, ждали %d", d, got, DaysBetween(d, got), n)
		}
		if got.AddDays(-n) != d {
			t.Fatalf("обратный сдвиг не вернул %v", d)
		}
	})
}

func TestProp_AddMonthsIsValidAndMonotone(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := genDate().Draw(t, "дата")
		n := rapid.IntRange(0, 600).Draw(t, "месяцев")
		got := d.AddMonths(n)
		if !got.Valid() {
			t.Fatalf("%v.AddMonths(%d) = %v невалидна", d, n, got)
		}
		if got.Compare(d) < 0 {
			t.Fatalf("сдвиг вперёд дал более раннюю дату: %v → %v", d, got)
		}
		// День прижимается, но никогда не растёт.
		if got.Day > d.Day {
			t.Fatalf("день вырос: %v → %v", d, got)
		}
	})
}

func TestProp_CompareMatchesDaysBetween(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b := genDate().Draw(t, "a"), genDate().Draw(t, "b")
		if sign(DaysBetween(a, b)) != -a.Compare(b) {
			t.Fatalf("Compare(%v,%v)=%d не согласован с DaysBetween=%d", a, b, a.Compare(b), DaysBetween(a, b))
		}
	})
}
