package agent

import (
	"fmt"
	"simpleAI/internal/tools"
	"strings"
)

func Run(query string) {
	fmt.Println("[reason] понял задачу:", query)
	if strings.Contains(query, "файл") {
		tools.ReadFile(query)
	}
}
