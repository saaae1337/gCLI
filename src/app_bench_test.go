package main

// Тесты bench: загрузчик задач и локальная проверка ожиданий.
// Сам прогон агента не тестируется — это сетевой сценарий.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadBenchTasksJSONC(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// JSONC с комментарием и висячей запятой.
	write("b-second.json", `{
		// задача на файлы
		"name": "второй",
		"prompt": "сделай файл",
		"expect": {"files_exist": ["x.txt"]},
	}`)
	write("a-first.json", `{
		"name": "первый",
		"prompt": "сделай файл",
		"expect": {"command_ok": "true"}
	}`)
	write("readme.txt", "не задача")

	tasks, err := loadBenchTasks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("задач: %d, ждали 2", len(tasks))
	}
	// Сортировка по имени файла: a-first до b-second.
	if tasks[0].Name != "первый" || tasks[1].Name != "второй" {
		t.Fatalf("порядок: %s, %s", tasks[0].Name, tasks[1].Name)
	}
}

func TestLoadBenchTasksValidation(t *testing.T) {
	dir := t.TempDir()
	bad := []string{
		`{"name":"x"}`,                  // нет prompt
		`{"name":"y","prompt":"делай"}`, // нет критериев
	}
	for i, j := range bad {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("bad%d.json", i)), []byte(j), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadBenchTasks(dir); err == nil {
			t.Fatalf("задача %d должна быть отклонена", i)
		}
		// чистим, чтобы следующий случай не нарывался на прошлую ошибку
		_ = os.Remove(filepath.Join(dir, fmt.Sprintf("bad%d.json", i)))
	}
	if _, err := loadBenchTasks(t.TempDir()); err == nil || !strings.Contains(err.Error(), "нет задач") {
		t.Fatalf("пустой каталог: %v", err)
	}
}

func TestBenchEvaluate(t *testing.T) {
	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, "report.md"), []byte("# Отчёт\nтело"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Всё проходит.
	e := benchExpect{
		FilesExist: []string{"report.md"},
		Contains:   []benchContains{{File: "report.md", Text: "тело"}},
		CommandOK:  "test -f report.md",
	}
	if fails := benchEvaluate(wd, e); len(fails) != 0 {
		t.Fatalf("ждали успех, получили: %v", fails)
	}
	// Три разных провала.
	e2 := benchExpect{
		FilesExist: []string{"нет.md"},
		Contains:   []benchContains{{File: "report.md", Text: "не-такого"}},
		CommandOK:  "exit 3",
	}
	fails := benchEvaluate(wd, e2)
	if len(fails) != 3 {
		t.Fatalf("ждали 3 провала, получили: %v", fails)
	}
}
