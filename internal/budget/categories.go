package budget

// Классификация категорий — единый доменный источник (OCP: правило расширяется
// добавлением в набор, а не правкой логики в скиллах).

// nonConsumptionCategories — категории «движения денег», а не потребления:
// переводы, погашение кредитов/долгов. Их НЕ включают в прогноз «съедаемых»
// трат и в остаток конверта — иначе двойной учёт обязательств (ADR-007 §4).
var nonConsumptionCategories = map[string]bool{
	"Переводы": true, "переводы": true,
	"Кредит": true, "кредит": true,
	"Долг": true, "долг": true,
}

// IsConsumptionCategory — true для потребительских категорий (еда, транспорт…),
// прогноз/факт которых имеет смысл вычитать из свободных денег.
func IsConsumptionCategory(category string) bool {
	return !nonConsumptionCategories[category]
}

// fixedExpenseCategories — предсказуемые ФИКСИРОВАННЫЕ траты (аренда, подписки,
// коммуналка). Пользователь их знает и обычно учитывает как recurring, поэтому
// в прогноз «переменных ежедневных трат, которые нельзя учесть» они НЕ входят
// (иначе прогноз раздувается арендой и уводит остаток в ложный минус).
var fixedExpenseCategories = map[string]bool{
	"Жильё": true, "жильё": true, "аренда": true, "Аренда": true,
	"Подписки": true, "подписки": true,
	"коммунальные услуги": true, "Коммунальные услуги": true,
	"водоснабжение": true,
}

// IsVariableDailyExpense — true для ПЕРЕМЕННЫХ ежедневных трат (еда, транспорт,
// кафе, развлечения…): потребительская И не фиксированная. Это то, что
// пользователь не может заранее учесть и где реально экономить.
func IsVariableDailyExpense(category string) bool {
	return IsConsumptionCategory(category) && !fixedExpenseCategories[category]
}

// OneOffIncomeCategories — категории разовых поступлений (при recurring_id IS
// NULL), исключаемых из якоря РЕГУЛЯРНОГО дохода (ADR-007 §8). Единый источник:
// используется как параметр SQL в GetRegularMonthlyIncomeAvg.
var OneOffIncomeCategories = []string{"Прочее", ""}
