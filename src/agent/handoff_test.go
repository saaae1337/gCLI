package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
	"gcli/tools"
)

// ---------- Автоматический handoff при обрыве хода ----------

// fakeSession — сессия для тестов автоснимка.
type fakeSession struct {
	msgs  []core.Message
	todos []tools.TodoItem
}

func (s *fakeSession) Messages() []core.Message    { return s.msgs }
func (s *fakeSession) AddMessage(m core.Message)   { s.msgs = append(s.msgs, m) }
func (s *fakeSession) AddUsage(u core.Usage)       {}
func (s *fakeSession) Turns() int                  { return len(s.msgs) }
func (s *fakeSession) SetTodos(t []tools.TodoItem) { s.todos = t }
func (s *fakeSession) Todos() []tools.TodoItem     { return s.todos }

// newHandoffAgent — агент с реестром в отдельном каталоге данных.
func newHandoffAgent(t *testing.T, dir string) (*Agent, *fakeSession, *[]string) {
	t.Helper()
	home := t.TempDir()
	old := os.Getenv("GCLI_HOME")
	os.Setenv("GCLI_HOME", home)
	t.Cleanup(func() { os.Setenv("GCLI_HOME", old) })

	sess := &fakeSession{}
	notes := &[]string{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	a := New(Deps{Registry: reg, Session: sess}, dir)
	a.OnNote = func(title, body string) { *notes = append(*notes, title+": "+body) }
	return a, sess, notes
}

// TestAutoHandoffAddsSnapshotToHistory — при обрыве хода снимок попадает в
// историю сессии. Без этого следующий ход начинается с нуля: модель не видит,
// что уже сделано, и повторяет работу или бросает её.
func TestAutoHandoffAddsSnapshotToHistory(t *testing.T) {
	dir := t.TempDir()
	a, sess, _ := newHandoffAgent(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("старое\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write := a.d.Registry.Get("write_file")
	if write == nil {
		t.Fatal("write_file не зарегистрирован")
	}
	if _, err := write.Handler(context.Background(), map[string]any{
		"path": "a.txt", "content": "новое\n",
	}); err != nil {
		t.Fatalf("write_file вернул ошибку: %v", err)
	}
	sess.SetTodos([]tools.TodoItem{{Content: "доделать правку", Status: "in_progress"}})

	a.autoHandoff(40)

	var found bool
	for _, m := range sess.Messages() {
		if strings.Contains(m.Content, "лимите итераций") &&
			strings.Contains(m.Content, "a.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("снимок не попал в историю сессию:\n%v", sess.Messages())
	}
}

// TestAutoHandoffExplainsWhyTurnEnded — снимок объясняет причину обрыва:
// следующий ход должен понимать, что произошло, а не гадать.
func TestAutoHandoffExplainsWhyTurnEnded(t *testing.T) {
	dir := t.TempDir()
	a, sess, _ := newHandoffAgent(t, dir)
	a.autoHandoff(17)

	if len(sess.Messages()) == 0 {
		t.Fatal("снимок не добавлен в историю")
	}
	if !strings.Contains(sess.Messages()[len(sess.Messages())-1].Content, "17") {
		t.Errorf("в снимке нет значения лимита:\n%s",
			sess.Messages()[len(sess.Messages())-1].Content)
	}
}

// TestAutoHandoffNoted — пользователь должен видеть, что снимок сохранён:
// иначе он не поймёт, почему следующий ход продолжает с середины.
func TestAutoHandoffNoted(t *testing.T) {
	dir := t.TempDir()
	a, _, notes := newHandoffAgent(t, dir)
	a.autoHandoff(40)

	if len(*notes) == 0 {
		t.Fatal("снимок не отмечен заметкой")
	}
	if !strings.Contains((*notes)[0], "handoff") {
		t.Errorf("заметка не про снимок: %s", (*notes)[0])
	}
}

// TestAutoHandoffSurvivesUnwritableHome — ошибка записи снимка не должна
// ломать ход: снимок — страховка, а не условие работы. Ход обязан
// финализироваться в любом случае.
func TestAutoHandoffSurvivesUnwritableHome(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	// Каталог на месте, но файлом: запись в него невозможна.
	blocker := filepath.Join(home, "handoff")
	if err := os.WriteFile(blocker, []byte("не каталог"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := os.Getenv("GCLI_HOME")
	os.Setenv("GCLI_HOME", home)
	t.Cleanup(func() { os.Setenv("GCLI_HOME", old) })

	sess := &fakeSession{}
	a := New(Deps{Registry: tools.New(tools.Env{WorkDir: dir, Session: sess}), Session: sess}, dir)
	notes := &[]string{}
	a.OnNote = func(title, body string) { *notes = append(*notes, title+": "+body) }

	// Не падаем — снимок просто не сохранится.
	a.autoHandoff(40)

	if len(*notes) == 0 {
		t.Error("об ошибке записи не сообщено")
	}
	if !strings.Contains((*notes)[0], "не удалось") {
		t.Errorf("ожидалась заметка об ошибке, получено: %s", (*notes)[0])
	}
}

// TestAutoHandoffSkippedWithoutTools — без инструментов ход не обрывается по
// лимиту итераций, поэтому снимок не нужен.
func TestAutoHandoffSkippedWithoutTools(t *testing.T) {
	dir := t.TempDir()
	a, sess, notes := newHandoffAgent(t, dir)
	a = a.WithAgentMode(false)

	a.autoHandoff(40)

	if len(sess.Messages()) != 0 {
		t.Error("снимок добавлен без инструментов")
	}
	if len(*notes) != 0 {
		t.Errorf("заметка без инструментов: %v", *notes)
	}
}
