package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Регрессии подвешивания exec-вызовов фоновым потомком.
//
// «sleep N & echo ok»: оболочка завершается сразу, фоновый sleep держит
// унаследованный stdout. Раньше Run ждал закрытия трубы, пока sleep не
// закончится сам: таймаут не спасал — убитой оказывалась только оболочка,
// и вызов инструмента висел до конца спящего потомка.

// TestBashBackgroundChildDoesNotHang — потомок держит вывод, оболочка ушла:
// вызов возвращается по WaitDelay с пометкой, а не висит.
func TestBashBackgroundChildDoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("фон через sh и наследование труб — POSIX-поведение")
	}
	dir := t.TempDir()
	r := New(Env{WorkDir: dir})

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.hBash(context.Background(), map[string]any{
			"command":     "sleep 120 & echo bg-marker-ok",
			"timeout_sec": 30,
		})
		done <- outcome{res, err}
	}()

	select {
	case oc := <-done:
		if oc.err != nil {
			t.Fatalf("ошибка вместо результата: %v", oc.err)
		}
		if !strings.Contains(oc.res.Text, "bg-marker-ok") {
			t.Errorf("вывод команды потерялся: %q", oc.res.Text)
		}
		if !strings.Contains(oc.res.Text, "удерживает вывод") {
			t.Errorf("нет пометки о потомке: %q", oc.res.Text)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("вызов не вернулся за 15 с: фоновый потомок подвесил bash")
	}
}

// TestBashTimeoutKillsBackgroundTree — таймаут добивает всё дерево: вызов
// возвращается сразу после deadline, а не когда фоновый потомок доспит.
func TestBashTimeoutKillsBackgroundTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("фон через sh и KillTree — POSIX-поведение")
	}
	dir := t.TempDir()
	r := New(Env{WorkDir: dir})

	start := time.Now()
	res, err := r.hBash(context.Background(), map[string]any{
		"command":     "sleep 120 & sleep 120",
		"timeout_sec": 3,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ошибка вместо результата: %v", err)
	}
	if !strings.Contains(res.Text, "Таймаут") {
		t.Errorf("ожидали пометку таймаута: %q", res.Text)
	}
	if elapsed > 10*time.Second {
		t.Errorf("таймаут вернулся за %v, а не сразу после deadline", elapsed)
	}
}
