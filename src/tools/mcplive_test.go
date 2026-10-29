package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gcli/core"
)

// ---------- фальшивый MCP-сервер ----------
//
// Как и с language server, сервер поднимается из тестового бинарника:
// настоящий MCP-сервер требует npx и сети, а тест должен быть
// детерминированным.

// envFakeMCP — по этой переменной тест работает сервером.
const envFakeMCP = "GCLI_TEST_MCP_FAKE"

// TestFakeMCPServer — точка входа. Без переменной это обычный тест.
func TestFakeMCPServer(t *testing.T) {
	mode := os.Getenv(envFakeMCP)
	if mode == "" {
		t.Skip("этот тест запускается только как фальшивый MCP-сервер")
	}
	serveFakeMCP(mode)
	os.Exit(0)
}

// serveFakeMCP — NDJSON JSON-RPC поверх stdio.
func serveFakeMCP(mode string) {
	in := bufio.NewReader(os.Stdin)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}
	reply := func(id any, result any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	fail := func(id any, msg string) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": msg}})
	}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			reply(msg.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0"},
			})
		case "notifications/initialized", "initialized":
			// нотификация — ответа не ждём
		case "tools/list":
			reply(msg.ID, map[string]any{"tools": []map[string]any{
				{"name": "echo", "description": "вернуть текст", "inputSchema": map[string]any{
					"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}},
					"required": []string{"text"},
				}},
				{"name": "boom", "description": "всегда падает", "inputSchema": map[string]any{}},
				{"name": "", "description": "без имени — должен быть пропущен", "inputSchema": map[string]any{}},
			}})
		case "tools/call":
			var p struct {
				Name string         `json:"name"`
				Args map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			switch {
			case p.Name == "boom":
				// Ошибка инструмента возвращается как результат с
				// isError, а не как ошибка протокола.
				reply(msg.ID, map[string]any{
					"isError": true,
					"content": []map[string]any{{"type": "text", "text": "сервер упал по-настоящему"}},
				})
			case mode == "silent" && p.Name == "echo":
				// ничего не отвечаем — проверка таймаута
			default:
				text, _ := p.Args["text"].(string)
				reply(msg.ID, map[string]any{
					"content": []map[string]any{{"type": "text", "text": "эхо: " + text}},
				})
			}
		case "ping":
			reply(msg.ID, map[string]any{})
		}
	}
	_ = fail
}

// mcpRegistry — реестр в каталоге с mcp.json, запускающим фальшивый сервер.
func mcpRegistry(t *testing.T, mode string) (*Registry, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	work := t.TempDir()
	t.Setenv(envFakeMCP, mode)

	exe, err := os.Executable()
	if err != nil {
		t.Skipf("не удалось узнать путь к тестовому бинарнику: %v", err)
	}
	cfg := map[string]any{"servers": map[string]any{"fake": map[string]any{
		"command":     exe,
		"args":        []string{"-test.run=TestFakeMCPServer"},
		"timeout_sec": 5,
	}}}
	dir := filepath.Join(home, ".gcli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(home, "mcp.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return New(Env{WorkDir: work}), work
}

// ---------- проверки ----------

// TestMCPToolsRegistered — инструменты сервера попадают в реестр под
// префиксом mcp__<сервер>__ и со схемой, объявленной сервером.
func TestMCPToolsRegistered(t *testing.T) {
	r, _ := mcpRegistry(t, "ok")
	n, warns := r.RegisterMCP()
	defer r.MCPShutdown()
	if n != 2 {
		t.Fatalf("зарегистрировано %d инструментов, ждали 2 (инструмент без имени пропускается): %v", n, warns)
	}
	tool := r.Get("mcp__fake__echo")
	if tool == nil {
		t.Fatal("инструмент mcp__fake__echo не зарегистрирован")
	}
	if tool.Category != "mcp" {
		t.Errorf("категория %q", tool.Category)
	}
	if !strings.Contains(tool.Def.Description, "вернуть текст") {
		t.Errorf("описание сервера потеряно: %q", tool.Def.Description)
	}
	if !strings.Contains(tool.Def.Schema, "\"text\"") {
		t.Errorf("схема сервера потеряна: %q", tool.Def.Schema)
	}
	if r.Get("mcp__fake__boom") == nil {
		t.Error("второй инструмент не зарегистрирован")
	}
}

// TestMCPCallReturnsText — вызов инструмента доходит до сервера и ответ
// возвращается модели текстом.
func TestMCPCallReturnsText(t *testing.T) {
	r, _ := mcpRegistry(t, "ok")
	r.RegisterMCP()
	defer r.MCPShutdown()

	res, err := r.Get("mcp__fake__echo").Handler(context.Background(),
		map[string]any{"text": "привет"})
	if err != nil {
		t.Fatalf("вызов: %v", err)
	}
	if !strings.Contains(res.Text, "эхо: привет") {
		t.Errorf("ответ сервера не дошёл: %q", res.Text)
	}
}

// TestMCPCallToolErrorIsNotFatal — ошибка инструмента возвращается как
// текст с пометкой, а не роняет ход: так MCP-серверы и сообщают об
// ошибках по своей части.
func TestMCPCallToolErrorIsNotFatal(t *testing.T) {
	r, _ := mcpRegistry(t, "ok")
	r.RegisterMCP()
	defer r.MCPShutdown()

	res, err := r.Get("mcp__fake__boom").Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("ошибка инструмента обрушила вызов: %v", err)
	}
	if res.Error == "" && !strings.Contains(res.Text, "упал") {
		t.Errorf("причина ошибки не сообщена: text=%q error=%q", res.Text, res.Error)
	}
}

// TestMCPCallNotConnectedIsHonest — вызов неподключённого сервера даёт
// понятный отказ с подсказкой, а не пустоту.
func TestMCPCallNotConnectedIsHonest(t *testing.T) {
	r, _ := mcpRegistry(t, "ok")
	h := r.mcpHandler("выдуманный", "mcp__выдуманный__x", "x", time.Second)
	_, err := h(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("вызов неподключённого сервера прошёл молча")
	}
	if !strings.Contains(err.Error(), "/mcp reload") {
		t.Errorf("в отказе нет подсказки: %v", err)
	}
}

// TestMCPTimeoutIsHonoured — сервер, который не отвечает, не должен
// висеть вечно: вызов обязан вернуться по таймауту из конфига.
func TestMCPTimeoutIsHonoured(t *testing.T) {
	r, _ := mcpRegistry(t, "silent")
	r.RegisterMCP()
	defer r.MCPShutdown()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := r.Get("mcp__fake__echo").Handler(ctx, map[string]any{"text": "x"})
	if err == nil {
		t.Fatal("молчащий сервер не дал ошибки по таймауту")
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("таймаут из конфига (5 с) не подействовал: ждали %v", time.Since(start))
	}
}

// TestMCPReloadRevives — /mcp reload обязан поднимать упавший сервер
// заново, иначе после сбоя инструменты молча перестают работать до
// перезапуска gcli.
//
// Проверяется не число зарегистрированных инструментов: повторная
// регистрация их пропускает, и это правильно — иначе в реестре
// появятся дубли. Проверяется то, что важно человеку: вызов после
// перезагрузки снова работает.
func TestMCPReloadRevives(t *testing.T) {
	r, _ := mcpRegistry(t, "ok")
	r.RegisterMCP()
	defer r.MCPShutdown()

	res, err := r.Get("mcp__fake__echo").Handler(context.Background(), map[string]any{"text": "до"})
	if err != nil || !strings.Contains(res.Text, "эхо: до") {
		t.Fatalf("до сбоя не работает: %v %q", err, res.Text)
	}

	// Рвём соединение, как это сделал бы упавший сервер.
	r.muMCP.Lock()
	conn := r.mcpConns["fake"]
	r.muMCP.Unlock()
	if conn == nil {
		t.Fatal("соединение не создано")
	}
	_ = conn.cmd.Process.Kill()
	// Wait только через stop(): параллельный cmd.Wait() из другой
	// горутины гонял бы за внутренние поля exec.Cmd (ловил -race).
	conn.stop()

	if _, err := r.Get("mcp__fake__echo").Handler(context.Background(),
		map[string]any{"text": "x"}); err == nil {
		t.Fatal("после убийства сервера вызов проходит — соединение считается живым")
	}

	r.MCPReload()
	r.RegisterMCP()

	res, err = r.Get("mcp__fake__echo").Handler(context.Background(), map[string]any{"text": "после"})
	if err != nil {
		t.Fatalf("после reload вызов не восстановился: %v", err)
	}
	if !strings.Contains(res.Text, "эхо: после") {
		t.Errorf("ответ после reload не тот: %q", res.Text)
	}
}

// TestMCPBrokenServerDoesNotBreakRegistry — сервер, который не
// запустился, обязан дать предупреждение и не помешать остальным
// инструментам: агент продолжает работать без него.
func TestMCPBrokenServerDoesNotBreakRegistry(t *testing.T) {
	r, _ := mcpRegistry(t, "ok")
	// Добавляем заведомо неработающий сервер в проектный конфиг.
	dir := filepath.Join(r.workDir, ".gcli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"servers":{"сломанный":{"command":"заведомо-нет-такой-программы"}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	_, warns := r.RegisterMCP()
	defer r.MCPShutdown()
	if len(warns) == 0 {
		t.Error("неработающий сервер не дал предупреждения")
	}
	if !strings.Contains(strings.Join(warns, "\n"), "сломанный") {
		t.Errorf("в предупреждении нет имени сервера: %v", warns)
	}
	if r.Count() == 0 {
		t.Error("сломанный сервер унёс с собой весь реестр")
	}
}

// TestMCPConfigMergesProjectOverGlobal — проектный конфиг перекрывает
// глобальный по имени сервера: иначе чужой репозиторий подменял бы
// доверенный сервер молча.
func TestMCPConfigMergesProjectOverGlobal(t *testing.T) {
	r, work := mcpRegistry(t, "ok")
	global := filepath.Join(coreHome(t), "mcp.json")
	if err := os.WriteFile(global, []byte(`{"servers":{"общий":{"command":"из-глобального"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Join(work, ".gcli")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "mcp.json"),
		[]byte(`{"servers":{"общий":{"command":"из-проекта"},"свой":{"command":"только-проект"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warns := r.LoadMCPConfig()
	if len(warns) != 0 {
		t.Fatalf("предупреждения при чтении: %v", warns)
	}
	if got := cfg.Servers["общий"].Command; got != "из-проекта" {
		t.Errorf("проектный конфиг не перекрыл глобальный: %q", got)
	}
	if cfg.Servers["свой"].Command != "только-проект" {
		t.Error("проектный сервер потерян")
	}
}

// TestMCPConfigBrokenIsReported — битый конфиг обязан быть назван, а не
// молча проигнорирован: иначе инструменты просто исчезают.
func TestMCPConfigBrokenIsReported(t *testing.T) {
	r, _ := mcpRegistry(t, "ok")
	if err := os.WriteFile(filepath.Join(coreHome(t), "mcp.json"), []byte("{бито"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, warns := r.LoadMCPConfig()
	if len(warns) == 0 {
		t.Error("битый конфиг не дал предупреждения")
	}
	if !strings.Contains(warns[0], "mcp.json") {
		t.Errorf("в предупреждении нет имени файла: %v", warns)
	}
}

// TestMCPShutdownDoesNotPanic — выход при живых серверах обязан быть
// тихим, иначе gcli падает на выходе вместо того, чтобы закрыться.
func TestMCPShutdownDoesNotPanic(t *testing.T) {
	r, _ := mcpRegistry(t, "ok")
	r.RegisterMCP()
	r.MCPShutdown()
	r.MCPShutdown()
}

// coreHome — каталог данных, подменённый через GCLI_HOME.
func coreHome(t *testing.T) string {
	t.Helper()
	return core.Home()
}
