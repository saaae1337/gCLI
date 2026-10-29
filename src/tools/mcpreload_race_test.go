package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- регрессия: двойной старт MCP-сервера при параллельном reload ----------
//
// RegisterMCPVersion проверяет «жив ли конн» и запускает сервер двумя
// отдельными шагами. Два параллельных прохода (/mcp reload из UI, пересборка
// реестра) без общего замка оба видели отсутствие конна и оба звали mcpStart:
// в карте оставался один процесс, второй жил сиротой — с горутинами-читателями
// и открытыми трубами, по одному на каждый повтор. Регрессия запускает два
// прохода одновременно против медленного фейк-сервера и требует: процесс
// ровно один.

const (
	envSlowMCP      = "GCLI_TEST_MCP_SLOW"
	envSlowMCPDelay = "GCLI_TEST_MCP_DELAY"
	envSlowMCPPid   = "GCLI_TEST_MCP_PIDFILE"
)

// TestFakeMCPSlowServer — точка входа фейка. Без переменной это обычный тест.
func TestFakeMCPSlowServer(t *testing.T) {
	if os.Getenv(envSlowMCP) == "" {
		t.Skip("этот тест запускается только как фальшивый медленный MCP-сервер")
	}
	serveFakeMCPSlow()
	os.Exit(0)
}

// serveFakeMCPSlow — NDJSON JSON-RPC поверх stdio с задержкой на initialize:
// окно задержки — то самое, в которое второй параллельный проход успевал бы
// запустить свой экземпляр сервера. Своё начало процесса пишет в pidfile.
func serveFakeMCPSlow() {
	if pf := os.Getenv(envSlowMCPPid); pf != "" {
		if f, err := os.OpenFile(pf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
		}
	}
	delay := 1200 * time.Millisecond
	if ms, err := strconv.Atoi(os.Getenv(envSlowMCPDelay)); err == nil && ms > 0 {
		delay = time.Duration(ms) * time.Millisecond
	}
	in := bufio.NewReader(os.Stdin)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			time.Sleep(delay)
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake-slow", "version": "1.0"},
			}})
		case "notifications/initialized", "initialized":
			// нотификация — ответа не ждём
		case "tools/list":
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{
				"tools": []map[string]any{
					{"name": "echo", "description": "эхо", "inputSchema": map[string]any{
						"type": "object", "properties": map[string]any{},
					}},
				},
			}})
		}
	}
}

// TestRegisterMCPConcurrentSingleStart — два параллельных RegisterMCPVersion
// поднимают ровно один процесс сервера и регистрируют его инструменты.
func TestRegisterMCPConcurrentSingleStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	work := t.TempDir()
	t.Setenv(envSlowMCP, "1")
	t.Setenv(envSlowMCPDelay, "1200")
	pidFile := filepath.Join(t.TempDir(), "pids")
	t.Setenv(envSlowMCPPid, pidFile)

	exe, err := os.Executable()
	if err != nil {
		t.Skipf("не удалось узнать путь к тестовому бинарнику: %v", err)
	}
	cfg := map[string]any{"servers": map[string]any{"fake": map[string]any{
		"command":     exe,
		"args":        []string{"-test.run=TestFakeMCPSlowServer"},
		"timeout_sec": 10,
	}}}
	body, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(home, "mcp.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(Env{WorkDir: work})
	defer r.MCPShutdown()

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			_, warns := r.RegisterMCPVersion("test")
			if len(warns) > 0 {
				errs <- fmt.Errorf("предупреждения: %v", warns)
				return
			}
			errs <- nil
		}()
	}
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("RegisterMCPVersion не завершился за 30с — взаимная блокировка сериализации")
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("сервер не запустился (pidfile пуст): %v", err)
	}
	lines := strings.Fields(string(raw))
	uniq := map[string]bool{}
	for _, l := range lines {
		uniq[l] = true
	}
	if len(uniq) != 1 {
		t.Fatalf("запущено %d разных процессов MCP-сервера (pids: %s) — двойной старт, гонка не закрыта",
			len(uniq), strings.Join(lines, ", "))
	}
	if r.Get("mcp__fake__echo") == nil {
		t.Fatal("инструмент mcp__fake__echo не зарегистрирован")
	}
}
