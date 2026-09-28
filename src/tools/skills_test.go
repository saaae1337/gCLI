package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillsDir — создать каталог навыков с одним навыком.
func skillsDir(t *testing.T, name, desc string) string {
	t.Helper()
	dir := t.TempDir()
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n\nИнструкции.\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSkillDescNotCutOff — описание навыка решает, сработает он или нет,
// поэтому в промпт должно попадать целиком, вместе с триггерами.
//
// Раньше стоял предел 110 символов, и описание обрезалось ровно на
// «Применять, когда…»: модель не видела ни одного слова-триггера
// и навык молча не срабатывал.
func TestSkillDescNotCutOff(t *testing.T) {
	dir := skillsDir(t, "review", "Ревью изменений: ищет баги и гонки данных. "+
		"Применять, когда просят проверить код, посмотреть diff, найти ошибку в правке или подготовить изменения к merge.")

	block := skillDesc(mustParse(t, dir, "review").Desc)
	if strings.Contains(block, "...") {
		t.Errorf("описание обрезано: %q", block)
	}
	for _, want := range []string{"diff", "merge", "ошибку в правке"} {
		if !strings.Contains(block, want) {
			t.Errorf("в описании потерян триггер %q: %q", want, block)
		}
	}
}

// TestSkillDescStaysOneLine — многострочное описание склеивается в строку,
// иначе список навыков в промпте расползается.
func TestSkillDescStaysOneLine(t *testing.T) {
	got := skillDesc("Первая строка\nвторая строка\n\nтретья")
	if strings.Contains(got, "\n") {
		t.Errorf("в промпте описание должно быть одной строкой: %q", got)
	}
	if !strings.Contains(got, "третья") {
		t.Errorf("часть описания потеряна: %q", got)
	}
}

// TestSkillDescTrimsAbsurdLength — аварийная обрезка остаётся, но помечает
// обрыв явно, чтобы модель не считала текст полным.
func TestSkillDescTrimsAbsurdLength(t *testing.T) {
	got := skillDesc(strings.Repeat("а", maxSkillDesc*2))
	if len([]rune(got)) > maxSkillDesc+4 {
		t.Errorf("описание не обрезано: %d символов", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("обрыв должен быть помечен: %q", got[len(got)-10:])
	}
}

// TestSkillsPromptBlockKeepsTriggers — сквозная проверка: триггер из
// описания навыка виден в блоке системного промпта.
func TestSkillsPromptBlockKeepsTriggers(t *testing.T) {
	dir := skillsDir(t, "verify", "Финальная проверка перед сдачей: сборка, тесты, формат. "+
		"Применять, когда просят проверить что правки работают или прогнать сборку.")

	// Навыки лежат в <workDir>/.gcli/skills.
	r := &Registry{workDir: dir}
	nested := filepath.Join(dir, ".gcli", "skills")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "verify.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "verify.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	block := r.SkillsPromptBlock()
	if block == "" {
		t.Fatal("блок навыков пуст")
	}
	for _, want := range []string{"verify", "прогнать сборку"} {
		if !strings.Contains(block, want) {
			t.Errorf("в блоке промпта потеряно %q:\n%s", want, block)
		}
	}
}

func mustParse(t *testing.T, dir, name string) Skill {
	t.Helper()
	s, err := ParseSkill(filepath.Join(dir, name+".md"), "тест")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
