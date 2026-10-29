//go:build !windows

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ---------- деревья процессов: MCP и LSP не должны сиротить потомков ----------
//
// Серверы обоих протоколов часто запускаются обёрткой: npx, uvx,
// shell-скрипт из package.json, а настоящий интерпретатор — её потомок.
// stop() раньше бил только прямой процесс: обёртка умирала, потомок
// оставался жить, держал унаследованный stdout-канал (читатель не видел
// EOF — горутина текла, stop() висел в ожидании его смерти) и доживал
// до конца сессии. Теперь сервер сидит в собственной группе процессов,
// и KillTree достаёт всё дерево одним сигналом.

// mcpOrphanPy — фейковый MCP-сервер: при старте плодит фонового потомка
// (sleep 3600), пишет его pid в первый аргумент, отвечает на initialize,
// дальше молчит (построчный JSON-RPC). Потомок практически бессмертен
// в масштабах теста: если тест увидел его живым, это осиротение, а не
// «не дождались естественной смерти».
const mcpOrphanPy = `import json, subprocess, sys

child = subprocess.Popen(["sleep", "3600"])
open(sys.argv[1], "w").write(str(child.pid))
for line in sys.stdin:
    try:
        req = json.loads(line)
    except Exception:
        continue
    if req.get("method") == "initialize":
        print(json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "serverInfo": {"name": "fake", "version": "1"}}}), flush=True)
`

// lspOrphanPy — фейковый LSP-сервер: тот же потомок, дальше
// Content-Length фрейминг. exit игнорируется и EOF по stdin переживается:
// сервер обязан остаться живым до KillTree, иначе он умрёт сам раньше
// остановки, и тест будет проверять не фикс, а собственную вежливость.
const lspOrphanPy = `import json, subprocess, sys, time

child = subprocess.Popen(["sleep", "3600"])
open(sys.argv[1], "w").write(str(child.pid))

def send(obj):
    body = json.dumps(obj).encode()
    sys.stdout.buffer.write(b"Content-Length: %d\r\n\r\n" % len(body) + body)
    sys.stdout.buffer.flush()

while True:
    headers = {}
    while True:
        ln = sys.stdin.buffer.readline()
        if not ln:
            time.sleep(0.05)
            break
        if ln in (b"\r\n", b"\n"):
            break
        k, _, v = ln.decode().partition(":")
        headers[k.strip().lower()] = v.strip()
    n = int(headers.get("content-length", "0"))
    if n == 0:
        continue
    req = json.loads(sys.stdin.buffer.read(n))
    m = req.get("method")
    if m == "initialize":
        send({"jsonrpc": "2.0", "id": req["id"], "result": {"capabilities": {}}})
    elif m == "shutdown":
        send({"jsonrpc": "2.0", "id": req["id"], "result": None})
`

// waitChildGone — ждать смерти потомка по pid из файла. Живой процесс
// отвечает kill(pid, 0) == nil; ESRCH — мёртв и дожат. Ошибка, а не
// t.Fatal: функция зовётся из горутины под watchdog, FailNow из чужой
// горутины запрещён.
func waitChildGone(pidFile string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				if err := syscall.Kill(pid, 0); err != nil {
					return nil
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("потомок пережил stop() — дерево процессов не убито")
}

// killPidFileBestEffort — добить потомка из pid-файла, если тест падает:
// осиротевший sleep 3600 не должен переживать тестовый прогон.
func killPidFileBestEffort(pidFile string) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 недоступен")
	}
}

// TestMCPStopKillsWholeTree — stop() MCP-соединения убивает и потомков
// сервера, а не только процесс-обёртку.
func TestMCPStopKillsWholeTree(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	srvPath := filepath.Join(dir, "orphan_mcp.py")
	pidPath := filepath.Join(dir, "child.pid")
	if err := os.WriteFile(srvPath, []byte(mcpOrphanPy), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := mcpStart(context.Background(), "fake",
		MCPServer{Command: "python3", Args: []string{srvPath, pidPath}}, "test")
	if err != nil {
		t.Fatalf("сервер не завёлся: %v", err)
	}
	t.Cleanup(func() { killPidFileBestEffort(pidPath) })

	done := make(chan error, 1)
	go func() {
		c.stop()
		done <- waitChildGone(pidPath, 5*time.Second)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("stop() не вернулся за 20с — вешает на живом потомке")
	}
}

// TestLSPStopKillsWholeTree — то же для LSP-соединения: вежливый
// shutdown/exit не спасает потомков от KillTree, если сервер сам не вышел.
func TestLSPStopKillsWholeTree(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	srvPath := filepath.Join(dir, "orphan_lsp.py")
	pidPath := filepath.Join(dir, "child.pid")
	if err := os.WriteFile(srvPath, []byte(lspOrphanPy), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := startLSPExec(dir, []string{"python3", srvPath, pidPath}, "go")
	if err != nil {
		t.Fatalf("сервер не завёлся: %v", err)
	}
	t.Cleanup(func() { killPidFileBestEffort(pidPath) })

	done := make(chan error, 1)
	go func() {
		c.stop()
		done <- waitChildGone(pidPath, 5*time.Second)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("stop() не вернулся за 20с — вешает на живом потомке")
	}
}
