package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gcli/core"
)

// TestMCPIntegration — полный цикл против реального подпроцесса:
// initialize → tools/list → tools/call. Сервер — python-скрипт; если
// python3 нет в системе, тест пропускается.
func TestMCPIntegration(t *testing.T) {
	server := os.Getenv("GCLI_TEST_MCP_SERVER")
	if server == "" {
		if _, err := exec.LookPath("python3"); err != nil {
			t.Skip("python3 недоступен — интеграционный тест MCP пропущен")
		}
		server = filepath.Join("..", "..", "scripts", "fake_mcp_server.py")
		if _, err := os.Stat(server); err != nil {
			server = "/home/z/my-project/scripts/fake_mcp_server.py"
			if _, err := os.Stat(server); err != nil {
				t.Skip("нет фейкового MCP-сервера — тест пропущен")
			}
		}
	}

	dir := t.TempDir()
	home := t.TempDir()
	proj := filepath.Join(dir, ".gcli", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(proj), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"servers":{"fake":{"command":"python3","args":["` + server + `"],"timeout_sec":30}}}`
	if err := os.WriteFile(proj, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := overrideHome(home)
	defer restore()

	r := New(Env{WorkDir: dir})
	n, warns := r.RegisterMCPVersion("test")
	if len(warns) > 0 {
		t.Fatalf("предупреждения: %v", warns)
	}
	if n != 2 {
		t.Fatalf("инструментов: %d, ожидалось 2", n)
	}

	// Имена mcp__fake__echo и mcp__fake__add зарегистрированы.
	tool := r.Get(MCPToolName("fake", "add"))
	if tool == nil {
		t.Fatal("инструмент add не зарегистрирован")
	}
	if tool.Category != "mcp" || tool.MCPSrv != "fake" || tool.MCPCall != "add" {
		t.Errorf("метаданные инструмента: %+v", tool)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := tool.Handler(ctx, map[string]any{"a": 2.0, "b": 40.0})
	if err != nil {
		t.Fatalf("вызов add: %v", err)
	}
	if res.Text != "сумма: 42" {
		t.Errorf("результат add: %q", res.Text)
	}

	// echo + isError=false.
	toolEcho := r.Get(MCPToolName("fake", "echo"))
	res2, err := toolEcho.Handler(ctx, map[string]any{"text": "привет"})
	if err != nil {
		t.Fatalf("вызов echo: %v", err)
	}
	if res2.Text != "привет" || res2.Summary == "" {
		t.Errorf("результат echo: %+v", res2)
	}

	// Субагенты не должны видеть MCP-инструменты.
	base := r.Base()
	if base.Get(MCPToolName("fake", "add")) != nil {
		t.Error("MCP-инструменты утекли в реестр субагентов")
	}

	// Очистка.
	r.MCPShutdown()
	if r.MCPToolCount() != 2 {
		t.Errorf("после shutdown число инструментов изменилось: %d", r.MCPToolCount())
	}
	_ = core.Version
}
