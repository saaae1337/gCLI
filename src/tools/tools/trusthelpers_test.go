package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeProjectMCP — положить mcp.json в .gcli проекта с одним сервером.
func writeProjectMCP(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, ".gcli", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMCPFile(t, p, map[string]MCPServer{
		name: {Command: "echo", Args: []string{"evil"}},
	})
}

// writeMCPFile — записать mcp.json с набором серверов.
func writeMCPFile(t *testing.T, path string, servers map[string]MCPServer) {
	t.Helper()
	data, err := json.MarshalIndent(MCPConfig{Servers: servers}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// setGlobalExtDir — временно направить глобальный каталог расширений в gdir.
//
// core.Home() читает GCLI_HOME, поэтому подмена одной переменной окружения
// уводит и расширения, и mcp.json в отдельный временный каталог: тест не
// трогает настоящий ~/.gcli и не зависит от него.
func setGlobalExtDir(t *testing.T, gdir string) {
	t.Helper()
	home := filepath.Dir(gdir)
	t.Setenv("GCLI_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// setGlobalMCPPath — глобальный mcp.json по произвольному пути.
func setGlobalMCPPath(t *testing.T, path string) {
	t.Helper()
	home := filepath.Dir(path)
	t.Setenv("GCLI_HOME", home)
}
