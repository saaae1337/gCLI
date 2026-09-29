package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectVerifyGo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cands := DetectVerify(dir)
	if len(cands) == 0 {
		t.Fatal("не найдено ни одной команды проверки для Go-проекта")
	}
	found := false
	for _, c := range cands {
		if c.Cmd == "go test ./..." {
			found = true
		}
	}
	if !found {
		t.Errorf("не предложена go test ./..., предложено: %v", cands)
	}
}

func TestDetectVerifyGitFirst(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cands := DetectVerify(dir)
	if len(cands) == 0 || !strings.Contains(cands[0].Cmd, "git status") {
		t.Errorf("ветка должна проверяться первой, получили: %v", cands)
	}
}

func TestDetectVerifyEmptyProject(t *testing.T) {
	if got := DetectVerify(t.TempDir()); len(got) != 0 {
		t.Errorf("пустому проекту не нужны команды проверки, получено: %v", got)
	}
}

func TestDetectVerifyNode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cands := DetectVerify(dir)
	if len(cands) == 0 || !strings.Contains(cands[0].Cmd, "npm test") {
		t.Errorf("для Node-проекта ожидался npm test, получено: %v", cands)
	}
}

func TestParseVerifySuccess(t *testing.T) {
	rep := ParseVerify("go test ./...", 0, 2*time.Second, "ok  gcli/core  0.5s\nok  gcli/tools 1.2s")
	if !rep.OK {
		t.Error("код выхода 0 должен означать успех")
	}
	if !strings.Contains(rep.Text(), "Проверка пройдена") {
		t.Errorf("нет вердикта:\n%s", rep.Text())
	}
}

func TestParseVerifyEmptySuccessIsSuspicious(t *testing.T) {
	// Код 0 и полностью пустой вывод — подозрительно, и об этом надо сказать.
	rep := ParseVerify("make check", 0, time.Second, "   \n\n")
	if !strings.Contains(rep.Text(), "ничего не проверяет") {
		t.Errorf("пустая проверка не помечена как подозрительная:\n%s", rep.Text())
	}
}

// «go test ./...» без -v не печатает счётчиков — ругаться на такой вывод
// нельзя, иначе инструмент врёт на каждом зелёном прогоне.
func TestParseVerifyGoOkWithoutCountersIsNotSuspicious(t *testing.T) {
	rep := ParseVerify("go test ./...", 0, time.Second, "ok  \tlive/p\t0.464s\n")
	if strings.Contains(rep.Text(), "ничего не проверяет") {
		t.Errorf("нормальный вывод go test принят за пустую проверку:\n%s", rep.Text())
	}
}

func TestParseVerifyGoCompileError(t *testing.T) {
	out := "./tools/verify.go:120:6: undefined: someFunc\n./tools/self.go:12:3: syntax error"
	rep := ParseVerify("go build ./...", 1, time.Second, out)
	if rep.OK {
		t.Error("код выхода 1 не должен считаться успехом")
	}
	if len(rep.Diagnostics) != 2 {
		t.Fatalf("ожидалось 2 диагностики, получено %d: %+v", len(rep.Diagnostics), rep.Diagnostics)
	}
	d := rep.Diagnostics[0]
	if d.File != "./tools/verify.go" || d.Line != 120 || d.Col != 6 {
		t.Errorf("диагностика разобрана неверно: %+v", d)
	}
	if !strings.Contains(d.Msg, "undefined: someFunc") {
		t.Errorf("потеряно сообщение компилятора: %q", d.Msg)
	}
	if !strings.Contains(rep.Text(), "verify.go:120") {
		t.Errorf("в отчёте нет адреса ошибки:\n%s", rep.Text())
	}
}

func TestParseVerifyTestFailures(t *testing.T) {
	out := "=== RUN   TestFoo\n--- FAIL: TestFoo (0.00s)\n    foo_test.go:10: got 3, want 5\nFAIL\nFAIL\tgcli/tools\t1.2s\n3 failed, 12 passed"
	rep := ParseVerify("go test ./...", 1, time.Second, out)
	if len(rep.Failures) != 1 {
		t.Fatalf("ожидалось 1 падение, получено %d: %+v", len(rep.Failures), rep.Failures)
	}
	if !strings.Contains(rep.Failures[0].Where, "TestFoo") {
		t.Errorf("не распознано имя теста: %q", rep.Failures[0].Where)
	}
	if !strings.Contains(rep.Failures[0].Output, "got 3, want 5") {
		t.Errorf("потеряно тело падения: %q", rep.Failures[0].Output)
	}
	// Заголовок отвечает на вопрос «какое», тело — на «почему». В отчёт
	// должно идти второе, иначе агент чинит падение вслепую.
	if !strings.Contains(rep.Failures[0].What, "got 3, want 5") {
		t.Errorf("в What вместо причины заголовок: %q", rep.Failures[0].What)
	}
}

// Тело падения печатается табами — так его выдаёт сам go test.
func TestParseVerifyReasonFromTabbedBody(t *testing.T) {
	out := "--- FAIL: TestAdd (0.00s)\n\tp_test.go:7: got 0, want 4\nFAIL\nFAIL\tlive/p\t0.469s\nFAIL"
	rep := ParseVerify("go test ./...", 1, time.Second, out)
	if len(rep.Failures) != 1 {
		t.Fatalf("ожидалось 1 падение, получено %d: %+v", len(rep.Failures), rep.Failures)
	}
	if got := rep.Failures[0].What; !strings.Contains(got, "p_test.go:7") || !strings.Contains(got, "got 0, want 4") {
		t.Errorf("причина падения не извлечена: %q", got)
	}
}

// Сводка пакета не должна выдаваться за отдельное падение.
func TestParseVerifyIgnoresPackageSummary(t *testing.T) {
	out := "--- FAIL: TestFoo (0.00s)\n    foo_test.go:10: got 3, want 5\nFAIL\nFAIL\tgcli/tools\t1.2s\n"
	rep := ParseVerify("go test ./...", 1, time.Second, out)
	if len(rep.Failures) != 1 {
		t.Fatalf("сводка пакета принята за падение теста: %+v", rep.Failures)
	}
	if strings.Contains(rep.Failures[0].Where, "gcli/tools") {
		t.Errorf("в отчёте пакет вместо теста: %q", rep.Failures[0].Where)
	}
}

func TestParseVerifyUnknownFailureKeepsTail(t *testing.T) {
	out := "какая-то невнятная ошибка\n" + strings.Repeat("мусор\n", 40) + "ФИНАЛ: нет такого символа"
	rep := ParseVerify("make", 2, time.Second, out)
	if len(rep.Failures) == 0 {
		t.Fatal("даже нераспознанный сбой должен попасть в отчёт")
	}
	// Хвост вывода важнее начала: причина обычно в конце.
	if !strings.Contains(rep.Failures[0].Output, "ФИНАЛ") {
		t.Errorf("хвост вывода потерян:\n%s", rep.Failures[0].Output)
	}
}

// Главный сценарий: правка внесла регрессию — агент должен увидеть именно её.
func TestVerifyDiffDetectsRegression(t *testing.T) {
	prev := &VerifyReport{
		Command:  "go test ./...",
		Failures: []Failure{{Where: "TestOld", What: "старое падение"}},
		Failed:   1,
		ExitCode: 1,
		OK:       false,
	}
	cur := &VerifyReport{
		Command:  "go test ./...",
		Failures: []Failure{{Where: "TestOld", What: "старое падение"}, {Where: "TestNew", What: "got 3, want 5"}},
		Failed:   2,
		ExitCode: 1,
		OK:       false,
	}
	cur.Diff(prev)
	if len(cur.NewFailures) != 1 || cur.NewFailures[0].Where != "TestNew" {
		t.Fatalf("ожидалось 1 новое падение TestNew, получено: %+v", cur.NewFailures)
	}
	if cur.Fixed != 0 {
		t.Errorf("Fixed = %d, ожидалось 0", cur.Fixed)
	}
	txt := cur.Text()
	for _, want := range []string{"НОВЫЕ падения", "TestNew", "Это твоя регрессия", "стало хуже"} {
		if !strings.Contains(txt, want) {
			t.Errorf("в отчёте нет %q:\n%s", want, txt)
		}
	}
}

func TestVerifyDiffDetectsImprovement(t *testing.T) {
	prev := &VerifyReport{
		Failures: []Failure{{Where: "A", What: "x"}, {Where: "B", What: "y"}},
		Failed:   2,
		ExitCode: 1,
		OK:       false,
	}
	// Единственное падение устранено, код выхода 0 — правка сработала.
	cur := &VerifyReport{ExitCode: 0, OK: true}
	cur.Diff(prev)
	if len(cur.NewFailures) != 0 {
		t.Errorf("новых падений быть не должно: %+v", cur.NewFailures)
	}
	if cur.Fixed != 2 {
		t.Errorf("Fixed = %d, ожидалось 2", cur.Fixed)
	}
	if !strings.Contains(cur.Text(), "Правка работает") {
		t.Errorf("успех правки не отмечен:\n%s", cur.Text())
	}
}

// Правка уменьшила число падений, но проект ещё красный: хвалить нельзя.
func TestVerifyDiffPartialImprovement(t *testing.T) {
	prev := &VerifyReport{Failures: []Failure{{Where: "A", What: "x"}, {Where: "B", What: "y"}}, ExitCode: 1, OK: false}
	cur := &VerifyReport{Failures: []Failure{{Where: "A", What: "x"}}, ExitCode: 1, OK: false}
	cur.Diff(prev)
	txt := cur.Text()
	if !strings.Contains(txt, "сдвинула в сторону успеха") {
		t.Errorf("частичное улучшение не отмечено:\n%s", txt)
	}
	if strings.Contains(txt, "Правка работает") {
		t.Errorf("неполная правка не должна выглядеть как успех:\n%s", txt)
	}
}

func TestVerifyDiffNoChange(t *testing.T) {
	prev := &VerifyReport{Failures: []Failure{{Where: "A", What: "x"}}, ExitCode: 1}
	cur := &VerifyReport{Failures: []Failure{{Where: "A", What: "x"}}, ExitCode: 1, OK: false}
	cur.Diff(prev)
	if !strings.Contains(cur.Text(), "не изменилось") {
		t.Errorf("отсутствие прогресса не отмечено:\n%s", cur.Text())
	}
}

func TestVerifyDiffWithoutBaseline(t *testing.T) {
	cur := &VerifyReport{Failures: []Failure{{Where: "A", What: "x"}}, ExitCode: 1, OK: false}
	cur.Diff(nil)
	if cur.HadBaseline {
		t.Error("без базовой линии HadBaseline должен быть false")
	}
	if strings.Contains(cur.Text(), "НОВЫЕ") {
		t.Error("без базовой линии нельзя утверждать, что падение новое")
	}
}

func TestVerifyHint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := VerifyHint(dir)
	if !strings.Contains(h, "go test") {
		t.Errorf("подсказка без команды проверки:\n%s", h)
	}
	if VerifyHint(t.TempDir()) != "" {
		t.Error("для неизвестного проекта подсказка должна быть пустой")
	}
}

// Сквозной сценарий: verify на настоящем временном проекте.
func TestVerifyToolEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module vtest\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p", "p.go"), []byte("package p\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GCLI_HOME", t.TempDir()) // изолируем базовую линию
	r := New(Env{WorkDir: dir})

	res, err := r.hVerify(nil, map[string]any{"command": "go test ./..."})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(res.Text, "Проверка пройдена") {
		t.Errorf("ожидался успех:\n%s", res.Text)
	}

	// Ломаем код по-настоящему — ссылка на несуществующий символ, а не
	// рабочая формула: умножение int компилируется и проверку не срабатывает.
	if err := os.WriteFile(filepath.Join(dir, "p", "p.go"), []byte("package p\n\nfunc Add(a, b int) int { return a + Missing(a, b) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := r.hVerify(nil, map[string]any{"command": "go build ./..."})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if strings.Contains(res2.Text, "Проверка пройдена") {
		t.Errorf("сломанный код прошёл проверку:\n%s", res2.Text)
	}
	if !strings.Contains(res2.Text, "undefined") {
		t.Errorf("причина сбоя не разобрана:\n%s", res2.Text)
	}
}

func TestVerifySuggestModeRunsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: dir})
	res, err := r.hVerify(nil, map[string]any{"suggest": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "go test") || !strings.Contains(res.Text, "ПОСЛЕ каждой") {
		t.Errorf("подсказка неполная:\n%s", res.Text)
	}
}

// Baseline разных проектов не должны смешиваться: иначе прогон в соседнем
// репозитории объявляется «сравнением» с чужими падениями.
func TestBaselineIsPerProject(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	if baselinePath(a) == baselinePath(b) {
		t.Fatal("у разных проектов должен быть свой baseline")
	}
	if baselinePath(a) != baselinePath(a) {
		t.Error("baseline одного проекта должен быть стабильным")
	}
	if !strings.HasPrefix(filepath.Base(baselinePath(a)), "verify") &&
		filepath.Base(filepath.Dir(baselinePath(a))) != "verify" {
		t.Errorf("неожиданный путь baseline: %s", baselinePath(a))
	}
}
