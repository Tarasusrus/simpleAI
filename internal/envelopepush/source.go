// Package envelopepush собирает тело утреннего пуша с конвертами.
//
// Отдельный пакет, а не метод скилла, ровно по одной причине: тело берётся у
// существующего показа конвертов (safe_to_spend), и переписывать его формат
// здесь нельзя — формат живёт в скилле и меняется там. Пакет только решает,
// ЕСТЬ ли что слать, и просит скилл напечатать то же, что оператор видит по
// запросу «сколько в конвертах».
package envelopepush

import (
	"context"
	"strings"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

// MorningHeader — шапка утреннего пуша. Тело под ней — вывод скилла как есть.
const MorningHeader = "☀️ Доброе утро! Конверты на сегодня:"

// envelopeStore — проверка, что раскладка вообще существует.
type envelopeStore interface {
	GetActiveEnvelope(ctx context.Context, chatID int64) (*budget.Envelope, bool, error)
}

// sharesSkill — показ конвертов (safe_to_spend). Берём его через plugin.Skill-
// совместимый Run, чтобы не зависеть от внутренних методов скилла.
type sharesSkill interface {
	Run(ctx context.Context, input string) (string, error)
}

// Source реализует notify.EnvelopeSource.
type Source struct {
	store envelopeStore
	skill sharesSkill
}

// New собирает источник утренних конвертов.
func New(store envelopeStore, skill sharesSkill) *Source {
	return &Source{store: store, skill: skill}
}

// sharesRequest — тот же запрос, что оператор задаёт словами: без суммы и со
// словом «конверты» скилл уходит в режим остатка по долям.
const sharesRequest = `{"question":"сколько осталось в конвертах"}`

// MorningEnvelopes возвращает тело пуша. Пустая строка = слать нечего:
// активного конверта нет либо показ ничего не напечатал.
func (s *Source) MorningEnvelopes(ctx context.Context, chatID int64, _ *time.Location) (string, error) {
	// Проверка конверта здесь, а не по тексту ответа: скилл на отсутствие
	// раскладки отвечает связной фразой («Активного конверта нет…»), и пуш
	// каждое утро повторял бы её вместо молчания.
	_, ok, err := s.store.GetActiveEnvelope(ctx, chatID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	body, err := s.skill.Run(context.WithValue(ctx, agent.ChatIDKey{}, chatID), sharesRequest)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) == "" {
		return "", nil
	}
	return MorningHeader + "\n\n" + body, nil
}
