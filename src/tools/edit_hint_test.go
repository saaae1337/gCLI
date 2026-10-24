package tools

// Тесты подсказки при неудачной правке: модель должна получать ответ, по
// которому видно, что именно исправить, а не просто «не найдено».

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEditFileSuggestsNearMatch — при несовпадении отступов инструмент
// показывает ближайшую строку: круг «прочитал — не нашёл — перечитал»
// обрывается с первого раза.
func TestEditFileSuggestsNearMatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	body := "package main\n\nfunc main() {\n\treturn nil\n}\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: dir})
	// Файл прочитан — иначе инструмент остановится на запрете.
	r.env.ReadFiles[p] = true

	_, err := r.hEditFile(context.Background(), map[string]any{
		"path":       "a.go",
		"old_string": "    return nil",
		"new_string": "\treturn nil",
	})
	if err == nil {
		t.Fatal("правка с неверным отступом должна была не найти old_string")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ближайшее совпадение") {
		t.Errorf("в ошибке нет подсказки:\n%s", msg)
	}
	if !strings.Contains(msg, "return nil") {
		t.Errorf("в подсказке нет самой строки:\n%s", msg)
	}
}
