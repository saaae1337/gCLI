package subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCustomAgentParsing — разбор .md-файла пользовательского агента.
func TestCustomAgentParsing(t *testing.T) {
	dir := t.TempDir()
	adir := filepath.Join(dir, ".gcli", "agents")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: api-migrator
description: Мигрирует API с v1 на v2
tools: read_file, grep, edit_file
model: glm-4.6
read_only: false
---

Ты — специалист по миграции API.

Правила:
1. Не трогай клиентский код.
`
	if err := os.WriteFile(filepath.Join(adir, "api-migrator.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cas := LoadCustomAgents(dir, filepath.Join(dir, "nohome"))
	if len(cas) != 1 {
		t.Fatalf("агентов: %d, ожидался 1", len(cas))
	}
	ca := cas[0]
	if ca.Name != "api-migrator" {
		t.Errorf("имя: %q", ca.Name)
	}
	if ca.Desc != "Мигрирует API с v1 на v2" {
		t.Errorf("описание: %q", ca.Desc)
	}
	if len(ca.Tools) != 3 || ca.Tools[0] != "read_file" {
		t.Errorf("инструменты: %v", ca.Tools)
	}
	if ca.Model != "glm-4.6" {
		t.Errorf("модель: %q", ca.Model)
	}
	if ca.ReadOnly {
		t.Error("read_only должен быть false")
	}
	if !strings.Contains(ca.Prompt, "миграции API") {
		t.Errorf("тело промпта потеряно: %q", ca.Prompt)
	}
}

// TestCustomAgentProjectOverridesGlobal — проектный агент перекрывает глобального.
func TestCustomAgentProjectOverridesGlobal(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	hdir := filepath.Join(home, "agents")
	wdir := filepath.Join(work, ".gcli", "agents")
	if err := os.MkdirAll(hdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wdir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(hdir, "dup.md"), []byte("---\nname: dup\ndescription: глобальный\n---\n\nГ."), 0o644)
	os.WriteFile(filepath.Join(wdir, "dup.md"), []byte("---\nname: dup\ndescription: проектный\n---\n\nП."), 0o644)

	cas := LoadCustomAgents(work, home)
	if len(cas) != 1 || cas[0].Desc != "проектный" {
		t.Errorf("проектный не перекрыл глобального: %+v", cas)
	}
}

// TestCustomPromptTail — к промпту кастомного агента добавляется общий хвост.
func TestCustomPromptTail(t *testing.T) {
	p := CustomPrompt("Ты — тестовый агент.", PromptContext{WorkDir: "/w", Summary: "задача"})
	if !strings.Contains(p, "Ты — тестовый агент.") {
		t.Error("тело потеряно")
	}
	if !strings.Contains(p, "/w") || !strings.Contains(p, "задача") {
		t.Error("контекст не добавлен")
	}
	if !strings.Contains(p, "Правила вывода") {
		t.Error("хвост правил вывода не добавлен")
	}
}

// TestToolsForNewTypes — новые типы получают корректные наборы инструментов.
func TestToolsForNewTypes(t *testing.T) {
	// frontend обязан видеть screenshot/read_image.
	allow, deny := ToolsFor(TypeFrontend)
	if !contains(allow, "screenshot") || !contains(allow, "read_image") {
		t.Errorf("frontend без глаз: %v", allow)
	}
	if contains(deny, "spawn_agent") == false {
		t.Error("frontend не должен порождать субагентов")
	}
	// planner — только чтение, но с сетью.
	allowP, denyP := ToolsFor(TypePlanner)
	if contains(allowP, "write_file") || contains(allowP, "bash") {
		t.Errorf("planner получил запись: %v", allowP)
	}
	if !contains(denyP, "spawn_agent") {
		t.Error("planner должен быть запрещён spawn_agent")
	}
	// tester может писать и запускать.
	allowT, _ := ToolsFor(TypeTester)
	if !contains(allowT, "write_file") || !contains(allowT, "bash") {
		t.Errorf("tester без записи/запуска: %v", allowT)
	}
	// никто не получает ask_user в allow.
	for _, tp := range Types {
		a, _ := ToolsFor(tp)
		if contains(a, "ask_user") {
			t.Errorf("%s получил ask_user", tp)
		}
	}
}

// TestPromptsNewTypesDistinct — промпты новых типов заполнены и различны.
func TestPromptsNewTypesDistinct(t *testing.T) {
	ctx := PromptContext{WorkDir: "/tmp", Summary: "задача"}
	seen := map[string]Type{}
	for _, tp := range Types {
		p := tp.Prompt(ctx)
		if strings.TrimSpace(p) == "" {
			t.Errorf("пустой промпт у %s", tp)
		}
		if !strings.Contains(p, "/tmp") {
			t.Errorf("в промпте %s нет каталога", tp)
		}
		// Промпт frontend обязан требовать скриншот.
		if tp == TypeFrontend && !strings.Contains(p, "screenshot") {
			t.Error("промпт frontend не требует скриншот")
		}
		// Не должно быть дублей между типами.
		key := p[:50]
		if prev, ok := seen[key]; ok {
			t.Errorf("промпты %s и %s совпадают", prev, tp)
		}
		seen[key] = tp
	}
}

// TestAgentTemplate — шаблон агента содержит все секции front matter.
func TestAgentTemplate(t *testing.T) {
	tpl := AgentTemplate
	for _, want := range []string{"name: %s", "description:", "tools:", "model:"} {
		if !strings.Contains(tpl, want) {
			t.Errorf("в шаблоне нет %q", want)
		}
	}
}
