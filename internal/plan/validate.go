package plan

import "fmt"

// Validate проверяет событие до расчёта: домен не чинит данные молча, а отказывает
// с понятной причиной, чтобы битая запись не исказила «Можно тратить».
func (e Event) Validate() error {
	if e.Key == "" {
		return fmt.Errorf("%w: пустой ключ", ErrInvalidEvent)
	}
	if e.Kind != KindIncome && e.Kind != KindPayment {
		return fmt.Errorf("%w: неизвестное направление %q", ErrInvalidEvent, e.Kind)
	}
	if e.State != StatePlanned && e.State != StateDone {
		return fmt.Errorf("%w: неизвестное состояние %q", ErrInvalidEvent, e.State)
	}
	if e.Date != nil && !e.Date.Valid() {
		return fmt.Errorf("%w: %v", ErrBadDate, *e.Date)
	}
	if e.Kind == KindIncome && e.Date == nil {
		return ErrIncomeNoDate
	}
	if e.State == StateDone && e.Date == nil {
		return fmt.Errorf("%w: у оплаченного нет даты", ErrInvalidEvent)
	}
	// Сумма обязательна везде, кроме планового разового платежа-напоминания.
	if e.Amount == nil {
		if e.Kind == KindIncome || e.State == StateDone || e.IsRecurring() {
			return ErrAmountRequired
		}
		return nil
	}
	if e.Amount.Minor <= 0 {
		return ErrAmountNonPositive
	}
	if !e.Amount.Cur.Supported() {
		return ErrUnsupportedCurrency
	}
	return nil
}
