package agent

import (
	"fmt"
	"log/slog"
	"simpleAI/internal/tools"
	"simpleAI/pkg/llm"
	"strings"
)

type Agent struct {
	llm.Client
	slog.Logger
	//cache for session store
}

func NewAgent(c llm.Client, l slog.Logger) *Agent {
	return &Agent{
		Client: c,
		Logger: l,
	}
}

func (a *Agent) Run(query string) {
	fmt.Println("[reason] понял задачу:", query)
	if strings.Contains(query, "файл") {
		tools.ReadFile(query)
	}
	r, err := a.Ask(query)
	if err != nil {
		a.Error("error", err)
	}
	a.Info("ИИ", "ответ", r)
}
