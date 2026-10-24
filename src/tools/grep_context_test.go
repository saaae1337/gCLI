package tools

// Тесты контекста в grep: показывать строки вокруг совпадения — значит
// не тратить отдельный вызов read_file на каждый найденный кусок кода.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGrepContextShowsSurroundingLines — контекст показывается и сверху, и
// снизу, а сама строка совпадения остаётся опознаваемой.
func TestGrepContextShowsSurroundingLines(t *testing.T) {
	work := t.TempDir()
	body := "package main\n\nfunc main() {\n\tneedle()\n}\n"
	if err := os.WriteFile(filepath.Join(work, "a.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: work})

	res, err := r.hGrep(context.Background(), map[string]any{
		"pattern": "needle", "path": ".", "context": 2,
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	for _, want := range []string{"func main()", "a.go:4:"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("в выдаче нет %q:\n%s", want, res.Text)
		}
	}
	// Контекст помечен отдельно от совпадения.
	if !strings.Contains(res.Text, "a.go-3-") {
		t.Errorf("контекстная строка не помечена:\n%s", res.Text)
	}
	// Число совпадений считается по совпадениям, а не по строкам выдачи.
	if !strings.Contains(res.Text, "Найдено 1 совпадений") {
		t.Errorf("счётчик совпадений неверен:\n%s", res.Text)
	}
}

// TestGrepWithoutContextStaysShort — без контекста выдача не меняется:
// лишние строки никто не просил.
func TestGrepWithoutContextStaysShort(t *testing.T) {
	work := t.TempDir()
	body := "package main\n\nfunc main() {\n\tneedle()\n}\n"
	if err := os.WriteFile(filepath.Join(work, "a.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: work})

	res, err := r.hGrep(context.Background(), map[string]any{"pattern": "needle", "path": "."})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if strings.Contains(res.Text, "a.go-3-") {
		t.Errorf("контекст появился без просьбы:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "a.go:4:") {
		t.Errorf("совпадение не найдено:\n%s", res.Text)
	}
}

// TestGrepContextNotDuplicated — соседние совпадения не печатают один и
// тот же контекст дважды.
func TestGrepContextNotDuplicated(t *testing.T) {
	work := t.TempDir()
	body := "одно\nдва\nтри\nчетыре\nпять\nшесть\nсемь\n"
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: work})

	res, err := r.hGrep(context.Background(), map[string]any{
		"pattern": "три|четыре", "path": ".", "context": 2,
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if n := strings.Count(res.Text, "a.txt-2-"); n > 1 {
		t.Errorf("строка контекста напечатана %d раз:\n%s", n, res.Text)
	}
}
