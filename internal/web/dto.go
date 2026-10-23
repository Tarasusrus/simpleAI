package web

import (
	"fmt"

	"simpleAI/internal/plan"
)

// WindowViewVersion — версия контракта ответа. Меняется при несовместимой правке.
const WindowViewVersion = 1

// Контракт ответа GET /api/plan/window (specs/income-plan-sheet/contract.md).
// Здесь только раскладка готовых чисел домена по полям: ни денег, ни бизнес-правил.
// Суммы — целые единицы валюты показа (рубли или баты), даты — «YYYY-MM-DD».

type WindowView struct {
	V      int        `json:"v"`
	Meta   MetaDTO    `json:"meta"`
	State  string     `json:"state"`
	Window *WindowDTO `json:"window"`
	// LastWindow — окно колонки «Прошлое окно» (даты для подписи); null, если его нет.
	LastWindow *WindowDTO `json:"lastWindow"`
	// AvgDays — за сколько дней считана колонка «Обычно».
	AvgDays    int           `json:"avgDays"`
	KPI        KPIDTO        `json:"kpi"`
	Timeline   []RowDTO      `json:"timeline"`
	Next       *BlockDTO     `json:"next"`
	Later      []MonthDTO    `json:"later"`
	Inbox      []RowDTO      `json:"inbox"`
	Categories []CategoryDTO `json:"categories"`
	// Limits — лимиты категорий окна (ADR-011); [] если их нет.
	Limits []LimitDTO `json:"limits"`
	// Unplanned — категории без действующего лимита с тратами окна (ADR-015 §3); [] если их нет.
	Unplanned []UnplannedDTO `json:"unplanned"`
}

// UnplannedDTO — категория без лимита и сколько в ней потрачено за окно.
type UnplannedDTO struct {
	Category string `json:"category"`
	Spent    int64  `json:"spent"`
}

type MetaDTO struct {
	Today       string `json:"today"`
	Cur         string `json:"cur"`
	RatesManual bool   `json:"ratesManual"`
	HasRate     bool   `json:"hasRate"`
	// Rate — рублей за бат, для поля на экране; 0, если курса нет.
	Rate float64 `json:"rate"`
	// LastEnd — конец закончившегося последнего окна, когда нового нет; иначе null.
	LastEnd *string `json:"lastEnd"`
	Skipped int     `json:"skipped"`
}

type WindowDTO struct {
	Start         string `json:"start"`
	End           string `json:"end"`
	DaysLeft      int    `json:"daysLeft"`
	AnchorPlanned bool   `json:"anchorPlanned"`
}

type KPIDTO struct {
	CanSpend        int64   `json:"canSpend"`
	PerDay          *int64  `json:"perDay"`
	Payments        int64   `json:"payments"`
	Spent           int64   `json:"spent"`
	SpentBefore     int64   `json:"spentBefore"`
	SpentBeforeFrom *string `json:"spentBeforeFrom"`
	Reserve         int64   `json:"reserve"`
	Received        int64   `json:"received"`
	ExpectedIncome  int64   `json:"expectedIncome"`
	Unpriced        int     `json:"unpriced"`
	UndatedCount    int     `json:"undatedCount"`
	UndatedSum      int64   `json:"undatedSum"`
	// LimitHold — сумма остатков лимитов: отложена и уже вычтена из canSpend.
	LimitHold        int64 `json:"limitHold"`
	LimitsExceedFree bool  `json:"limitsExceedFree"`
	// UnplannedSpent — часть spent в категориях без лимита; в canSpend отдельно не вычитается.
	UnplannedSpent int64 `json:"unplannedSpent"`
}

type RowDTO struct {
	Key           string  `json:"key"`
	Date          *string `json:"date"`
	Name          string  `json:"name"`
	Kind          string  `json:"kind"`
	Amount        *int64  `json:"amount"`
	OverdueDays   int     `json:"overdueDays"`
	Expected      bool    `json:"expected"`
	NotArrived    bool    `json:"notArrived"`
	NoAmount      bool    `json:"noAmount"`
	WindowMissing bool    `json:"windowMissing"`
	WindowPassed  bool    `json:"windowPassed"`
	// Права строки выводит домен (plan.Event.Actions); фронт их не угадывает по ключу.
	Editable   bool `json:"editable"`
	Reopenable bool `json:"reopenable"`
}

type BlockDTO struct {
	Count int      `json:"count"`
	Sum   int64    `json:"sum"`
	Items []RowDTO `json:"items"`
}

type MonthDTO struct {
	Year  int      `json:"year"`
	Month int      `json:"month"`
	Count int      `json:"count"`
	Sum   int64    `json:"sum"`
	Items []RowDTO `json:"items"`
}

type CategoryDTO struct {
	Name    string   `json:"name"`
	Avg     int64    `json:"avg"`
	Planned int64    `json:"planned"`
	Free    int64    `json:"free"`
	Last    int64    `json:"last"`
	Now     int64    `json:"now"`
	Recent  []RowDTO `json:"recent"`
}

func dateStr(d plan.Date) string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

func datePtr(d *plan.Date) *string {
	if d == nil {
		return nil
	}
	s := dateStr(*d)
	return &s
}

func rows(in []plan.Row) []RowDTO {
	out := make([]RowDTO, 0, len(in)) // пустой список — [], а не null
	for _, r := range in {
		out = append(out, RowDTO{
			Key: r.Key, Date: datePtr(r.Date), Name: r.Name, Kind: string(r.Kind), Amount: r.Amount,
			OverdueDays: r.OverdueDays, Expected: r.Expected, NotArrived: r.NotArrived, NoAmount: r.NoAmount,
			WindowMissing: r.WindowMissing, WindowPassed: r.WindowPassed,
			Editable: r.Editable, Reopenable: r.Reopenable,
		})
	}
	return out
}

// toWindowView раскладывает результат сервиса по полям контракта.
func toWindowView(res plan.Result, cur plan.Currency) WindowView {
	v := res.View
	out := WindowView{
		V: WindowViewVersion,
		Meta: MetaDTO{
			Today: dateStr(res.Meta.Today), Cur: string(cur),
			RatesManual: res.Meta.RatesManual, HasRate: res.Meta.HasRate, Rate: float64(res.Meta.RubPerThbMicro) / 1_000_000, LastEnd: datePtr(res.Meta.LastEnd), Skipped: res.Meta.Skipped,
		},
		State: string(v.State),
		KPI: KPIDTO{
			CanSpend: v.CanSpend, PerDay: v.PerDay, Payments: v.Payments, Spent: v.Spent, SpentBefore: v.SpentBefore, Reserve: v.Reserve,
			Received: v.Income.Received, ExpectedIncome: v.Income.Expected, Unpriced: v.Unpriced,
			UndatedCount: v.Undated.Count, UndatedSum: v.Undated.Sum,
			LimitHold: v.LimitHold, LimitsExceedFree: v.LimitsExceedFree,
			UnplannedSpent: v.UnplannedSpent,
		},
		Timeline:   rows(v.Timeline),
		Inbox:      rows(v.Undated.Items),
		Later:      make([]MonthDTO, 0, len(v.Later)),
		Categories: make([]CategoryDTO, 0, len(v.Categories)),
		Limits:     limitDTOs(v.Limits),
		Unplanned:  unplannedDTOs(v.Unplanned),
	}
	if v.Window != nil {
		out.Window = &WindowDTO{
			Start: dateStr(v.Window.Start), End: dateStr(v.Window.End),
			DaysLeft: v.Window.DaysLeft, AnchorPlanned: v.Window.AnchorPlanned,
		}
	}
	out.AvgDays = v.AvgDays
	if v.LastWindow != nil {
		out.LastWindow = &WindowDTO{Start: dateStr(v.LastWindow.Start), End: dateStr(v.LastWindow.End)}
	}
	if v.Next != nil {
		out.Next = &BlockDTO{Count: v.Next.Count, Sum: v.Next.Sum, Items: rows(v.Next.Items)}
	}
	for _, m := range v.Later {
		out.Later = append(out.Later, MonthDTO{Year: m.Year, Month: m.Month, Count: m.Count, Sum: m.Sum, Items: rows(m.Items)})
	}
	for _, c := range v.Categories {
		out.Categories = append(out.Categories, CategoryDTO{Name: c.Name, Avg: c.Avg, Planned: c.Planned, Free: c.Free, Last: c.Last, Now: c.Now, Recent: rows(c.Recent)})
	}
	return out
}

func unplannedDTOs(in []plan.UnplannedRow) []UnplannedDTO {
	out := make([]UnplannedDTO, 0, len(in)) // пустой список — [], а не null
	for _, r := range in {
		out = append(out, UnplannedDTO{Category: r.Category, Spent: r.Spent})
	}
	return out
}
