package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
	"gcli/tools"
)

// initApp — приложение для /init в каталоге с указанным содержимым.
//
// Изолированный GCLI_HOME обязателен: /init пишет gcli.json в рабочий
// каталог и перечитывает глобальный конфиг — без изоляции тесты трогали бы
// настоящие настройки пользователя.
func initApp(t *testing.T, files map[string]string) (*app, *strings.Builder, string) {
	t.Helper()
	t.Setenv("GCLI_HOME", t.TempDir())
	t.Setenv("GCLI_SANDBOX", "")

	work := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o600); err != nil {
			t.Fatalf("подготовка %s: %v", name, err)
		}
	}
	buf := &strings.Builder{}
	store := core.NewStore()
	store.Ensure()
	tr := tools.New(tools.Env{WorkDir: work})
	return &app{
		repo:    &core.Repo{Store: store, Cfg: core.DefaultConfig()},
		store:   store,
		ui:      testUI(buf),
		workDir: work,
		tools:   tr,
		memory:  tools.NewMemory(work, store),
	}, buf, work
}

// goProject — минимальный проект с go.mod в корне.
func goProject() map[string]string {
	return map[string]string{"go.mod": "module example.com/demo\n\ngo 1.22\n"}
}

// TestInitCreatesBothFiles — /init должен создать и память проекта, и
// правила: по отдельности от каждого половина работы.
func TestInitCreatesBothFiles(t *testing.T) {
	a, buf, work := initApp(t, goProject())
	a.cmdInit("")

	md, err := os.ReadFile(filepath.Join(work, "GCLI.md"))
	if err != nil {
		t.Fatalf("GCLI.md не создан: %v", err)
	}
	if !strings.Contains(string(md), "Стек: Go") {
		t.Errorf("в GCLI.md нет найденного стека:\n%s", md)
	}
	cfg, err := os.ReadFile(filepath.Join(work, "gcli.json"))
	if err != nil {
		t.Fatalf("gcli.json не создан: %v", err)
	}
	if !strings.Contains(string(cfg), "go test") {
		t.Errorf("в gcli.json нет команды тестов:\n%s", cfg)
	}
	got := out(buf)
	if !strings.Contains(got, "GCLI.md") || !strings.Contains(got, "gcli.json") {
		t.Errorf("не сказано, что создано:\n%s", got)
	}
}

// TestInitRulesTakeEffectImmediately — главный смысл gcli.json: правила
// должны действовать в текущей сессии, а не после перезапуска.
func TestInitRulesTakeEffectImmediately(t *testing.T) {
	a, _, _ := initApp(t, goProject())
	a.cmdInit("")

	if d, _, ok := a.rules.Decide("bash", "go test ./..."); !ok || d != core.PermAllow {
		t.Errorf("созданное правило должно действовать сразу, получили %s %v", d, ok)
	}
	if d, _, ok := a.rules.Decide("bash", "git push origin main"); !ok || d != core.PermAsk {
		t.Errorf("push должен спрашивать, получили %s %v", d, ok)
	}
}

// TestInitKeepsExistingFiles — написанное руками не перезаписывается.
func TestInitKeepsExistingFiles(t *testing.T) {
	a, buf, work := initApp(t, map[string]string{
		"go.mod":    "module m\n\ngo 1.22\n",
		"GCLI.md":   "# мои правила\n",
		"gcli.json": `{"permissions":{"rules":["bash(rm *): deny"]}}`,
	})
	a.cmdInit("")

	md, _ := os.ReadFile(filepath.Join(work, "GCLI.md"))
	if !strings.Contains(string(md), "мои правила") {
		t.Errorf("GCLI.md затёрт:\n%s", md)
	}
	cfg, _ := os.ReadFile(filepath.Join(work, "gcli.json"))
	if !strings.Contains(string(cfg), "rm *") {
		t.Errorf("правила затёрты:\n%s", cfg)
	}
	if !strings.Contains(out(buf), "уже есть") {
		t.Errorf("надо сказать, что файлы уже были:\n%s", out(buf))
	}
}

// TestInitKeepsMemoryWhenOnlyMemoryExists — если есть только GCLI.md,
// gcli.json всё равно надо создать: иначе /init придётся вызывать дважды.
func TestInitKeepsMemoryWhenOnlyMemoryExists(t *testing.T) {
	a, _, work := initApp(t, map[string]string{
		"go.mod":  "module m\n\ngo 1.22\n",
		"GCLI.md": "# мои правила\n",
	})
	a.cmdInit("")

	md, _ := os.ReadFile(filepath.Join(work, "GCLI.md"))
	if !strings.Contains(string(md), "мои правила") {
		t.Errorf("GCLI.md затёрт:\n%s", md)
	}
	if _, err := os.Stat(filepath.Join(work, "gcli.json")); err != nil {
		t.Errorf("gcli.json должен был быть создан: %v", err)
	}
}

// TestInitForceOverwrites — /init force переписывает оба файла.
func TestInitForceOverwrites(t *testing.T) {
	a, _, work := initApp(t, map[string]string{
		"go.mod":    "module m\n\ngo 1.22\n",
		"GCLI.md":   "# мои правила\n",
		"gcli.json": `{"permissions":{"rules":["bash(rm *): deny"]}}`,
	})
	a.cmdInit("force")

	md, _ := os.ReadFile(filepath.Join(work, "GCLI.md"))
	if strings.Contains(string(md), "мои правила") {
		t.Error("force должен перезаписать GCLI.md")
	}
	if !strings.Contains(string(md), "Стек: Go") {
		t.Errorf("после force GCLI.md должен быть сгенерирован:\n%s", md)
	}
}

// TestInitEmptyProjectKeepsPlaceholders — на пустом проекте /init не
// придумывает команды: он честно оставляет место для человека.
func TestInitEmptyProjectKeepsPlaceholders(t *testing.T) {
	a, _, work := initApp(t, nil)
	a.cmdInit("")

	md, _ := os.ReadFile(filepath.Join(work, "GCLI.md"))
	if !strings.Contains(string(md), "<команда сборки>") {
		t.Errorf("плейсхолдер должен остаться:\n%s", md)
	}
}

// TestInitShowWritesNothing — /init show только показывает. Проверка
// обязательна: команда, обещанная в подсказке, не должна молча затирать
// файлы проекта.
func TestInitShowWritesNothing(t *testing.T) {
	a, buf, work := initApp(t, map[string]string{
		"go.mod":    "module m\n\ngo 1.22\n",
		"GCLI.md":   "# мои правила\n",
		"gcli.json": `{"permissions":{"rules":["bash(rm *): deny"]}}`,
	})
	a.cmdInit("show")

	md, _ := os.ReadFile(filepath.Join(work, "GCLI.md"))
	if !strings.Contains(string(md), "мои правила") {
		t.Errorf("show не должен трогать файлы:\n%s", md)
	}
	if _, err := os.Stat(filepath.Join(work, "gcli.json")); err != nil {
		t.Errorf("show не должен создавать файлы: %v", err)
	}
	got := out(buf)
	for _, want := range []string{"Go", "go test", "allow", "ask", "deny"} {
		if !strings.Contains(got, want) {
			t.Errorf("в /init show нет %q:\n%s", want, got)
		}
	}
}

// TestInitShowWithoutToolsExplains — когда стек не найден, показать надо
// именно это, а не пустую таблицу.
func TestInitShowWithoutToolsExplains(t *testing.T) {
	a, buf, _ := initApp(t, nil)
	a.cmdInit("show")

	if !strings.Contains(out(buf), "стек не найден") {
		t.Errorf("надо сказать, что стек не найден:\n%s", out(buf))
	}
}
