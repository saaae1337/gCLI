package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFileURL — локальный путь для браузера обязан быть file:///,
// иначе на Windows буква диска попадает в Host и файл не открывается.
func TestFileURL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "page.html")
	want := "file:///" + strings.TrimPrefix(filepath.ToSlash(p), "/")
	if got := fileURL(p); got != want {
		t.Errorf("fileURL = %q, ожидался %q", got, want)
	}
	if runtime.GOOS == "windows" {
		if got := fileURL(`C:\work\page.html`); got != "file:///C:/work/page.html" {
			t.Errorf("fileURL Windows = %q", got)
		}
	}
}

// TestAttachImageRejectsOversizedBeforeRead — слишком большой файл
// отклоняется по Stat, не попадая целиком в память.
func TestAttachImageRejectsOversizedBeforeRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "huge.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxImageBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	r := New(Env{WorkDir: dir})
	if _, err := r.attachImageFile(p, "read_image"); err == nil {
		t.Fatal("изображение больше потолка принято")
	}
}
