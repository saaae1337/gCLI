package tui

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Регрессии 0.9.1: асинхронная сборка списка файлов для @-дополнения и
// синхронизация markDirty в публичных методах интерфейса.

// TestFilePaletteAsyncRefresh — fileItems не должен блокироваться на
// обходе каталога. Раньше WalkDir шёл синхронно под a.mu: тик, отрисовка
// и горутины агента замирали, пока обходилось всё дерево проекта.
func TestFilePaletteAsyncRefresh(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.go", "beta.md", "sub/gamma.ts"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := newTestApp(strings.NewReader(""), &bytes.Buffer{})
	app.opt.WorkDir = dir

	// Сборка, которая не заканчивается, пока тест не разрешит.
	started := make(chan struct{})
	release := make(chan struct{})
	orig := collectFilesFn
	collectFilesFn = func(root string) []string {
		close(started)
		<-release
		return orig(root)
	}
	defer func() { collectFilesFn = orig }()

	app.mu.Lock()
	app.comp.Insert("@al")
	begin := time.Now()
	first := app.fileItems()
	elapsed := time.Since(begin)
	busy := app.filesBusy
	app.mu.Unlock()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("фоновая сборка не началась")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("fileItems ждал обход каталога: %v", elapsed)
	}
	if !busy {
		t.Fatal("filesBusy не выставлен на время сборки")
	}
	if len(first) != 0 {
		t.Fatalf("пока идёт сборка, список должен быть пустым/старым, получено %v", first)
	}

	close(release)
	var items []string
	waitFor(t, "результат фоновой сборки в палитре", func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		items = app.fileItems()
		return len(items) == 1
	})
	if items[0] != "alpha.go" {
		t.Fatalf("подсказка после сборки: %v", items)
	}
}

// TestAsyncUIMethodsRace — Toast/SetMascot/SetCronEnabled обязаны
// трогать dirty под мьютексом. Раньше markDirty звался после Unlock:
// -race ловил гонку с циклом Run, читающим тот же флаг каждый тик.
func TestAsyncUIMethodsRace(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)
	done := make(chan error, 1)
	go func() { done <- app.Run(Handlers{}) }()
	waitFor(t, "запуск", app.Running)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				app.Toast("т")
				app.SetMascot(true)
				app.SetCronEnabled(true)
				app.SetMascot(false)
				app.SetCronEnabled(false)
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	app.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился")
	}
}
