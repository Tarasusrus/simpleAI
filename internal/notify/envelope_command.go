package notify

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"simpleAI/internal/budget"
)

// DefaultEnvelopeHour — час утреннего пуша, когда оператор не назвал время.
const DefaultEnvelopeHour = 8

// DefaultEnvelopeTimezone — пояс по умолчанию: оператор живёт в Таиланде,
// доход считает в рублях. Пояс UTC давал бы пуш посреди ночи.
const DefaultEnvelopeTimezone = "Asia/Bangkok"

// EnvelopeCommand — распознанная команда управления утренней рассылкой конвертов.
type EnvelopeCommand struct {
	Enable  bool
	HasTime bool // время названо явно; иначе Hour/Minute не заполнены
	Hour    int
	Minute  int
}

// EnvelopeReminderStore — минимум, нужный для включения/выключения рассылки.
type EnvelopeReminderStore interface {
	GetReminder(ctx context.Context, chatID int64) (*budget.Reminder, error)
	SetEnvelopeReminder(ctx context.Context, r budget.Reminder) error
}

// Границу слова пишем через класс «не буква» вручную: \b в RE2 — ASCII-only,
// с кириллицей он не срабатывает вовсе (первая версия парсера молча не узнавала
// ни одной фразы оператора).
var (
	// «в 7:30», «в 7.30», «в 9» — время после предлога, чтобы не подобрать сумму из соседней фразы.
	envelopeTimeRe = regexp.MustCompile(`(?:^|[^\p{L}])в\s+(\d{1,2})(?:[:.](\d{2}))?(?:[^\d]|$)`)
	negationRe     = regexp.MustCompile(`(?:^|[^\p{L}])(не|выключи|отключи|перестань|прекрати|хватит)(?:[^\p{L}]|$)`)
	positiveRe     = regexp.MustCompile(`(?:^|[^\p{L}])(присылай|шли|высылай|включи|напоминай|показывай|отправляй)(?:[^\p{L}]|$)`)
)

// ParseEnvelopeCommand разбирает фразу оператора вида «присылай конверты по утрам [в 7:30]»
// или «не присылай конверты». Второй результат false — фраза не про расписание конвертов.
//
// Разбор детерминированный, мимо LLM: включение и выключение рассылки не должно
// зависеть от того, угадала ли модель нужный action — цена ошибки здесь
// молчаливая (пуш просто не приходит, и оператор об этом не узнает).
func ParseEnvelopeCommand(text string) (EnvelopeCommand, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" || !strings.Contains(t, "конверт") {
		return EnvelopeCommand{}, false
	}

	morning := strings.Contains(t, "утр") // «по утрам», «утром», «утренние»
	neg := negationRe.MatchString(t)
	pos := positiveRe.MatchString(t)

	switch {
	case neg && (pos || morning):
		// «не присылай конверты» — про утро можно и не упоминать.
		return EnvelopeCommand{Enable: false}, true
	case pos && morning:
		cmd := EnvelopeCommand{Enable: true}
		if m := envelopeTimeRe.FindStringSubmatch(t); m != nil {
			h, err := strconv.Atoi(m[1])
			if err != nil || h > 23 {
				return cmd, true
			}
			min := 0
			if m[2] != "" {
				min, err = strconv.Atoi(m[2])
				if err != nil || min > 59 {
					return cmd, true
				}
			}
			cmd.HasTime, cmd.Hour, cmd.Minute = true, h, min
		}
		return cmd, true
	default:
		return EnvelopeCommand{}, false
	}
}

// ApplyEnvelopeCommand сохраняет настройку и возвращает ответ оператору.
// defaultTZ используется, только если у чата ещё нет сохранённого пояса.
func ApplyEnvelopeCommand(ctx context.Context, store EnvelopeReminderStore, chatID int64, cmd EnvelopeCommand, defaultTZ string) (string, error) {
	if defaultTZ == "" {
		defaultTZ = "UTC"
	}

	r := budget.Reminder{ChatID: chatID, Timezone: defaultTZ, EnvelopeHour: DefaultEnvelopeHour}
	// Ошибка чтения = настройки ещё нет: строка создаётся первым включением,
	// и падать из-за её отсутствия нельзя.
	if cur, err := store.GetReminder(ctx, chatID); err == nil && cur != nil {
		r = *cur
		if r.Timezone == "" {
			r.Timezone = defaultTZ
		}
		if !r.EnvelopeEnabled && r.EnvelopeHour == 0 && r.EnvelopeMinute == 0 {
			r.EnvelopeHour = DefaultEnvelopeHour
		}
	}
	r.ChatID = chatID
	r.EnvelopeEnabled = cmd.Enable
	if cmd.Enable && cmd.HasTime {
		r.EnvelopeHour, r.EnvelopeMinute = cmd.Hour, cmd.Minute
	}

	if err := store.SetEnvelopeReminder(ctx, r); err != nil {
		return "", err
	}

	if !cmd.Enable {
		return "🔕 Утренние конверты больше не присылаю.", nil
	}
	return fmt.Sprintf("✉️ Буду присылать конверты каждое утро в %02d:%02d (%s).", r.EnvelopeHour, r.EnvelopeMinute, r.Timezone), nil
}
