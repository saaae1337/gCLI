package main

// Примеры из examples/ проверяем тем же кодом, что и настоящие файлы
// в .gcli/, и отдельно — что front matter написан так, как это принято
// в самом проекте.
//
// Зачем. Примеры разъезжаются с кодом тихо: навык перестаёт парситься
// после правки front matter, расширение — после смены схемы, и README
// продолжает предлагать то, что не работает. Ни один сценарий
// использования это не поймает — до первого «скопировал и не
// сработало».

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/tools"
)

// repoRoot — корень репозитория относительно каталога src.
const repoRoot = ".."

// frontMatter — разобрать front matter файла.
//
// Отдельная функция, а не поле Skill, потому что ParseSkill подставляет
// description из первой строки тела, если в front matter его нет. Для
// примера это не годится: описание обязано быть написано руками, иначе
// агент увидит вместо него «# code-reviewer» — заголовок, который
// ничего не сообщает о том, когда звать навык.
func frontMatter(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---") {
		t.Errorf("%s: нет front matter", filepath.Base(path))
		return nil
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		t.Errorf("%s: front matter не закрыт", filepath.Base(path))
		return nil
	}
	fm := map[string]string{}
	for _, line := range strings.Split(text[3:3+end], "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, ":"); ok {
			fm[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return fm
}

func TestExamplesSkillsParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot, "examples", "skills", "*.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("в examples/skills нет ни одного файла")
	}
	for _, f := range files {
		s, err := tools.ParseSkill(f, "project")
		if err != nil {
			t.Errorf("навык %s не разбирается: %v", filepath.Base(f), err)
			continue
		}
		if !tools.ValidSkillName(s.Name) {
			t.Errorf("навык %s: имя %q не проходит проверку", filepath.Base(f), s.Name)
		}
		if s.Body == "" {
			t.Errorf("навык %s: пустое тело", filepath.Base(f))
		}
		fm := frontMatter(t, f)
		if fm["name"] == "" {
			t.Errorf("навык %s: в front matter нет name", filepath.Base(f))
		}
		if fm["description"] == "" {
			t.Errorf("навык %s: в front matter нет description — агент не поймёт, когда звать навык", filepath.Base(f))
		}
	}
}

func TestExamplesCommandsParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot, "examples", "commands", "*.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("в examples/commands нет ни одного файла")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("команда %s: %v", filepath.Base(f), err)
			continue
		}
		c := parseCustomCommand(string(data))
		if c.Description == "" {
			t.Errorf("команда %s: пустое description — в /help будет пустая строка", filepath.Base(f))
		}
		if len(c.Body) == 0 {
			t.Errorf("команда %s: пустое тело", filepath.Base(f))
		}
		fm := frontMatter(t, f)
		if fm["description"] == "" {
			t.Errorf("команда %s: в front matter нет description", filepath.Base(f))
		}
	}
}

func TestExamplesExtensionsParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot, "examples", "extensions", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("в examples/extensions нет ни одного файла")
	}
	// Копируем в .gcli/extensions рабочего каталога — это ровно тот путь,
	// который читает ExtDirs, и заодно проверяется, что пример кладётся
	// туда, куда велит README.
	work := t.TempDir()
	dir := filepath.Join(work, ".gcli", "extensions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("каталог расширений: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("расширение %s: %v", filepath.Base(f), err)
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), data, 0o600); err != nil {
			t.Fatalf("копирование %s: %v", filepath.Base(f), err)
		}
	}
	r := tools.New(tools.Env{WorkDir: work})
	exts := r.LoadExtensions()
	if len(exts) != len(files) {
		t.Errorf("загрузилось %d расширений из %d — пример не в том формате, который ждёт загрузчик",
			len(exts), len(files))
	}
	for _, e := range exts {
		if e.Name == "" || e.Desc == "" {
			t.Errorf("расширение без имени или описания: %q / %q", e.Name, e.Desc)
		}
		if len(e.Tools) == 0 {
			t.Errorf("расширение %s: ни одного инструмента", e.Name)
		}
	}
}
