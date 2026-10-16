package plan

import (
	"errors"
	"testing"
)

func dp(y, m, d int) *Date              { x := Date{y, m, d}; return &x }
func mp(minor int64, c Currency) *Money { return &Money{minor, c} }

func okPayment() Event {
	return Event{Key: "planned:1", Kind: KindPayment, State: StatePlanned, Name: "Стоматолог",
		Amount: mp(200000, CurTHB), Date: dp(2026, 10, 21), Source: SourcePlanned}
}

func TestEventValidate(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Event)
		want error // nil = валидно
	}{
		{"валидный платёж", func(e *Event) {}, nil},
		{"пустой ключ", func(e *Event) { e.Key = "" }, ErrInvalidEvent},
		{"плановый платёж без суммы допустим", func(e *Event) { e.Amount = nil }, nil},
		{"плановый платёж без даты допустим", func(e *Event) { e.Date = nil }, nil},
		{"оплаченный без суммы", func(e *Event) { e.State = StateDone; e.Amount = nil }, ErrAmountRequired},
		{"оплаченный без даты", func(e *Event) { e.State = StateDone; e.Date = nil }, ErrInvalidEvent},
		{"повторяющийся без суммы", func(e *Event) { e.RuleID = "r1"; e.Amount = nil }, ErrAmountRequired},
		{"нулевая сумма", func(e *Event) { e.Amount = mp(0, CurTHB) }, ErrAmountNonPositive},
		{"отрицательная сумма", func(e *Event) { e.Amount = mp(-1, CurTHB) }, ErrAmountNonPositive},
		{"валюта USD", func(e *Event) { e.Amount = mp(100, "USD") }, ErrUnsupportedCurrency},
		{"поступление без даты", func(e *Event) { e.Kind = KindIncome; e.Date = nil }, ErrIncomeNoDate},
		{"поступление без суммы", func(e *Event) { e.Kind = KindIncome; e.Amount = nil }, ErrAmountRequired},
		{"невалидная дата", func(e *Event) { e.Date = dp(2026, 2, 30) }, ErrBadDate},
		{"неизвестный вид", func(e *Event) { e.Kind = "x" }, ErrInvalidEvent},
		{"неизвестное состояние", func(e *Event) { e.State = "x" }, ErrInvalidEvent},
		{"WindowStart при заданной Date не ошибка", func(e *Event) { e.WindowStart = dp(2026, 10, 5) }, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := okPayment()
			c.mod(&e)
			err := e.Validate()
			if c.want == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, ждали nil", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("Validate() = %v, ждали %v", err, c.want)
			}
		})
	}
}

func TestEvent_IsRecurring_OnlyByRuleID(t *testing.T) {
	e := okPayment()
	if e.IsRecurring() {
		t.Fatal("разовое событие признано повторяющимся")
	}
	e.RuleID = "loan"
	if !e.IsRecurring() {
		t.Fatal("событие с RuleID не признано повторяющимся")
	}
}
