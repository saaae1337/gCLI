package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gcli/core"
)

// ExtTool — один инструмент расширения: shell-команда или HTTP-запрос.
type ExtTool struct {
	Name       string            `json:"name"`
	Desc       string            `json:"description"`
	Command    string            `json:"command,omitempty"`
	URL        string            `json:"url,omitempty"`
	Method     string            `json:"method,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	TimeoutSec int               `json:"timeout_sec,omitempty"`
	NoConfirm  bool              `json:"no_confirm,omitempty"`
	Workdir    string            `json:"workdir,omitempty"`
	ReadOnly   bool              `json:"read_only,omitempty"`
}

// Extension — манифест расширения.
type Extension struct {
	Name  string    `json:"name"`
	Desc  string    `json:"description"`
	Tools []ExtTool `json:"tools"`
}

// ExtDirs — каталоги расширений (проектные имеют приоритет).
func (r *Registry) ExtDirs() [][2]string {
	return [][2]string{
		{filepath.Join(r.workDir, ".gcli", "extensions"), "проект"},
		{filepath.Join(core.Home(), "extensions"), "глобальный"},
	}
}

// LoadExtensions — прочитать манифесты расширений.
func (r *Registry) LoadExtensions() []Extension {
	var out []Extension
	seen := map[string]bool{}
	for _, d := range r.ExtDirs() {
		ents, err := os.ReadDir(d[0])
		if err != nil {
			continue
		}
		var files []string
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				files = append(files, filepath.Join(d[0], e.Name()))
			}
		}
		sort.Strings(files)
		for _, f := range files {
			ext, err := parseExtension(f)
			if err != nil || ext.Name == "" || seen[ext.Name] {
				continue
			}
			seen[ext.Name] = true
			out = append(out, ext)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parseExtension(path string) (Extension, error) {
	var ext Extension
	data, err := os.ReadFile(path)
	if err != nil {
		return ext, err
	}
	if err := json.Unmarshal(data, &ext); err != nil {
		return ext, err
	}
	return ext, nil
}

const schemaExt = `{"type":"object","properties":{"input":{"type":"string","description":"Входные данные/аргументы. Подставляются вместо {input} в команду или запрос; также передаются на stdin."}},"required":[]}`

// extToolCount — число подключённых инструментов расширений.
var extToolCount int

// LoadExtensionTools — зарегистрировать инструменты из расширений.
func (r *Registry) LoadExtensionTools() (int, []string) {
	var warns []string
	count := 0
	for _, ext := range r.LoadExtensions() {
		for _, t := range ext.Tools {
			if !reExtName.MatchString(t.Name) {
				warns = append(warns, fmt.Sprintf("расширение %s: плохое имя инструмента «%s» (нужны a-z, 0-9, _, 3–31 символ)", ext.Name, t.Name))
				continue
			}
			desc := t.Desc
			if desc == "" {
				desc = "инструмент расширения " + ext.Name
			}
			switch {
			case t.Command != "":
				name, w := r.registerExt(ext.Name, t.Name, desc, schemaExt, "ext", r.extCmdHandler(ext, t))
				warns = append(warns, w...)
				_ = name
				count++
			case t.URL != "":
				name, w := r.registerExt(ext.Name, t.Name, desc, schemaExt, "ext", r.extHTTPHandler(ext, t))
				warns = append(warns, w...)
				_ = name
				count++
			default:
				warns = append(warns, fmt.Sprintf("расширение %s: у «%s» нет command и url — пропущен", ext.Name, t.Name))
			}
		}
	}
	extToolCount = count
	return count, warns
}

// ExtToolCount — количество инструментов расширений.
func ExtToolCount() int { return extToolCount }

// extCmdHandler — shell-инструмент расширения.
func (r *Registry) extCmdHandler(ext Extension, t ExtTool) Handler {
	return func(ctx context.Context, m map[string]any) (Result, error) {
		input := ArgStr(m, "input")
		cmdStr := strings.ReplaceAll(t.Command, "{input}", input)
		if strings.TrimSpace(cmdStr) == "" {
			return Result{}, fmt.Errorf("пустая команда")
		}
		if t.ReadOnly && r.env.ReadOnly {
			// В режиме только чтения расширение-запись всё равно пропускаем.
			return Result{Error: "режим «только чтение»"}, nil
		}
		if !t.NoConfirm && r.env.Confirm != nil {
			ok := r.env.Confirm(ConfirmReq{
				Kind:   ConfirmExec,
				Detail: cmdStr,
				Reason: "расширение " + ext.Name,
			})
			if !ok {
				return Result{Text: "Команда отклонена пользователем", Summary: "отклонено"}, nil
			}
		}
		timeout := core.Clamp(t.TimeoutSec, 5, 600)
		if timeout == 0 {
			timeout = 60
		}
		workdir := r.workDir
		if t.Workdir != "" {
			workdir = r.resolvePath(t.Workdir)
		}

		cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		shell, sargs := ShellCommand(cmdStr)
		cmdEx := exec.CommandContext(cctx, shell, sargs...)
		cmdEx.Dir = workdir
		if input != "" {
			cmdEx.Stdin = strings.NewReader(input + "\n")
		}
		outBytes, err := cmdEx.CombinedOutput()
		out := string(outBytes)
		lines := len(core.SplitLines(out))
		out = core.TruncateUTF8(out, 8000, 8000)
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				out += fmt.Sprintf("\n[код выхода: %d]", ee.ExitCode())
			} else if cctx.Err() == context.DeadlineExceeded {
				return Result{
					Text:    fmt.Sprintf("Таймаут %ds. Частичный вывод:\n%s", timeout, out),
					Summary: "таймаут",
				}, nil
			} else {
				return Result{}, fmt.Errorf("%v", err)
			}
		}
		if strings.TrimSpace(out) == "" {
			out = "[нет вывода]"
		}
		return Result{Text: out, Summary: fmt.Sprintf("%s: %d строк", ext.Name, lines)}, nil
	}
}

// extHTTPHandler — HTTP-инструмент расширения.
func (r *Registry) extHTTPHandler(ext Extension, t ExtTool) Handler {
	return func(ctx context.Context, m map[string]any) (Result, error) {
		input := ArgStr(m, "input")
		method := strings.ToUpper(t.Method)
		if method == "" {
			if t.Body != "" {
				method = http.MethodPost
			} else {
				method = http.MethodGet
			}
		}
		u := strings.ReplaceAll(t.URL, "{input}", url.QueryEscape(input))
		var rdr io.Reader
		body := strings.ReplaceAll(t.Body, "{input}", input)
		if body != "" {
			rdr = strings.NewReader(body)
		}
		timeout := core.Clamp(t.TimeoutSec, 5, 300)
		if timeout == 0 {
			timeout = 30
		}
		hctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(hctx, method, u, rdr)
		if err != nil {
			return Result{}, err
		}
		for k, v := range t.Headers {
			req.Header.Set(k, v)
		}
		if body != "" && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", r.env.HTTPClient.userAgent())
		}

		resp, err := r.env.HTTPClient.Client.Do(req)
		if err != nil {
			return Result{}, fmt.Errorf("запрос не удался: %v", err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		s := string(data)
		if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "html") {
			s = HTMLToText(s)
		}
		if len([]rune(s)) > 12000 {
			s = core.TruncateUTF8(s, 6000, 6000)
		}
		return Result{
			Text:    fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, u, s),
			Summary: fmt.Sprintf("%s → HTTP %d", ext.Name, resp.StatusCode),
		}, nil
	}
}

const extTemplate = `{
  "name": "%s",
  "description": "Описание расширения: какие возможности оно добавляет агенту",
  "tools": [
    {
      "name": "proj_status",
      "description": "Статус git-репозитория проекта",
      "command": "git status --short --branch",
      "timeout_sec": 30
    },
    {
      "name": "api_call",
      "description": "Пример HTTP-инструмента: запрос к внешнему API",
      "url": "https://api.github.com/repos/golang/go",
      "method": "GET",
      "headers": { "Accept": "application/json" },
      "timeout_sec": 20
    }
  ]
}
`

// ExtTemplate — шаблон нового расширения.
func ExtTemplate(name string) string { return fmt.Sprintf(extTemplate, name) }
