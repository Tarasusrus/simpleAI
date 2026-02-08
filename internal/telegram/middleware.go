// Package telegram реализует прикладной слой Telegram-бота: роутер, middleware, контекст и обработку вложений.
// Пакет также сохраняет raw-ingest payload для дальнейшей обработки; точка входа Router.HandleUpdate.
package telegram

import (
	"context"
	"fmt"
	"sync"
	"time"
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
				if err := tctx.Reply(fmt.Sprintf("Этот чат (%d) не разрешен.", chatID)); err != nil {
					if tctx.Logger != nil {
						tctx.Logger.Error("telegram reply failed", "err", err)
					}
				}
				return nil
			}
			return next(ctx, tctx)
		}
	}
}

func LogUpdates() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, tctx *Context) error {
			if tctx.Logger != nil && tctx.Update.ChatID != 0 {
				tctx.Logger.Info("telegram update",
					"request_id", tctx.RequestID,
					"chat_id", tctx.Update.ChatID,
					"from", tctx.Update.UserName,
					"command", commandForLog(tctx.Update.Text),
				)
			}
			return next(ctx, tctx)
		}
	}
}

func LogDuration() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, tctx *Context) error {
			start := time.Now()
			err := next(ctx, tctx)
			if tctx.Logger != nil && tctx.Update.ChatID != 0 {
				tctx.Logger.Info("telegram update processed",
					"request_id", tctx.RequestID,
					"chat_id", tctx.Update.ChatID,
					"duration_ms", time.Since(start).Milliseconds(),
					"error", err,
				)
			}
			return err
		}
	}
}

func commandForLog(text string) string {
	cmd, ok := parseCommand(text)
	if !ok {
		return ""
	}
	return "/" + cmd
}

func RateLimit(minInterval time.Duration) Middleware {
	if minInterval <= 0 {
		return func(next Handler) Handler {
			return func(ctx context.Context, tctx *Context) error {
				return next(ctx, tctx)
			}
		}
	}

	var (
		mu       sync.Mutex
		lastSeen = map[int64]time.Time{}
	)

	return func(next Handler) Handler {
		return func(ctx context.Context, tctx *Context) error {
			chatID, err := tctx.ChatID()
			if err != nil {
				return err
			}
			now := time.Now()
			mu.Lock()
			prev := lastSeen[chatID]
			if !prev.IsZero() && now.Sub(prev) < minInterval {
				mu.Unlock()
				if err := tctx.Reply("Слишком часто. Попробуй чуть позже."); err != nil {
					if tctx.Logger != nil {
						tctx.Logger.Error("telegram reply failed", "err", err)
					}
				}
				return nil
			}
			lastSeen[chatID] = now
			mu.Unlock()
			return next(ctx, tctx)
		}
	}
}

func DeduplicateUpdates(maxEntries int) Middleware {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	var (
		mu    sync.Mutex
		order []int
		seen  = map[int]struct{}{}
	)

	return func(next Handler) Handler {
		return func(ctx context.Context, tctx *Context) error {
			updateID := tctx.Update.ID
			mu.Lock()
			if _, ok := seen[updateID]; ok {
				mu.Unlock()
				return nil
			}
			seen[updateID] = struct{}{}
			order = append(order, updateID)
			if len(order) > maxEntries {
				old := order[0]
				order = order[1:]
				delete(seen, old)
			}
			mu.Unlock()
			return next(ctx, tctx)
		}
	}
}
