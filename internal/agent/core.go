package agent

import (
	"fmt"
	"log/slog"
	"simpleAI/pkg/llm"
)

type Agent struct {
	llm.Client
	*slog.Logger
	// cache for session store
}

func NewAgent(c llm.Client, l *slog.Logger) *Agent {
	return &Agent{
		Client: c,
		Logger: l,
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
