package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Доверие к коду на диске.
//
// Зачем. Расширение и MCP-сервер — это чужой код, который запускается на
// машине пользователя. Раньше всё, что лежало в .gcli/extensions и
// .gcli/mcp.json чужого репозитория, молча подключалось при старте: клон
// репозитория — и на машине уже исполняется произвольный код, причём до
// первого вопроса пользователю.
//
// Правило здесь сознательно простое: код из ПРОЕКТА требует согласия один
// раз и запоминается по содержимому (хеш), а не по имени. Согласие по
// имени не годится — правка манифеста после подтверждения тихо меняет то,
// что запускается. Код пользователя (~/.gcli) доверен изначально: его писал
// сам пользователь, и требовать подтверждения самому себе — просто шум.

// TrustLevel — уровень доверия к источнику кода.
type TrustLevel string

const (
	// TrustUser — глобальный каталог пользователя: доверен изначально.
	TrustUser TrustLevel = "user"
	// TrustProject — код из проекта: нужно согласие, запомненное по хешу.
	TrustProject TrustLevel = "project"
	// TrustNone — согласия не было.
	TrustNone TrustLevel = "none"
)

// trustStore — куда пишутся подтверждения.
//
// Отдельный файл рядом с конфигом, а не поле в config.json: config
// синхронизируется между машинами (и лежит в репозитории у некоторых),
// а доверие — решение конкретного человека на конкретной машине. Скопированный
// на чужую машину файл доверия означал бы «я доверяю коду на чужом компьютере».
type trustStore struct {
	mu     sync.Mutex
	path   string
	hashes map[string]string // отпечаток кода → «что это и когда подтверждено»
}

// trustKey — ключ для записи доверия.
func trustKey(kind, name string) string { return kind + ":" + name }

// hashSources — отпечаток содержимого набора файлов.
//
// Хешируем содержимое, а не пути: переименование файла не должно требовать
// нового подтверждения, а правка одной строки в манифесте — должна.
func hashSources(files map[string]string) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s\x00%s\x00", n, files[n])
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// NewTrustStore — хранилище доверия рядом с каталогом данных.
//
// Файл доверия (trusted.json) лежит рядом с config.json, но не в нём самом:
// config синхронизируется между машинами (и иногда лежит в репозитории), а
// доверие к коду — решение конкретного человека на конкретной машине.
func NewTrustStore(home string) *trustStore { return newTrustStore(home) }

func newTrustStore(home string) *trustStore {
	ts := &trustStore{hashes: map[string]string{}}
	if home != "" {
		ts.path = filepath.Join(home, "trusted.json")
		ts.load()
	}
	return ts
}

func (ts *trustStore) load() {
	if ts.path == "" {
		return
	}
	data, err := os.ReadFile(ts.path)
	if err != nil {
		return
	}
	m := map[string]string{}
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	ts.mu.Lock()
	ts.hashes = m
	ts.mu.Unlock()
}

func (ts *trustStore) save() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(ts.path), 0o755)
	if data, err := json.MarshalIndent(ts.hashes, "", "  "); err == nil {
		_ = os.WriteFile(ts.path, data, 0o600)
	}
}

// Check — уровень доверия к набору файлов кода.
func (ts *trustStore) Check(kind, name string, files map[string]string) (TrustLevel, string) {
	if kind == "user" {
		return TrustUser, ""
	}
	if ts == nil {
		return TrustNone, ""
	}
	h := hashSources(files)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if got, ok := ts.hashes[trustKey(kind, name)]; ok && got == h {
		return TrustProject, ""
	}
	return TrustNone, h
}

// Approve — запомнить согласие.
func (ts *trustStore) Approve(kind, name, hash, note string) {
	if ts == nil || kind == "user" {
		return
	}
	ts.mu.Lock()
	ts.hashes[trustKey(kind, name)] = hash
	ts.mu.Unlock()
	ts.save()
	_ = note
}

// Forget — забыть согласия (команда «доверять заново»).
func (ts *trustStore) Forget(kind string) int {
	if ts == nil {
		return 0
	}
	ts.mu.Lock()
	n := 0
	for k := range ts.hashes {
		if kind == "" || strings.HasPrefix(k, kind+":") {
			delete(ts.hashes, k)
			n++
		}
	}
	ts.mu.Unlock()
	if n > 0 {
		ts.save()
	}
	return n
}

// List — что доверено (для /permissions и /tools).
func (ts *trustStore) List() map[string]string {
	out := map[string]string{}
	if ts == nil {
		return out
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for k, v := range ts.hashes {
		out[k] = v
	}
	return out
}

// ---------- Проверка проекта ----------

// ProjectCodeWarning — описание кода, которому ещё не доверяют.
type ProjectCodeWarning struct {
	Kind  string // "ext" | "mcp"
	Name  string
	Path  string
	Hash  string
	What  string // что именно будет выполнено
	Count int    // сколько инструментов это даст
}

// Label — короткое имя источника для UI: «расширение x» / «MCP x».
func (w ProjectCodeWarning) Label() string {
	if w.Kind == "mcp" {
		return "MCP " + w.Name
	}
	return "расширение " + w.Name
}

// ScanProjectCode — найти расширения и MCP-серверы из проекта, которым
// не доверяют, и описать, что они собираются запустить.
//
// Ничего не выполняется: только чтение манифестов и подсчёт команд. Это
// позволяет показать пользователю вопрос ДО того, как что-то запустится.
func (r *Registry) ScanProjectCode() []ProjectCodeWarning {
	var out []ProjectCodeWarning
	trust := r.env.Trust

	// Расширения.
	dirs := r.ExtDirs()
	for _, d := range dirs {
		if d[1] != "проект" {
			continue
		}
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
			if err != nil || ext.Name == "" {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			level, hash := trust.Check("ext", ext.Name, map[string]string{p: string(data)})
			// Глобальный каталог не сканируется выше, а подтверждённое
			// проектное — уже разрешено: показывать его как «ждущее
			// подтверждения» значило бы врать в списке.
			if level != TrustNone {
				continue
			}
			out = append(out, ProjectCodeWarning{
				Kind: "ext", Name: ext.Name, Path: p, Hash: hash,
				What:  extWhat(ext),
				Count: len(ext.Tools),
			})
		}
	}

	// MCP-серверы: читаем ТОЛЬКО проектный конфиг (глобальный доверен).
	p := filepath.Join(r.workDir, ".gcli", "mcp.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return out
	}
	var cfg MCPConfig
	_ = json.Unmarshal(data, &cfg)
	names := make([]string, 0, len(cfg.Servers))
	for n := range cfg.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		srv := cfg.Servers[n]
		if strings.TrimSpace(srv.Command) == "" {
			continue
		}
		if srv.Enabled != nil && !*srv.Enabled {
			continue
		}
		level, hash := trust.Check("mcp", n, map[string]string{p: string(data)})
		if level != TrustNone {
			continue
		}
		cmdLine := srv.Command
		if len(srv.Args) > 0 {
			cmdLine += " " + strings.Join(srv.Args, " ")
		}
		out = append(out, ProjectCodeWarning{
			Kind: "mcp", Name: n, Path: p, Hash: hash, What: cmdLine,
		})
	}
	return out
}

func extWhat(ext Extension) string {
	var cmds []string
	for _, t := range ext.Tools {
		switch {
		case t.Command != "":
			cmds = append(cmds, t.Command)
		case t.URL != "":
			cmds = append(cmds, t.URL)
		}
	}
	return strings.Join(cmds, " · ")
}

// TrustCode — запомнить согласие пользователя на конкретный код.
func (r *Registry) TrustCode(w ProjectCodeWarning) {
	if r.env.Trust == nil {
		return
	}
	data, err := os.ReadFile(w.Path)
	if err != nil {
		return
	}
	r.env.Trust.Approve(w.Kind, w.Name, hashSources(map[string]string{w.Path: string(data)}), w.What)
}

// FindPendingCode — найти недоверенный код по имени (для /ext trust x).
//
// Команда получает имя, а не путь: имя расширения или сервера — то, что
// человек видит в списке. Ищем среди реально найденного кода, а не «где-то
// там»: подтверждать то, чего нет, бессмысленно.
func (r *Registry) FindPendingCode(kind, name string) (ProjectCodeWarning, bool) {
	for _, w := range r.ScanProjectCode() {
		if w.Kind == kind && strings.EqualFold(w.Name, name) {
			return w, true
		}
	}
	return ProjectCodeWarning{}, false
}

// UntrustCode — забыть все согласия (для /permissions reset).
func (r *Registry) UntrustCode() int {
	if r.env.Trust == nil {
		return 0
	}
	return r.env.Trust.Forget("")
}

// TrustedList — что доверено сейчас (для /permissions).
func (r *Registry) TrustedList() map[string]string {
	if r.env.Trust == nil {
		return nil
	}
	return r.env.Trust.List()
}
