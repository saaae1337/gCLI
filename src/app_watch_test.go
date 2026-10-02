package main

// Тесты чистых частей watch-режима: обход дерева и разница снимков.
// Сам цикл с агентом не тестируется — он интерактивный.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchFileMapAndChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "x", "i.js"), []byte("垃圾"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := watchFileMap(dir)
	if _, ok := st[filepath.Join(dir, "main.go")]; !ok {
		t.Fatal("main.go не отслеживается")
	}
	if _, ok := st[filepath.Join(dir, "node_modules", "x", "i.js")]; ok {
		t.Fatal("node_modules не должен отслеживаться")
	}
	if watchChanged(st, st) {
		t.Fatal("одинаковые снимки не должны считаться изменением")
	}
	// Изменение содержимого.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main // v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st2 := watchFileMap(dir)
	if !watchChanged(st, st2) {
		t.Fatal("изменённый файл не замечен")
	}
	// Удаление.
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	if !watchChanged(st2, watchFileMap(dir)) {
		t.Fatal("удаление файла не замечено")
	}
	// Создание.
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !watchChanged(st2, watchFileMap(dir)) {
		t.Fatal("создание файла не замечено")
	}
}

func TestWatchRunCommand(t *testing.T) {
	out, code, timedOut := watchRunCommand(t.TempDir(), "echo ok", time.Minute)
	if code != 0 || timedOut || !containsStr(out, "ok") {
		t.Fatalf("ok-команда: code=%d out=%q timeout=%v", code, out, timedOut)
	}
	out, code, _ = watchRunCommand(t.TempDir(), "echo boom >&2; exit 3", time.Minute)
	if code != 3 {
		t.Fatalf("код выхода: %d, out=%q", code, out)
	}
	if out2, _, timeout := watchRunCommand(t.TempDir(), "sleep 5", 300*time.Millisecond); timeout || out2 != "" {
		_ = out2
	}
}

func TestTailChars(t *testing.T) {
	if got := tailChars("abcdef", 3); got != "def" {
		t.Fatalf("tail: %q", got)
	}
	if got := tailChars("abc", 10); got != "abc" {
		t.Fatalf("короткая строка: %q", got)
	}
	if got := tailChars("  abc  ", 10); got != "abc" {
		t.Fatalf("обрезка пробелов: %q", got)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
