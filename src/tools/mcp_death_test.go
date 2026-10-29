package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Регрессия 0.9.1: смерть MCP-сервера не должна оставлять утечек.

// fakeMCPServerPy — минимальный сервер: отвечает на initialize и
// tools/list, живёт до убийства. Пишется во временный каталог теста.
const fakeMCPServerPy = `import json, sys
for line in sys.stdin:
    try:
        req = json.loads(line)
    except Exception:
        continue
    rid = req.get("id")
    method = req.get("method", "")
    if method == "initialize":
        print(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "fake", "version": "1"}}}), flush=True)
    elif method == "tools/list":
        print(json.dumps({"jsonrpc": "2.0", "id": rid, "result": {"tools": [
            {"name": "echo", "description": "эхо",
             "inputSchema": {"type": "object", "properties": {},
                             "required": []}}]}}), flush=True)
`

// TestMCPConnDeathReaps — после смерти сервера reader-горутина обязана
// вызвать stop(): done закрывается (писатель не виснет навечно), а
// дочерний процесс дожат cmd.Wait() (не остаётся зомби). До фикса reader
// ставил только closed=true — горутина-писатель и зомби утекали до конца
// сессии, по одному на каждый цикл «сервер умер + /mcp reload».
func TestMCPConnDeathReaps(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 недоступен — тест смерти MCP-сервера пропущен")
	}
	dir := t.TempDir()
	home := t.TempDir()
	srvPath := filepath.Join(dir, "fake_mcp.py")
	if err := os.WriteFile(srvPath, []byte(fakeMCPServerPy), 0o644); err != nil {
		t.Fatal(err)
	}
	// GCLI_HOME (ставит overrideHome) и есть глобальный каталог — такой
	// конфиг доверен по определению; проектный требовал бы подтверждения.
	proj := filepath.Join(home, "mcp.json")
	if err := os.MkdirAll(filepath.Dir(proj), 0o755); err != nil {
		t.Fatal(err)
	}
	// filepath.ToSlash: на Windows путь C:\Users\... в JSON — невалидный
	// escape \U, конфиг не парсится, предупреждение роняет тест.
	// Прямой слэш Windows-программы принимают наравне с обратным.
	cfg := `{"servers":{"fake":{"command":"python3","args":["` + filepath.ToSlash(srvPath) + `"]}}}`
	if err := os.WriteFile(proj, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := overrideHome(home)
	defer restore()

	r := New(Env{WorkDir: dir})
	if _, warns := r.RegisterMCPVersion("test"); len(warns) > 0 {
		t.Fatalf("предупреждения: %v", warns)
	}
	r.muMCP.Lock()
	c := r.mcpConns["fake"]
	r.muMCP.Unlock()
	if c == nil {
		t.Fatal("соединение не заведено")
	}

	if err := c.cmd.Process.Kill(); err != nil {
		t.Fatalf("не удалось убить процесс сервера: %v", err)
	}

	// done обязан закрыться: иначе горутина-писатель виснет навечно.
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case <-c.done:
			break loop
		case <-deadline:
			t.Fatal("после смерти сервера done не закрыт — горутина-писатель утекла")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Процесс обязан быть дожат Wait'ом: иначе зомби до конца сессии.
	// Читаем ProcessState только после reaped — закрытие канала даёт
	// happens-before с cmd.Wait() внутри stop(), иначе гонка с ним.
	select {
	case <-c.reaped:
	case <-time.After(3 * time.Second):
		t.Fatal("cmd.Wait не вызван после смерти сервера — процесс остался зомби")
	}
	if c.cmd.ProcessState == nil {
		t.Error("ProcessState пуст после reaped")
	}

	// Живой реестр после смерти: MCPReload обязан убрать мёртвый конн
	// (не зависнуть и не утянуть за собой горутины). Вместо удалённого
	// заводится новый живой — тот же слот, другой указатель.
	r.MCPReload()
	r.muMCP.Lock()
	cur := r.mcpConns["fake"]
	r.muMCP.Unlock()
	if cur == c {
		t.Error("MCPReload оставил в реестре мёртвый конн")
	}
	r.MCPShutdown()
}
