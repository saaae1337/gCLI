package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// ---------- project_info ----------

// goProject — минимальный Go-модуль с точкой входа.
func goProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	return root
}

// TestProjectInfoFindsGoModuleUp — корень ищется ВВЕРХ от рабочего каталога.
// Именно этот порядок обязателен: рабочий каталог почти всегда в глубоком
// подкаталоге, и поиск вниз уводил в vendor.
func TestProjectInfoFindsGoModuleUp(t *testing.T) {
	root := goProject(t)
	deep := filepath.Join(root, "src", "tools")
	mustWrite(t, filepath.Join(deep, "tools.go"), "package tools\n")

	r := newTestReg(t, deep)
	res := mustRun(t, r, "project_info", nil)

	if !strings.Contains(res.Text, "Go") {
		t.Errorf("стек Go не определён:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "НЕ является корнем проекта") {
		t.Errorf("не предупреждено, что рабочий каталог — подкаталог:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "go build ./...") {
		t.Errorf("не предложена команда сборки:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "remember") {
		t.Errorf("нет подсказки запомнить факты о проекте:\n%s", res.Text)
	}
}

// TestProjectInfoFindsEntryPoints — каталоги с package main попадают в отчёт.
func TestProjectInfoFindsEntryPoints(t *testing.T) {
	root := goProject(t)
	mustWrite(t, filepath.Join(root, "cmd", "gcli", "main.go"), "package main\n\nfunc main() {}\n")

	r := newTestReg(t, root)
	res := mustRun(t, r, "project_info", nil)
	if !strings.Contains(res.Text, "cmd/gcli") {
		t.Errorf("точка входа не найдена:\n%s", res.Text)
	}
}

// TestProjectInfoIgnoresTestFiles — _test.go не точка входа.
func TestProjectInfoIgnoresTestFiles(t *testing.T) {
	root := goProject(t)
	mustWrite(t, filepath.Join(root, "internal", "x", "x_test.go"), "package main\n")

	r := newTestReg(t, root)
	res := mustRun(t, r, "project_info", nil)
	if strings.Contains(res.Text, "internal/x") {
		t.Errorf("_test.go принят за точку входа:\n%s", res.Text)
	}
}

// TestFindModuleUpStopsAtRoot — go.mod не ищется бесконечно вверх.
func TestFindModuleUpStopsAtRoot(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		dir = filepath.Dir(dir)
	}
	if p, stack := findModuleUp(dir); p != "" {
		t.Errorf("нашёл %s (%s) за пределами проекта", p, stack)
	}
}

// TestFindModuleUpFromNested — конфиг в родителе находится с любой глубины.
func TestFindModuleUpFromNested(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), "{\"name\":\"x\"}\n")
	deep := filepath.Join(root, "a", "b", "c")
	mustWrite(t, filepath.Join(deep, "f.js"), "1\n")

	p, stack := findModuleUp(deep)
	if p != filepath.Join(root, "package.json") {
		t.Fatalf("найден %q вместо package.json", p)
	}
	if stack != "Node" {
		t.Errorf("стек %q вместо Node", stack)
	}
}

// TestFindModuleDownSkipsVendor — модули в vendor и node_modules не считаются
// проектом: иначе корнем становился бы поддерево зависимостей.
func TestFindModuleDownSkipsVendor(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "app", "vendor", "dep", "go.mod"), "module dep\n")
	mustWrite(t, filepath.Join(root, "app", "node_modules", "p", "package.json"), "{}\n")
	mustWrite(t, filepath.Join(root, "real", "go.mod"), "module real\n")

	p, stack := findModuleDown(root, filepath.Join(root, "app"))
	if p != filepath.Join(root, "real", "go.mod") {
		t.Fatalf("найден %q (стек %q) вместо настоящего модуля", p, stack)
	}
	if stack != "Go" {
		t.Errorf("стек %q вместо Go", stack)
	}
}

// TestFindModuleDownIgnoresWorkdirSubtree — модуль внутри рабочего каталога
// чужий: он лежит в поддереве, из которого запускают.
func TestFindModuleDownIgnoresWorkdirSubtree(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "deep", "sub", "go.mod"), "module sub\n")
	mustWrite(t, filepath.Join(root, "go.mod"), "module top\n")

	p, _ := findModuleDown(root, filepath.Join(root, "deep", "sub"))
	if p != filepath.Join(root, "go.mod") {
		t.Errorf("найден %q: взят модуль из поддерева рабочего каталога", p)
	}
}

// TestFindModuleDownNoRoot — без корня репозитория искать нечего.
func TestFindModuleDownNoRoot(t *testing.T) {
	if p, _ := findModuleDown("", t.TempDir()); p != "" {
		t.Errorf("без корня репозитория найден %q", p)
	}
}

// TestFindMainPackagesRespectsDepth — обход ограничен по глубине: точка входа
// глубже maxDepth не должна стоить обхода всего репозитория.
func TestFindMainPackagesRespectsDepth(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a", "b", "c", "d", "e", "main.go"), "package main\n")

	if got := findMainPackages(root); len(got) != 0 {
		t.Errorf("найдено %v за пределами допустимой глубины", got)
	}
}

// TestProjectInfoSkipsHugeDirs — служебные каталоги не обходятся.
func TestProjectInfoSkipsHugeDirs(t *testing.T) {
	root := goProject(t)
	mustWrite(t, filepath.Join(root, "vendor", "x", "main.go"), "package main\n")
	mustWrite(t, filepath.Join(root, "node_modules", "y", "main.go"), "package main\n")

	r := newTestReg(t, root)
	res := mustRun(t, r, "project_info", nil)
	if strings.Contains(res.Text, "vendor") || strings.Contains(res.Text, "node_modules") {
		t.Errorf("обход зашёл в служебные каталоги:\n%s", res.Text)
	}
}

// TestProjectInfoNonGitProject — без git корень всё равно находится, и
// внятно сказано, что сравнивать не с чем.
func TestProjectInfoNonGitProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "pyproject.toml"), "[project]\nname='x'\n")

	r := newTestReg(t, root)
	res := mustRun(t, r, "project_info", nil)
	if !strings.Contains(res.Text, "Python") {
		t.Errorf("стек Python не определён:\n%s", res.Text)
	}
	if res.Error != "" {
		t.Errorf("project_info вернул ошибку вне git: %v", res.Error)
	}
}

// TestProjectInfoNodeBuildCommands — по package.json предлагаются npm-команды.
func TestProjectInfoNodeBuildCommands(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), "{\"name\":\"x\"}\n")

	r := newTestReg(t, root)
	res := mustRun(t, r, "project_info", nil)
	if !strings.Contains(res.Text, "npm test") {
		t.Errorf("не предложена проверка для Node:\n%s", res.Text)
	}
}

// TestProjectInfoEmptyDir — пустой каталог не роняет инструмент.
func TestProjectInfoEmptyDir(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	res := mustRun(t, r, "project_info", nil)
	if res.Text == "" {
		t.Error("пустой отчёт для пустого каталога")
	}
	if res.Error != "" {
		t.Errorf("ошибка на пустом каталоге: %v", res.Error)
	}
}

// TestProjectInfoRootEqualsWorkDir — в корне проекта предупреждения нет.
func TestProjectInfoRootEqualsWorkDir(t *testing.T) {
	root := goProject(t)
	r := newTestReg(t, root)
	res := mustRun(t, r, "project_info", nil)
	if strings.Contains(res.Text, "НЕ является корнем") {
		t.Errorf("ложное предупреждение, хотя мы в корне:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "он же рабочий каталог") {
		t.Errorf("не сказано, что корень совпадает с рабочим каталогом:\n%s", res.Text)
	}
}

// TestModName — имя модуля читается из go.mod.
func TestModName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "go.mod")
	mustWrite(t, p, "// комментарий\nmodule example.com/x\n\ngo 1.22\n")

	if name, ok := modName(p); !ok || name != "example.com/x" {
		t.Errorf("modName = %q, %v", name, ok)
	}
	// Не go.mod — имени модуля нет.
	mustWrite(t, filepath.Join(dir, "package.json"), "{}")
	if _, ok := modName(filepath.Join(dir, "package.json")); ok {
		t.Error("modName выдумал модуль для package.json")
	}
	// Отсутствующий файл — не паника.
	if _, ok := modName(filepath.Join(dir, "нет-такого.mod")); ok {
		t.Error("modName вернул модуль для несуществующего файла")
	}
}

// TestDetectCommandsByMarker — команды собираются по конфигам проекта.
func TestDetectCommandsByMarker(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module m\n")
	mustWrite(t, filepath.Join(dir, "Makefile"), "test:\n\techo ok\n")

	build, test := detectCommands(dir, []string{"cmd/gcli"})
	if len(build) == 0 || !strings.HasPrefix(build[0], "go build") {
		t.Errorf("команды сборки Go не предложены: %v", build)
	}
	if len(test) == 0 {
		t.Errorf("команды проверки не предложены: %v", test)
	}
	// Точка входа не в корне — должна быть команда с явным пакетом.
	joined := strings.Join(build, "\n")
	if !strings.Contains(joined, "./cmd/gcli") {
		t.Errorf("нет команды со сборкой бинарника: %v", build)
	}
}

// TestInsideDir — самодостаточная проверка «путь внутри каталога».
func TestInsideDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "proj")
	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(base, "a", "b.go"), true},
		{base, true},
		{filepath.Join(base, "..", "other"), false},
		{t.TempDir(), false},
	}
	for _, c := range cases {
		if got := insideDir(base, c.path); got != c.want {
			t.Errorf("insideDir(%q) = %v, ожидалось %v", c.path, got, c.want)
		}
	}
	if insideDir("", base) {
		t.Error("insideDir с пустым каталогом должен быть false")
	}
}

// TestSkipWalkDir — служебные каталоги исключены, исходники — нет.
func TestSkipWalkDir(t *testing.T) {
	for _, n := range []string{".git", "vendor", "node_modules", "dist", "build", ".gcli", "testdata"} {
		if !skipWalkDir(n) {
			t.Errorf("каталог %q обходится, а не должен", n)
		}
	}
	for _, n := range []string{"src", "cmd", "internal", "tools"} {
		if skipWalkDir(n) {
			t.Errorf("каталог %q пропускается, а не должен", n)
		}
	}
}

// TestProjectInfoNoMainNoBuildDot — модуль без main: предлагается «go build .».
func TestProjectInfoNoMainNoBuildDot(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module m\n")
	mustWrite(t, filepath.Join(root, "lib.go"), "package m\n")

	r := newTestReg(t, root)
	res := mustRun(t, r, "project_info", nil)
	if !strings.Contains(res.Text, "go build .") {
		t.Errorf("для модуля без точки входа нет команды сборки:\n%s", res.Text)
	}
}

// TestFindMainPackagesMissingRoot — несуществующий корень не роняет разведку:
// WalkDir вернёт ошибку на первой же записи, и d будет nil.
func TestFindMainPackagesMissingRoot(t *testing.T) {
	if got := findMainPackages(filepath.Join(t.TempDir(), "нет-такого")); got != nil {
		t.Errorf("для несуществующего корня получено %v", got)
	}
	if p, stack := findModuleDown(filepath.Join(t.TempDir(), "нет"), ""); p != "" || stack != "" {
		t.Errorf("для несуществующего корня найдено %q (%q)", p, stack)
	}
}

// TestProjectInfoMissingRoot — корень, которого нет, не должен ронять инструмент.
func TestProjectInfoMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такого")
	r := newTestReg(t, missing)
	if _, err := run(t, r, "project_info", nil); err != nil {
		t.Errorf("project_info упал на несуществующем каталоге: %v", err)
	}
}
