package telegram

import (
	"context"
	"fmt"
)

func RequireAllowedChats(allowed []int64) Middleware {
	allowAll := len(allowed) == 0
	allowedSet := make(map[int64]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}

	return func(next Handler) Handler {
		return func(ctx context.Context, tctx *Context) error {
			if allowAll {
				return next(ctx, tctx)
			}
			chatID, err := tctx.ChatID()
			if err != nil {
				return err
			}
			if _, ok := allowedSet[chatID]; !ok {
				_ = tctx.Reply(fmt.Sprintf("Этот чат (%d) не разрешен.", chatID))
				return nil
			}
			return next(ctx, tctx)
		}
	}
}

func LogUpdates() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, tctx *Context) error {
			if tctx.Logger != nil && tctx.Update.Message != nil {
				tctx.Logger.Info("telegram update",
					"chat_id", tctx.Update.Message.Chat.ID,
					"from", tctx.Update.Message.From.UserName,
					"command", tctx.Update.Message.Command(),
				)
			}
			return next(ctx, tctx)
		}
	}
}
