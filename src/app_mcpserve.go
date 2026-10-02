package main

// MCP-сервер: gcli как инструмент для других агентов.
//
// Зачем: сегодня gcli — MCP-клиент (tools/mcp.go), и это одна дорога.
// Обратная дорога делает gcli частью чужих систем: Claude Desktop, Cursor,
// любой MCP-хост может спросить у gcli «что делает миссия» или послать
// ему разовый промпт — не разбираясь в его REPL и флагах. Расширение
// экосистемы наружу при нулевых затратах: протокол тот же, что умеет
// клиент, но с другой стороны стола.
//
// Формат — NDJSON JSON-RPC 2.0 по stdio, как у MCP. Всё, что доступно
// наружу, — три инструмента: разовый промпт (изолированный дочерний
// процесс собственного бинарника), статус миссии и отчёт миссии.
// Никакого REPL и никаких потоков: MCP-хост ждёт ответ tools/call.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"gcli/core"
)

type mcpSrvRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpSrvResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpSrvError    `json:"error,omitempty"`
}

type mcpSrvError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Коды ошибок JSON-RPC.
const (
	mcpSrvParse     = -32700
	mcpSrvMethod    = -32601
	mcpSrvParams    = -32602
	mcpSrvInternal  = -32603
	mcpSrvToolTimeo = 10 * time.Minute
)

// mcpSrvTool — описание инструмента для tools/list.
type mcpSrvTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema struct {
		Type       string                `json:"type"`
		Properties map[string]mcpSrvProp `json:"properties"`
		Required   []string              `json:"required,omitempty"`
	} `json:"inputSchema"`
}

type mcpSrvProp struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// mcpSrvTools — то, что gcli показывает MCP-хосту.
func mcpSrvTools() []mcpSrvTool {
	mk := func(name, desc string, props map[string]mcpSrvProp, req []string) mcpSrvTool {
		var t mcpSrvTool
		t.Name, t.Description = name, desc
		t.InputSchema.Type = "object"
		t.InputSchema.Properties = props
		t.InputSchema.Required = req
		return t
	}
	return []mcpSrvTool{
		mk("gcli_prompt",
			"Разовый запрос к gcli — терминальному ИИ-агенту. Агент видит проект в workdir, может читать файлы, писать код, запускать команды и проверять результат. Блокирует до ответа (потолок — timeout_min минут).",
			map[string]mcpSrvProp{
				"prompt":      {"string", "задача для агента одной строкой"},
				"workdir":     {"string", "каталог проекта (по умолчанию — каталог запуска gcli -mcp-serve)"},
				"timeout_min": {"integer", "потолок времени на задачу, минут (1..10, по умолчанию 5)"},
			},
			[]string{"prompt"}),
		mk("gcli_mission_status",
			"Состояние автономного прогона gcli в указанном каталоге: цель, режим, счётчики, остановки. Читает .gcli/mission.json и mission_state.json — быстро и без запуска агента.",
			map[string]mcpSrvProp{
				"workdir": {"string", "каталог проекта (по умолчанию — текущий)"},
			},
			nil),
		mk("gcli_mission_report",
			"Markdown-отчёт по прогону gcli: цель, режим, итоги, хроника из журнала. Строится на лету из .gcli/mission_journal.log.",
			map[string]mcpSrvProp{
				"workdir": {"string", "каталог проекта (по умолчанию — текущий)"},
			},
			nil),
	}
}

// runMCPServer — цикл stdio: строка запроса → строка ответа.
// Завершение — EOF на stdin или "exit"-нотификация.
func runMCPServer() {
	runMCPServerStdio(os.Stdin, os.Stdout)
}

// runMCPServerStdio — рабочая часть с явными потоками: без подмены
// глобального os.Stdin тесты не гоняются, а при подмене есть гонка
// с восстановлением globals. Явные параметры — честнее.
func runMCPServerStdio(in io.Reader, out io.Writer) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	outW := bufio.NewWriter(out)

	write := func(v any) {
		data, err := json.Marshal(v)
		if err != nil {
			return
		}
		outW.Write(data)
		outW.WriteByte('\n')
		outW.Flush()
	}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req mcpSrvRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			write(mcpSrvResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &mcpSrvError{Code: mcpSrvParse, Message: "битый JSON: " + err.Error()}})
			continue
		}
		if req.Method == "exit" {
			return
		}
		if len(req.ID) == 0 {
			// Нотификации не отвечают; initialized и прочие — игнор.
			continue
		}
		write(handleMCPCall(req))
	}
}

// handleMCPCall — маршрутизация методов сервера.
func handleMCPCall(req mcpSrvRequest) mcpSrvResponse {
	resp := mcpSrvResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		initRes, _ := json.Marshal(map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gcli", "version": core.Version},
		})
		resp.Result = initRes
	case "ping":
		resp.Result = json.RawMessage("{}")
	case "tools/list":
		res, _ := json.Marshal(map[string]any{"tools": mcpSrvTools()})
		resp.Result = res
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &mcpSrvError{Code: mcpSrvParams, Message: "битые params: " + err.Error()}
			return resp
		}
		text, err := mcpSrvToolCall(params.Name, params.Arguments)
		if err != nil {
			// Ошибка инструмента — по MCP это content с isError, а не
			// RPC-ошибка: хост показывает текст, а не падает.
			res, _ := json.Marshal(map[string]any{
				"content": []map[string]string{{"type": "text", "text": err.Error()}},
				"isError": true,
			})
			resp.Result = res
			return resp
		}
		res, _ := json.Marshal(map[string]any{
			"content": []map[string]string{{"type": "text", "text": text}},
		})
		resp.Result = res
	default:
		resp.Error = &mcpSrvError{Code: mcpSrvMethod, Message: "нет такого метода: " + req.Method}
	}
	return resp
}

// mcpSrvToolCall — исполнение одного инструмента.
func mcpSrvToolCall(name string, args json.RawMessage) (string, error) {
	var m map[string]any
	_ = json.Unmarshal(args, &m) // пустые аргументы допустимы
	get := func(k string) string {
		if v, ok := m[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	switch name {
	case "gcli_prompt":
		prompt := get("prompt")
		if prompt == "" {
			return "", fmt.Errorf("укажи prompt")
		}
		workdir := get("workdir")
		if workdir == "" {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			workdir = wd
		}
		minutes := 5
		if v, ok := m["timeout_min"].(float64); ok {
			minutes = int(v)
		}
		if minutes < 1 {
			minutes = 1
		}
		if minutes > 10 {
			minutes = 10
		}
		return mcpSrvPrompt(workdir, prompt, time.Duration(minutes)*time.Minute)
	case "gcli_mission_status":
		workdir := get("workdir")
		if workdir == "" {
			wd, _ := os.Getwd()
			workdir = wd
		}
		return mcpSrvMissionStatus(workdir)
	case "gcli_mission_report":
		workdir := get("workdir")
		if workdir == "" {
			wd, _ := os.Getwd()
			workdir = wd
		}
		recs, tailOK, err := core.ReadJournal(core.JournalPath(workdir))
		if err != nil {
			recs, tailOK = nil, true
		}
		mission, _, _ := core.LoadMission(core.MissionPath(workdir))
		st, hasState, _ := core.LoadMissionState(core.MissionStatePath(workdir))
		return buildMissionReport(missionReportData{
			Mission: mission, State: st, HasState: hasState, Records: recs, TailOK: tailOK,
		}), nil
	default:
		return "", fmt.Errorf("нет такого инструмента: %s", name)
	}
}

// mcpSrvPrompt — разовый ход агента в дочернем процессе собственного
// бинарника. Изоляция важна: MCP-сервер живёт в stdio хоста, и любой
// вывод/паника агентского цикла внутри этого процесса сломали бы протокол.
func mcpSrvPrompt(workdir, prompt string, timeout time.Duration) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("не найти собственный бинарник: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-p", "-json", prompt)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("агент не уложился в %s; вывод: %s", timeout, core.Truncate(msg, 2000))
		}
		return "", fmt.Errorf("ход не удался: %s", core.Truncate(msg, 2000))
	}
	if len(out) == 0 {
		return "", fmt.Errorf("агент вернул пустой ответ")
	}
	return string(out), nil
}

// mcpSrvMissionStatus — сводка состояния прогона с диска.
func mcpSrvMissionStatus(workdir string) (string, error) {
	if fi, err := os.Stat(workdir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("каталог недоступен: %s", workdir)
	}
	mission, _, err := core.LoadMission(core.MissionPath(workdir))
	if err != nil || (mission.Objective == "" && mission.Mode == core.MissionNormal) {
		return "прогона нет: .gcli/mission.json пуст или отсутствует", nil
	}
	st, hasState, _ := core.LoadMissionState(core.MissionStatePath(workdir))
	var b strings.Builder
	fmt.Fprintf(&b, "цель: %s\nрежим: %s\n", core.OneLine(mission.Objective), mission.Mode)
	fmt.Fprintf(&b, "сводка: %s\n", mission.Summary())
	if hasState {
		fmt.Fprintf(&b, "состояние: итераций %d, вызовов %d, токенов %d, остановка: %s\n",
			st.Iters, st.ToolCalls, st.Tokens, st.StopReason)
	} else {
		b.WriteString("состояние: ещё не сохранялось\n")
	}
	return b.String(), nil
}

// mcpSrvServeSelf — запуск собственного бинарника как MCP-сервера
// (для проверки: клиент в этом же процессе).
func mcpSrvSelfCheck(exe string) error {
	cmd := exec.Command(exe, "-mcp-serve")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = stdin.Close()
		_, _ = cmd.Process.Wait()
	}()
	// initialize → tools/list → выход.
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	} {
		if _, err := fmt.Fprintln(stdin, line); err != nil {
			return err
		}
	}
	r := bufio.NewScanner(stdout)
	for r.Scan() {
		var resp struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(r.Bytes(), &resp) != nil {
			continue
		}
		if string(resp.ID) == "2" && !strings.Contains(string(resp.Result), "gcli_prompt") {
			return fmt.Errorf("tools/list не содержит gcli_prompt")
		}
	}
	return cmd.Wait()
}
