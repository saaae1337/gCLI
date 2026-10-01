package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------- Тесты пакетного режима ----------

// newMultiReg — реестр в новом временном каталоге с прогрессом.
func newMultiReg(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r := New(Env{WorkDir: dir})
	return r, dir
}

// mustWriteFile — создать файл внутри каталога и вернуть полный путь.
func mustWriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// callTool — вызвать инструмент по имени, падая на ошибке.
func callTool(t *testing.T, r *Registry, name string, args map[string]any) Result {
	t.Helper()
	tool := r.Get(name)
	if tool == nil {
		t.Fatalf("инструмент %s не найден", name)
	}
	res, err := tool.Handler(context.Background(), args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// TestMultiReadReadsAllFilesInOrder — пакетное чтение отдаёт все файлы
// в порядке аргументов, а не в порядке завершения.
func TestMultiReadReadsAllFilesInOrder(t *testing.T) {
	r, dir := newMultiReg(t)
	mustWriteFile(t, dir, "a.txt", "alpha\n")
	mustWriteFile(t, dir, "b.txt", "bravo\n")
	mustWriteFile(t, dir, "c.txt", "charlie\n")

	res := callTool(t, r, "multi_read", map[string]any{
		"paths": []any{"c.txt", "a.txt", "b.txt"},
	})
	for _, want := range []string{"charlie", "alpha", "bravo"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("в отчёте нет %q:\n%s", want, res.Text)
		}
	}
	ia, ic := strings.Index(res.Text, "alpha"), strings.Index(res.Text, "charlie")
	if ic > ia {
		t.Errorf("порядок нарушен: c.txt должен идти раньше a.txt")
	}
	if !strings.Contains(res.Summary, "3 прочитано") {
		t.Errorf("сводка неверна: %s", res.Summary)
	}
}

// TestMultiReadDedupesPaths — повторяющийся путь выполняется один раз и
// не задваивается в отчёте.
func TestMultiReadDedupesPaths(t *testing.T) {
	r, dir := newMultiReg(t)
	mustWriteFile(t, dir, "one.txt", "solo\n")

	res := callTool(t, r, "multi_read", map[string]any{
		"paths": []any{"one.txt", "one.txt", "./one.txt"},
	})
	if n := strings.Count(res.Text, "solo"); n != 1 {
		t.Errorf("содержимое показано %d раз, ожидался 1:\n%s", n, res.Text)
	}
	if !strings.Contains(res.Summary, "1 прочитано") {
		t.Errorf("сводка: %s", res.Summary)
	}
}

// TestMultiReadIsolatesErrors — несуществующий файл не роняет пачку.
func TestMultiReadIsolatesErrors(t *testing.T) {
	r, dir := newMultiReg(t)
	mustWriteFile(t, dir, "good.txt", "есть\n")

	res := callTool(t, r, "multi_read", map[string]any{
		"paths": []any{"good.txt", "missing.txt"},
	})
	if !strings.Contains(res.Text, "есть") {
		t.Errorf("хороший файл потерян:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "ОШИБКА") {
		t.Errorf("нет пометки об ошибке:\n%s", res.Text)
	}
	if !strings.Contains(res.Summary, "1 с ошибкой") {
		t.Errorf("сводка: %s", res.Summary)
	}
}

// TestMultiReadSinglePathAccepted — модель часто передаёт path вместо paths.
func TestMultiReadSinglePathAccepted(t *testing.T) {
	r, dir := newMultiReg(t)
	mustWriteFile(t, dir, "solo.txt", "solo\n")

	res := callTool(t, r, "multi_read", map[string]any{"path": "solo.txt"})
	if !strings.Contains(res.Text, "solo") {
		t.Errorf("одиночный path не прочитан:\n%s", res.Text)
	}
}

// TestMultiReadBudgetTruncatesTail — общий бюджет режет хвост пачки, но
// помечает это явно: молчаливое обрезание выглядит как «файла нет».
func TestMultiReadBudgetTruncatesTail(t *testing.T) {
	r, dir := newMultiReg(t)
	mustWriteFile(t, dir, "big1.txt", strings.Repeat("x\n", 900))
	mustWriteFile(t, dir, "big2.txt", strings.Repeat("y\n", 900))

	res := callTool(t, r, "multi_read", map[string]any{
		"paths":     []any{"big1.txt", "big2.txt"},
		"max_chars": 3000,
	})
	if !strings.Contains(res.Text, "исчерпан общий бюджет") {
		t.Errorf("нет пометки об исчерпании бюджета:\n%s", res.Text)
	}
	if !strings.Contains(res.Summary, "усечено") {
		t.Errorf("сводка не отметила усечение: %s", res.Summary)
	}
}

// TestMultiReadRejectsTooMany — лимит целей на вызов.
func TestMultiReadRejectsTooMany(t *testing.T) {
	r, _ := newMultiReg(t)
	var paths []any
	for i := 0; i < multiMaxTargets+1; i++ {
		paths = append(paths, fmt.Sprintf("f%d.txt", i))
	}
	_, err := r.hMultiRead(context.Background(), map[string]any{"paths": paths})
	if err == nil || !strings.Contains(err.Error(), "максимум") {
		t.Errorf("ожидалась ошибка о лимите, получено: %v", err)
	}
}

// TestMultiEditAppliesEachFile — пачка правок применяется к разным файлам.
func TestMultiEditAppliesEachFile(t *testing.T) {
	r, dir := newMultiReg(t)
	p1 := mustWriteFile(t, dir, "one.go", "package a\n\nfunc Old() {}\n")
	p2 := mustWriteFile(t, dir, "two.go", "package b\n\nfunc Old() {}\n")
	// edit_file требует предварительного чтения.
	_, _ = r.hReadFile(context.Background(), map[string]any{"path": p1})
	_, _ = r.hReadFile(context.Background(), map[string]any{"path": p2})

	res := callTool(t, r, "multi_edit", map[string]any{"edits": []any{
		map[string]any{"path": p1, "old_string": "func Old()", "new_string": "func New()"},
		map[string]any{"path": p2, "old_string": "func Old()", "new_string": "func Fresh()"},
	}})
	if !strings.Contains(res.Summary, "2 применено") {
		t.Errorf("сводка: %s (текст: %s)", res.Summary, res.Text)
	}
	for p, want := range map[string]string{p1: "func New()", p2: "func Fresh()"} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s не обновлён:\n%s", p, data)
		}
	}
}

// TestMultiEditRejectsDuplicatePath — две правки одного файла запрещены:
// параллельно они бы затирали друг друга.
func TestMultiEditRejectsDuplicatePath(t *testing.T) {
	r, dir := newMultiReg(t)
	p := mustWriteFile(t, dir, "dup.go", "aaa\nbbb\n")

	_, err := r.hMultiEdit(context.Background(), map[string]any{"edits": []any{
		map[string]any{"path": p, "old_string": "aaa", "new_string": "xxx"},
		map[string]any{"path": p, "old_string": "bbb", "new_string": "yyy"},
	}})
	if err == nil || !strings.Contains(err.Error(), "дважды") {
		t.Fatalf("ожидался отказ по дублю файла, получено: %v", err)
	}
	// Файл не тронут.
	data, _ := os.ReadFile(p)
	if string(data) != "aaa\nbbb\n" {
		t.Errorf("файл изменён несмотря на отказ:\n%s", data)
	}
}

// TestMultiEditAtomicRollsBack — при ошибке в одной правке не применяется
// ни одна: файлы остаются как были.
func TestMultiEditAtomicRollsBack(t *testing.T) {
	r, dir := newMultiReg(t)
	p1 := mustWriteFile(t, dir, "ok1.go", "keep me\n")
	p2 := mustWriteFile(t, dir, "bad.go", "no such string\n")

	res := callTool(t, r, "multi_edit", map[string]any{"edits": []any{
		map[string]any{"path": p1, "old_string": "keep me", "new_string": "changed"},
		map[string]any{"path": p2, "old_string": "missing", "new_string": "x"},
	}, "atomic": true})

	data, _ := os.ReadFile(p1)
	if string(data) != "keep me\n" {
		t.Errorf("atomic-пачка применила первую правку: %s", data)
	}
	if !strings.Contains(res.Text, "Ни один файл не изменён") {
		t.Errorf("нет честного сообщения об откате:\n%s", res.Text)
	}
}

// TestMultiEditAtomicAppliesAll — успешная atomic-пачка применяет всё.
func TestMultiEditAtomicAppliesAll(t *testing.T) {
	r, dir := newMultiReg(t)
	p1 := mustWriteFile(t, dir, "f1.txt", "old one\n")
	p2 := mustWriteFile(t, dir, "f2.txt", "old two\n")

	callTool(t, r, "multi_edit", map[string]any{"edits": []any{
		map[string]any{"path": p1, "old_string": "old one", "new_string": "new one"},
		map[string]any{"path": p2, "old_string": "old two", "new_string": "new two"},
	}, "atomic": true})

	for p, want := range map[string]string{p1: "new one", p2: "new two"} {
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), want) {
			t.Errorf("%s: ожидалось %q, получено %q", p, want, data)
		}
	}
}

// TestMultiEditReadOnlyBlocked — в режиме «только чтение» пакетная правка
// запрещена целиком, а не «частично проходит».
func TestMultiEditReadOnlyBlocked(t *testing.T) {
	dir := t.TempDir()
	r := New(Env{WorkDir: dir, ReadOnly: true})
	res, err := r.hMultiEdit(context.Background(), map[string]any{"edits": []any{
		map[string]any{"path": "x.txt", "old_string": "a", "new_string": "b"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "только чтение") {
		t.Errorf("ожидался отказ read-only, получено: %+v", res)
	}
}

// TestMultiEditLoggedForRevert — правки пачки попадают в журнал и откатываются.
func TestMultiEditLoggedForRevert(t *testing.T) {
	r, dir := newMultiReg(t)
	p := mustWriteFile(t, dir, "log.txt", "before\n")
	// edit_file требует предварительного чтения — и multi_edit тоже.
	_, _ = r.hReadFile(context.Background(), map[string]any{"path": p})

	callTool(t, r, "multi_edit", map[string]any{"edits": []any{
		map[string]any{"path": p, "old_string": "before", "new_string": "after"},
	}})

	list := r.change().all()
	if len(list) != 1 || list[0].Label != "multi_edit" {
		t.Fatalf("правка не записана в журнал: %+v", list)
	}
	if _, err := r.hRevertLast(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("откат: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "before\n" {
		t.Errorf("откат не восстановил файл: %q", data)
	}
}

// TestMultiGrepAcrossDirs — один паттерн по нескольким каталогам.
func TestMultiGrepAcrossDirs(t *testing.T) {
	r, dir := newMultiReg(t)
	mustWriteFile(t, dir, "one/keep.go", "package one\n")
	mustWriteFile(t, dir, "two/keep.go", "package two\n")

	res := callTool(t, r, "multi_grep", map[string]any{
		"pattern": "package",
		"paths":   []any{"one", "two"},
		"include": "*.go",
	})
	// Пути приходят от filepath.RelToWD, а он на Windows печатает обратные
	// слэши. Сравниваем через filepath.ToSlash: тест проверяет контракт
	// «совпадение есть», а не формат разделителя.
	if !strings.Contains(filepath.ToSlash(res.Text), "one/keep.go") ||
		!strings.Contains(filepath.ToSlash(res.Text), "two/keep.go") {
		t.Errorf("совпадения из обоих каталогов не найдены:\n%s", res.Text)
	}
}

// TestMultiGrepNoMatches — пустой результат не выглядит ошибкой.
func TestMultiGrepNoMatches(t *testing.T) {
	r, dir := newMultiReg(t)
	mustWriteFile(t, dir, "a.txt", "ничего\n")
	res := callTool(t, r, "multi_grep", map[string]any{"pattern": "zzz", "paths": []any{"."}})
	if !strings.Contains(res.Summary, "совпадений нет") {
		t.Errorf("сводка: %s", res.Summary)
	}
}

// TestMultiBashRunsAll — команды выполняются, у каждой виден свой вывод.
func TestMultiBashRunsAll(t *testing.T) {
	r, _ := newMultiReg(t)
	res := callTool(t, r, "multi_bash", map[string]any{
		"commands": []any{"echo one", "echo two"},
	})
	if !strings.Contains(res.Text, "one") || !strings.Contains(res.Text, "two") {
		t.Errorf("не все команды выполнены:\n%s", res.Text)
	}
	if !strings.Contains(res.Summary, "2 успешно") {
		t.Errorf("сводка: %s", res.Summary)
	}
}

// TestMultiBashKeepsOrder — команды нумеруются в порядке аргументов.
func TestMultiBashKeepsOrder(t *testing.T) {
	r, _ := newMultiReg(t)
	res := callTool(t, r, "multi_bash", map[string]any{
		"commands":   []any{"echo AAA", "echo BBB", "echo CCC"},
		"sequential": true,
	})
	iA, iB, iC := strings.Index(res.Text, "AAA"), strings.Index(res.Text, "BBB"), strings.Index(res.Text, "CCC")
	if iA < 0 || iB < 0 || iC < 0 || !(iA < iB && iB < iC) {
		t.Errorf("порядок команд нарушен:\n%s", res.Text)
	}
}

// TestMultiBashTrimsOutput — длинный вывод режется до max_output на команду.
func TestMultiBashTrimsOutput(t *testing.T) {
	r, _ := newMultiReg(t)
	res := callTool(t, r, "multi_bash", map[string]any{
		"commands":   []any{"for i in $(seq 1 50); do echo line-$i; done"},
		"max_output": 5,
	})
	if !strings.Contains(res.Text, "ещё 45 строк") {
		t.Errorf("вывод не усечён:\n%s", res.Text)
	}
}

// TestMultiBashDedupe — одинаковые команды не выполняются дважды.
func TestMultiBashDedupe(t *testing.T) {
	r, _ := newMultiReg(t)
	res := callTool(t, r, "multi_bash", map[string]any{
		"commands": []any{"echo dup", "echo dup"},
	})
	if !strings.Contains(res.Summary, "1 успешно") {
		t.Errorf("дубли не убраны: %s", res.Summary)
	}
}

// TestProgressEventsFired — пачка отдаёт прогресс: по событию на цель плюс
// финальное. Без этого UI не может показать «3/7», а пользователь гадает.
func TestProgressEventsFired(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"p1.txt", "p2.txt", "p3.txt"} {
		mustWriteFile(t, dir, n, "x\n")
	}
	var mu sync.Mutex
	var events []ProgressEvent
	r := New(Env{WorkDir: dir, OnProgress: func(ev ProgressEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}})
	if _, err := r.hMultiRead(context.Background(), map[string]any{
		"paths": []any{"p1.txt", "p2.txt", "p3.txt"},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 4 {
		t.Fatalf("событий %d, ожидалось 4 (3 цели + финал)", len(events))
	}
	if events[0].Title != "multi_read" || events[0].Total != 3 {
		t.Errorf("первое событие: %+v", events[0])
	}
	last := events[len(events)-1]
	if !last.Final || !last.Ok || last.Done != 3 {
		t.Errorf("финальное событие: %+v", last)
	}
}

// TestMultiToolsNeverPanic — паника внутри одной цели не роняет пачку.
func TestMultiToolsNeverPanic(t *testing.T) {
	items := []batchItem{{label: "a"}, {err: fmt.Errorf("boom")}}
	if items[0].Failed() || !items[1].Failed() {
		t.Errorf("Failed() определён неверно")
	}
	res, err := runSafe(func(context.Context) (Result, error) {
		panic("испорченный инструмент")
	}, context.Background())
	if err == nil {
		t.Errorf("паника не перехвачена: %+v", res)
	}
}

// TestDedupeHelpers — утилиты дедупликации.
func TestDedupeHelpers(t *testing.T) {
	got := dedupeStrings([]string{"a", "b", "a", "c", "b"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("dedupeStrings: %v", got)
	}
	paths := dedupePaths([]string{"src/a.go", "src\\a.go", " src/a.go ", ""})
	if len(paths) != 1 {
		t.Errorf("dedupePaths: %v", paths)
	}
}

// TestMultiRegisteredAsBuiltins — все четыре инструмента на месте.
func TestMultiRegisteredAsBuiltins(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	for _, name := range []string{"multi_read", "multi_edit", "multi_grep", "multi_bash"} {
		if !r.Has(name) {
			t.Errorf("%s не зарегистрирован", name)
		}
	}
}
