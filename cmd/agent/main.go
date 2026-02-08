// Package main содержит код пакета main и его задачи.
package main

import (
	"bufio"
	"io"
	"log"
	"os"

	"simpleAI/config"
	llmfactory "simpleAI/internal/adapters/llm"
	"simpleAI/internal/agent"
	"simpleAI/internal/tools"
)

func main() {
	// Конфиг — как и раньше, из env/файла
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Err while load the config: ", err)
	}

	l := tools.NewLogger()
	llmClient, err := llmfactory.NewClient(cfg, l)
	if err != nil {
		log.Fatal("Err while init llm client: ", err)
	}
	a := agent.NewAgent(llmClient, l)

	// diff читаем из stdin
	diff, err := readStdin()
	if err != nil {
		log.Fatal("Err while read diff from stdin: ", err)
	}
	if len(diff) == 0 {
		log.Fatal("no diff provided on stdin")
	}

	a.Run(diff)
}

func readStdin() (string, error) {
	// Проверяем, есть ли вообще что-то в stdin (или запустили из терминала без пайпа)
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}

	// Если stdin — терминал, а не пайп/файл — считаем это ошибкой
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", io.EOF
	}

	data, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
