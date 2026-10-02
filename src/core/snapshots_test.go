package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requireGit — снапшоты держатся на git из PATH; без него тестировать нечего.
func requireGit(t *testing.T) {
	t.Helper()
	if !SnapshotsAvailable() {
		t.Skip("git нет в PATH — снапшоты отключаются, тестировать нечего")
	}
}

func TestSnapshotLifecycle(t *testing.T) {
	requireGit(t)
	wd := t.TempDir()
	write := func(name, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(wd, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("main.go", "package main\n")
	s1, took, err := TakeSnapshot(wd, "первый ход")
	if err != nil || !took {
		t.Fatalf("первый снимок: took=%v err=%v", took, err)
	}
	if s1.Hash == "" || s1.Files != 1 {
		t.Fatalf("первый снимок: %+v", s1)
	}

	write("main.go", "package main // правка\n")
	write("docs/notes.md", "заметка")
	s2, took, err := TakeSnapshot(wd, "второй ход: правки в main.go и docs")
	if err != nil || !took {
		t.Fatalf("второй снимок: took=%v err=%v", took, err)
	}
	if s2.Hash == s1.Hash {
		t.Fatal("второй снимок получил хеш первого")
	}
	if !strings.HasPrefix(s2.Subject, "ход: ") {
		t.Fatalf("тема снимка без префикса: %q", s2.Subject)
	}

	snaps, err := ListSnapshots(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("в списке %d снимков, ждал 2: %+v", len(snaps), snaps)
	}
	if snaps[0].Hash != s1.Hash || snaps[1].Hash != s2.Hash {
		t.Fatal("список не в порядке от старого к новому")
	}

	// Откат к первому: правка main.go исчезает, файл docs/notes.md удаляется.
	if _, err := RestoreSnapshot(wd, s1.Hash); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(wd, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package main\n" {
		t.Fatalf("main.go не откатился: %q", data)
	}
	if _, err := os.Stat(filepath.Join(wd, "docs", "notes.md")); !os.IsNotExist(err) {
		t.Fatal("созданный после снимка файл должен был исчезнуть при откате")
	}

	// Откат можно отменить: точка ухода (второй снимок) осталась видна
	// в списке через ветку keep/pre-revert.
	snaps, err = ListSnapshots(wd)
	if err != nil {
		t.Fatal(err)
	}
	foundPre := false
	for _, s := range snaps {
		if s.Hash == s2.Hash {
			foundPre = true
		}
	}
	if !foundPre {
		t.Fatal("точка ухода (снимок до отката) не видна в списке — отменить откат нечем")
	}

	// Грязное состояние перед откатом тоже коммитится: правка, сделанная
	// после последнего снимка, не должна исчезать бесследно.
	write("main.go", "package main // грязная правка между снимками\n")
	if _, err := RestoreSnapshot(wd, s1.Hash); err != nil {
		t.Fatal(err)
	}
	snaps, err = ListSnapshots(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) < 3 {
		t.Fatalf("грязное состояние перед откатом не попало в список: %d снимков", len(snaps))
	}
}

func TestSnapshotNoChanges(t *testing.T) {
	requireGit(t)
	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, took, err := TakeSnapshot(wd, "раз"); err != nil || !took {
		t.Fatalf("took=%v err=%v", took, err)
	}
	_, took, err := TakeSnapshot(wd, "два без изменений")
	if err != nil {
		t.Fatal(err)
	}
	if took {
		t.Fatal("снимок без изменений не должен создаваться")
	}
}

func TestSnapshotExcludes(t *testing.T) {
	requireGit(t)
	wd := t.TempDir()
	for _, p := range []string{"src/main.go", "node_modules/pkg/index.js", ".git/HEAD", "dist/out.bin"} {
		if err := os.MkdirAll(filepath.Join(wd, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wd, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap, took, err := TakeSnapshot(wd, "ход с мусором")
	if err != nil || !took {
		t.Fatalf("took=%v err=%v", took, err)
	}
	if snap.Files != 1 {
		t.Fatalf("в снимке %d файлов, ждал 1 (только src/main.go): %+v", snap.Files, snap)
	}
}

func TestSnapshotGuardWorkDir(t *testing.T) {
	requireGit(t)
	// Домашний каталог снимать нельзя.
	if h, err := os.UserHomeDir(); err == nil {
		if err := EnsureSnapshotsRepo(h); err == nil {
			t.Fatal("домашний каталог не должен сниматься")
		}
	}
	// Обычный временный — можно.
	if err := EnsureSnapshotsRepo(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotsUnavailableWithoutGit(t *testing.T) {
	// SnapshotsAvailable честно отражает отсутствие git — на это
	// завязан тихий пропуск в turn: страховка не должна ломать ход.
	if SnapshotsAvailable() {
		return // git есть — проверять нечего
	}
	wd := t.TempDir()
	if _, _, err := TakeSnapshot(wd, "ход"); !strings.Contains(err.Error(), "git") {
		t.Fatalf("ждали понятную ошибку про git, получили: %v", err)
	}
}

func TestSnapshotsEnabledDefault(t *testing.T) {
	if !(Config{}).SnapshotsEnabled() {
		t.Fatal("по умолчанию снимки должны быть включены (nil = on)")
	}
	off := false
	if (Config{Snapshots: &off}).SnapshotsEnabled() {
		t.Fatal("snapshots: false должен выключать снимки")
	}
}
