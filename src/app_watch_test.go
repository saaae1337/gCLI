package main

// Тесты чистых частей watch-режима: обход дерева и разница снимков.
// Сам цикл с агентом не тестируется — он интерактивный.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gcli/tools"
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

// shellCmd — команда с нужным кодом возврата для оболочки, которой
// реально пойдёт watch.
//
// Тест не должен зависеть от платформы И от того, что машина думает
// про оболочку: watch берёт tools.ShellCommand (на Windows это Git Bash,
// если установлен, иначе cmd), и проверять надо именно его поведение.
// Поэтому спрашиваем ту же функцию, а не подставляем синтаксис cmd.exe:
// «exit /b 3» для Git Bash — не число, «exit 3» для cmd — тоже не выход.
func shellCmd(code int) string {
	shell, _ := tools.ShellCommand("")
	if strings.Contains(strings.ToLower(shell), "cmd") {
		return fmt.Sprintf("cmd /c exit /b %d", code)
	}
	return fmt.Sprintf("exit %d", code)
}

// slowCmd — команда, которая гарантированно живёт дольше таймаута.
func slowCmd() string {
	if strings.Contains(strings.ToLower(shellOf()), "cmd") {
		return "ping -n 6 127.0.0.1 >nul"
	}
	return "sleep 5"
}

func shellOf() string {
	shell, _ := tools.ShellCommand("")
	return shell
}

func TestWatchRunCommand(t *testing.T) {
	out, code, timedOut := watchRunCommand(t.TempDir(), "echo ok", time.Minute)
	if code != 0 || timedOut || !containsStr(out, "ok") {
		t.Fatalf("ok-команда: code=%d out=%q timeout=%v", code, out, timedOut)
	}
	// Код возврата должен доезжать честно: watch по нему решает, «чинить»
	// или «всё зелёное». Раньше проверка ждала POSIX «exit 3», который
	// под cmd.exe печатался как текст и давал код 0.
	out, code, timedOut = watchRunCommand(t.TempDir(), shellCmd(3), time.Minute)
	if code != 3 || timedOut {
		t.Fatalf("код выхода: %d, out=%q, timeout=%v", code, out, timedOut)
	}
	// Таймаут: команда не успевает, флаг поднимается, успехом не считается.
	out, code, timedOut = watchRunCommand(t.TempDir(), slowCmd(), 300*time.Millisecond)
	if !timedOut {
		t.Fatalf("ожидался таймаут: code=%d out=%q", code, out)
	}
	if code == 0 {
		t.Fatalf("таймаут не должен считаться успехом: code=%d", code)
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
