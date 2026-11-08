package telegram

import (
	"context"
	"testing"

	"simpleAI/internal/core"
)

func runWithChat(mw Middleware, chatID int64) (called bool, bot *captureBot) {
	bot = &captureBot{}
	h := mw(func(context.Context, *Context) error { called = true; return nil })
	_ = h(context.Background(), &Context{Bot: bot, Update: core.Update{ChatID: chatID}})
	return called, bot
}

func TestRequireAllowedChatsEmptyListDeniesEveryone(t *testing.T) {
	called, bot := runWithChat(RequireAllowedChats(nil), 42)
	if called {
		t.Fatal("пустой список не должен пускать никого")
	}
	if len(bot.sent) != 1 {
		t.Fatalf("ожидался отказ пользователю, sent=%v", bot.sent)
	}
}

func TestRequireAllowedChatsListed(t *testing.T) {
	mw := RequireAllowedChats([]int64{7})
	if called, _ := runWithChat(mw, 7); !called {
		t.Fatal("чат из списка должен проходить")
	}
	if called, _ := runWithChat(mw, 8); called {
		t.Fatal("чужой чат не должен проходить")
	}
}

func TestAllowAnyChatPassesEveryone(t *testing.T) {
	if called, _ := runWithChat(AllowAnyChat(), 99); !called {
		t.Fatal("AllowAnyChat должен пускать всех")
	}
}
