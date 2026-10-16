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

// minPlausibleRate — нижний потолок правдоподобия. Бат дороже рубля с запасом, и
// «курс 0,27» почти наверняка опечатка или курс в обратную сторону (бат за
// рубль): принять его молча значит уменьшить все конверты примерно в десять раз.
const minPlausibleRate = 1.0

// rateVerdict — результат проверки названного курса на правдоподобие.
type rateVerdict int

const (
	rateOK rateVerdict = iota
	rateTooLow
	rateTooHigh
)

// checkRate отделяет правдоподобный курс от опечатки. Границы включительные.
// Чистая функция: проверку можно прогнать без базы.
func checkRate(rate float64) rateVerdict {
	switch {
	case rate < minPlausibleRate:
		return rateTooLow
	case rate > maxPlausibleRate:
		return rateTooHigh
	}
	return rateOK
}

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
	// Курс бата к рублю живёт в единицах, а не в десятках и не в долях: «курс 27»
	// — почти наверняка «2,7» без запятой, «курс 0,27» — опечатка или обратный
	// курс. Молча принять такое значение нельзя: конверты исказятся в десять раз
	// и будут выглядеть правдоподобно.
	switch checkRate(rate) {
	case rateOK:
		// правдоподобный курс — сохраняем ниже
	case rateTooHigh:
		msg := fmt.Sprintf("Курс %s ₽/฿ — это в разы больше обычного%s.", safetospend.FmtRate(rate), s.currentRateHint(ctx))
		if hint := rate / 10; checkRate(hint) == rateOK {
			msg += fmt.Sprintf(" Если имелось в виду с десятыми, скажи ещё раз: «курс %s».", safetospend.FmtRate(hint))
		}
		return msg, nil
	case rateTooLow:
		return fmt.Sprintf("Курс %s ₽/฿ — меньше рубля за бат, так не бывает%s. Скажи ещё раз: «курс 2,7» — сколько рублей за один бат.",
			safetospend.FmtRate(rate), s.currentRateHint(ctx)), nil
	}

	if err := s.store.SetManualRate(ctx, rateCurrency, rate); err != nil {
		slog.Default().ErrorContext(ctx, "set_rate", "err", err, "rate", rate)
		return "Не удалось сохранить курс — попробуй ещё раз.", nil
	}
	slog.Default().InfoContext(ctx, "set_rate", "rate", rate, "currency", rateCurrency)

	return fmt.Sprintf("💱 Курс: %s ₽/฿ (вручную). Считаю по нему, пока не скажешь «курс авто».",
		safetospend.FmtRate(rate)), nil
}

// currentRateHint — «, сейчас 2,54» для сообщений об отклонённом курсе. Берётся
// действующий курс из базы, а не зашитое число: оно устарело бы при первом
// же сдвиге курса. Нет данных — пустая строка, сообщение остаётся осмысленным.
func (s *BudgetSkill) currentRateHint(ctx context.Context) string {
	src, ok, err := s.store.GetRateSource(ctx, rateCurrency)
	if err != nil || !ok {
		return ""
	}
	return fmt.Sprintf(" (сейчас %s)", safetospend.FmtRate(src.RateToRUB))
}

// clearRate возвращает автоматический курс. Идемпотентно: «курс авто» без
// заданного ручного — не ошибка, а подтверждение того же состояния.
func (s *BudgetSkill) clearRate(ctx context.Context) (string, error) {
	if err := s.store.ClearManualRate(ctx, rateCurrency); err != nil {
		slog.Default().ErrorContext(ctx, "clear_rate", "err", err)
		return "Не удалось вернуть автоматический курс — попробуй ещё раз.", nil
	}
	src, ok, err := s.store.GetRateSource(ctx, rateCurrency)
	if err != nil {
		slog.Default().WarnContext(ctx, "clear_rate: read back", "err", err)
		return "💱 Вернул автоматический курс.", nil
	}
	if !ok || !src.HasAuto {
		return "💱 Ручной курс снят. Автоматический подтянется в ближайшие сутки.", nil
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
		if !src.HasAuto {
			b.WriteString("\nАвтоматического курса пока нет — скажи «курс авто», чтобы снять ручной.")
			return b.String(), nil
		}
		fmt.Fprintf(&b, "\nАвтоматический сейчас %s ₽/฿ — скажи «курс авто», чтобы вернуться к нему.",
			safetospend.FmtRate(src.Auto))
		return b.String(), nil
	}
	fmt.Fprintf(&b, "💱 Курс: %s ₽/฿ (автоматически, обновлён %s).",
		safetospend.FmtRate(src.RateToRUB), src.UpdatedAt.Local().Format("02.01 15:04"))
	b.WriteString("\nЕсли в обменнике другой — скажи «курс 2,7», буду считать по нему.")
	return b.String(), nil
}
