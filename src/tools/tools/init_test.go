package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
)

// initReg — реестр в тестовом каталоге проекта.
func initReg(t *testing.T, work string) *Registry {
	t.Helper()
	return New(Env{WorkDir: work})
}

// TestInitProfileFindsGoProject — /init обязан узнать стек по конфигам,
// иначе он пишет файл из одних плейсхолдеров.
func TestInitProfileFindsGoProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.22\n")

	p := initReg(t, root).InitProfileFor()
	if !hasStack(p.Stacks, "Go") {
		t.Errorf("стек Go не найден: %v", p.Stacks)
	}
	if len(p.Build) == 0 {
		t.Error("команды сборки не определены")
	}
	if len(p.Test) == 0 || !strings.Contains(p.Test[0], "go test") {
		t.Errorf("команда тестов не определена: %v", p.Test)
	}
}

// TestInitProfileFindsNodeProject — второй стек не должен ломать /init.
func TestInitProfileFindsNodeProject(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"x","scripts":{"build":"tsc"}}`)

	p := initReg(t, root).InitProfileFor()
	if !hasStack(p.Stacks, "Node") {
		t.Errorf("стек Node не найден: %v", p.Stacks)
	}
	if !hasCmd(p.Build, "npm run build") {
		t.Errorf("команда сборки Node не найдена: %v", p.Build)
	}
}

// TestInitProfileEmptyProject — на пустом каталоге /init не должен
// выдумывать команды: остаются плейсхолдеры.
func TestInitProfileEmptyProject(t *testing.T) {
	p := initReg(t, t.TempDir()).InitProfileFor()
	if len(p.Build) != 0 || len(p.Test) != 0 {
		t.Errorf("пустой проект: стек не выдумывается, получили %v %v", p.Build, p.Test)
	}
	if p.Stack() != "" {
		t.Errorf("стек пустого проекта должен быть пуст, получили %q", p.Stack())
	}
}

// TestMemoryDocFillsFoundFacts — найденные команды обязаны попасть в файл:
// в этом весь смысл /init.
func TestMemoryDocFillsFoundFacts(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.22\n")

	doc := MemoryDoc(initReg(t, root).InitProfileFor())
	for _, want := range []string{"Стек: Go", "go build", "go test", "## Команды", "## Структура"} {
		if !strings.Contains(doc, want) {
			t.Errorf("в GCLI.md нет %q:\n%s", want, doc)
		}
	}
}

// TestMemoryDocKeepsPlaceholders — неизвестное остаётся видимым
// плейсхолдером, а не выдуманной командой.
func TestMemoryDocKeepsPlaceholders(t *testing.T) {
	doc := MemoryDoc(InitProfile{WorkDir: t.TempDir(), Root: t.TempDir()})
	for _, want := range []string{"<что это за проект>", "<команда сборки>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("плейсхолдер %q потерялся:\n%s", want, doc)
		}
	}
}

// TestPermissionRulesCoverStackCommands — правила должны разрешать ровно
// те команды, которые /init нашёл в проекте: иначе агент будет спрашивать
// разрешение на каждый go test.
func TestPermissionRulesCoverStackCommands(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.22\n")

	rules := PermissionRules(initReg(t, root).InitProfileFor())
	var rs core.Rules
	if err := (&core.PermissionCfg{Rules: rules}).AddTo(&rs); err != nil {
		t.Fatalf("сгенерированные правила не разбираются: %v\n%v", err, rules)
	}
	for _, cmd := range []string{"go build ./...", "go test ./..."} {
		if d, _, ok := rs.Decide("bash", cmd); !ok || d != core.PermAllow {
			t.Errorf("команда %q должна быть разрешена, получили %s %v", cmd, d, ok)
		}
	}
	// Разрушительное и внешнее остаётся вопросом — это и есть смысл
	// раздельных режимов.
	for _, cmd := range []string{"rm -rf build", "git push origin main"} {
		if d, _, ok := rs.Decide("bash", cmd); !ok || d != core.PermAsk {
			t.Errorf("команда %q должна спрашивать, получили %s %v", cmd, d, ok)
		}
	}
}

// TestPermissionRulesDenySecrets — секреты не нужны для работы с кодом.
func TestPermissionRulesDenySecrets(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module m\n\ngo 1.22\n")

	var rs core.Rules
	rules := PermissionRules(initReg(t, root).InitProfileFor())
	if err := (&core.PermissionCfg{Rules: rules}).AddTo(&rs); err != nil {
		t.Fatalf("разбор правил: %v", err)
	}
	if d, _, ok := rs.Decide("read_file", "app/.env"); !ok || d != core.PermDeny {
		t.Errorf("чтение .env должно быть запрещено, получили %s %v", d, ok)
	}
}

// TestPermissionRulesNodeStack — правила подстраиваются под стек.
func TestPermissionRulesNodeStack(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"x"}`)

	rules := PermissionRules(initReg(t, root).InitProfileFor())
	var rs core.Rules
	if err := (&core.PermissionCfg{Rules: rules}).AddTo(&rs); err != nil {
		t.Fatalf("разбор правил: %v\n%v", err, rules)
	}
	if d, _, _ := rs.Decide("bash", "npm run build"); d != core.PermAllow {
		t.Errorf("npm run build должен быть разрешён, получили %s", d)
	}
	if d, _, _ := rs.Decide("bash", "npm publish"); d != core.PermAsk {
		t.Errorf("npm publish должен спрашивать, получили %s", d)
	}
	// Go-правила не должны попадать в Node-проект.
	for _, r := range rules {
		if strings.HasPrefix(r, "bash(go ") {
			t.Errorf("Go-правило в Node-проекте: %q", r)
		}
	}
}

// TestPermissionRulesDocParses — сгенерированный gcli.json обязан быть
// читаемым движком правил, иначе /init создаст файл, который не работает.
func TestPermissionRulesDocParses(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module m\n\ngo 1.22\n")
	doc := core.ProjectConfigDoc(PermissionRules(initReg(t, root).InitProfileFor()))

	path := filepath.Join(root, "gcli.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("запись: %v", err)
	}
	cfg, _, err := core.LoadProjectConfig(root)
	if err != nil {
		t.Fatalf("сгенерированный gcli.json не разбирается: %v\n%s", err, doc)
	}
	if cfg == nil || cfg.Permissions == nil || len(cfg.Permissions.Rules) == 0 {
		t.Fatalf("правил в сгенерированном файле нет:\n%s", doc)
	}
}

// hasCmd — есть ли команда в списке. Своё имя, потому что containsStr в
// пакете уже занят другой сигнатурой.
func hasCmd(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
