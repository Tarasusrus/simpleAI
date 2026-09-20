package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"simpleAI/internal/constants"
	"simpleAI/internal/core"
)

// captureBot запоминает отправленные пользователю тексты.
type captureBot struct{ sent []string }

func (b *captureBot) Updates(context.Context) (<-chan core.Update, error) { return nil, nil }

func (b *captureBot) Send(_ context.Context, _ int64, text string) error {
	b.sent = append(b.sent, text)
	return nil
}

func (b *captureBot) Reply(_ context.Context, _ int64, _ int, text string) error {
	b.sent = append(b.sent, text)
	return nil
}

func (b *captureBot) lastSent() string {
	if len(b.sent) == 0 {
		return ""
	}
	return b.sent[len(b.sent)-1]
}

// failingAgent всегда падает с заданной ошибкой.
type failingAgent struct{ err error }

func (a *failingAgent) Ask(context.Context, string) (string, error) { return "", a.err }

func (a *failingAgent) AskWithMeta(context.Context, string, *int64) (string, error) {
	return "", a.err
}

// failingCallbackSkill всегда падает с заданной ошибкой.
type failingCallbackSkill struct{ err error }

func (s *failingCallbackSkill) HandleCallbackData(context.Context, int64, string) (core.CallbackResult, error) {
	return core.CallbackResult{}, s.err
}

func newTestContext(t *testing.T, bot core.Bot, agent AgentService, update core.Update) *Context {
	t.Helper()
	return &Context{Bot: bot, Update: update, Agent: agent, MediaDir: t.TempDir()}
}

// Ошибка агента не уезжает пользователю дословно.
func TestHandleDefault_AgentError_HumanMessage(t *testing.T) {
	bot := &captureBot{}
	tctx := newTestContext(t, bot, &failingAgent{err: errors.New("pgx: connection refused")},
		core.Update{ChatID: 42, MessageID: 7, Text: "Продукты 512 Бат"})

	if err := HandleDefault(context.Background(), tctx); err != nil {
		t.Fatal(err)
	}
	if bot.lastSent() != constants.MsgAgentFailed {
		t.Errorf("want %q, got %q", constants.MsgAgentFailed, bot.lastSent())
	}
}

// Ошибка обработчика inline-кнопки не уезжает пользователю дословно.
func TestBudgetCallback_SkillError_HumanMessage(t *testing.T) {
	bot := &captureBot{}
	handler := NewHandleBudgetCallback(&failingCallbackSkill{err: errors.New("pgx: no rows in result set")})
	tctx := newTestContext(t, bot, nil,
		core.Update{ChatID: 42, MessageID: 7, IsCallback: true, CallbackData: "budget:1"})

	if err := handler(context.Background(), tctx); err != nil {
		t.Fatal(err)
	}
	if bot.lastSent() != constants.MsgSkillFailed {
		t.Errorf("want %q, got %q", constants.MsgSkillFailed, bot.lastSent())
	}
}

// Инвариант: каким бы ни был текст внутренней ошибки, пользователь его не видит.
func TestProperty_InternalErrorTextNeverSentToUser(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		token := rapid.StringMatching(`[a-z]{6,12}`).Draw(rt, "token")
		err := fmt.Errorf("internal: %s", token)

		bot := &captureBot{}
		tctx := newTestContext(t, bot, &failingAgent{err: err},
			core.Update{ChatID: 42, MessageID: 7, Text: "трата 100"})
		if hErr := HandleDefault(context.Background(), tctx); hErr != nil {
			rt.Fatal(hErr)
		}

		cbBot := &captureBot{}
		handler := NewHandleBudgetCallback(&failingCallbackSkill{err: err})
		cbCtx := newTestContext(t, cbBot, nil,
			core.Update{ChatID: 42, MessageID: 7, IsCallback: true, CallbackData: "budget:1"})
		if hErr := handler(context.Background(), cbCtx); hErr != nil {
			rt.Fatal(hErr)
		}

		for _, sent := range append(bot.sent, cbBot.sent...) {
			if strings.Contains(sent, token) {
				rt.Fatalf("текст внутренней ошибки ушёл пользователю: %q", sent)
			}
		}
	})
}
