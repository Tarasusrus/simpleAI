// Package telegram содержит код пакета telegram и его задачи.
package telegram

import (
	"context"
	"fmt"
	"strings"
)

type Handler func(ctx context.Context, tctx *Context) error

type Middleware func(next Handler) Handler

type Router struct {
	commands       map[string]Handler
	defaultHandler Handler
	middlewares    []Middleware
}

func NewRouter() *Router {
	return &Router{
		commands: make(map[string]Handler),
	}
}

func (r *Router) Use(mw Middleware) {
	r.middlewares = append(r.middlewares, mw)
}

func (r *Router) HandleCommand(command string, handler Handler) {
	r.commands[command] = handler
}

func (r *Router) HandleDefault(handler Handler) {
	r.defaultHandler = handler
}

func (r *Router) HandleUpdate(ctx context.Context, tctx *Context) error {
	if tctx.Update.ChatID == 0 {
		return nil
	}

	handler := r.defaultHandler
	if cmd, ok := parseCommand(tctx.Update.Text); ok {
		if h, exists := r.commands[cmd]; exists {
			handler = h
		} else {
			handler = r.defaultHandler
		}
	}
	if handler == nil {
		return fmt.Errorf("no handler configured")
	}

	finalHandler := handler
	for i := len(r.middlewares) - 1; i >= 0; i-- {
		finalHandler = r.middlewares[i](finalHandler)
	}
	return finalHandler(ctx, tctx)
}

func parseCommand(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "", false
	}
	cmd := strings.TrimPrefix(parts[0], "/")
	if idx := strings.Index(cmd, "@"); idx >= 0 {
		cmd = cmd[:idx]
	}
	if cmd == "" {
		return "", false
	}
	return cmd, true
}
