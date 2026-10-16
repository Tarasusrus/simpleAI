package plan

import (
	"errors"
	"testing"
	"time"
)

// Эталон арифметики — стандартный time: домен считает дни сам (без time внутри),
// а тест сверяет его результат с проверенной реализацией на тысячах дат.
func stdAddDays(y, m, d, n int) Date {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
	return Date{t.Year(), int(t.Month()), t.Day()}
}

func TestNewDate_Validates(t *testing.T) {
	cases := []struct {
		name    string
		y, m, d int
		ok      bool
	}{
		{"обычная", 2026, 10, 14, true},
		{"29 февраля високосного", 2024, 2, 29, true},
		{"29 февраля невисокосного", 2026, 2, 29, false},
		{"31 апреля", 2026, 4, 31, false},
		{"месяц 0", 2026, 0, 1, false},
		{"месяц 13", 2026, 13, 1, false},
		{"день 0", 2026, 1, 0, false},
		{"2100 не високосный", 2100, 2, 29, false},
		{"2000 високосный", 2000, 2, 29, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := NewDate(c.y, c.m, c.d)
			if c.ok {
				if err != nil || d != (Date{c.y, c.m, c.d}) {
					t.Fatalf("NewDate(%d,%d,%d) = %v, %v; ждали успех", c.y, c.m, c.d, d, err)
				}
				return
			}
			if !errors.Is(err, ErrBadDate) {
				t.Fatalf("NewDate(%d,%d,%d) err = %v; ждали ErrBadDate", c.y, c.m, c.d, err)
			}
		})
	}
}

func TestAddDays_MatchesStdlibOracle(t *testing.T) {
	starts := []Date{{2024, 2, 28}, {2026, 10, 14}, {2026, 12, 31}, {2026, 1, 1}, {2100, 2, 28}, {1999, 12, 31}}
	for _, s := range starts {
		for n := -800; n <= 800; n += 7 {
			got := s.AddDays(n)
			want := stdAddDays(s.Year, s.Month, s.Day, n)
			if got != want {
				t.Fatalf("%v.AddDays(%d) = %v, ждали %v", s, n, got, want)
			}
		}
	}
}

func TestAddMonths_ClampsToMonthEnd(t *testing.T) {
	cases := []struct {
		from Date
		n    int
		want Date
	}{
		{Date{2026, 1, 31}, 1, Date{2026, 2, 28}},
		{Date{2024, 1, 31}, 1, Date{2024, 2, 29}},
		{Date{2026, 1, 31}, 2, Date{2026, 3, 31}},
		{Date{2026, 8, 31}, 6, Date{2027, 2, 28}},
		{Date{2026, 10, 31}, 1, Date{2026, 11, 30}},
		{Date{2026, 3, 15}, -3, Date{2025, 12, 15}},
		{Date{2024, 2, 29}, 12, Date{2025, 2, 28}},
		{Date{2024, 2, 29}, 48, Date{2028, 2, 29}},
		{Date{2026, 12, 31}, 1, Date{2027, 1, 31}},
		{Date{2026, 1, 31}, -2, Date{2025, 11, 30}},
	}
	for _, c := range cases {
		if got := c.from.AddMonths(c.n); got != c.want {
			t.Errorf("%v.AddMonths(%d) = %v, ждали %v", c.from, c.n, got, c.want)
		}
	}
}

func TestDaysBetween_SignAndOracle(t *testing.T) {
	a := Date{2026, 10, 14}
	if got := DaysBetween(a, Date{2026, 10, 18}); got != 4 {
		t.Fatalf("позже: %d, ждали 4", got)
	}
	if got := DaysBetween(Date{2026, 10, 18}, a); got != -4 {
		t.Fatalf("раньше: %d, ждали -4", got)
	}
	if got := DaysBetween(a, a); got != 0 {
		t.Fatalf("равные: %d", got)
	}
	// Окно 05.10–18.10: сегодня 14.10 → осталось end−today+1 = 5 дней.
	if left := DaysBetween(Date{2026, 10, 14}, Date{2026, 10, 18}) + 1; left != 5 {
		t.Fatalf("daysLeft = %d, ждали 5", left)
	}
	for n := -2000; n <= 2000; n += 13 {
		if got := DaysBetween(a, a.AddDays(n)); got != n {
			t.Fatalf("DaysBetween(a, a+%d) = %d", n, got)
		}
	}
}

func TestCompare(t *testing.T) {
	a, b := Date{2026, 10, 14}, Date{2026, 10, 15}
	same := Date{2026, 10, 14}
	if a.Compare(b) != -1 || b.Compare(a) != 1 || a.Compare(same) != 0 {
		t.Fatal("Compare возвращает неверный знак")
	}
	if (Date{2025, 12, 31}).Compare(Date{2026, 1, 1}) != -1 {
		t.Fatal("год важнее месяца")
	}
	if (Date{2026, 1, 31}).Compare(Date{2026, 2, 1}) != -1 {
		t.Fatal("месяц важнее дня")
	}
}

func TestValid(t *testing.T) {
	if !(Date{2026, 10, 14}).Valid() {
		t.Fatal("корректная дата признана невалидной")
	}
	for _, d := range []Date{{}, {2026, 2, 30}, {2026, 13, 1}, {2026, 1, 0}} {
		if d.Valid() {
			t.Fatalf("%v признана валидной", d)
		}
	}
}

// AC-1.6: «сегодня» берётся в зоне плана. 18:30Z = 01:30 следующего дня по Бангкоку.
func TestDateOf_UsesPlanZone(t *testing.T) {
	bkk, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Skipf("нет tzdata: %v", err)
	}
	tm := time.Date(2026, 10, 14, 18, 30, 0, 0, time.UTC)
	if got, want := DateOf(tm, bkk), (Date{2026, 10, 15}); got != want {
		t.Fatalf("DateOf = %v, ждали %v", got, want)
	}
	if got, want := DateOf(tm, time.UTC), (Date{2026, 10, 14}); got != want {
		t.Fatalf("DateOf(UTC) = %v, ждали %v", got, want)
	}
}

func TestAddDays_ZeroIsIdentity(t *testing.T) {
	d := Date{2026, 10, 14}
	if d.AddDays(0) != d {
		t.Fatal("AddDays(0) изменил дату")
	}
}
