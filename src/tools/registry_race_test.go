package tools

// Регрессия 5.5.0: копирование карты прочитанных файлов под блокировкой.
//
// markRead пишет в карту под readMu, а Base/Restrict/Merge копируют её при
// создании реестра субагента — тоже из горутины. Без блокировки на том же
// месте Go падает с фатальной «concurrent map iteration and map write», и
// такой падёж не ловится утверждением в тесте: он убивает процесс целиком.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestSnapshotReadConcurrent — копирование карты под конкурентной записью.
func TestSnapshotReadConcurrent(t *testing.T) {
	dir := t.TempDir()
	const files = 24
	paths := make([]string, files)
	for i := range paths {
		p := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[i] = p
	}

	shared := map[string]bool{}
	r := New(Env{WorkDir: dir, ReadFiles: shared})

	// Писатели: помечают файлы прочитанными, как это делает read_file.
	writer := sync.WaitGroup{}
	for _, p := range paths {
		writer.Add(1)
		go func(p string) {
			defer writer.Done()
			for i := 0; i < 200; i++ {
				markRead(shared, p)
			}
		}(p)
	}

	// Читатели: снимают копии карты, как это делают копии реестра.
	reader := sync.WaitGroup{}
	for i := 0; i < 8; i++ {
		reader.Add(1)
		go func() {
			defer reader.Done()
			for i := 0; i < 200; i++ {
				_ = r.Base()
				_ = r.CountRead()
				_ = snapshotRead(shared)
			}
		}()
	}

	// Дать гонке шансов случиться: писатели и читатели идут одновременно.
	writer.Wait()
	reader.Wait()
}

// TestSnapshotReadNilAndCopy — nil не должен паниковать, а копия обязана быть
// независимой от исходной карты.
func TestSnapshotReadNilAndCopy(t *testing.T) {
	if got := snapshotRead(nil); got == nil {
		t.Fatal("snapshotRead(nil) вернул nil-карту, нужен пустой map")
	}

	src := map[string]bool{"a": true}
	got := snapshotRead(src)
	if !got["a"] {
		t.Error("копия потеряла значение")
	}
	got["b"] = true
	if src["b"] {
		t.Error("копия делит память с исходной картой")
	}
}

// TestBaseReadFilesIsolated — копия реестра не делит карту с родителем.
func TestBaseReadFilesIsolated(t *testing.T) {
	dir := t.TempDir()
	shared := map[string]bool{}
	r := New(Env{WorkDir: dir, ReadFiles: shared})
	markRead(shared, filepath.Join(dir, "parent.txt"))

	sub := r.Base()
	// Субагент прочитал свой файл — право редактировать главному это не даёт.
	subOnly := filepath.Join(dir, "sub.txt")
	markRead(sub.env.ReadFiles, subOnly)

	if wasRead(sub.env.ReadFiles, subOnly) != true {
		t.Error("файл субагента не виден самому субагенту")
	}
	if wasRead(shared, subOnly) {
		t.Error("чтение субагента просочилось в карту главного агента")
	}
	if wasRead(sub.env.ReadFiles, filepath.Join(dir, "parent.txt")) != true {
		t.Error("копия реестра потеряла уже прочитанные файлы")
	}
}
