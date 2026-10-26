# Domain types и сигнатуры (internal/plan, v8.4) — КОНТРАКТ ФАЗЫ 1

Решения: constraints C1–C17, requirements R1–R5. Изменения сигнатур — только вместе с этим файлом. Пакет `plan`, модуль `simpleAI`. Разрешённые импорты (не-тестовый код): `errors, fmt (только для ошибок), sort, slices, strings, math, math/bits, time (только DateOf)`; `time.Now` запрещён. Единицы и значения перечислений в домене и БД — латиницей (`day|week|month|year`, `income|payment`, `planned|done`); экран локализует.

## Date (без времени)
```go
type Date struct{ Year, Month, Day int }
func NewDate(y, m, d int) (Date, error)             // ErrBadDate
func DateOf(t time.Time, loc *time.Location) Date    // единственное место с time.Time
func (d Date) AddDays(n int) Date
func (d Date) AddMonths(n int) Date                  // клампинг дня на последний день месяца
func (d Date) Compare(o Date) int                    // −1,0,1
func DaysBetween(a, b Date) int                      // b − a в днях (знак: b позже a → >0)
func (d Date) Valid() bool
```
Арифметика дней — вручную (days-from-civil), без `time`. `daysLeft = DaysBetween(today, end)+1`, `overdueDays = DaysBetween(date, today)`.

## Currency, Money, Rate
```go
type Currency string // CurTHB="THB", CurRUB="RUB"; иная → ErrUnsupportedCurrency
type Money struct{ Minor int64; Cur Currency }   // сатанги / копейки
type Rate struct{ RubPerThbMicro int64 }         // ₽ за 1 ฿, микро; 2,5 = 2_500_000; ≤0 → ErrBadRate
func Convert(rate *Rate, m Money, to Currency) (whole int64, err error) // целые единицы показа; rate может быть nil при той же валюте
```
`Convert`: числитель и знаменатель целые, ОДНО округление half-away-from-zero (и для отрицательных): ฿→₽ `num=minor·rate, den=1e8`; ₽→฿ `num=minor·1e6, den=rate·100`; та же валюта `round(minor/100)`. Переполнение int64 (через `math/bits`) → `ErrOverflow`. `ErrNoRate` — только если нужен перевод между разными валютами; совпадение валют курс не использует; `Rate != nil` и ≤0 → `ErrBadRate` всегда. В `View` ВСЕ суммы — целые единицы показа (`int64`); каждое событие переводится и округляется один раз, итоги — сумма округлённых частей. Конверсия `float64`/`NUMERIC` → `Minor`/микро — на границе `planrepo`.

## Event
```go
type Kind string   // KindIncome, KindPayment
type State string  // StatePlanned, StateDone
type Source string // SourceTransaction, SourceRecurring, SourcePlanned
type Event struct {
  Key         string  // уникален: "transaction:<id>", "planned:<id>", "recurring:<ruleID>@YYYY-MM-DD"
  Kind        Kind; State State; Name string
  Amount      *Money  // nil ТОЛЬКО у планового разового платежа
  Date        *Date   // nil допустим только у планового платежа; у поступления и у done — нельзя
  WindowStart *Date   // учитывается ТОЛЬКО при Date==nil
  Category    string  // пусто = «Без категории»
  Source      Source
  JoinsWindow bool    // только у Kind=income: false (ноль) — зарплата/аванс, открывает окно; true — обычный приход, попадает в окно по дате (ADR-019). В базе колонка opens_window (= !JoinsWindow); поле инвертировано, чтобы нулевое значение оставалось прежним поведением
  RuleID      string  // пусто = разовое
  ScheduledFor *Date  // срок правила, к которому относится факт или плановое
}
func (e Event) IsRecurring() bool { return e.RuleID != "" } // поля Recurring нет
func (e Event) Validate() error
```
`Validate`: пустой `Key` / done без `Date` → `ErrInvalidEvent`; `Amount==nil` у поступления, у done, у повторяющегося → `ErrAmountRequired`; `Amount ≤ 0` → `ErrAmountNonPositive`; валюта не THB/RUB → `ErrUnsupportedCurrency`; поступление без `Date` → `ErrIncomeNoDate`; невалидная дата → `ErrBadDate`.
Контракт Reader: на каждый срок правила — либо плановое, либо факт; оба с одним `(RuleID, ScheduledFor)` → `Compute` возвращает `ErrDuplicateTerm`.

## Rule и повторы
```go
type Unit string // UnitDay, UnitWeek, UnitMonth, UnitYear
type Rule struct{ Anchor Date; N int; Unit Unit; DayOfMonth *int; Until *Date; Amount Money }
type RuleWithExceptions struct{ ID, Name, Category string; Kind Kind; Rule Rule; Exceptions []Date }
func Next(r Rule, after Date) (next Date, ok bool, err error)        // СТРОГО после after; ok=false если Until прошёл; без потолка (арифметика)
func Occurrences(r Rule, from, to Date) ([]Date, error)               // обе границы включительно; from>to → пусто
func Expand(rules []RuleWithExceptions, from, to Date) ([]Event, error)
```
Срок n: day/week — `Anchor + n·N·(1|7)` дней; month/year — месяцы `n·N·(1|12)` от якоря с днём `DayOfMonth` (если задан и Unit month/year, иначе день якоря), клампинг на последний день месяца. Сроки раньше `Anchor` не выдаются (при `DayOfMonth` n=0 может оказаться раньше якоря — пропускается). Ошибки: `N<1`, `Amount ≤ 0` или неподдерживаемая валюта, неизвестный `Unit`, `DayOfMonth` вне 1..31 или при day/week → `ErrBadRule`. `Until < Anchor` → пусто без ошибки; в день `Until` срок включается.
`Expand`: для каждого срока вне `Exceptions` выдаёт `Event{Source:recurring, Key:"recurring:"+ID+"@"+YYYY-MM-DD, State:planned, RuleID:ID, Date:=ScheduledFor:=&срок, Amount:&Rule.Amount, Kind, Name, Category}`; результат отсортирован по (Date, Key). Потолок 1 000 на ВЕСЬ результат после вычитания исключений: первые 1 000 + `ErrTooMany`. Читатель берёт `from` для расходов не раньше начала первого окна, для поступлений — с самого начала.

## Окна
```go
type Window struct{ Start Date; Incomes []Event } ; func (w Window) End() Date /* Start+13 */ ; func (w Window) AnchorPlanned() bool // Incomes[0] — ожидаемое
func BuildWindows(incomes []Event) []Window // пропускает Kind≠income и Date==nil; пустой вход → []Window{}
type StateKind string // StateCurrent, StateUpcoming, StateBetween, StateEnded, StateNone
type WindowState struct{ Kind StateKind; Index int; DaysLeft int }
func Locate(ws []Window, today Date) WindowState
func ResolveWindow(ws []Window, windowStart Date) (idx int, ok bool) // первое окно с End ≥ windowStart
```
`BuildWindows`: сортировка по (Date, факт раньше плана, Key); окно открывает самое раннее непокрытое; результат не зависит от порядка входа. Reader отдаёт ВСЕ поступления с начала.
`Locate`: `current` → Index=i, DaysLeft=`end−today+1`; `upcoming` → Index=0, 14; `between` → Index=k (последнее закончившееся окно), следующее = k+1, 0; `ended` → последний, 0; `none` → Index=−1.

## Compute
```go
type ComputeInput struct{ Windows []Window; Events []Event /*ТОЛЬКО платежи; Kind income → ErrInvalidEvent*/; Rate *Rate; Cur Currency; Today Date; ReservePct int /*0..100, иначе ErrBadReservePct*/ }
func Compute(in ComputeInput) (View, error) // ошибка → пустой View и err; ошибки Validate оборачиваются: fmt.Errorf("%w: %w", ErrInvalidEvent, err)
```
Поступления окна — ТОЛЬКО из `Windows[Index].Incomes` (`received` = done, `expected` = planned; `anchorPlanned = Incomes[0].State==planned`).
Описываемое окно: `Index` из `Locate`; в `between`/`ended` — это закончившееся окно справочно (`daysLeft=0`, `perDay=nil`, расчёт как обычно); `upcoming` — первое окно (14). `none` → пустой `View` без окна.
**Платёж окна** (`Kind=payment`, `State=planned`, `Amount≠nil`), приоритет, событие считается ОДИН раз: (1) при `State=current` просроченный разовый (`!IsRecurring && Date<today`) — платёж текущего окна независимо от даты, `OverdueDays>0`; (2) `Date ∈ [start,end]`; (3) `Date==nil` и `ResolveWindow(WindowStart)` = этот индекс. Вне `current` просроченные разовые в расчёт не входят, но видны в ленте с `OverdueDays` (решение по умолчанию). Повторяющееся плановое с датой в прошлом вне окон в `View` не попадает.
**`spent`** = `done` платежи с `Date ∈ окно`. `reserve = round_half_away(Σ income(received+expected, уже округлённые) · ReservePct / 100)`. `canSpend = income − payments − spent − reserve` (знаковое). `perDay = floor(canSpend/daysLeft)` при `canSpend ≥ 0` и `daysLeft > 0`, иначе nil.
`PrevRemainder` в фазе 1 НЕТ (YAGNI): остаток прошлого окна не переносится и не показывается; «Окно закончилось, следующее начнётся DD.MM» без цифр.

## View (Go-скелет; `meta` заполняет Service, не Compute)
```go
type View struct {
  State WindowStateKind; Window *WindowInfo          // nil при none
  Income struct{ Received, Expected int64 }
  CanSpend, Payments, Spent, Reserve int64
  PerDay *int64
  Unpriced int                                       // платежи окна без суммы (число)
  Undated struct{ Count, PricedCount int; Sum int64; Items []Row } // без даты и без выбора окна / «окно прошло» / «окна нет»
  Timeline []Row                                     // полный; режет фронт
  Next *NextBlock                                    // nil, если нет следующего окна; {Count включает без суммы; Sum только с суммой; Items}
  Later []MonthBucket                                // месяцы m0..m5 от месяца today; {Year,Month,Count,Sum,Items}; бакет без-датного — месяц Start резолвленного окна; границы отбрасываются
  Categories []CategoryRow
}
type WindowInfo struct{ Start, End Date; DaysLeft int; AnchorPlanned bool }
type Row struct{ Key string; Date *Date; Name string; Kind Kind; Amount *int64; OverdueDays int; Expected, NotArrived, NoAmount, WindowMissing, WindowPassed bool }
type CategoryRow struct{ Name string; Last, Now int64; Recent []Row }
```
Порядок ленты: просроченные платежи по дате ↑ первыми; затем по дате; при равной дате: поступление-факт, поступление-план, платёж; затем `strings.ToLower(Name)`, затем `Key`. «В это окно» без даты — после датированных (по названию и `Key`). Просроченное поступление (`NotArrived`) стоит по дате. `OverdueDays` только у плановых разовых платежей.
Категории: метод `calc.categories()` (внутри `Compute`; строка трат без категории — константа `UncategorizedName = "Без категории"`): «Прошлое» = `spent` окна `i−1` по категории, «Сейчас» = `spent` текущего; в `between`/`ended` «Сейчас»=0, «Прошлое»=последнее окно; «Без категории» последней и только при ненулевой сумме; порядок «Сейчас»↓, «Прошлое»↓, название; `Recent` — 5 последних трат категории по текущему и прошлому окнам вместе, по дате↓ затем `Key`.

## Ошибки
`ErrBadDate, ErrUnsupportedCurrency, ErrBadRate, ErrNoRate, ErrOverflow, ErrBadRule, ErrTooMany, ErrAmountRequired, ErrAmountNonPositive, ErrIncomeNoDate, ErrInvalidEvent, ErrDuplicateTerm, ErrBadReservePct`.

## Scope
`ChatIDs []int64`. Транзакции общие, области не подчиняются (граница `planrepo`, не домена).
