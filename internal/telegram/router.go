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
	if tctx.Update.Message == nil {
		return nil
	}

	handler := r.defaultHandler
	if tctx.Update.Message.IsCommand() {
		cmd := strings.TrimPrefix(tctx.Update.Message.Command(), "/")
		if h, ok := r.commands[cmd]; ok {
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
