package main

import (
	"log"
	"os"
	"simpleAI/config"
	"simpleAI/internal/agent"
	"simpleAI/internal/tools"
	"simpleAI/pkg/llm"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: agent <config>")
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Err while load the config", err)
	}
	l := tools.NewLogger()
	c := llm.NewClient(cfg.APIKey, l, cfg)
	a := agent.NewAgent(c, l)

	a.Run(os.Args[1])

}
