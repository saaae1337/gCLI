package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestSessionSnapshotIsDeepCopy — снимок не делит срезы и карту грантов
// с живой сессией: marshal снимка идёт за пределами sessMu, и append
// писателя в общий массив был бы гонкой чтения с записью.
func TestSessionSnapshotIsDeepCopy(t *testing.T) {
	a := testApp(t)
	a.AddMessage(core.Message{Role: core.RoleUser, Content: "первое"})
	a.grantBashExact("ls -la")

	sessMu.Lock()
	snap := a.sessionSnapshotLocked()
	sessMu.Unlock()

	// Писатели продолжают менять живую сессию после снимка.
	a.AddMessage(core.Message{Role: core.RoleAssistant, Content: "второе"})
	a.grantBashExact("rm -rf /")
	a.recordSubagent(core.SubagentRecord{ID: "s1", Name: "исследователь", Status: "done"})

	if len(snap.Messages) != 1 {
		t.Errorf("в снимке %d сообщений, ожидалось 1 — срез не скопирован", len(snap.Messages))
	}
	if len(snap.Perms.BashExact) != 1 || snap.Perms.BashExact["rm -rf /"] {
		t.Errorf("карта грантов в снимке общая с сессией: %v", snap.Perms.BashExact)
	}
	if len(snap.SubagentRuns) != 0 {
		t.Errorf("в снимке %d записей журнала, ожидалось 0 — срез не скопирован", len(snap.SubagentRuns))
	}
	if got := a.subagentRunCount(); got != 1 {
		t.Errorf("журнал живой сессии = %d, ожидалось 1 — снимок не должен писать в оригинал", got)
	}
}

// TestSaveSessionDoesNotHoldSessMuDuringSave — тяжёлая сессия: marshal
// и запись занимают сотни миллисекунд, и всё это время чтение истории
// обязано работать. Старый код звал SaveSession под sessMu — Messages()
// ждала окончания marshal и дискового I/O вместе со всем остальным.
func TestSaveSessionDoesNotHoldSessMuDuringSave(t *testing.T) {
	a := testApp(t)
	const n = 120000
	big := strings.Repeat("слово ", 80)
	sessMu.Lock()
	a.sess.Messages = make([]core.Message, n)
	for i := range a.sess.Messages {
		a.sess.Messages[i] = core.Message{Role: core.RoleUser, Content: big}
	}
	sessMu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.saveSession()
	}()
	// Даём saveSession дойти до маршала: на старом коде marshal идёт под замком.
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	_ = a.Messages()
	waited := time.Since(start)
	if waited > 400*time.Millisecond {
		t.Fatalf("Messages() ждала %v — saveSession держит sessMu на marshal и записи", waited)
	}
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("saveSession не завершился за 60с")
	}
}
