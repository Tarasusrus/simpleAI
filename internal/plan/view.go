package plan

// View — готовая модель окна для экрана и бота. Все суммы — целые единицы валюты
// показа; домен ничего не форматирует, текст («не хватает N», «просрочен N дн.»)
// собирают BFF и фронт по этим числам и флагам.
type View struct {
	State  StateKind
	Window *WindowInfo // nil при State=none
	// LastWindow — окно, по которому считается колонка «Прошлое окно» (с датами, чтобы
	// экран подписал, о каком периоде речь); nil, если такого окна нет.
	LastWindow *WindowInfo
	// AvgDays — период усреднения колонки «Обычно».
	AvgDays int

	Income IncomeSum
	// CanSpend знаковое: отрицательное значение — недостача.
	CanSpend, Payments, Spent, Reserve int64
	// PerDay nil при daysLeft=0 или недостаче: «0» и «∞» тут были бы ложью.
	PerDay *int64
	// Unpriced — платежи окна без суммы (число); в расчёт не входят.
	Unpriced int

	Undated    UndatedBlock
	Timeline   []Row // полный; режет фронт — это не расчёт
	Next       *NextBlock
	Later      []MonthBucket
	Categories []CategoryRow
}

// IncomeSum — поступления окна отдельно по состоянию.
type IncomeSum struct{ Received, Expected int64 }

// WindowInfo — описание окна.
type WindowInfo struct {
	Start, End    Date
	DaysLeft      int
	AnchorPlanned bool
}

// Row — строка ленты, «без даты», следующего окна и деталей категории.
type Row struct {
	Key         string
	Date        *Date
	Name        string
	Kind        Kind
	Amount      *int64 // целые единицы показа; nil — суммы нет
	OverdueDays int    // только у плановых разовых платежей
	Expected    bool   // ожидаемое поступление
	NotArrived  bool   // ожидаемое поступление, дата которого прошла
	NoAmount    bool
	// WindowMissing — выбрано «в следующее окно», которого ещё нет.
	WindowMissing bool
	// WindowPassed — выбранное окно уже прошло, а платёж не оплачен.
	WindowPassed bool
}

// UndatedBlock — платежи без даты и без окна: лежат здесь и в расчёт не входят.
type UndatedBlock struct {
	Count       int   // все платежи списка
	PricedCount int   // из них с суммой
	Sum         int64 // сумма только пунктов с суммой
	Items       []Row
}

// NextBlock — платежи следующего окна. Count включает платежи без суммы, Sum — нет.
type NextBlock struct {
	Count int
	Sum   int64
	Items []Row
}

// MonthBucket — платежи более дальних месяцев (горизонт: шесть месяцев).
type MonthBucket struct {
	Year, Month int
	Count       int
	Sum         int64
	Items       []Row
}

// CategoryRow — траты категории: в прошлом окне и в текущем.
type CategoryRow struct {
	Name string
	// Avg — обычные траты за окно (14 дней): сумма за AvgDays дней, приведённая к 14.
	Avg       int64
	Last, Now int64
	Recent    []Row
}
