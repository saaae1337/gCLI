package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- changes и revert_last ----------

// newTestReg — реестр в каталоге dir без подтверждений и записей в память.
func newTestReg(t *testing.T, dir string) *Registry {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	return New(Env{WorkDir: dir})
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("не прочитали %s: %v", path, err)
	}
	return string(data)
}

func run(t *testing.T, r *Registry, name string, args map[string]any) (Result, error) {
	t.Helper()
	tc := r.Get(name)
	if tc == nil {
		t.Fatalf("инструмент %s не зарегистрирован", name)
	}
	return tc.Handler(context.Background(), args)
}

func mustRun(t *testing.T, r *Registry, name string, args map[string]any) Result {
	t.Helper()
	res, err := run(t, r, name, args)
	if err != nil {
		t.Fatalf("%s вернул ошибку: %v", name, err)
	}
	return res
}

// TestChangeLogWriteFile — правка попадает в журнал и видна в changes.
func TestChangeLogWriteFile(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)

	mustRun(t, r, "write_file", map[string]any{"path": "a.txt", "content": "привет"})

	res := mustRun(t, r, "changes", nil)
	if !strings.Contains(res.Text, "a.txt") {
		t.Errorf("changes не показал изменённый файл:\n%s", res.Text)
	}
}

// TestChangeLogEditFile — edit_file тоже журналируется.
func TestChangeLogEditFile(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	mustWrite(t, filepath.Join(dir, "a.txt"), "привет")

	mustRun(t, r, "read_file", map[string]any{"path": "a.txt"})
	mustRun(t, r, "edit_file", map[string]any{
		"path": "a.txt", "old_string": "привет", "new_string": "пока",
	})

	res := mustRun(t, r, "changes", nil)
	if !strings.Contains(res.Text, "edit_file") {
		t.Errorf("changes не указал инструмент правки:\n%s", res.Text)
	}
	if got := mustRead(t, filepath.Join(dir, "a.txt")); got != "пока" {
		t.Errorf("файл не изменён: %q", got)
	}
}

// TestRevertRestoresContent — откат возвращает прежнее содержимое.
func TestRevertRestoresContent(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	p := filepath.Join(dir, "a.txt")
	mustWrite(t, p, "старое")

	mustRun(t, r, "write_file", map[string]any{"path": "a.txt", "content": "новое"})
	if _, err := run(t, r, "revert_last", nil); err != nil {
		t.Fatalf("откат вернул ошибку: %v", err)
	}
	if got := mustRead(t, p); got != "старое" {
		t.Errorf("после отката ожидалось «старое», получено %q", got)
	}
}

// TestRevertDeletesNewFile — откат СОЗДАННОГО файла удаляет его, а не оставляет
// пустой файл. Это регрессия: existed вычислялся уже после записи на диск,
// и только что созданный файл всегда считался «изменённым».
func TestRevertDeletesNewFile(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	p := filepath.Join(dir, "new.txt")

	mustRun(t, r, "write_file", map[string]any{"path": "new.txt", "content": "что-то"})
	if _, err := run(t, r, "revert_last", nil); err != nil {
		t.Fatalf("откат вернул ошибку: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("созданный файл не удалён после отката (os.Stat err = %v)", err)
	}
}

// TestRevertDryRunDoesNothing — предпросмотр отката не трогает диск.
func TestRevertDryRunDoesNothing(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	p := filepath.Join(dir, "a.txt")
	mustWrite(t, p, "старое")

	mustRun(t, r, "write_file", map[string]any{"path": "a.txt", "content": "новое"})
	res := mustRun(t, r, "revert_last", map[string]any{"dry_run": true})

	if got := mustRead(t, p); got != "новое" {
		t.Errorf("dry_run изменил файл: %q", got)
	}
	if !strings.Contains(res.Text, "diff") {
		t.Errorf("предпросмотр отката не показал diff:\n%s", res.Text)
	}
	// Запись в журнале должна остаться: откат не применён.
	if c, ok := r.change().last(); !ok || c.Path != p {
		t.Errorf("предпросмотр отката съел запись журнала")
	}
}

// TestRevertRefusesForeignEdit — файл меняли не мы: откат обязан отказать,
// иначе он затрёт чужую работу.
func TestRevertRefusesForeignEdit(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	p := filepath.Join(dir, "a.txt")
	mustWrite(t, p, "старое")

	mustRun(t, r, "write_file", map[string]any{"path": "a.txt", "content": "новое"})
	// Правка «со стороны»: не наша.
	mustWrite(t, p, "чужая правка")

	if _, err := run(t, r, "revert_last", nil); err == nil {
		t.Fatal("откат чужой правки должен был отказать, но прошёл")
	}
	if got := mustRead(t, p); got != "чужая правка" {
		t.Errorf("файл изменён, хотя откат отказал: %q", got)
	}
}

// TestRevertEmptyLog — откатывать нечего: внятная ошибка, а не паника.
func TestRevertEmptyLog(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	if _, err := run(t, r, "revert_last", nil); err == nil {
		t.Fatal("ожидалась ошибка «откатывать нечего»")
	}
}

// TestRevertReadOnly — в режиме «только чтение» откат запрещён.
func TestRevertReadOnly(t *testing.T) {
	dir := t.TempDir()
	r := New(Env{WorkDir: dir, ReadOnly: true})
	res := mustRun(t, r, "revert_last", nil)
	if res.Error == "" {
		t.Errorf("в режиме только чтение ожидался запрет отката, получено: %+v", res)
	}
}

// TestChangeLogSharedWithSubagent — журнал общий у главного агента и
// субагента: иначе changes главного не видел бы правок субагента.
func TestChangeLogSharedWithSubagent(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	sub := r.Base()

	mustRun(t, sub, "write_file", map[string]any{"path": "sub.txt", "content": "из субагента"})

	res := mustRun(t, r, "changes", nil)
	if !strings.Contains(res.Text, "sub.txt") {
		t.Errorf("главный агент не увидел правку субагента:\n%s", res.Text)
	}
}

// TestChangeLogSharedAfterRestrict — Restrict не теряет журнал.
func TestChangeLogSharedAfterRestrict(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	mustRun(t, r, "write_file", map[string]any{"path": "a.txt", "content": "x"})

	sub := r.Restrict(nil, nil)
	res := mustRun(t, sub, "changes", nil)
	if !strings.Contains(res.Text, "a.txt") {
		t.Errorf("Restrict потерял журнал правок:\n%s", res.Text)
	}
}

// TestChangeLogSharedAfterMerge — Merge не теряет журнал.
func TestChangeLogSharedAfterMerge(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	mustRun(t, r, "write_file", map[string]any{"path": "a.txt", "content": "x"})

	merged := r.Merge(r.Base())
	res := mustRun(t, merged, "changes", nil)
	if !strings.Contains(res.Text, "a.txt") {
		t.Errorf("Merge потерял журнал правок:\n%s", res.Text)
	}
}

// TestChangeNilLogSurvives — реестр, собранный вручную (не через New), не падает
// на первой правке: журнал создаётся по требованию.
func TestChangeNilLogSurvives(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{byName: map[string]*Tool{}, workDir: dir, env: Env{WorkDir: dir}}
	r.registerBuiltins()

	res, err := run(t, r, "write_file", map[string]any{"path": "a.txt", "content": "x"})
	if err != nil {
		t.Fatalf("write_file на реестре без журнала вернул ошибку: %v", err)
	}
	if res.Text == "" {
		t.Errorf("пустой результат записи")
	}
	if c, ok := r.change().last(); !ok || c.Path != filepath.Join(dir, "a.txt") {
		t.Errorf("правка не попала в журнал")
	}
}

// TestChangesFilterByPath — фильтр по файлу.
func TestChangesFilterByPath(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	mustRun(t, r, "write_file", map[string]any{"path": "a.txt", "content": "x"})
	mustRun(t, r, "write_file", map[string]any{"path": "b.txt", "content": "y"})

	res := mustRun(t, r, "changes", map[string]any{"path": "a.txt"})
	if !strings.Contains(res.Text, "a.txt") {
		t.Errorf("фильтр потерял нужный файл:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "b.txt") {
		t.Errorf("фильтр показал чужой файл:\n%s", res.Text)
	}
}

// TestChangeLines — счётчик строк правки.
func TestChangeLines(t *testing.T) {
	c := Change{Old: "one\ntwo\nthree\n", New: "one\ntwo\nfour\n"}
	added, removed := c.Lines()
	if added != 1 || removed != 1 {
		t.Errorf("Lines() = +%d/-%d, ожидалось +1/-1", added, removed)
	}
}

// TestUnifiedDiffShowsChange — diff показывает изменённое место.
func TestUnifiedDiffShowsChange(t *testing.T) {
	d := unifiedDiff("a\nb\nc\n", "a\nB\nc\n", "f.txt")
	if !strings.Contains(d, "-b") || !strings.Contains(d, "+B") {
		t.Errorf("diff не показал замену:\n%s", d)
	}
}
