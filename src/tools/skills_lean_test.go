package tools

// Тесты экономного блока навыков: под задачу идёт описание, а не весь
// список — но имена остаются, и навык по-прежнему можно вызвать.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
)

// projectSkill — положить навык в .gcli/skills проекта.
func projectSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	nested := filepath.Join(dir, ".gcli", "skills")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n\nИнструкции.\n"
	if err := os.WriteFile(filepath.Join(nested, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// manySkillsDir — каталог с несколькими навыками (описания длинные).
func manySkillsDir(t *testing.T) string {
	t.Helper()
	// Глобальные навыки пользователя не должны влиять на проверку.
	t.Setenv("GCLI_HOME", t.TempDir())
	dir := t.TempDir()
	nested := filepath.Join(dir, ".gcli", "skills")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"review", "commit", "migrate", "profile", "benchmark"}
	for i, n := range names {
		desc := strings.Repeat("Подробное описание навыка "+n+" для работы с кодом. ", 12)
		desc += "Применять, когда просят " + n + " и разбираются с тестами."
		body := "---\nname: " + n + "\ndescription: " + desc + "\n---\n\n# " + n + "\n\nИнструкции.\n"
		if err := os.WriteFile(filepath.Join(nested, n+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	return dir
}

// TestSkillsPromptBlockForIsLeaner — экономный блок заметно меньше
// полного, но в нём остаются имена всех навыков: навык по-прежнему можно
// найти и вызвать.
func TestSkillsPromptBlockForIsLeaner(t *testing.T) {
	dir := manySkillsDir(t)
	r := &Registry{workDir: dir}

	full := r.SkillsPromptBlock()
	lean := r.SkillsPromptBlockFor("надо проверить ревью и поправить тесты")
	if lean == "" {
		t.Fatal("экономный блок пуст")
	}
	if core.ApproxTokens(lean) >= core.ApproxTokens(full) {
		t.Errorf("экономный блок не меньше полного: %d против %d",
			core.ApproxTokens(lean), core.ApproxTokens(full))
	}
	// Имена всех навыков на месте.
	for _, n := range []string{"review", "commit", "migrate", "profile", "benchmark"} {
		if !strings.Contains(lean, n) {
			t.Errorf("в экономном блоке потерян навык %q:\n%s", n, lean)
		}
	}
}

// TestSkillsPromptBlockForDescribesMatch — подходящие навыки получают
// описание, остальные — только имя.
func TestSkillsPromptBlockForDescribesMatch(t *testing.T) {
	dir := manySkillsDir(t)
	r := &Registry{workDir: dir}

	lean := r.SkillsPromptBlockFor("сделай ревью изменений и проверь diff")
	// Описаний немного: столько навыков и стоит раскрывать.
	described := 0
	for _, line := range strings.Split(lean, "\n") {
		if strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "- прочие:") {
			described++
		}
	}
	if described == 0 {
		t.Errorf("в блоке нет ни одного описанного навыка:\n%s", lean)
	}
	if described > skillsLeanTop {
		t.Errorf("описаний %d, ожидалось не больше %d:\n%s", described, skillsLeanTop, lean)
	}
	// Описание чужого навыка в блоке не должно быть.
	if strings.Contains(lean, "Подробное описание навыка benchmark") {
		t.Errorf("в блоке описание неподходящего навыка:\n%s", lean)
	}
}

// TestSkillsPromptBlockForKeepsShortList — короткий список не урезается:
// экономить нечего, а потерять подсказку жалко.
func TestSkillsPromptBlockForKeepsShortList(t *testing.T) {
	t.Setenv("GCLI_HOME", t.TempDir())
	dir := t.TempDir()
	projectSkill(t, dir, "review", "Ревью изменений: ищет баги.")
	r := &Registry{workDir: dir}
	// Встроенные навыки выключаем: иначе список не короткий.
	for i := range builtinSkills {
		r.SetSkillsOff(append(r.skillsOff, builtinSkills[i].Name))
	}
	lean := r.SkillsPromptBlockFor("что угодно")
	if !strings.Contains(lean, "Ревью изменений") {
		t.Errorf("короткий список потерял описание:\n%s", lean)
	}
}
