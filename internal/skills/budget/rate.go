package budgetskill

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"simpleAI/internal/skills/safetospend"
)

// rateCurrency — валюта, курс которой правится словами.
//
// Одна и только одна: rates хранит «₽ за 1 единицу», рубль в этой шкале равен
// единице по определению, а доллар и евро в конвертах не участвуют. Оператор
// живёт в батах и спрашивает про бат — «курс 2,7» без названия валюты значит
// именно его.
const rateCurrency = "THB"

// maxPlausibleRate — потолок правдоподобия для курса ₽ за 1 ฿. Бат к рублю
// исторически держится в единицах (2–4). Значение выше почти наверняка сказано
// без запятой («курс 27» вместо «2,7»), и принять его молча значит раздуть все
// конверты в десять раз убедительно выглядящими числами.
const maxPlausibleRate = 10.0

// setRate задаёт ручной курс бата (simpleAI-su6l).
//
// Зачем это вообще: автокурс приходит из open.er-api.com — межбанк. Оператор
// меняет наличными в Тайланде, где курс другой, и все конверты считались по
// цифре, на которую он не мог повлиять никак, даже рестартом бота.
//
// Ручной курс живёт в отдельной колонке и переживает суточный тик воркера —
// иначе его затёрло бы в ближайшие 24 часа.
func (s *BudgetSkill) setRate(ctx context.Context, req budgetInput) (string, error) {
	rate := req.Amount
	if rate <= 0 {
		return "Не понял курс. Скажи «курс 2,7» — сколько рублей за один бат.", nil
	}
	// Курс бата к рублю живёт в единицах, а не в десятках: «курс 27» — это почти
	// наверняка «2,7», сказанное без запятой. Молча принять такое значение
	// нельзя: конверты раздуются в десять раз и будут выглядеть правдоподобно.
	if rate > maxPlausibleRate {
		return fmt.Sprintf("Курс %s ₽/฿ — это в разы больше обычного (около 2,5). "+
			"Если правда столько, скажи ещё раз с десятыми: «курс %s».",
			safetospend.FmtRate(rate), safetospend.FmtRate(rate/10)), nil
	}

	if err := s.store.SetManualRate(ctx, rateCurrency, rate); err != nil {
		slog.Default().ErrorContext(ctx, "set_rate", "err", err, "rate", rate)
		return "Не удалось сохранить курс — попробуй ещё раз.", nil
	}
	slog.Default().InfoContext(ctx, "set_rate", "rate", rate, "currency", rateCurrency)

	return fmt.Sprintf("💱 Курс: %s ₽/฿ (вручную). Считаю по нему, пока не скажешь «курс авто».",
		safetospend.FmtRate(rate)), nil
}

// clearRate возвращает автоматический курс. Идемпотентно: «курс авто» без
// заданного ручного — не ошибка, а подтверждение того же состояния.
func (s *BudgetSkill) clearRate(ctx context.Context) (string, error) {
	if err := s.store.ClearManualRate(ctx, rateCurrency); err != nil {
		slog.Default().ErrorContext(ctx, "clear_rate", "err", err)
		return "Не удалось вернуть автоматический курс — попробуй ещё раз.", nil
	}
	src, ok, err := s.store.GetRateSource(ctx, rateCurrency)
	if err != nil || !ok {
		slog.Default().WarnContext(ctx, "clear_rate: read back", "err", err, "found", ok)
		return "💱 Вернул автоматический курс.", nil
	}
	return fmt.Sprintf("💱 Вернул автоматический курс %s ₽/฿ (обновлён %s).",
		safetospend.FmtRate(src.RateToRUB), src.UpdatedAt.Local().Format("02.01 15:04")), nil
}

// rateStatus — «какой сейчас курс». Отдельным ответом показывает, ручной он или
// автоматический: без этого оператор не отличит подействовавшую команду от
// проигнорированной.
func (s *BudgetSkill) rateStatus(ctx context.Context) (string, error) {
	src, ok, err := s.store.GetRateSource(ctx, rateCurrency)
	if err != nil {
		slog.Default().ErrorContext(ctx, "rate_status", "err", err)
		return "Не удалось получить курс — попробуй позже.", nil
	}
	if !ok {
		return "Курса бата пока нет — он подтянется автоматически в ближайшие сутки.", nil
	}
	var b strings.Builder
	if src.Manual {
		fmt.Fprintf(&b, "💱 Курс: %s ₽/฿ (вручную, задан %s).",
			safetospend.FmtRate(src.RateToRUB), src.UpdatedAt.Local().Format("02.01 15:04"))
		fmt.Fprintf(&b, "\nАвтоматический сейчас %s ₽/฿ — скажи «курс авто», чтобы вернуться к нему.",
			safetospend.FmtRate(src.Auto))
		return b.String(), nil
	}
	fmt.Fprintf(&b, "💱 Курс: %s ₽/฿ (автоматически, обновлён %s).",
		safetospend.FmtRate(src.RateToRUB), src.UpdatedAt.Local().Format("02.01 15:04"))
	b.WriteString("\nЕсли в обменнике другой — скажи «курс 2,7», буду считать по нему.")
	return b.String(), nil
}
