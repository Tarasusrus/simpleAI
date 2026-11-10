package budget

import (
	"testing"
	"time"
)

// Трата без валюты («кафе 405») записывается в батах — валюте по умолчанию.
//
// Мутация: вернуть "RUB" в AddTransaction — тест краснеет.
func TestAddTransaction_DefaultCurrencyIsTHB(t *testing.T) {
	s, ctx := rateTestStore(t)
	const desc = "default-currency-test кафе"
	clean := func() {
		if _, err := s.pool.Exec(ctx, `DELETE FROM budget_transaction WHERE description = $1`, desc); err != nil {
			t.Errorf("очистка: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)
	if err := s.AddTransaction(ctx, Transaction{Type: "expense", Amount: 405, Description: desc, Date: time.Date(2099, 1, 2, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	var cur string
	if err := s.pool.QueryRow(ctx, `SELECT currency FROM budget_transaction WHERE description = $1`, desc).Scan(&cur); err != nil {
		t.Fatal(err)
	}
	if cur != "THB" || DefaultCurrency != CurrencyTHB {
		t.Fatalf("валюта %q, ждали THB", cur)
	}
}
