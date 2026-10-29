package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
	"gcli/ui"
)

// TestSaveSessionSurfacesDiskErrorOnce — ошибка записи сессии не должна
// глотаться молча: полный диск или недоступный путь означали, что история
// перестаёт переживать перезапуск без единого слова. Первая ошибка —
// предупреждение, дальше — тихо (сессию пишут на каждый ход).
func TestSaveSessionSurfacesDiskErrorOnce(t *testing.T) {
	dir := t.TempDir()
	// Каталог сессий занят файлом: ни mkdir, ни запись внутрь невозможны.
	if err := os.WriteFile(filepath.Join(dir, "sessions"), []byte("занято"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf := &strings.Builder{}
	a := &app{}
	a.ui = ui.New(ui.Options{
		Theme: "ember", Unicode: true, Color: false, Grade: ui.Color16,
		Animations: false, Width: 80, Out: buf,
	})
	a.repo = &core.Repo{Store: &core.Store{Root: dir}, Cfg: core.DefaultConfig()}
	a.sess = a.repo.NewSession("prov", "model", dir)

	a.saveSession()
	if !strings.Contains(buf.String(), "не сохраняется") {
		t.Fatalf("первая ошибка записи не показана:\n%s", buf.String())
	}

	buf.Reset()
	a.saveSession()
	if strings.Contains(buf.String(), "не сохраняется") {
		t.Fatalf("предупреждение повторяется на каждую попытку:\n%s", buf.String())
	}
}
