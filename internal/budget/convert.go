package budget

// ToTHB конвертирует amount из currency в THB через rates (currency → rate_to_rub):
//
//	thb = amount * rates[currency] / rates["THB"]
//
// Единый источник конверсии для всех skill'ов (устраняет разбросанные инлайны).
// Возвращает ok=false, если нет курса THB или валюты amount.
func ToTHB(amount float64, currency string, rates map[string]float64) (float64, bool) {
	thbRate, ok := rates["THB"]
	if !ok || thbRate == 0 {
		return 0, false
	}
	rate, ok := rates[currency]
	if !ok || rate == 0 {
		return 0, false
	}
	return amount * rate / thbRate, true
}
