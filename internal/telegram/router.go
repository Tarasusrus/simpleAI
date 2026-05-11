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
	commands         map[string]Handler
	callbackPrefixes []callbackRoute
	defaultHandler   Handler
	middlewares      []Middleware
}

type callbackRoute struct {
	prefix  string
	handler Handler
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

// HandleCallback регистрирует handler для callback_data с заданным префиксом.
func (r *Router) HandleCallback(prefix string, handler Handler) {
	r.callbackPrefixes = append(r.callbackPrefixes, callbackRoute{prefix: prefix, handler: handler})
}

func (r *Router) HandleUpdate(ctx context.Context, tctx *Context) error {
	if tctx.Update.ChatID == 0 {
		return nil
	}

	var handler Handler

	if tctx.Update.IsCallback {
		for _, route := range r.callbackPrefixes {
			if strings.HasPrefix(tctx.Update.CallbackData, route.prefix) {
				handler = route.handler
				break
			}
		}
	} else {
		handler = r.defaultHandler
		if cmd, ok := parseCommand(tctx.Update.Text); ok {
			if h, exists := r.commands[cmd]; exists {
				handler = h
			}
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
