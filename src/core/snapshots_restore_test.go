package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupSnapshots — каталог с одним закоммиченным снимком.
func setupSnapshots(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("первая\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !SnapshotsAvailable() {
		t.Skip("git не в PATH")
	}
	if _, took, err := TakeSnapshot(dir, "ход 1"); err != nil || !took {
		t.Skipf("снимок не снялся: took=%v err=%v", took, err)
	}
	return dir
}

// breakIndex — испортить индекс теневого репозитория.
//
// Убийство именно индекса, а не HEAD: EnsureSnapshotsRepo проверяет
// только HEAD и репозиторий считает готовым, а git add на битом индексе
// падает. Это то же самое по последствиям, что переполненный диск или
// снятые права в момент отката.
func breakIndex(t *testing.T, dir string) {
	t.Helper()
	idx := filepath.Join(SnapshotsGitDir(dir), "index")
	if err := os.Remove(idx); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestRestoreSnapshotRefusesWhenSafetySnapshotFails — откат обязан
// отказаться, если страховочный снимок не снялся.
//
// Смысл /revert — вернуть проект назад, но с правом вернуться и
// вперёд: перед reset --hard текущее состояние коммитится, а на него
// вешается ветка keep/pre-revert. Ошибки обоих шагов глушились, и
// разрушающий reset шёл даже тогда, когда страховки нет.
//
// Цена — необратимая потеря работы: правка, ради которой человек
// жался на откат, исчезает, а вернуть её нечем. Молчание при этом
// худшее, потому что выглядит как успех.
func TestRestoreSnapshotRefusesWhenSafetySnapshotFails(t *testing.T) {
	dir := setupSnapshots(t)
	// Правка, которую человек рассчитывает откатить.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("вторая\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	breakIndex(t, dir)

	snaps, err := ListSnapshots(dir)
	if err != nil || len(snaps) == 0 {
		t.Skipf("снимки не читаются: %v", err)
	}

	_, err = RestoreSnapshot(dir, snaps[0].Hash)
	if err == nil {
		t.Fatal("откат состоялся, хотя страховочный снимок не снялся")
	}
	if !strings.Contains(err.Error(), "откат") {
		t.Errorf("в отказе не сказано, что именно сломалось: %v", err)
	}
}

// TestRestoreSnapshotKeepsWorkOnRefusal — при отказе работа не должна
// пропасть: агент мог править файл минуты назад, и «откат не удался»
// обязано означать «файл на месте».
func TestRestoreSnapshotKeepsWorkOnRefusal(t *testing.T) {
	dir := setupSnapshots(t)
	const work = "ценная и несохранённая работа\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(work), 0o644); err != nil {
		t.Fatal(err)
	}
	breakIndex(t, dir)

	snaps, err := ListSnapshots(dir)
	if err != nil || len(snaps) == 0 {
		t.Skipf("снимки не читаются: %v", err)
	}
	_, _ = RestoreSnapshot(dir, snaps[0].Hash)

	got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("файл пропал после неудачного отката: %v", err)
	}
	if string(got) != work {
		t.Errorf("работа потеряна: %q", got)
	}
}
