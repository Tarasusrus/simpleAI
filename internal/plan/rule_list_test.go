package plan

import (
	"context"
	"testing"
)

type fakeLister struct{ list RuleList }

func (f fakeLister) ListRules(context.Context, Scope) (RuleList, error) { return f.list, nil }

func TestMonthlyEquivalent(t *testing.T) {
	cases := []struct {
		name   string
		n      int
		unit   Unit
		amount int64
		want   int64
	}{
		{"месяц", 1, UnitMonth, 1000_00, 1000_00},
		{"каждые 2 месяца", 2, UnitMonth, 1000_00, 500_00},
		{"год", 1, UnitYear, 1200_00, 100_00},
		{"неделя", 1, UnitWeek, 1200, 5200},           // 1200·52/12 = 5200
		{"день", 1, UnitDay, 1200, 36500},             // 1200·365/12 = 36500
		{"округление от нуля", 3, UnitMonth, 100, 33}, // 33,33 → 33
		{"половина вверх", 2, UnitMonth, 101, 51},     // 50,5 → 51
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := MonthlyEquivalent(Rule{Anchor: d(2026, 1, 1), N: c.n, Unit: c.unit, Amount: Money{c.amount, CurRUB}})
			if err != nil || got.Minor != c.want || got.Cur != CurRUB {
				t.Fatalf("got %+v, %v; want %d", got, err, c.want)
			}
		})
	}
}

func TestMonthlyEquivalent_BadRule(t *testing.T) {
	if _, err := MonthlyEquivalent(Rule{Anchor: d(2026, 1, 1), N: 0, Unit: UnitMonth, Amount: Money{100, CurRUB}}); err == nil {
		t.Fatal("шаг 0 должен быть отвергнут")
	}
}

func rulesSvc(recs []RuleRecord, rate *Rate, today Date) *Service {
	return NewService(&fakeReader{}, nil, func() Date { return today }, Scope{}, WithRuleLister(fakeLister{RuleList{recs, rate}}))
}

func TestListRules_PausedNextAndMonthly(t *testing.T) {
	rent := RuleRecord{RuleWithExceptions: RuleWithExceptions{
		ID: "rent", Name: "аренда", Kind: KindPayment, Category: "Жильё",
		Rule:       Rule{Anchor: d(2026, 1, 15), N: 1, Unit: UnitMonth, Amount: Money{18000_00, CurTHB}},
		Exceptions: []Date{d(2026, 3, 15)}, // этот срок пропущен
	}}
	gym := RuleRecord{Paused: true, RuleWithExceptions: RuleWithExceptions{
		ID: "gym", Name: "зал", Kind: KindPayment,
		Rule: Rule{Anchor: d(2026, 1, 1), N: 1, Unit: UnitYear, Amount: Money{12000_00, CurRUB}},
	}}
	rows, err := rulesSvc([]RuleRecord{rent, gym}, &Rate{RubPerThbMicro: 2_500_000}, d(2026, 3, 10)).ListRules(context.Background(), CurRUB)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[0].Next == nil || *rows[0].Next != d(2026, 4, 15) {
		t.Errorf("следующий срок аренды = %v, нужен 2026-04-15 (15 марта пропущено)", rows[0].Next)
	}
	if rows[0].Monthly == nil || *rows[0].Monthly != 45000 { // 18000฿ · 2,5
		t.Errorf("аренда в месяц = %v", rows[0].Monthly)
	}
	if !rows[1].Paused || rows[1].Next != nil || rows[1].Monthly == nil || *rows[1].Monthly != 1000 {
		t.Errorf("зал: %+v", rows[1])
	}
}

func TestListRules_NoRateKeepsRows(t *testing.T) {
	rec := RuleRecord{RuleWithExceptions: RuleWithExceptions{ID: "r", Name: "x", Kind: KindPayment,
		Rule: Rule{Anchor: d(2026, 1, 1), N: 1, Unit: UnitMonth, Amount: Money{100_00, CurTHB}}}}
	rows, err := rulesSvc([]RuleRecord{rec}, nil, d(2026, 1, 2)).ListRules(context.Background(), CurRUB)
	if err != nil || len(rows) != 1 || rows[0].Monthly != nil {
		t.Fatalf("без курса строка остаётся, «в месяц» пусто: %+v %v", rows, err)
	}
}

func TestListRules_InvalidRuleDoesNotBreakList(t *testing.T) {
	good := RuleRecord{RuleWithExceptions: RuleWithExceptions{ID: "g", Name: "ok", Kind: KindPayment,
		Rule: Rule{Anchor: d(2026, 1, 1), N: 1, Unit: UnitMonth, Amount: Money{100_00, CurRUB}}}}
	bad := RuleRecord{RuleWithExceptions: RuleWithExceptions{ID: "b", Name: "usd", Kind: KindPayment,
		Rule: Rule{Anchor: d(2026, 1, 1), N: 1, Unit: UnitMonth, Amount: Money{100_00, "USD"}}}}
	rows, err := rulesSvc([]RuleRecord{bad, good}, nil, d(2026, 1, 2)).ListRules(context.Background(), CurRUB)
	if err != nil || len(rows) != 2 || !rows[0].Invalid || rows[1].Invalid || rows[1].Monthly == nil {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

// Сумма «в месяц» × 12 не расходится с годовой суммой правила больше чем на
// половину минимальной единицы за каждый месяц.
func TestMonthlyEquivalent_YearTotal(t *testing.T) {
	yearly := map[Unit]func(n int, a int64) float64{
		UnitDay:   func(n int, a int64) float64 { return float64(a) * 365 / float64(n) },
		UnitWeek:  func(n int, a int64) float64 { return float64(a) * 52 / float64(n) },
		UnitMonth: func(n int, a int64) float64 { return float64(a) * 12 / float64(n) },
		UnitYear:  func(n int, a int64) float64 { return float64(a) / float64(n) },
	}
	for unit, year := range yearly {
		for n := 1; n <= 13; n++ {
			for _, a := range []int64{1, 99, 12345, 9_999_999} {
				m, err := MonthlyEquivalent(Rule{Anchor: d(2026, 1, 1), N: n, Unit: unit, Amount: Money{a, CurRUB}})
				if err != nil {
					t.Fatal(err)
				}
				if diff := float64(m.Minor*12) - year(n, a); diff > 6.0001 || diff < -6.0001 {
					t.Errorf("%s N=%d a=%d: %d·12 против %.2f", unit, n, a, m.Minor, year(n, a))
				}
			}
		}
	}
}

func TestListRules_Off(t *testing.T) {
	s := NewService(&fakeReader{}, nil, func() Date { return d(2026, 1, 1) }, Scope{})
	if _, err := s.ListRules(context.Background(), CurRUB); err != ErrRulesOff {
		t.Fatalf("err = %v", err)
	}
}
