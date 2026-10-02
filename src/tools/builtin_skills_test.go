package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinSkillsExist — встроенные навыки на месте и видны в списке.
func TestBuiltinSkillsExist(t *testing.T) {
	skills := BuiltinSkills()
	if len(skills) < 3 {
		t.Fatalf("ожидалось минимум 3 встроенных навыка, получено %d", len(skills))
	}
	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
		if s.Body == "" || s.Desc == "" {
			t.Errorf("навык %s: пустое тело или описание", s.Name)
		}
	}
	for _, want := range []string{"frontend-dev", "design-review", "frontend-frameworks"} {
		if !names[want] {
			t.Errorf("нет встроенного навыка %s", want)
		}
	}
}

// TestBuiltinSkillsInRegistry — LoadSkills отдаёт встроенные навыки.
func TestBuiltinSkillsInRegistry(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	skills := r.LoadSkills()
	found := false
	for _, s := range skills {
		if s.Name == "frontend-dev" {
			found = true
			if s.Scope != "встроенный" {
				t.Errorf("scope встроенного навыка: %q", s.Scope)
			}
		}
	}
	if !found {
		t.Error("frontend-dev не попал в реестр навыков")
	}
}

// TestUserSkillOverridesBuiltin — пользовательский навык перекрывает встроенный.
func TestUserSkillOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	sdir := filepath.Join(dir, ".gcli", "skills")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: frontend-dev\ndescription: свой вариант\n---\n\nМои правила."
	if err := os.WriteFile(filepath.Join(sdir, "frontend-dev.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: dir})
	var found *Skill
	for i := range r.LoadSkills() {
		if r.LoadSkills()[i].Name == "frontend-dev" {
			found = &r.LoadSkills()[i]
		}
	}
	if found == nil {
		t.Fatal("frontend-dev не найден")
	}
	if found.Scope != "проект" || !strings.Contains(found.Body, "Мои правила") {
		t.Errorf("пользовательский навык не перекрыл встроенный: %+v", found)
	}
}

// TestLoadSkillsBuiltinPromptBlock — встроенные навыки попадают в промпт.
func TestLoadSkillsBuiltinPromptBlock(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	block := r.SkillsPromptBlock()
	if !strings.Contains(block, "frontend-dev") {
		t.Error("промпт не содержит встроенный навык frontend-dev")
	}
}
