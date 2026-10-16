package tools

import (
	"os"
	"strings"
)

// DefaultMCPAddr — MCP-сервер слушает только loopback. Раньше он стоял на ":8080",
// то есть на всех интерфейсах сервера, и без аутентификации отдавал инструменты
// любому, кто достучится до порта. Удалённый доступ — через SSH-туннель; чтобы
// открыть порт осознанно, задай MCP_ADDR явно.
const DefaultMCPAddr = "127.0.0.1:8080"

// MCPAddr возвращает адрес прослушивания MCP: MCP_ADDR или DefaultMCPAddr.
func MCPAddr() string {
	if v := strings.TrimSpace(os.Getenv("MCP_ADDR")); v != "" {
		return v
	}
	return DefaultMCPAddr
}
