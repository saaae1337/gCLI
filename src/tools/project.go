package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gcli/core"
)

// ---------- project_info ----------
//
// Самая частая потеря итерации в чужом проекте — не понять, где корень и чем
// собирать. На практике выглядит так: агент делает `go build ./...` из
// каталога, где нет go.mod, получает «directory prefix . does not contain main
// module», и тратит вызов на поиск go.mod. Или хуже — находит модуль, но не
// догадывается, что точка входа в другом подкаталоге.
//
// verify отвечает на «чем проверять», но не отвечает на «что это вообще».
// project_info закрывает именно этот вопрос одним дешевым вызовом.

// projInfo — снимок устройства проекта.
type projInfo struct {
	WorkDir string
	Root    string // корень проекта (репозиторий или модуль)
	GitRoot string
	// Стек.
	Stacks []string
	// Точка входа: каталоги с main / бинарники.
	EntryPoints []string
	// Команды сборки/проверки, найденные по конфигам.
	Build   []string
	Test    []string
	Note    []string
	TopDirs []string
	// Remember — факты о проекте, которые стоит сохранить между сессиями.
	// Самостоятельно вспоминать их агент не станет, а rediscover'ить проект
	// заново каждый запуск — это ровно та потеря времени, ради которой
	// project_info и делается.
	Remember []string
}

func (r *Registry) projectInfo() projInfo {
	p := projInfo{WorkDir: r.workDir}
	p.GitRoot = gitRepoRoot(r.workDir)

	// Корень модуля: сначала ищем конфиг ВВЕРХ от рабочего каталога (мы внутри
	// модуля), и только если ничего нет — ВНИЗ.
	//
	// Обратный порядок обязателен и именно на нём раньше ломалось: рабочий
	// каталог почти всегда в глубоком подкаталоге (src/tools, cmd/...), вниз от
	// него лежат чужие модули в vendor, а нужный go.mod — на уровне корня
	// репозитория. Поиск вниз без проверки результата давал корень в
	// node_modules как «проект», и после этого `go build ./...` падал.
	modFile, stack := findModuleUp(r.workDir)
	if modFile == "" {
		modFile, stack = findModuleDown(p.GitRoot, r.workDir)
	}
	if modFile != "" {
		p.Root = filepath.Dir(modFile)
		p.Stacks = append(p.Stacks, stack)
	}
	if p.Root == "" {
		p.Root = p.GitRoot
	}
	if p.Root == "" {
		p.Root = r.workDir
	}

	// Метки про модуль: в корне может лежать второй конфиг другого стека.
	for _, m := range []struct{ name, stack string }{
		{"go.mod", "Go"}, {"package.json", "Node"}, {"Cargo.toml", "Rust"},
		{"pyproject.toml", "Python"}, {"requirements.txt", "Python"},
		{"Makefile", "Make"}, {"justfile", "Just"}, {"CMakeLists.txt", "CMake"},
		{"pom.xml", "Maven"}, {"build.gradle", "Gradle"},
	} {
		if !hasStr(p.Stacks, m.stack) && existsGlob(p.Root, m.name) {
			p.Stacks = append(p.Stacks, m.stack)
		}
	}
	if p.GitRoot == "" {
		p.Note = append(p.Note, "Это не git-репозиторий: откат через revert_last работает, но сравнить с прошлой версией нечем.")
	}

	// Точки входа Go — каталоги с package main.
	p.EntryPoints = findMainPackages(p.Root)
	// Команды: не угадываем, а читаем из конфигов.
	p.Build, p.Test = detectCommands(p.Root, p.EntryPoints)
	p.Remember = rememberHints(p, modFile)
	return p
}

// moduleMarkers — конфиги модулей в порядке приоритета.
//
// Первый найденный и определяет корень: у монорепозитория с Go в корне и
// Node в подкаталоге «проектом» всё равно считается Go-модуль.
var moduleMarkers = []struct{ name, stack string }{
	{"go.mod", "Go"},
	{"package.json", "Node"},
	{"Cargo.toml", "Rust"},
	{"pyproject.toml", "Python"},
	{"go.work", "Go"},
}

// findModuleUp — ближайший конфиг модуля вверх по дереву.
func findModuleUp(dir string) (string, string) {
	for _, m := range moduleMarkers {
		if p := findUp(dir, m.name); p != "" {
			return p, m.stack
		}
	}
	return "", ""
}

// findModuleDown — ближайший конфиг модуля вниз по дереву.
//
// from — корень репозитория (если он есть), limit — каталог, из которого
// искать нельзя: конфиг не может лежать в поддереве рабочего каталога, там
// лежат чужие модули, и «проектом» окажется vendor. Без этой границы
// project_info в любом Go-проекте с зависимостями указывал на vendor.
//
// Обход ограничен по глубине: монорепозиторий с тысячей каталогов не должен
// превращать один дешёвый вызов в минуту сканирования.
func findModuleDown(root, limit string) (string, string) {
	if root == "" {
		return "", ""
	}
	// maxDepth — сколько уровней вниз обходить от корня репозитория.
	const maxDepth = 4

	var best, bestStack string
	bestScore := 1 << 30
	for _, m := range moduleMarkers {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if skipWalkDir(d.Name()) {
					return filepath.SkipDir
				}
				if depth(root, path) >= maxDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() != m.name {
				return nil
			}
			// Мусор: в vendor и node_modules модулей не бывает по смыслу.
			if hasPathSegment(filepath.Dir(path), "vendor") || hasPathSegment(filepath.Dir(path), "node_modules") {
				return nil
			}
			// Модуль внутри рабочего каталога — чужой. Без этой проверки
			// project_info в Go-проекте с зависимостями объявлял «проектом»
			// поддерево, из которого запускали, а не настоящий корень.
			if insideDir(limit, path) {
				return nil
			}
			if s := depth(root, path); s < bestScore {
				best, bestStack, bestScore = path, m.stack, s
			}
			return nil
		})
	}
	return best, bestStack
}

// depth — расстояние от корня до файла в уровнях каталогов.
func depth(root, file string) int {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return 1 << 30
	}
	return strings.Count(filepath.ToSlash(rel), "/")
}

// insideDir — находится ли path внутри каталога dir.
func insideDir(dir, path string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hasPathSegment — есть ли в пути указанный сегмент каталога.
func hasPathSegment(p, seg string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == seg {
			return true
		}
	}
	return false
}

// hasStr — есть ли значение в списке.
func hasStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// findUp — идти вверх до корня, пока не найдём файл.
func findUp(dir, name string) string {
	cur := dir
	for i := 0; i < 8; i++ {
		p := filepath.Join(cur, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return ""
}

// findMainPackages — каталоги с func main (точки входа бинарников).
//
// Обход ограничен по глубине: в монорепозитории «вниз» уходит весь vendor и
// сгенерированный код, а точка входа модуля почти всегда в паре каталогов от
// корня. Без ограничения один project_info на большом репозитории стоил бы
// десятки секунд.
func findMainPackages(root string) []string {
	var out []string
	seen := map[string]bool{}
	// maxDepth — сколько каталогов вниз обходить от корня модуля.
	const maxDepth = 3
	// maxHits — сколько точек входа нам достаточно, чтобы дать знать.
	const maxHits = 20

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		// err != nil бывает и при d == nil: обходить дальше нечего.
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skipWalkDir(d.Name()) {
				return filepath.SkipDir
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr == nil && strings.Count(filepath.ToSlash(rel), "/") >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if reMainPkg.Match(data) {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			dir := filepath.ToSlash(rel)
			if dir != "." && !seen[dir] {
				seen[dir] = true
				out = append(out, dir)
				if len(out) >= maxHits {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// skipWalkDir — каталоги, где искать точки входа бессмысленно.
func skipWalkDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "build", "dist", ".gcli", "testdata":
		return true
	}
	return false
}

var reMainPkg = regexp.MustCompile(`(?m)^package\s+main\b`)

// detectCommands — команды сборки и проверки по конфигам проекта.
func detectCommands(root string, entries []string) (build, test []string) {
	if existsGlob(root, "go.mod") {
		build = append(build, "go build ./...")
		test = append(test, "go test ./...")
		// Точка входа не в корне — без указания пакета бинарник не соберётся.
		if len(entries) == 0 {
			build = append(build, "go build .")
		} else {
			for _, e := range entries {
				build = append(build, "go build ./"+e)
			}
		}
	}
	if existsGlob(root, "package.json") {
		build = append(build, "npm run build")
		test = append(test, "npm test")
	}
	if existsGlob(root, "Cargo.toml") {
		build = append(build, "cargo build")
		test = append(test, "cargo test")
	}
	if existsGlob(root, "pyproject.toml") {
		test = append(test, "python -m pytest -q")
	}
	if existsGlob(root, "Makefile") {
		build = append(build, "make")
		test = append(test, "make test")
	}
	if existsGlob(root, "justfile") {
		test = append(test, "just test")
	}
	if existsGlob(root, "CMakeLists.txt") {
		build = append(build, "cmake --build build")
	}
	return build, test
}

// rememberHints — факты, которые стоит записать в долговременную память.
//
// Собираются только из того, что машина уже знает наверняка (конфиги, git,
// точки входа). Ничего не выдумывается: агент получает готовые строки, а сам
// решает, стоит ли это того.
func rememberHints(p projInfo, modFile string) []string {
	var out []string
	// Корень проекта — самая частая и самая полезная запись: без неё агент в
	// новой сессии снова ищет go.mod по всему репозиторию.
	if rel := core.RelToWD(p.WorkDir, p.Root); rel != "" && rel != "." {
		out = append(out, fmt.Sprintf("проект %s: корень — %s, а рабочий каталог — %s",
			filepath.Base(p.Root), p.Root, p.WorkDir))
	}
	if modFile != "" {
		if name, ok := modName(modFile); ok {
			out = append(out, fmt.Sprintf("модуль %s: go.mod в %s", name, p.Root))
		}
	}
	if len(p.Stacks) > 0 {
		out = append(out, "стек: "+strings.Join(p.Stacks, ", "))
	}
	if len(p.Build) > 0 {
		out = append(out, "сборка: "+p.Build[0])
	}
	if len(p.Test) > 0 {
		out = append(out, "проверка: "+p.Test[0])
	}
	if len(p.EntryPoints) > 0 && len(p.EntryPoints) <= 4 {
		out = append(out, "точки входа: "+strings.Join(p.EntryPoints, ", "))
	}
	return out
}

// modName — имя модуля из go.mod (путь module, без кавычек).
func modName(modFile string) (string, bool) {
	if filepath.Base(modFile) != "go.mod" {
		return "", false
	}
	data, err := os.ReadFile(modFile)
	if err != nil {
		return "", false
	}
	for _, l := range core.SplitLines(string(data)) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "module ") {
			name := strings.TrimSpace(strings.TrimPrefix(l, "module "))
			name = strings.Trim(name, `"`)
			return name, name != ""
		}
	}
	return "", false
}

// Text — отчёт для модели.
func (p projInfo) Text() string {
	var b strings.Builder
	b.WriteString("# Проект\n\n")
	if len(p.Stacks) > 0 {
		fmt.Fprintf(&b, "Стек: %s.\n", strings.Join(p.Stacks, ", "))
	} else {
		b.WriteString("Стек: не опознан по корневым конфигам.\n")
	}
	// Относительный путь: абсолютный в контексте модели бесполезен и шумит.
	if rel := core.RelToWD(p.WorkDir, p.Root); rel != "" && rel != "." {
		fmt.Fprintf(&b, "Корень проекта: %s (рабочий каталог: %s)\n", p.Root, p.WorkDir)
	} else {
		fmt.Fprintf(&b, "Корень проекта: %s — он же рабочий каталог.\n", p.Root)
	}
	if p.GitRoot != "" && p.GitRoot != p.Root {
		fmt.Fprintf(&b, "Репозиторий git: %s\n", p.GitRoot)
	}
	if p.GitRoot == p.Root && p.GitRoot != "" {
		b.WriteString("Репозиторий git: здесь же.\n")
	}
	if p.WorkDir != p.Root {
		b.WriteString("⚠ Рабочий каталог НЕ является корнем проекта: команды вида «go build ./...»\n" +
			"из него не сработают. Запускай с workdir, равным корню, либо указывай пакет явно.\n")
	}

	if len(p.EntryPoints) > 0 {
		fmt.Fprintf(&b, "\nТочки входа (package main): %s\n", strings.Join(p.EntryPoints, ", "))
	}
	if len(p.Build) > 0 {
		fmt.Fprintf(&b, "\nСборка:\n")
		for _, c := range p.Build {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	if len(p.Test) > 0 {
		b.WriteString("Проверка:\n")
		for _, c := range p.Test {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	for _, n := range p.Note {
		fmt.Fprintf(&b, "\n%s\n", n)
	}
	b.WriteString("\nНе гадай команду сборки — бери отсюда, либо спроси через verify {suggest:true}.")

	// Предложение записать в память: факты уже собраны, но без явного
	// «сохрани» они исчезнут вместе с сессией, и проект придётся обследовать
	// заново в следующем запуске.
	if len(p.Remember) > 0 {
		b.WriteString("\n\n# Стоит запомнить между сессиями\n")
		for _, f := range p.Remember {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\nЭти факты уже собраны и проверены по конфигам проекта. Если проектом\n" +
			"будешь заниматься ещё, запиши их одним вызовом, чтобы не искать заново:\n" +
			"remember {action:\"write\", fact:\"<факт>\"}")
	}
	return strings.TrimSpace(b.String())
}

// hProjectInfo — устройство проекта.
func (r *Registry) hProjectInfo(_ context.Context, _ map[string]any) (Result, error) {
	info := r.projectInfo()
	return Result{
		Text:    info.Text(),
		Summary: fmt.Sprintf("корень: %s, стек: %s", core.RelToWD(r.workDir, info.Root), strings.Join(info.Stacks, "+")),
	}, nil
}
