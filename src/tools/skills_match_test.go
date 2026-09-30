package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillHome — изолированный ~/.gcli: без него тесты видят реальные
// навыки пользователя и их состояние зависит от машины.
func skillHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".gcli")
	t.Setenv("GCLI_HOME", home)
	return home
}

// skillFile — положить навык в <workDir>/.gcli/skills и вернуть workDir.
func skillFile(t *testing.T, name, content string) string {
	t.Helper()
	work := t.TempDir()
	writeSkillPlain(t, work, name, content)
	return work
}

// writeSkillPlain — положить навык в <workDir>/.gcli/skills.
func writeSkillPlain(t *testing.T, work, name, content string) error {
	t.Helper()
	dir := filepath.Join(work, ".gcli", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644)
}

func loadSkill(t *testing.T, r *Registry, args map[string]any) (Result, error) {
	t.Helper()
	r.RegisterSkills()
	return r.hLoadSkill(context.Background(), args)
}

func TestLoadSkillByName(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "review", "---\nname: review\ndescription: Ревью кода\n---\n\nШаги ревью.\n")
	r := New(Env{WorkDir: work})
	res, err := loadSkill(t, r, map[string]any{"name": "review"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Шаги ревью") {
		t.Errorf("тело навыка не отдано:\n%s", res.Text)
	}
	if !strings.Contains(res.Summary, "review") {
		t.Errorf("сводка: %s", res.Summary)
	}
}

func TestLoadSkillRejectsUnknownName(t *testing.T) {
	skillHome(t)
	r := New(Env{WorkDir: t.TempDir()})
	_, err := loadSkill(t, r, map[string]any{"name": "нет-такого"})
	if err == nil || !strings.Contains(err.Error(), "не найден") {
		t.Errorf("неизвестное имя не отклонено: %v", err)
	}
}

func TestLoadSkillRequiresNameOrQuery(t *testing.T) {
	skillHome(t)
	r := New(Env{WorkDir: t.TempDir()})
	_, err := loadSkill(t, r, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "name навыка или query") {
		t.Errorf("пустой вызов не отклонён: %v", err)
	}
}

func TestLoadSkillExpandsArguments(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "audit", "---\nname: audit\ndescription: Аудит\n---\n\nПроверь $ARGUMENTS и почини.\n")
	r := New(Env{WorkDir: work})
	res, err := loadSkill(t, r, map[string]any{
		"name": "audit",
		"args": "src/tools/multi.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "Проверь $ARGUMENTS") {
		t.Errorf("маркер не подставлен:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "Проверь src/tools/multi.go и почини") {
		t.Errorf("аргумент подставлен не туда:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "$ARGUMENTS = src/tools/multi.go") {
		t.Errorf("в шапке нет значения аргумента:\n%s", res.Text)
	}
}

func TestLoadSkillKeepsMarkerWithoutArgs(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "audit", "---\nname: audit\ndescription: Аудит\n---\n\nПроверь $ARGUMENTS.\n")
	r := New(Env{WorkDir: work})
	res, err := loadSkill(t, r, map[string]any{"name": "audit"})
	if err != nil {
		t.Fatal(err)
	}
	// Пустая подстановка превратила бы инструкцию в «Проверь .» — модель бы
	// не поняла, что аргумент забыт. Маркер обязан остаться видимым.
	if !strings.Contains(res.Text, "$ARGUMENTS") {
		t.Errorf("маркер пропал без args:\n%s", res.Text)
	}
}

func TestLoadSkillQueryUsesItAsArgs(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "audit", "---\nname: audit\ndescription: Аудит\n---\n\nПроверь $ARGUMENTS.\n")
	r := New(Env{WorkDir: work})
	res, err := loadSkill(t, r, map[string]any{
		"query": "аудит доступа к памяти",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Проверь аудит доступа к памяти") {
		t.Errorf("query не подставлен в $ARGUMENTS:\n%s", res.Text)
	}
}

func TestLoadSkillPicksByQuery(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "review", "---\nname: code-review\ndescription: Ревью изменений: ищет баги и гонки данных\nwhen: посмотреть diff, подготовить к merge\n---\n\nПравила ревью.\n")
	if err := writeSkillPlain(t, work, "frontend-dev", "---\nname: frontend-dev\ndescription: Вёрстка HTML и CSS, адаптив, тёмная тема\n---\n\nПравила вёрстки.\n"); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: work})
	res, err := loadSkill(t, r, map[string]any{"query": "посмотри diff перед merge"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Правила ревью") {
		t.Errorf("навык подобран неверно:\n%s", res.Text)
	}
}

func TestLoadSkillQueryMissExplains(t *testing.T) {
	skillHome(t)
	r := New(Env{WorkDir: t.TempDir()})
	_, err := loadSkill(t, r, map[string]any{"query": "квантовая хромодинамика"})
	if err == nil || !strings.Contains(err.Error(), "не подобрался") {
		t.Errorf("неудачный подбор не объяснён: %v", err)
	}
}

func TestLoadSkillRespectsSkillOff(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "audit", "---\nname: audit\ndescription: Аудит\n---\n\nПроверки.\n")
	r := New(Env{WorkDir: work, SkillsOff: []string{"audit"}})
	_, err := loadSkill(t, r, map[string]any{"name": "audit"})
	if err == nil || !strings.Contains(err.Error(), "выключен") {
		t.Errorf("выключенный навык отдан: %v", err)
	}
}

func TestSkillWhenGoesToPrompt(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "audit", "---\nname: audit\ndescription: Аудит\nwhen: перед merge, по чужому PR\n---\n\nПроверки.\n")
	r := New(Env{WorkDir: work})
	block := r.SkillsPromptBlock()
	for _, want := range []string{"audit", "перед merge", "чужому PR"} {
		if !strings.Contains(block, want) {
			t.Errorf("в блоке промпта потеряно %q:\n%s", want, block)
		}
	}
}

func TestParseSkillReadsWhen(t *testing.T) {
	work := skillFile(t, "x", "---\nname: x\ndescription: d\nwhen: при словах foo bar\n---\n\nтело\n")
	s := mustParse(t, filepath.Join(work, ".gcli", "skills"), "x")
	if s.When != "при словах foo bar" {
		t.Errorf("when не разобран: %q", s.When)
	}
	if !strings.Contains(s.Triggers(), "foo") {
		t.Errorf("when не попал в триггеры: %q", s.Triggers())
	}
}

func TestScoreSkillPrefersNameOverDescription(t *testing.T) {
	byName := Skill{Name: "review", Desc: "проверка изменений"}
	byDesc := Skill{Name: "verify-changes", Desc: "финальная проверка перед сдачей, сборка и тесты"}
	if got := ScoreSkill(byName, "сделай review").Score; got < 4 {
		t.Errorf("совпадение по имени должно давать 4 и выше, получено %d", got)
	}
	if got := ScoreSkill(byDesc, "сделай review").Score; got != 0 {
		t.Errorf("чужое слово не должно поднимать оценку: %d", got)
	}
}

func TestScoreSkillIgnoresStopWords(t *testing.T) {
	s := Skill{Name: "docs-writer", Desc: "написание текстов"}
	if got := ScoreSkill(s, "для меня и в на с"); got.Score != 0 {
		t.Errorf("стоп-слова подняли оценку: %+v", got)
	}
}

func TestRankSkillsStableForEqualScores(t *testing.T) {
	skills := []Skill{
		{Name: "zeta", Desc: "общее описание"},
		{Name: "alpha", Desc: "общее описание"},
		{Name: "mid", Desc: "общее описание"},
	}
	got := RankSkills(skills, "общее описание")
	names := []string{got[0].Skill.Name, got[1].Skill.Name, got[2].Skill.Name}
	if strings.Join(names, ",") != "alpha,mid,zeta" {
		t.Errorf("порядок при равных оценках нестабилен: %v", names)
	}
}

func TestMatchSkillSkipsDisabled(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "review", "---\nname: code-review\ndescription: Ревью кода, баги\n---\n\nПравила ревью.\n")
	r := New(Env{WorkDir: work, SkillsOff: []string{"code-review"}})
	if _, err := r.MatchSkill("ревью кода баги"); err == nil {
		t.Errorf("выключенный навык не должен подбираться")
	}
}

// TestLoadSkillSuggestsAlternatives — подобранный навык не всегда единственный:
// похожие полезно назвать, иначе модель выбирает наугад.
func TestLoadSkillSuggestsAlternatives(t *testing.T) {
	skillHome(t)
	work := skillFile(t, "review", "---\nname: code-review\ndescription: Ревью изменений: баги и гонки данных\n---\n\nПравила ревью.\n")
	if err := writeSkillPlain(t, work, "changes", "---\nname: refactoring\ndescription: Ревью изменений: баги и гонки данных, убор дублей\n---\n\nПравила чистки.\n"); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: work})
	res, err := loadSkill(t, r, map[string]any{"query": "ревью изменений баги гонки"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Похожие навыки:") {
		t.Errorf("в ответе нет подсказки похожих навыков:\n%s", res.Text)
	}
}

func TestExpandSkillArgsLeavesBodyWithoutMarker(t *testing.T) {
	if got := ExpandSkillArgs("без маркера", "x"); got != "без маркера" {
		t.Errorf("тело изменено без маркера: %q", got)
	}
}

func TestBuiltinBatchSkillExists(t *testing.T) {
	s := builtinByName("batch-and-delegate")
	if s == nil {
		t.Fatal("встроенный навык про пакетный режим не найден")
	}
	if s.When == "" {
		t.Errorf("у встроенного навыка нет триггеров when — автоподбор по нему не заработает")
	}
	for _, want := range []string{"multi_read", "spawn_agents"} {
		if !strings.Contains(s.Body, want) {
			t.Errorf("в навыке не описан %s", want)
		}
	}
}
