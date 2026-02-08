package agent

import (
	"fmt"
	"log/slog"
	"simpleAI/internal/plugin"
	"simpleAI/pkg/llm"
)

type Agent struct {
	llm.Client
	*slog.Logger
	Registry *plugin.Registry
	// cache for session store
}

func NewAgent(c llm.Client, l *slog.Logger) *Agent {
	return NewAgentWithRegistry(c, l, nil)
}

func NewAgentWithRegistry(c llm.Client, l *slog.Logger, registry *plugin.Registry) *Agent {
	return &Agent{
		Client:   c,
		Logger:   l,
		Registry: registry,
	}
}

func (a *Agent) Run(query string) {
	a.Debug("RunFunc", "len query", len(query))
	r, err := a.Ask(query)
	if err != nil {
		a.Error("RunFunc", "Err", err)
		return
	}
	fmt.Println(r)
}
