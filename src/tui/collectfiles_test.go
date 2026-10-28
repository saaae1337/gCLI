package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Регрессия: счётчик глубины в collectFiles рос на входе в каталог и
// никогда не убывал — «глубиной» оказывалось число уже обойдённых
// каталогов. Корень съедал единицу, и в проекте с шестью каталогами
// хвост верхнеуровневых молча выпадал из @-подсказки файлов.

func TestCollectFilesAllTopLevelDirs(t *testing.T) {
	dir := t.TempDir()
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	for _, n := range names {
		p := filepath.Join(dir, n, "f.txt")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := collectFiles(dir)
	var want []string
	for _, n := range names {
		want = append(want, n+"/f.txt")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("список файлов: %v, ожидалось %v", got, want)
	}
}

func TestCollectFilesDepthCap(t *testing.T) {
	dir := t.TempDir()
	// Вложенность шести каталогов собирается, седьмого — нет.
	deep6 := filepath.Join(dir, "x", "1", "2", "3", "4", "5")
	deep7 := filepath.Join(deep6, "6")
	for _, p := range []string{filepath.Join(deep6, "ok.txt"), filepath.Join(deep7, "no.txt")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := collectFiles(dir)
	want := []string{"x/1/2/3/4/5/ok.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("список файлов: %v, ожидалось %v", got, want)
	}
}

func TestCollectFilesSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{
		filepath.Join(dir, "keep.go"),
		filepath.Join(dir, "node_modules", "junk.js"),
		filepath.Join(dir, ".git", "config"),
		filepath.Join(dir, ".hidden", "x.txt"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := collectFiles(dir)
	want := []string{"keep.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("список файлов: %v, ожидалось %v", got, want)
	}
}
