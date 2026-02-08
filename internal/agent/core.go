// Package agent содержит код пакета agent и его задачи.
package agent

import (
	"context"
	"fmt"
	"log/slog"

	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
)

type Agent struct {
	LLM core.LLM
	*slog.Logger
	Registry *plugin.Registry
	// cache for session store
}

func NewAgent(c core.LLM, l *slog.Logger) *Agent {
	return NewAgentWithRegistry(c, l, nil)
}

func NewAgentWithRegistry(c core.LLM, l *slog.Logger, registry *plugin.Registry) *Agent {
	return &Agent{
		LLM:      c,
		Logger:   l,
		Registry: registry,
	}
}

func (a *Agent) Run(query string) {
	a.Debug("RunFunc", "len query", len(query))
	r, err := a.LLM.Ask(context.Background(), query)
	if err != nil {
		a.Error("RunFunc", "Err", err)
		return
	}
	fmt.Println(r)
}
