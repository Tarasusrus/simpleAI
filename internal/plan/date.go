// Package plan — чистый домен «окна плана»: даты, деньги, повторы, окна и расчёт.
//
// Без ввода-вывода: ни базы, ни HTTP, ни Telegram, ни часов. «Сегодня» приходит
// параметром, чтобы экран, бот и воркер считали от одной даты, а тесты не зависели
// от времени запуска. Контракт — specs/income-plan-sheet/domain-types.md.
package plan

import (
	"errors"
	"time"
)

// ErrBadDate — несуществующая календарная дата (30 февраля, месяц 13).
var ErrBadDate = errors.New("plan: некорректная дата")

// Date — календарная дата без времени и зоны. Время и пояс в домене не нужны:
// транзакции хранят DATE, а граница суток определяется зоной плана ровно один
// раз — в DateOf.
type Date struct{ Year, Month, Day int }

// NewDate проверяет и собирает дату.
func NewDate(y, m, d int) (Date, error) {
	dt := Date{y, m, d}
	if !dt.Valid() {
		return Date{}, ErrBadDate
	}
	return dt, nil
}

// DateOf — единственное место, где домен видит time.Time: переводит момент в
// календарный день зоны плана (Asia/Bangkok), чтобы ночью по Бангкоку «сегодня»
// не оставалось вчерашним, как у сервера в UTC.
func DateOf(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()
	return Date{y, int(m), d}
}

// Valid сообщает, что дата существует в григорианском календаре.
func (d Date) Valid() bool {
	return d.Month >= 1 && d.Month <= 12 && d.Day >= 1 && d.Day <= daysInMonth(d.Year, d.Month)
}

// Compare возвращает -1, 0 или 1 — порядок по календарю.
func (d Date) Compare(o Date) int {
	switch {
	case d.Year != o.Year:
		return sign(d.Year - o.Year)
	case d.Month != o.Month:
		return sign(d.Month - o.Month)
	default:
		return sign(d.Day - o.Day)
	}
}

// AddDays сдвигает дату на n календарных суток (n может быть отрицательным).
func (d Date) AddDays(n int) Date {
	return fromDayNumber(dayNumber(d) + n)
}

// AddMonths сдвигает на n месяцев; день, которого нет в целевом месяце, прижимается
// к его последнему дню (31.01 + 1 мес = 28.02). Здесь же причина, почему повторы
// считаются от якоря, а не цепочкой: цепочка от 28.02 уже не вернётся к 31.03.
func (d Date) AddMonths(n int) Date {
	return monthShift(d, n, d.Day)
}

// DaysBetween — число суток от a до b (b позже a → положительное).
func DaysBetween(a, b Date) int {
	return dayNumber(b) - dayNumber(a)
}

// monthShift сдвигает месяц на n и ставит день day (с прижатием к концу месяца).
func monthShift(d Date, n, day int) Date {
	total := d.Year*12 + (d.Month - 1) + n
	y := floorDiv(total, 12)
	m := total - y*12 + 1
	if dim := daysInMonth(y, m); day > dim {
		day = dim
	}
	return Date{y, m, day}
}

func isLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

func daysInMonth(y, m int) int {
	switch m {
	case 2:
		if isLeap(y) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}

func sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	}
	return 0
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// dayNumber — число суток от 1970-01-01 (алгоритм days-from-civil Говарда Хиннанта).
// Свой счёт вместо time.Time: арифметика дат не зависит от зоны и перехода на
// летнее время, поэтому «через 14 дней» всегда ровно 14 календарных дней.
func dayNumber(d Date) int {
	y := d.Year
	m := d.Month
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := m + 9
	if m > 2 {
		mp = m - 3
	}
	doy := (153*mp+2)/5 + d.Day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// fromDayNumber — обратное преобразование к dayNumber.
func fromDayNumber(z int) Date {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	day := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return Date{y, m, day}
}

// String — ISO-вид YYYY-MM-DD: им пользуются ключи событий и сообщения об ошибках.
// Без fmt и strconv: домен не форматирует текст для людей, но ключ должен быть
// стабильным и сортируемым.
func (d Date) String() string {
	return pad(d.Year, 4) + "-" + pad(d.Month, 2) + "-" + pad(d.Day, 2)
}

func pad(n, width int) string {
	if n < 0 {
		return "-" + pad(-n, width)
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	for len(digits) < width {
		digits = append([]byte{'0'}, digits...)
	}
	return string(digits)
}
