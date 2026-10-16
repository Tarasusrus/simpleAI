package plan

import (
	"errors"
	"math"
	"testing"
)

func rate(micro int64) *Rate { return &Rate{RubPerThbMicro: micro} }

// Числа — из golden-сценариев спеки (AC-4.1, AC-4.2): курс 2,5 и 2,7.
func TestConvert_GoldenAmounts(t *testing.T) {
	cases := []struct {
		name string
		m    Money
		to   Currency
		r    *Rate
		want int64
	}{
		{"593 ฿ → ₽ при 2,5 (1482,5 → 1483, half-away)", Money{59300, CurTHB}, CurRUB, rate(2_500_000), 1483},
		{"1400 ฿ → ₽ при 2,5", Money{140000, CurTHB}, CurRUB, rate(2_500_000), 3500},
		{"310 ฿ → ₽ при 2,5", Money{31000, CurTHB}, CurRUB, rate(2_500_000), 775},
		{"18000 ฿ → ₽ при 2,5", Money{1_800_000, CurTHB}, CurRUB, rate(2_500_000), 45000},
		{"98000 ₽ → ฿ при 2,5", Money{9_800_000, CurRUB}, CurTHB, rate(2_500_000), 39200},
		{"1700 ₽ → ฿ при 2,5", Money{170_000, CurRUB}, CurTHB, rate(2_500_000), 680},
		{"98000 ₽ → ฿ при 2,7 (36296,3)", Money{9_800_000, CurRUB}, CurTHB, rate(2_700_000), 36296},
		{"1700 ₽ → ฿ при 2,7 (629,6 → 630)", Money{170_000, CurRUB}, CurTHB, rate(2_700_000), 630},
		{"593 ฿ → ₽ при 2,7 (1601,1)", Money{59300, CurTHB}, CurRUB, rate(2_700_000), 1601},
		{"1400 ฿ → ₽ при 2,7", Money{140000, CurTHB}, CurRUB, rate(2_700_000), 3780},
		{"310 ฿ → ₽ при 2,7", Money{31000, CurTHB}, CurRUB, rate(2_700_000), 837},
		{"18000 ฿ → ₽ при 2,7", Money{1_800_000, CurTHB}, CurRUB, rate(2_700_000), 48600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert(c.r, c.m, c.to)
			if err != nil || got != c.want {
				t.Fatalf("Convert = %d, %v; ждали %d", got, err, c.want)
			}
		})
	}
}

func TestConvert_RoundsHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		name string
		m    Money
		to   Currency
		want int64
	}{
		{"та же валюта, .50 вверх", Money{123450, CurRUB}, CurRUB, 1235},
		{"та же валюта, .49 вниз", Money{123449, CurRUB}, CurRUB, 1234},
		{"отрицательное .50 — от нуля", Money{-123450, CurRUB}, CurRUB, -1235},
		{"отрицательное .49", Money{-123449, CurRUB}, CurRUB, -1234},
		{"ровно 0,5 ฿ → 1", Money{125, CurRUB}, CurTHB, 1}, // 1,25 ₽ / 2,5
		{"ровно -0,5 ฿ → -1", Money{-125, CurRUB}, CurTHB, -1},
		{"ноль", Money{0, CurTHB}, CurRUB, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert(rate(2_500_000), c.m, c.to)
			if err != nil || got != c.want {
				t.Fatalf("Convert = %d, %v; ждали %d", got, err, c.want)
			}
		})
	}
}

// Совпадение валют не требует курса (AC-4.4: курс нужен только для перевода).
func TestConvert_SameCurrencyNeedsNoRate(t *testing.T) {
	got, err := Convert(nil, Money{170_000, CurRUB}, CurRUB)
	if err != nil || got != 1700 {
		t.Fatalf("Convert = %d, %v; ждали 1700", got, err)
	}
}

func TestConvert_RateErrors(t *testing.T) {
	if _, err := Convert(nil, Money{100, CurTHB}, CurRUB); !errors.Is(err, ErrNoRate) {
		t.Fatalf("нет курса при переводе ฿→₽: %v, ждали ErrNoRate", err)
	}
	for _, bad := range []int64{0, -1, -2_500_000} {
		// Курс ≤ 0 — ошибка ВСЕГДА, даже если перевод не нужен: это сигнал битых данных.
		if _, err := Convert(rate(bad), Money{100, CurRUB}, CurRUB); !errors.Is(err, ErrBadRate) {
			t.Fatalf("курс %d при той же валюте: %v, ждали ErrBadRate", bad, err)
		}
		if _, err := Convert(rate(bad), Money{100, CurTHB}, CurRUB); !errors.Is(err, ErrBadRate) {
			t.Fatalf("курс %d при переводе: %v, ждали ErrBadRate", bad, err)
		}
	}
}

func TestConvert_UnsupportedCurrency(t *testing.T) {
	if _, err := Convert(rate(2_500_000), Money{100, "USD"}, CurRUB); !errors.Is(err, ErrUnsupportedCurrency) {
		t.Fatalf("USD источник: %v", err)
	}
	if _, err := Convert(rate(2_500_000), Money{100, CurRUB}, "EUR"); !errors.Is(err, ErrUnsupportedCurrency) {
		t.Fatalf("EUR цель: %v", err)
	}
}

func TestConvert_Overflow(t *testing.T) {
	// Результат не помещается в int64.
	if _, err := Convert(rate(math.MaxInt64/2), Money{math.MaxInt64, CurTHB}, CurRUB); !errors.Is(err, ErrOverflow) {
		t.Fatalf("ждали ErrOverflow, получили %v", err)
	}
	// Знаменатель rate*100 не помещается.
	if _, err := Convert(rate(math.MaxInt64), Money{100, CurRUB}, CurTHB); !errors.Is(err, ErrOverflow) {
		t.Fatalf("знаменатель: ждали ErrOverflow, получили %v", err)
	}
	// Граница: крупные, но допустимые суммы не ломаются.
	if got, err := Convert(rate(2_500_000), Money{1_000_000_000_000, CurTHB}, CurRUB); err != nil || got != 25_000_000_000 {
		t.Fatalf("крупная сумма: %d, %v", got, err)
	}
}

func TestCurrencySupported(t *testing.T) {
	if !CurTHB.Supported() || !CurRUB.Supported() || Currency("USD").Supported() || Currency("").Supported() {
		t.Fatal("Supported() отдаёт неверный ответ")
	}
}
