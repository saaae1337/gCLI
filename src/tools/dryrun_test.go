package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// ---------- dry_run ----------

// TestDryRunLeavesFileAlone — главный смысл инструмента: показать, что будет,
// ничего не сделав. Если предпросмотр пишет на диск, он бесполезен.
func TestDryRunLeavesFileAlone(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	mustWrite(t, p, "старое\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "write_file",
		"args": `{"path":"a.txt","content":"новое"}`,
	})

	if got := mustRead(t, p); got != "старое\n" {
		t.Errorf("dry_run изменил файл: %q", got)
	}
	if c, ok := r.change().last(); ok {
		t.Errorf("предпросмотр попал в журнал правок: %s", c.Rel)
	}
	if !strings.Contains(res.Text, "существует") {
		t.Errorf("не сказано, что файл уже есть:\n%s", res.Text)
	}
}

// TestDryRunResolvesRelativeToWorkDir — относительный путь разрешается от
// рабочего каталога агента, а не от каталога процесса. Иначе предпросмотр
// врал бы ровно там, где агент перестраховывается.
func TestDryRunResolvesRelativeToWorkDir(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "старое\n")
	elsewhere := t.TempDir()
	mustWrite(t, filepath.Join(elsewhere, "a.txt"), "другой проект\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "write_file",
		"args": `{"path":"a.txt","content":"новое"}`,
	})
	if !strings.Contains(res.Text, "файл существует") {
		t.Errorf("предпросмотр искал файл не в том каталоге:\n%s", res.Text)
	}
}

// TestDryRunNewFile — для несуществующего файла сказано, что он будет создан.
func TestDryRunNewFile(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "write_file",
		"args": `{"path":"новый.txt","content":"что-то"}`,
	})
	if !strings.Contains(res.Text, "СОЗДАН") {
		t.Errorf("не сказано, что файл будет создан:\n%s", res.Text)
	}
	// Без пути предпросмотр не выдумывает результат, а объясняет, чего
	// не хватает: это не ошибка вызова, а недостающий аргумент.
	res2 := mustRun(t, r, "dry_run", map[string]any{
		"tool": "write_file", "args": `{"content":"без пути"}`,
	})
	if !strings.Contains(res2.Text, "путь не указан") {
		t.Errorf("без пути предпросмотр не сказал, чего не хватает:\n%s", res2.Text)
	}
}

// TestDryRunEditFileComputesRealDiff — для edit_file показывается результат
// реальной замены, а не «файл будет перезаписан».
func TestDryRunEditFileComputesRealDiff(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package main\n\nfunc main() {}\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "edit_file",
		"args": `{"path":"a.go","old_string":"func main() {}","new_string":"func main() { run() }"}`,
	})
	if !strings.Contains(res.Text, "diff") {
		t.Errorf("нет diff результата замены:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "run()") {
		t.Errorf("в diff нет самой замены:\n%s", res.Text)
	}
	// Файл не тронут.
	if got := mustRead(t, filepath.Join(dir, "a.go")); strings.Contains(got, "run()") {
		t.Errorf("предпросмотр реально применил правку: %q", got)
	}
}

// TestDryRunEditFileWarnsAboutMissingOldString — если old_string не найден,
// реальный вызов упадёт. Предпросмотр обязан об этом сказать заранее,
// иначе он вводит в заблуждение именно в опасный момент.
func TestDryRunEditFileWarnsAboutMissingOldString(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package main\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "edit_file",
		"args": `{"path":"a.go","old_string":"нет-такого-текста","new_string":"x"}`,
	})
	if !strings.Contains(res.Text, "НЕ найден") {
		t.Errorf("нет предупреждения о непопадании old_string:\n%s", res.Text)
	}
}

// TestDryRunEditFileWarnsAboutAmbiguousMatch — неоднозначное совпадение без
// replace_all реальный вызов отвергнет.
func TestDryRunEditFileWarnsAboutAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "x\ny\nx\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "edit_file",
		"args": `{"path":"a.go","old_string":"x","new_string":"z"}`,
	})
	if !strings.Contains(res.Text, "replace_all") {
		t.Errorf("нет предупреждения о неоднозначной замене:\n%s", res.Text)
	}
}

// TestDryRunEditFileReplaceAll — с replace_all замена посчитана целиком.
func TestDryRunEditFileReplaceAll(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "x\ny\nx\n")

	r := newTestReg(t, dir)
	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "edit_file",
		"args": `{"path":"a.go","old_string":"x","new_string":"z","replace_all":true}`,
	})
	if strings.Contains(res.Text, "⚠") {
		t.Errorf("replace_all подан как проблема:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "diff") {
		t.Errorf("нет diff для replace_all:\n%s", res.Text)
	}
}

// TestDryRunRevertShowsDiff — откат тоже показывается, а не выполняется.
func TestDryRunRevertShowsDiff(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	mustWrite(t, p, "старое\n")
	r := newTestReg(t, dir)
	mustRun(t, r, "write_file", map[string]any{"path": "a.txt", "content": "новое\n"})

	res := mustRun(t, r, "dry_run", map[string]any{"tool": "revert_last"})
	if !strings.Contains(res.Text, "diff") {
		t.Errorf("предпросмотр отката без diff:\n%s", res.Text)
	}
	if got := mustRead(t, p); got != "новое\n" {
		t.Errorf("предпросмотр отката изменил файл: %q", got)
	}
}

// TestDryRunCommandShowsWorkingDir — каталог запуска указан: без него
// предпросмотр не может честно сказать, где выполнится команда.
func TestDryRunCommandShowsWorkingDir(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)

	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "bash", "args": `{"command":"go build ./..."}`,
	})
	if !strings.Contains(res.Text, "каталог запуска") {
		t.Errorf("не указан каталог запуска:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "go build ./...") {
		t.Errorf("не показана команда:\n%s", res.Text)
	}
	// Ничего не запустилось: побочных эффектов быть не могло.
	if _, err := run(t, r, "dry_run", map[string]any{"tool": "bash"}); err != nil {
		t.Errorf("предпросмотр без команды вернул ошибку вместо пояснения: %v", err)
	}
}

// TestDryRunCommandWithOtherWorkdir — каталог запуска отличается от рабочего.
func TestDryRunCommandWithOtherWorkdir(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	r := newTestReg(t, dir)

	res := mustRun(t, r, "dry_run", map[string]any{
		"tool": "job", "args": `{"command":"make test","workdir":"` + filepath.ToSlash(other) + `"}`,
	})
	if !strings.Contains(res.Text, "не равен рабочему каталогу") {
		t.Errorf("не отмечено отличие каталогов:\n%s", res.Text)
	}
}

// TestDryRunUnknownTool — несуществующий инструмент: список доступных, чтобы
// модель не гадала по буквам.
func TestDryRunUnknownTool(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	if _, err := run(t, r, "dry_run", map[string]any{"tool": "write_fil"}); err == nil {
		t.Fatal("ожидалась ошибка для несуществующего инструмента")
	}
	if _, err := run(t, r, "dry_run", nil); err == nil {
		t.Fatal("ожидалась ошибка «укажи tool»")
	}
}

// TestDryRunNetTools — для сетевых инструментов честно сказано, что файлы
// проекта не затрагиваются.
func TestDryRunNetTools(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	res := mustRun(t, r, "dry_run", map[string]any{"tool": "web_fetch"})
	if !strings.Contains(res.Text, "файлы проекта не меняются") {
		t.Errorf("сетевой инструмент описан неверно:\n%s", res.Text)
	}
}
