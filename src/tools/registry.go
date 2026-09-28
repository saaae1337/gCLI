// Package tools — инструменты агента: работа с файлами, поиск, shell,
// веб, планирование. Инструменты регистрируются в реестре и вызываются
// агентным циклом по имени.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"gcli/core"
)

// Handler — обработчик инструмента.
type Handler func(ctx context.Context, m map[string]any) (Result, error)

// readMu — защита общей карты прочитанных файлов.
//
// Реестр копируется по значению (Base/Restrict/Merge), но env.ReadFiles —
// ссылочное поле, и копии разделяют одну карту с главным реестром.
// При параллельных субагентах несколько горутин пишут в неё одновременно,
// что в Go даёт фатальную ошибку «concurrent map writes».
var readMu sync.Mutex

// markRead — отметить файл как прочитанный.
func markRead(m map[string]bool, p string) {
	if m == nil {
		return
	}
	readMu.Lock()
	m[p] = true
	readMu.Unlock()
}

// wasRead — был ли файл прочитан (нужно для edit_file).
func wasRead(m map[string]bool, p string) bool {
	if m == nil {
		return false
	}
	readMu.Lock()
	defer readMu.Unlock()
	return m[p]
}

// Result — результат инструмента для модели.
type Result struct {
	// Text — текст, который уходит в контекст модели.
	Text string
	// Summary — короткая строка для UI (превью результата).
	Summary string
	// Error — мягкая ошибка: показывается в UI, но не ломает цикл.
	Error string
	// Images — изображения для зрения модели (screenshot, read_image).
	// Прикладываются к контексту сразу после результата инструмента.
	Images []core.Image
}

// Tool — зарегистрированный инструмент.
type Tool struct {
	Def          ToolDef
	Handler      Handler
	Category     string // read | write | exec | net | plan | think | agent | mcp | ext
	NeedsConfirm bool
	ExtName      string // если инструмент из расширения
	// MCPSrv / MCPCall — сервер MCP и исходное имя инструмента на нём
	// (составное имя mcp__<server>__<tool> санитизируется, а серверу нужно
	// оригинальное имя).
	MCPSrv  string
	MCPCall string
}

// Registry — набор инструментов.
type Registry struct {
	tools     []*Tool
	byName    map[string]*Tool
	workDir   string
	env       Env
	skillsOff []string

	// muMCP защищает карту живых MCP-соединений (перезагрузка из UI-потока
	// может совпасть с вызовом инструмента из агентной горутины).
	muMCP    sync.Mutex
	mcpConns map[string]*mcpConn
}

// Env — зависимости инструментов от окружения (для тестов и изоляции).
type Env struct {
	WorkDir string
	// Confirm — запрос подтверждения у пользователя (nil = без подтверждений).
	Confirm func(req ConfirmReq) bool
	// Record — записать чекпоинт файла перед изменением.
	Record func(path, label string)
	// Session — доступ к сессии (для todo_write).
	Session SessionRef
	// OnTodo — вызывается после обновления списка задач.
	OnTodo func(todos []TodoItem)
	// OnNote — вызывается после записи заметки.
	OnNote func(title, body string)
	// SkillsOff — навыки, выключенные пользователем.
	SkillsOff []string
	// Spawn — запуск субагента.
	Spawn func(ctx context.Context, args SpawnArgs) (SpawnResult, error)
	// Ask — задать вопрос пользователю.
	Ask func(question string, options []string) (string, error)
	// Agents — список активных/завершённых субагентов.
	Agents func(action, name string) string
	// Self — снимок собственного состояния агента (инструмент self_status).
	Self func() SelfReport
	// Depth — текущая глубина вложенности агента.
	Depth int
	// MaxDepth — максимальная глубина вложенности.
	MaxDepth int
	// ReadFiles — множество прочитанных файлов (для edit_file).
	ReadFiles map[string]bool
	// ReadOnly — режим «только чтение» (субагент-исследователь).
	ReadOnly bool
	// HTTPClient — клиент для сетевых инструментов.
	HTTPClient *HTTPClient
}

// ConfirmReq — запрос подтверждения.
type ConfirmReq struct {
	Kind   ConfirmKind
	Path   string // для записи файлов
	Old    string // старое содержимое (для диффа)
	New    string // новое содержимое
	Detail string // команда / URL / описание
	Reason string
}

// ConfirmKind — тип подтверждения.
type ConfirmKind string

const (
	ConfirmWrite ConfirmKind = "write"
	ConfirmExec  ConfirmKind = "exec"
	ConfirmNet   ConfirmKind = "net"
	ConfirmAgent ConfirmKind = "agent"
)

// String — человекочитаемое имя вида подтверждения.
func (k ConfirmKind) String() string {
	switch k {
	case ConfirmWrite:
		return "изменение файла"
	case ConfirmExec:
		return "выполнение команды"
	case ConfirmNet:
		return "сетевой запрос"
	case ConfirmAgent:
		return "запуск субагента"
	}
	return string(k)
}

// SpawnArgs — параметры запуска субагента.
type SpawnArgs struct {
	Type     string
	Task     string
	Name     string
	Model    string
	ReadOnly bool
	Depth    int
}

// SpawnResult — результат работы субагента.
type SpawnResult struct {
	Name     string
	Summary  string
	Full     string
	Usage    core.Usage
	Turns    int
	ToolCall int
}

// SessionRef — минимальный интерфейс сессии для инструментов.
type SessionRef interface {
	SetTodos(todos []TodoItem)
	Todos() []TodoItem
}

// TodoItem — задача плана.
type TodoItem struct {
	Content string
	Status  string
}

// New — создать реестр инструментов.
func New(env Env) *Registry {
	if env.ReadFiles == nil {
		env.ReadFiles = map[string]bool{}
	}
	if env.HTTPClient == nil {
		env.HTTPClient = NewHTTPClient()
	}
	r := &Registry{
		byName:    map[string]*Tool{},
		workDir:   env.WorkDir,
		env:       env,
		skillsOff: env.SkillsOff,
	}
	r.registerBuiltins()
	return r
}

// Base — реестр только со встроенными инструментами и скиллами,
// без расширений и субагентов. Используется как основа для субагентов.
func (r *Registry) Base() *Registry {
	out := &Registry{
		byName:    map[string]*Tool{},
		workDir:   r.workDir,
		env:       r.env,
		skillsOff: r.skillsOff,
	}
	for _, t := range r.tools {
		// Инструменты расширений и MCP-серверов живут только у главного
		// агента: субагентам — встроенный набор.
		if t.ExtName != "" || t.MCPSrv != "" {
			continue
		}
		cp := *t
		out.tools = append(out.tools, &cp)
		out.byName[t.Def.Name] = &cp
	}
	return out
}

// RegisterExtTools — подключить инструменты из расширений (с проверкой имён).
func (r *Registry) RegisterExtTools() []string {
	_, warns := r.LoadExtensionTools()
	return warns
}

// Defs — определения инструментов для API.
func (r *Registry) Defs() []ToolDef {
	out := make([]ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Def)
	}
	return out
}

// ToolDef — описание инструмента для модели (совпадает с core.ToolDef,
// чтобы не дублировать структуру).
type ToolDef = core.ToolDef

// Names — имена всех инструментов.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Def.Name)
	}
	return out
}

// Get — найти инструмент по имени.
func (r *Registry) Get(name string) *Tool { return r.byName[name] }

// Count — количество инструментов.
func (r *Registry) Count() int { return len(r.tools) }

// ExtCount — количество инструментов из расширений.
func (r *Registry) ExtCount() int {
	n := 0
	for _, t := range r.tools {
		if t.ExtName != "" {
			n++
		}
	}
	return n
}

// All — все инструменты.
func (r *Registry) All() []*Tool { return r.tools }

// Has — есть ли инструмент.
func (r *Registry) Has(name string) bool { _, ok := r.byName[name]; return ok }

// Restrict — вернуть копию реестра с ограниченным набором инструментов.
//
// allow (если непустой) — белый список имён; deny — чёрный список.
// Инструменты записи автоматически исключаются при readOnly.
// Коллизии безопасны: копия использует те же обработчики, но свой набор имён,
// поэтому инструмент «load_skill» не будет конфликтовать с уже зарегистрированным.
func (r *Registry) Restrict(allow, deny []string) *Registry {
	allowSet := toSet(allow)
	denySet := toSet(deny)

	out := &Registry{
		byName:    map[string]*Tool{},
		workDir:   r.workDir,
		env:       r.env,
		skillsOff: r.skillsOff,
	}
	out.env.ReadOnly = r.env.ReadOnly
	// Копия карты прочитанных файлов: субагент не должен «наследовать»
	// право редактировать файлы, которые прочитал главный агент.
	rf := make(map[string]bool, len(r.env.ReadFiles))
	for k := range r.env.ReadFiles {
		rf[k] = true
	}
	out.env.ReadFiles = rf

	for _, t := range r.tools {
		if len(allowSet) > 0 && !allowSet[t.Def.Name] {
			continue
		}
		if denySet[t.Def.Name] {
			continue
		}
		if out.env.ReadOnly && isWriteTool(t.Def.Name) {
			continue
		}
		cp := *t
		out.tools = append(out.tools, &cp)
		out.byName[t.Def.Name] = &cp
	}
	return out
}

func toSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func isWriteTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "task_note", "todo_write":
		return true
	}
	return false
}

// Merge — вернуть копию реестра с добавленными инструментами (для объединения
// результатов нескольких параллельных субагентов).
func (r *Registry) Merge(others ...*Registry) *Registry {
	out := &Registry{byName: map[string]*Tool{}, workDir: r.workDir, env: r.env, skillsOff: r.skillsOff}
	for _, reg := range append([]*Registry{r}, others...) {
		for _, t := range reg.tools {
			if out.byName[t.Def.Name] != nil {
				continue
			}
			cp := *t
			out.tools = append(out.tools, &cp)
			out.byName[t.Def.Name] = &cp
		}
	}
	return out
}

// WorkDir — рабочий каталог реестра.
func (r *Registry) WorkDir() string { return r.workDir }

// CountRead — сколько файлов агент уже прочитал в этой сессии.
func (r *Registry) CountRead() int {
	readMu.Lock()
	defer readMu.Unlock()
	return len(r.env.ReadFiles)
}

// SetDepth — задать глубину вложенности в копии реестра.
//
// env копируется по значению, поэтому глубина из главного агента (0)
// попадала в субагента. Из-за этого субагент с неограниченным набором
// инструментов (тип custom) видел spawn_agent с Depth=0 и мог порождать
// субагентов снова и снова — рекурсия без ограничения глубины.
func (r *Registry) SetDepth(depth int) *Registry {
	r.env.Depth = depth
	if r.env.MaxDepth <= 0 {
		r.env.MaxDepth = 1
	}
	return r
}

// register — добавить инструмент.
func (r *Registry) register(name, desc, schema, category string, confirm bool, h Handler) {
	t := &Tool{
		Def:          ToolDef{Name: name, Description: desc, Schema: schema},
		Handler:      h,
		Category:     category,
		NeedsConfirm: confirm,
	}
	r.tools = append(r.tools, t)
	r.byName[name] = t
}

// registerExt — добавить инструмент расширения (учитывая коллизии имён).
func (r *Registry) registerExt(extName, name, desc, schema, category string, h Handler) (string, []string) {
	final := name
	warns := []string{}
	if r.byName[final] != nil {
		final = "x_" + name
	}
	if r.byName[final] != nil {
		warns = append(warns, fmt.Sprintf("расширение %s: имя «%s» занято — пропущен", extName, name))
		return final, warns
	}
	t := &Tool{
		Def:      ToolDef{Name: final, Description: "[" + extName + "] " + desc, Schema: schema},
		Handler:  h,
		Category: category,
		ExtName:  extName,
	}
	r.tools = append(r.tools, t)
	r.byName[final] = t
	return final, warns
}

// ---------- Хелперы разбора аргументов ----------

// ParseArgs — разобрать JSON-аргументы инструмента.
func ParseArgs(raw string) map[string]any {
	m := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return m
	}
	_ = json.Unmarshal([]byte(raw), &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

// ArgStr — строковый аргумент.
func ArgStr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return ""
}

// ArgInt — целочисленный аргумент со значением по умолчанию.
func ArgInt(m map[string]any, k string, def int) int {
	if v, ok := m[k].(float64); ok {
		return int(v)
	}
	return def
}

// ArgBool — булев аргумент.
func ArgBool(m map[string]any, k string) bool {
	v, _ := m[k].(bool)
	return v
}

// ArgFloat — число с плавающей точкой.
func ArgFloat(m map[string]any, k string, def float64) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return def
}

// ArgStrSlice — массив строк.
func ArgStrSlice(m map[string]any, k string) []string {
	raw, ok := m[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ---------- Общие проверки ----------

// resolvePath — преобразовать путь от модели в абсолютный.
//
// Модель часто передаёт «C:/gcli/src» или «C:\gcli\src» — такие пути
// должны оставаться как есть, а не склеиваться с рабочим каталогом.
func (r *Registry) resolvePath(p string) string {
	return core.AbsPath(r.workDir, p)
}

// reExtName — валидное имя инструмента расширения.
var reExtName = regexp.MustCompile(`^[a-z][a-z0-9_]{2,30}$`)
