package main

import (
	"fmt"
	"os"
	"simpleAI/internal/agent"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: agent <config>")
		os.Exit(1)
	}
	agent.Run(os.Args[1])

}
