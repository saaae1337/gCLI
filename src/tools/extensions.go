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
	// InputFile — имя переменной окружения, в которую кладётся вход.
	//
	// Вход нельзя подставлять в строку команды текстом: «{input}» внутри
	// «git commit -m "{input}» — это готовая инъекция команд оболочкой
	// (вход вида «"; rm -rf ~; #» выполнится). Поэтому расширение получает
	// вход в переменной окружения (по умолчанию GCLI_INPUT) и читает её так,
	// как удобно: "$GCLI_INPUT" в bash, %GCLI_INPUT% в cmd, $env:GCLI_INPUT
	// в PowerShell. Если поле не задано, используется имя по умолчанию.
	InputFile string `json:"input_env,omitempty"`
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

const schemaExt = `{"type":"object","properties":{"input":{"type":"string","description":"Входные данные/аргументы. Для shell-инструментов приходят в переменную окружения (GCLI_INPUT) и stdin — в команде читай \"$GCLI_INPUT\"/\"%GCLI_INPUT%\", вставлять вход в строку команды нельзя. Для HTTP-инструментов подставляются в url/body как раньше."}},"required":[]}`

// extToolCount — число подключённых инструментов расширений.
var extToolCount int

// extTrusted — можно ли подключать расширение.
//
// Расширение из глобального каталога доверено: его писал пользователь.
// Расширение из проекта требует явного согласия, и оно уже было дано ранее
// — тогда подключаем молча. Всё остальное пропускаем: подключать инструмент,
// который сейчас никто не разрешал, нельзя, даже если он лежит рядом.
func (r *Registry) extTrusted(ext Extension) (bool, string) {
	// Находим манифест на диске: отпечаток считается по содержимому, а не
	// по имени файла. Путь заодно говорит, чей это код.
	path, kind := extManifestPath(r, ext.Name)
	if path == "" {
		return false, ""
	}
	if kind == "user" {
		return true, ""
	}
	// Хранилища доверия нет (тесты, неполная инициализация) — согласие
	// спросить и запомнить нечем, а подключать чужой код молча нельзя.
	if r.env.Trust == nil {
		return false, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	level, hash := r.env.Trust.Check("ext", ext.Name, map[string]string{path: string(data)})
	if level == TrustProject {
		return true, ""
	}
	return false, hash
}

// extManifestPath — путь к манифесту расширения и его уровень доверия.
func extManifestPath(r *Registry, name string) (path, kind string) {
	for _, d := range r.ExtDirs() {
		ents, err := os.ReadDir(d[0])
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			p := filepath.Join(d[0], e.Name())
			ext, err := parseExtension(p)
			if err != nil || ext.Name != name {
				continue
			}
			k := "ext"
			if d[1] == "глобальный" {
				k = "user"
			}
			return p, k
		}
	}
	return "", ""
}

// LoadExtensionTools — зарегистрировать инструменты из расширений.
func (r *Registry) LoadExtensionTools() (int, []string) {
	var warns []string
	count := 0
	for _, ext := range r.LoadExtensions() {
		ok, _ := r.extTrusted(ext)
		if !ok {
			// Молча пропускать нельзя: агент (и пользователь) должны знать,
			// что инструменты из проекта не подключены. Подробности —
			// в /ext и /permissions, а здесь короткая строка в лог старта.
			warns = append(warns, fmt.Sprintf(
				"расширение %s: из проекта, требует подтверждения (/ext trust %s)", ext.Name, ext.Name))
			continue
		}
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

// extInputEnv — переменная окружения с входом расширения.
func extInputEnv(t ExtTool) string {
	name := strings.TrimSpace(t.InputFile)
	if name == "" {
		return "GCLI_INPUT"
	}
	// Имя переменной приходит из манифеста расширения, то есть из файла на
	// диске. Оставляем только безопасный набор символов: иначе расширение
	// могло бы подсунуть «FOO=bar PATH=/tmp» и дописать в окружение процесса
	// произвольные переменные.
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		}
		return -1
	}, name)
	if clean == "" {
		return "GCLI_INPUT"
	}
	return clean
}

// extCmdHandler — shell-инструмент расширения.
func (r *Registry) extCmdHandler(ext Extension, t ExtTool) Handler {
	return func(ctx context.Context, m map[string]any) (Result, error) {
		input := ArgStr(m, "input")
		envName := extInputEnv(t)
		// Вход не подставляется в строку команды: «{input}» в команде —
		// это инъекция команд оболочкой. Вместо этого он кладётся в
		// переменную окружения, а команда читает её как "$GCLI_INPUT"
		// (bash), "%GCLI_INPUT%" (cmd) или "$env:GCLI_INPUT" (PowerShell).
		// Если автор расширения написал {input} — говорим прямо, как
		// переписать, вместо тихой склейки.
		if strings.Contains(t.Command, "{input}") {
			return Result{}, fmt.Errorf(
				"расширение %s: вход нельзя вставлять в команду текстом — это позволяет "+
					"выполнить что угодно (инъекция в shell). Перепиши команду так, чтобы она "+
					"читала переменную окружения %s: bash → \"$%s\", cmd → \"%%%s%%\", "+
					"PowerShell → \"$env:%s\"", ext.Name, envName, envName, envName, envName)
		}
		cmdStr := t.Command
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
			// Каталог расширения — это путь, который задал не пользователь
			// в этот момент, а код на диске, поэтому песочница режет его так же,
			// как любой другой путь от «модели».
			abs, err := r.pathArg(t.Workdir)
			if err != nil {
				return Result{}, err
			}
			workdir = abs
		}

		cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		shell, sargs := ShellCommand(cmdStr)
		cmdEx := exec.CommandContext(cctx, shell, sargs...)
		cmdEx.Dir = workdir
		// Вход едет и в переменную окружения, и в stdin: переменная нужна
		// командам вида «echo "$GCLI_INPUT"», stdin — тем, кто читает построчно.
		cmdEx.Env = append(os.Environ(), envName+"="+input, "GCLI=1")
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
		// SSRF: URL расширения — это текст из манифеста плюс вход модели.
		// Соблазн «дай мне любой адрес» должен упираться в ту же проверку,
		// что и web_fetch, иначе расширение становится обходом фильтра.
		if err := checkSSRF(u); err != nil {
			return Result{}, err
		}
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
