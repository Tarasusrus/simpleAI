package tools

import "testing"

func TestMCPAddr(t *testing.T) {
	t.Setenv("MCP_ADDR", "")
	if got := MCPAddr(); got != "127.0.0.1:8080" {
		t.Errorf("по умолчанию %q, ожидался loopback", got)
	}
	t.Setenv("MCP_ADDR", " 0.0.0.0:9000 ")
	if got := MCPAddr(); got != "0.0.0.0:9000" {
		t.Errorf("MCP_ADDR не учтён: %q", got)
	}
}
