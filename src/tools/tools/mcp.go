package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gcli/core"
)

// ---------- MCP (Model Context Protocol) ----------
//
// Поддержка внешних MCP-серверов — как в Claude Code. Сервер — процесс
// (node, python, npx-пакет), общающийся по stdio в JSON-RPC 2.0: клиент
// отправляет запрос строкой, сервер отвечает строкой. Инструменты сервера
// регистрируются в реестре агента как обычные инструменты с префиксом
// mcp__<сервер>__<инструмент>.
//
// Конфиг — mcp.json в ~/.gcli (глобальный) и ./.gcli (проект, приоритет):
//
//      {
//        "servers": {
//          "filesystem": {
//            "command": "npx",
//            "args": ["-y", "@modelcontextprotocol/server-filesystem", "."],
//            "timeout_sec": 120
//          }
//        }
//      }

// MCPServer — описание MCP-сервера в конфиге.
type MCPServer struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	TimeoutSec int               `json:"timeout_sec,omitempty"` // потолок одного вызова инструмента
	Enabled    *bool             `json:"enabled,omitempty"`
	Workdir    string            `json:"workdir,omitempty"`
}

// MCPConfig — содержимое mcp.json.
type MCPConfig struct {
	Servers map[string]MCPServer `json:"servers"`
}

// mcpToolDef — инструмент, объявленный сервером.
type mcpToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPDirs — пути к конфигам MCP (проектные перекрывают глобальные).
func (r *Registry) MCPDirs() [][2]string {
	return [][2]string{
		{filepath.Join(core.Home(), "mcp.json"), "глобальный"},
		{filepath.Join(r.workDir, ".gcli", "mcp.json"), "проект"},
	}
}

// LoadMCPConfig — прочитать конфиги серверов (проект перекрывает глобальный).
func (r *Registry) LoadMCPConfig() (MCPConfig, []string) {
	var warns []string
	merged := MCPConfig{Servers: map[string]MCPServer{}}
	for _, d := range r.MCPDirs() {
		data, err := os.ReadFile(d[0])
		if err != nil {
			continue
		}
		var cfg MCPConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			warns = append(warns, fmt.Sprintf("mcp.json (%s): %v", d[1], err))
			continue
		}
		for name, srv := range cfg.Servers {
			if strings.TrimSpace(srv.Command) == "" {
				warns = append(warns, fmt.Sprintf("mcp.json (%s): сервер «%s» без command — пропущен", d[1], name))
				continue
			}
			merged.Servers[name] = srv
		}
	}
	return merged, warns
}

// MCPTemplate — шаблон mcp.json.
const MCPTemplate = `{
  "servers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."],
      "timeout_sec": 120
    },
    "fetch": {
      "command": "uvx",
      "args": ["mcp-server-fetch"],
      "timeout_sec": 60
    }
  }
}
`

// ---------- JSON-RPC 2.0 поверх stdio ----------

// mcpConn — живое соединение с MCP-сервером.
type mcpConn struct {
	cmd   *exec.Cmd
	stdin chan string
	// callMu — запросы строго по одному: reader раздаёт ответы по
	// единственному активному каналу pending.
	callMu sync.Mutex
	nextID int
	idMu   sync.Mutex
	// pending — атомарный: reader читает его без общего с call() мьютекса
	// (иначе reader блокирует call, который держит мьютекс до ответа, —
	// рукопожатие падало по таймауту).
	pending atomic.Pointer[chan mcpResponse]
	scanner *bufio.Scanner
	closed  atomic.Bool
}

// mcpResponse — ответ сервера (result или error).
type mcpResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Err    *mcpRemoteError `json:"error,omitempty"`
}

type mcpRemoteError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *mcpRemoteError) Error() string { return e.Message }

// mcpRequest — исходящий запрос/уведомление.
type mcpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

const (
	mcpProtocolVersion = "2024-11-05"
	mcpInitTimeout     = 20 * time.Second
	mcpListTimeout     = 20 * time.Second
)

// mcpStart — запустить процесс сервера и выполнить рукопожатие initialize.
func mcpStart(ctx context.Context, name string, srv MCPServer, version string) (*mcpConn, error) {
	cmd := exec.CommandContext(ctx, srv.Command, srv.Args...)
	cmd.Dir = srv.Workdir
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// MCP-серверы не должны рисовать в наш stderr-лог: прячем его.
	cmd.Stderr = nil

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("не удалось запустить %s: %v", srv.Command, err)
	}

	c := &mcpConn{
		cmd:     cmd,
		stdin:   make(chan string, 8),
		scanner: bufio.NewScanner(stdoutPipe),
	}
	c.scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)

	// Читатель: строки ответов раздаются ожидающим запросам.
	go func() {
		for c.scanner.Scan() {
			line := strings.TrimSpace(c.scanner.Text())
			if line == "" {
				continue
			}
			var resp mcpResponse
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				continue // уведомления и мусор молча пропускаем
			}
			if p := c.pending.Load(); p != nil {
				select {
				case (*p) <- resp:
				default:
				}
			}
		}
		c.closed.Store(true)
	}()

	// Писатель: сериализуем записи в stdin.
	go func() {
		for line := range c.stdin {
			if _, err := stdinPipe.Write([]byte(line + "\n")); err != nil {
				return
			}
		}
		stdinPipe.Close()
	}()

	// initialize → notifications/initialized.
	ctxInit, cancel := context.WithTimeout(ctx, mcpInitTimeout)
	defer cancel()
	if _, err := c.call(ctxInit, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "gcli", "version": version},
	}); err != nil {
		c.stop()
		return nil, fmt.Errorf("MCP «%s»: рукопожатие не удалось: %v", name, err)
	}
	c.notify("notifications/initialized", nil)
	return c, nil
}

// call — один JSON-RPC запрос. Строго по одному (callMu): ответ reader
// отдаёт по единственному активному каналу pending.
func (c *mcpConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	if c.closed.Load() {
		return nil, fmt.Errorf("сервер остановлен")
	}
	c.idMu.Lock()
	c.nextID++
	id := c.nextID
	c.idMu.Unlock()

	req, err := json.Marshal(mcpRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}

	ch := make(chan mcpResponse, 1)
	c.pending.Store(&ch)
	defer c.pending.Store(nil)

	select {
	case c.stdin <- string(req):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case resp := <-ch:
		if resp.ID != id {
			return nil, fmt.Errorf("чужой id ответа: %d", resp.ID)
		}
		if resp.Err != nil {
			return nil, resp.Err
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notify — уведомление без ответа.
func (c *mcpConn) notify(method string, params any) {
	req, err := json.Marshal(mcpRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return
	}
	select {
	case c.stdin <- string(req):
	default:
	}
}

// stop — остановить процесс сервера.
func (c *mcpConn) stop() {
	if c.closed.Swap(true) {
		return
	}
	close(c.stdin)
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
}

// alive — процесс ещё жив?
func (c *mcpConn) alive() bool {
	return !c.closed.Load() && c.cmd.ProcessState == nil
}

// ---------- Регистрация инструментов MCP ----------

// reMCPName — безопасное имя сервера/инструмента в составном имени.
var reMCPName = regexp.MustCompile(`[^a-z0-9_]+`)

func mcpSanitize(s string) string {
	return strings.Trim(reMCPName.ReplaceAllString(strings.ToLower(s), "_"), "_")
}

// MCPToolName — имя инструмента MCP в реестре: mcp__<сервер>__<инструмент>.
func MCPToolName(server, tool string) string {
	return "mcp__" + mcpSanitize(server) + "__" + mcpSanitize(tool)
}

// mcpServerRegistered — инструменты этого сервера уже в реестре?
func (r *Registry) mcpServerRegistered(name string) bool {
	for _, t := range r.tools {
		if t.MCPSrv == name {
			return true
		}
	}
	return false
}

// mcpTrusted — можно ли запускать MCP-сервер.
//
// Глобальный конфиг (~/.gcli/mcp.json) доверен: его писал пользователь.
// Проектный (.gcli/mcp.json) требует согласия, запомненного по отпечатку
// файла. Отпечаток берём по всему файлу, а не по одной записи сервера:
// подтверждая «filesystem», пользователь должен подтвердить и то, что рядом
// в том же файле появится другой сервер.
//
// Порядок обхода обратный (сначала проект), и это не косметика: LoadMCPConfig
// отдаёт приоритет проектному конфигу, значит и проверять доверие надо по
// тому файлу, из которого возьмётся определение. Иначе проектный сервер,
// названный так же, как глобальный, выполнялся бы под крышкой глобального
// доверия.
func (r *Registry) mcpTrusted(name string) bool {
	dirs := r.MCPDirs()
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		data, err := os.ReadFile(d[0])
		if err != nil {
			continue
		}
		var cfg MCPConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		srv, ok := cfg.Servers[name]
		if !ok || strings.TrimSpace(srv.Command) == "" {
			continue
		}
		if d[1] == "глобальный" {
			return true
		}
		// Без хранилища доверия (тесты, неполная инициализация) проектный
		// код не запускаем — причина подтверждения исчезла бы, а риск нет.
		if r.env.Trust == nil {
			return false
		}
		level, _ := r.env.Trust.Check("mcp", name, map[string]string{d[0]: string(data)})
		return level == TrustProject
	}
	return false
}

// RegisterMCP — подключить все серверы из конфигов. Возвращает число
// зарегистрированных инструментов и список предупреждений.
func (r *Registry) RegisterMCP() (int, []string) {
	return r.RegisterMCPVersion(core.Version)
}

// RegisterMCPVersion — то же, с явной версией (для тестов).
func (r *Registry) RegisterMCPVersion(version string) (int, []string) {
	cfg, warns := r.LoadMCPConfig()
	if len(cfg.Servers) == 0 {
		return 0, warns
	}
	if r.mcpConns == nil {
		r.mcpConns = map[string]*mcpConn{}
	}

	// Стабильный порядок серверов.
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	count := 0
	for _, name := range names {
		srv := cfg.Servers[name]
		if srv.Enabled != nil && !*srv.Enabled {
			continue
		}
		// Сервер из проекта без согласия не запускаем: это чужой код, и он
		// стартует ДО первого вопроса пользователя. Подробности — /mcp trust.
		if !r.mcpTrusted(name) {
			warns = append(warns, fmt.Sprintf(
				"MCP «%s»: сервер из проекта, требует подтверждения (/mcp trust %s)", name, name))
			continue
		}
		if r.mcpServerRegistered(name) {
			continue // сервер уже подключён
		}
		conn, err := mcpStart(context.Background(), name, srv, version)
		if err != nil {
			warns = append(warns, err.Error())
			continue
		}
		r.mcpConns[name] = conn

		listCtx, cancel := context.WithTimeout(context.Background(), mcpListTimeout)
		raw, err := conn.call(listCtx, "tools/list", map[string]any{})
		cancel()
		if err != nil {
			warns = append(warns, fmt.Sprintf("MCP «%s»: список инструментов не получен: %v", name, err))
			continue
		}
		var list struct {
			Tools []mcpToolDef `json:"tools"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			warns = append(warns, fmt.Sprintf("MCP «%s»: кривой ответ tools/list: %v", name, err))
			continue
		}
		for _, t := range list.Tools {
			if strings.TrimSpace(t.Name) == "" {
				continue
			}
			schema := strings.TrimSpace(string(t.InputSchema))
			if schema == "" {
				schema = `{"type":"object","properties":{}}`
			}
			desc := t.Description
			if desc == "" {
				desc = "инструмент MCP-сервера " + name
			}
			full := MCPToolName(name, t.Name)
			if r.byName[full] != nil {
				continue
			}
			r.registerMCPTool(name, full, t.Name, desc, schema)
			count++
		}
		if len(list.Tools) == 0 {
			warns = append(warns, fmt.Sprintf("MCP «%s»: сервер не объявил ни одного инструмента", name))
		}
	}
	return count, warns
}

// registerMCPTool — один инструмент MCP-сервера в реестре.
func (r *Registry) registerMCPTool(server, fullName, callName, desc, schema string) {
	t := &Tool{
		Def:      ToolDef{Name: fullName, Description: "[mcp:" + server + "] " + desc, Schema: schema},
		Handler:  r.mcpHandler(server, fullName, callName),
		Category: "mcp",
		MCPSrv:   server,
		MCPCall:  callName,
	}
	r.tools = append(r.tools, t)
	r.byName[fullName] = t
}

// mcpHandler — вызов tools/call на сервере.
func (r *Registry) mcpHandler(server, fullName, callName string) Handler {
	return func(ctx context.Context, m map[string]any) (Result, error) {
		conn := r.mcpConns[server]
		if conn == nil || !conn.alive() {
			return Result{}, fmt.Errorf("MCP-сервер «%s» не подключён — /mcp reload", server)
		}
		// Таймаут: из конфига сервера (по умолчанию 120 с).
		var timeout time.Duration
		if cfg, _ := r.LoadMCPConfig(); cfg.Servers[server].TimeoutSec > 0 {
			timeout = time.Duration(core.Clamp(cfg.Servers[server].TimeoutSec, 5, 600)) * time.Second
		} else {
			timeout = 120 * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		args := m
		if a, ok := m["arguments"].(map[string]any); ok {
			args = a
		}
		raw, err := conn.call(cctx, "tools/call", map[string]any{
			"name":      callName,
			"arguments": args,
		})
		if err != nil {
			return Result{}, fmt.Errorf("MCP «%s»: %v", server, err)
		}
		return mcpParseCallResult(raw, server)
	}
}

// mcpParseCallResult — разобрать результат tools/call: текст + картинки.
func mcpParseCallResult(raw json.RawMessage, server string) (Result, error) {
	var res struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		} `json:"content"`
		IsError bool   `json:"isError"`
		Err     string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return Result{}, fmt.Errorf("MCP «%s»: не удалось разобрать ответ: %v", server, err)
	}
	var b strings.Builder
	var imgs []core.Image
	for _, c := range res.Content {
		switch c.Type {
		case "", "text":
			if c.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(c.Text)
			}
		case "image":
			if len(imgs) < 4 && c.Data != "" {
				mime := c.MimeType
				if mime == "" {
					mime = "image/png"
				}
				imgs = append(imgs, core.Image{MIME: mime, Data: c.Data})
			}
		case "resource":
			b.WriteString("[resource] (содержимое ресурса не поддерживается этим инструментом)\n")
		}
	}
	out := Result{Text: strings.TrimSpace(b.String()), Images: imgs}
	if out.Text == "" && len(imgs) == 0 {
		out.Text = "(пустой результат от MCP-сервера)"
	}
	out.Summary = core.Truncate(core.OneLine(out.Text), 90)
	if res.IsError {
		out.Error = strings.TrimSpace(out.Text)
		out.Text = ""
	}
	return out, nil
}

// MCPToolCount — сколько инструментов MCP подключено к реестру.
func (r *Registry) MCPToolCount() int {
	n := 0
	for _, t := range r.tools {
		if t.MCPSrv != "" {
			n++
		}
	}
	return n
}

// MCPStatus — сводка по MCP-серверам для /mcp и /tools.
func (r *Registry) MCPStatus() (servers []string, tools int) {
	cfg, _ := r.LoadMCPConfig()
	for _, t := range r.tools {
		if t.MCPSrv != "" {
			tools++
		}
	}
	for name, srv := range cfg.Servers {
		state := "вкл"
		if srv.Enabled != nil && !*srv.Enabled {
			state = "выкл"
		}
		// Сервер из проекта без согласия не запускается — говорим об этом
		// прямо, иначе «вкл» в списке выглядит как «работает», а не работает.
		if state == "вкл" && !r.mcpTrusted(name) {
			state = "требует подтверждения"
		}
		live := ""
		if c := r.mcpConns[name]; c != nil && c.alive() {
			live = " · подключён"
		}
		servers = append(servers, fmt.Sprintf("%s (%s%s) — %s %s", name, state, live, srv.Command, strings.Join(srv.Args, " ")))
	}
	sort.Strings(servers)
	return servers, tools
}

// MCPReload — переподключиться к серверам и дозарегистрировать инструменты.
func (r *Registry) MCPReload() (int, []string) {
	r.muMCP.Lock()
	for name, c := range r.mcpConns {
		if !c.alive() {
			delete(r.mcpConns, name)
		}
	}
	r.muMCP.Unlock()
	return r.RegisterMCP()
}

// MCPShutdown — остановить все MCP-процессы (при выходе).
func (r *Registry) MCPShutdown() {
	r.muMCP.Lock()
	conns := r.mcpConns
	r.mcpConns = nil
	r.muMCP.Unlock()
	for _, c := range conns {
		c.stop()
	}
}
