package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------- handoff и handoff_read ----------

// withHome — подменить каталог данных gcli на время теста.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Cleanup(overrideHome(home))
	return home
}

// todoSession — сессия с фиксированным планом.
type todoSession struct{ todos []TodoItem }

func (s *todoSession) SetTodos(todos []TodoItem) { s.todos = todos }
func (s *todoSession) Todos() []TodoItem         { return s.todos }

// TestHandoffSavesAndReadsBack — снимок пишется на диск и читается обратно.
// Без записи на диск он не пережил бы сжатие контекста, а без чтения следующий
// ход не узнал бы, на чём остановился предыдущий.
func TestHandoffSavesAndReadsBack(t *testing.T) {
	withHome(t)
	dir := t.TempDir()
	sess := &todoSession{}
	r := newTestReg(t, dir)
	r.env.Session = sess

	mustRun(t, r, "write_file", map[string]any{"path": "a.go", "content": "package a\n"})
	sess.SetTodos([]TodoItem{
		{Content: "починить парсер", Status: "in_progress"},
		{Content: "написать тест", Status: "pending"},
	})

	res := mustRun(t, r, "handoff", map[string]any{
		"next":    "дописать обработку ошибки",
		"blocked": "нужен ответ пользователя про формат",
		"done":    []any{"разобрался с причиной"},
	})
	if res.Error != "" {
		t.Fatalf("handoff вернул ошибку: %s", res.Error)
	}
	// Модель должна увидеть снимок сейчас, а не после следующего хода.
	if !strings.Contains(res.Text, "дописать обработку ошибки") {
		t.Errorf("в ответе нет следующего шага:\n%s", res.Text)
	}

	back := mustRun(t, r, "handoff_read", nil)
	for _, want := range []string{
		"починить парсер", "написать тест", "a.go",
		"дописать обработку ошибки", "нужен ответ пользователя",
	} {
		if !strings.Contains(back.Text, want) {
			t.Errorf("в снимке нет %q:\n%s", want, back.Text)
		}
	}
	// Незакрытый пункт помечен, закрытый — галочкой.
	if !strings.Contains(back.Text, "▸") || !strings.Contains(back.Text, "☐") {
		t.Errorf("статусы пунктов не отражены:\n%s", back.Text)
	}
}

// TestHandoffReadWithoutSnapshot — отсутствие снимка не ошибка, а понятная
// подсказка, что ход до handoff не дошёл.
func TestHandoffReadWithoutSnapshot(t *testing.T) {
	withHome(t)
	r := newTestReg(t, t.TempDir())
	res := mustRun(t, r, "handoff_read", nil)
	if !strings.Contains(res.Text, "Снимка состояния по этому проекту нет") {
		t.Errorf("нет внятного ответа об отсутствии снимка:\n%s", res.Text)
	}
}

// TestHandoffPerProject — снимки разных проектов не путаются: агент в чужом
// репозитории не должен читать чужой снимок и продолжать чужую работу.
func TestHandoffPerProject(t *testing.T) {
	withHome(t)
	dirA, dirB := t.TempDir(), t.TempDir()

	rA := newTestReg(t, dirA)
	mustRun(t, rA, "handoff", map[string]any{"next": "работа проекта A"})

	rB := newTestReg(t, dirB)
	mustRun(t, rB, "handoff", map[string]any{"next": "работа проекта B"})

	backA := mustRun(t, rA, "handoff_read", nil)
	if !strings.Contains(backA.Text, "проекта A") {
		t.Errorf("проект A прочитал чужой снимок:\n%s", backA.Text)
	}
	if strings.Contains(backA.Text, "проекта B") {
		t.Errorf("снимки разных проектов смешались:\n%s", backA.Text)
	}
}

// TestHandoffSurvivesContextCompaction — снимок на диске, а не в памяти
// агента: переживает новый процесс и сжатие контекста.
func TestHandoffSurvivesContextCompaction(t *testing.T) {
	withHome(t)
	dir := t.TempDir()
	r := newTestReg(t, dir)
	mustRun(t, r, "handoff", map[string]any{"next": "пережить перезапуск"})

	// Совсем новый реестр — как новый ход после сжатия.
	fresh := newTestReg(t, dir)
	back := mustRun(t, fresh, "handoff_read", nil)
	if !strings.Contains(back.Text, "пережить перезапуск") {
		t.Errorf("снимок не пережил новый ход:\n%s", back.Text)
	}
}

// TestAutoHandoffFromLiveState — автоматический снимок собирается из живого
// состояния реестра: модели в этот момент уже недоступно, полагаться на её
// слова было бы нельзя.
func TestAutoHandoffFromLiveState(t *testing.T) {
	withHome(t)
	dir := t.TempDir()
	sess := &todoSession{}
	r := newTestReg(t, dir)
	r.env.Session = sess

	mustRun(t, r, "write_file", map[string]any{"path": "changed.go", "content": "x\n"})
	sess.SetTodos([]TodoItem{{Content: "доделать правку", Status: "in_progress"}})

	txt, err := r.AutoHandoff("ход оборвался на лимите итераций")
	if err != nil {
		t.Fatalf("AutoHandoff вернул ошибку: %v", err)
	}
	if !strings.Contains(txt, "changed.go") {
		t.Errorf("снимок не содержит изменённых файлов:\n%s", txt)
	}
	if !strings.Contains(txt, "доделать правку") {
		t.Errorf("снимок не содержит плана:\n%s", txt)
	}
	if !strings.Contains(txt, "лимите итераций") {
		t.Errorf("снимок не объясняет, почему ход прерван:\n%s", txt)
	}
	// Снимок должен быть доступен следующему ходу.
	back := mustRun(t, r, "handoff_read", nil)
	if !strings.Contains(back.Text, "лимите итераций") {
		t.Errorf("автоснимок не сохранён на диск:\n%s", back.Text)
	}
}

// TestAutoHandoffWithoutChanges — пустой ход тоже даёт полезный снимок:
// сказано, что правок не было, и это лучше молчания.
func TestAutoHandoffWithoutChanges(t *testing.T) {
	withHome(t)
	r := newTestReg(t, t.TempDir())
	txt, err := r.AutoHandoff("обрыв")
	if err != nil {
		t.Fatalf("AutoHandoff вернул ошибку: %v", err)
	}
	if !strings.Contains(txt, "Ничего") {
		t.Errorf("не сказано, что правок не было:\n%s", txt)
	}
}

// TestAutoHandoffReportsLastVerify — снимок помнит результат последней
// проверки: названия падений без повода следующий ход повторит заново.
func TestAutoHandoffReportsLastVerify(t *testing.T) {
	withHome(t)
	dir := t.TempDir()
	r := newTestReg(t, dir)

	home := homeDir(t)
	if err := os.MkdirAll(filepath.Join(home, "verify"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := baselineData{
		Command:  "go test ./...",
		Failed:   2,
		Passed:   5,
		At:       time.Now(),
		Failures: []Failure{{Where: "gcli/tools", What: "TestJobStop"}},
	}
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baselinePath(dir), data, 0o644); err != nil {
		t.Fatal(err)
	}

	txt, err := r.AutoHandoff("обрыв")
	if err != nil {
		t.Fatalf("AutoHandoff вернул ошибку: %v", err)
	}
	if !strings.Contains(txt, "упало 2") {
		t.Errorf("нет числа падений:\n%s", txt)
	}
	if !strings.Contains(txt, "TestJobStop") {
		t.Errorf("нет названий падающих тестов:\n%s", txt)
	}
}

// TestAutoHandoffGreenBaseline — зелёный прогон тоже фиксируется: следующий
// ход не должен заново всё проверять.
func TestAutoHandoffGreenBaseline(t *testing.T) {
	withHome(t)
	dir := t.TempDir()
	r := newTestReg(t, dir)

	home := homeDir(t)
	if err := os.MkdirAll(filepath.Join(home, "verify"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(baselineData{Command: "go test ./...", Passed: 12, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baselinePath(dir), data, 0o644); err != nil {
		t.Fatal(err)
	}

	txt, _ := r.AutoHandoff("обрыв")
	if !strings.Contains(txt, "зелёный") {
		t.Errorf("зелёный прогон не отмечен:\n%s", txt)
	}
}

// TestChangesSince — файлы, изменённые после момента: снимок за ход не должен
// тащить правки предыдущих.
func TestChangesSince(t *testing.T) {
	dir := t.TempDir()
	r := newTestReg(t, dir)
	mustRun(t, r, "write_file", map[string]any{"path": "old.txt", "content": "1"})

	cut := time.Now()
	time.Sleep(2 * time.Millisecond)
	mustRun(t, r, "write_file", map[string]any{"path": "new.txt", "content": "2"})

	got := r.ChangesSince(cut)
	if len(got) != 1 {
		t.Fatalf("ChangesSince вернул %d правок, ожидалась 1", len(got))
	}
	if !strings.HasSuffix(got[0].Rel, "new.txt") {
		t.Errorf("вернулась не та правка: %s", got[0].Rel)
	}
}

// TestHandoffSlugStable — имя файла снимка устойчиво к разному написанию
// одного и того же пути, иначе снимок «теряется» после перезапуска.
func TestHandoffSlugStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	a := slugOf(dir)
	b := slugOf(filepath.Clean(dir) + string(filepath.Separator))
	if a != b {
		t.Errorf("slug неустойчив: %q против %q", a, b)
	}
	if slugOf(dir) == slugOf(filepath.Join(t.TempDir(), "other")) {
		t.Error("разные проекты получили одинаковый slug")
	}
}

// homeDir — каталог данных gcli, подменённый тестом.
func homeDir(t *testing.T) string {
	t.Helper()
	h := os.Getenv("GCLI_HOME")
	if h == "" {
		t.Fatal("GCLI_HOME не подменён")
	}
	return h
}
