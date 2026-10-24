package main

// Регрессионные тесты багфикс-прохода 6.2.1 (сервер/ввод): «A» в
// подтверждениях, структурированный /v1/mission, пометка таймаута в watch.

import (
	"strings"
	"testing"
	"time"

	"gcli/core"
	"gcli/ui"
)

// ansApp — приложение с буферным UI и подкачанными строками ввода.
func ansApp(t *testing.T, answers ...string) *app {
	t.Helper()
	buf := &strings.Builder{}
	a := &app{}
	a.ui = ui.New(ui.Options{
		Theme: "ember", Unicode: true, Color: false, Grade: ui.Color16,
		Animations: false, Width: 80, Out: buf,
	})
	s := &stdinReader{
		lines: make(chan string, len(answers)+1),
		keys:  make(chan byte, 8),
		out:   buf,
	}
	for _, ans := range answers {
		s.lines <- ans
	}
	a.stdin = s
	return a
}

// TestReadAnsUpperAChoosesAll — Shift+A обязан выбирать «все», а не
// молча спускаться до «всегда эта команда»: раньше ветка «A» была
// недостижима после ToLower.
func TestReadAnsUpperAChoosesAll(t *testing.T) {
	a := ansApp(t, "A")
	if got := a.readAns(true, true); got != confirmAll {
		t.Errorf("A с доступным «все» = %d, хотели %d (confirmAll)", got, confirmAll)
	}
	// «A» без доступного «все» — не «всегда», а переспрос, после которого
	// «y» нормально завершает диалог.
	a2 := ansApp(t, "A", "y")
	if got := a2.readAns(false, false); got != confirmYes {
		t.Errorf("после отказа «A» ответ «y» = %d, хотели %d (confirmYes)", got, confirmYes)
	}
	// Строчная «a» — по-прежнему «всегда».
	a3 := ansApp(t, "a")
	if got := a3.readAns(true, true); got != confirmAlways {
		t.Errorf("a = %d, хотели %d (confirmAlways)", got, confirmAlways)
	}
}

// TestWatchTimeoutLabeled — таймаут watch-проверки помечается как таймаут,
// а не «FAIL (код -1)» без объяснения.
func TestWatchTimeoutLabeled(t *testing.T) {
	dir := t.TempDir()
	_, code, timedOut := watchRunCommand(dir, slowCmd(), 300*time.Millisecond)
	if !timedOut {
		t.Error("ожидалась пометка таймаута")
	}
	if code == 0 {
		t.Error("таймаут не должен считаться успехом")
	}
	// Успешная команда не помечается таймаутом.
	_, code, timedOut = watchRunCommand(dir, shellCmd(0), 5*time.Second)
	if code != 0 || timedOut {
		t.Errorf("успешная команда: code=%d timedOut=%v", code, timedOut)
	}
}

// TestSessionIDValidation — id сессии из сети не должен проходить с «..»
// и разделителями (защита LoadSession/DeleteSession от обхода путей).
func TestSessionIDValidation(t *testing.T) {
	for _, bad := range []string{"../../etc/passwd", "", "..", "a/b", "a\\b", ".hidden", "x:y", strings.Repeat("x", 65)} {
		if core.ValidSessionID(bad) {
			t.Errorf("плохой id %q прошёл проверку", bad)
		}
	}
	for _, good := range []string{"20260102-150405-ab12", "sess_1", "S.PNG", "a-b_c.json"} {
		if !core.ValidSessionID(good) {
			t.Errorf("нормальный id %q отвергнут", good)
		}
	}
}
