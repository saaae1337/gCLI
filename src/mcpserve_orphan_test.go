//go:build !windows

package main

// Регрессия дерева процессов mcpSrvPrompt: таймаут обязан убивать всё
// дерево, а не только прямого ребёнка. Фейковый gcli (скрипт) заводит
// фонового потомка sleep 3600 и засыпает сам; после таймаута потомок
// должен быть мёртв. На старом коде (без PGroup/KillTree) потомок
// переживал вызов как сирота — тест это ловит. Паттерн — tools/proctree_test.go.

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMCPSrvPromptKillsWholeTree(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	script := filepath.Join(dir, "fake-gcli.sh")
	body := "#!/bin/sh\n" +
		"sleep 3600 &\n" +
		"echo $! > " + pidFile + "\n" +
		"sleep 3600\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	old := mcpSrvExecutable
	mcpSrvExecutable = func() (string, error) { return script, nil }
	t.Cleanup(func() { mcpSrvExecutable = old })

	// Вызов сам упрётся в таймаут; дедлайн короткий — только на старт скрипта.
	done := make(chan error, 1)
	go func() {
		_, err := mcpSrvPrompt(dir, "регрессия дерева процессов", 300*time.Millisecond)
		done <- err
	}()

	// Ждём появления pid-файла: потомок завёлся.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("фоновый потомок не завёлся: нет pid-файла")
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid := 0
	for _, c := range string(raw) {
		if c >= '0' && c <= '9' {
			pid = pid*10 + int(c-'0')
		} else if pid > 0 {
			break
		}
	}
	if pid <= 0 {
		t.Fatalf("не разобрать pid из %q", string(raw))
	}
	// Страховка: если тест упадёт раньше проверки, потомок не должен
	// остаться сиротой после завершения теста.
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	})

	// Вызов обязан вернуться — висеть на потомке ему нельзя.
	select {
	case err := <-done:
		if err == nil {
			t.Log("mcpSrvPrompt вернулся без ошибки — скрипт успел бы доработать? проверяем потомка всё равно")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("mcpSrvPrompt не вернулся за 20с — висит на живом потомке")
	}

	// Потомок должен быть мёртв: таймаут убивает всё дерево.
	gone := false
	deadline = time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			gone = true
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("фоновый потомок (pid %d) пережил таймаут — сирота: убит только прямой ребёнок", pid)
	}
}
