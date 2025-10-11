package main

import (
	"fmt"
	"log"
	"os"
	"simpleAI/config"
	"simpleAI/internal/agent"
	"simpleAI/internal/tools"
	"simpleAI/pkg/llm"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	l := tools.NewLogger()
	c := llm.NewClient(cfg.APIKey, *l)
	a := agent.NewAgent(*c, *l)

	if len(os.Args) < 2 {
		fmt.Println("Usage: agent <config>")
		os.Exit(1)
	}
	a.Run(os.Args[1])

}
