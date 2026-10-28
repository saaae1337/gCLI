package main

// Регрессии фонового хода: cron-ход не должен читать stdin. Раньше
// confirm/askUser/askTrustCode в запасном пути звали readAns, и второй
// Scan поверх REPL-цикла крал его строку: «y» для агента уходило в
// REPL (или наоборот), а вопрос без человека висел до его возвращения.
// Теперь фоновый ход отказывает сам себе — видимо и без чтения ввода.

import (
	"strings"
	"testing"

	"gcli/tools"
)

// bgStdin — stdinReader с одной строкой в буфере: после правдоподобной
// кражи её не должно быть, тест проверяет это повторным Scan.
func bgStdin(line string) *stdinReader {
	s := &stdinReader{lines: make(chan string, 1), keys: make(chan byte, 1)}
	s.lines <- line
	return s
}

// setBgTurn — пометить приложение, будто ход идёт из фоновой горутины
// (как делает cronTurn) или из читателя ввода (как turn).
func setBgTurn(a *app, bg bool) {
	a.mu.Lock()
	a.turnInteractive = !bg
	a.mu.Unlock()
}

// TestBackgroundConfirmDeniesWithoutStdin — фоновый ход просит подтвердить
// команду: отказ без чтения stdin, строка пользователя остаётся на месте.
func TestBackgroundConfirmDeniesWithoutStdin(t *testing.T) {
	a, buf := cmdApp(t)
	a.stdin = bgStdin("y") // человек ответил бы «да» — но это строка REPL
	setBgTurn(a, true)

	if a.confirm(tools.ConfirmReq{Kind: tools.ConfirmExec, Detail: "ls", Reason: "проверка"}) {
		t.Fatal("фоновой ход не должен получить подтверждение без человека")
	}
	if !strings.Contains(out(buf), "отклонено") {
		t.Errorf("отказ обязан быть видимым, вывод:\n%s", out(buf))
	}
	if !strings.Contains(out(buf), "/autopilot on") {
		t.Errorf("нужна подсказка про автономный режим, вывод:\n%s", out(buf))
	}
	// Строка не украдена: «y» дождался настоящего читателя.
	if !a.stdin.Scan() || a.stdin.Text() != "y" {
		t.Error("stdin-строка потеряна — фоновый ход её украл")
	}
}

// TestInteractiveConfirmStillAsks — ход из читателя ввода продолжает
// спрашивать: «n» потребляется readAns, канал остаётся пустым.
func TestInteractiveConfirmStillAsks(t *testing.T) {
	a, buf := cmdApp(t)
	a.stdin = bgStdin("n")
	setBgTurn(a, false) // интерактивный ход (REPL/TUI)

	if a.confirm(tools.ConfirmReq{Kind: tools.ConfirmExec, Detail: "ls"}) {
		t.Fatal("ответ «n» должен означать отказ")
	}
	if strings.Contains(out(buf), "отклонено") {
		t.Error("интерактивному ходу отказ фонового хода не полагается")
	}
	select {
	case l := <-a.stdin.lines:
		t.Errorf("строка %q не потреблена вопросом — осталась в канале", l)
	default:
		// Прочитано readAns — правильно.
	}
}

// TestInteractiveConfirmYesGrants — «y» в интерактивном ходе по-прежнему
// разрешает команду: отказ фонового хода не сломал обычный путь.
func TestInteractiveConfirmYesGrants(t *testing.T) {
	a, _ := cmdApp(t)
	a.stdin = bgStdin("y")
	setBgTurn(a, false)

	if !a.confirm(tools.ConfirmReq{Kind: tools.ConfirmExec, Detail: "ls"}) {
		t.Fatal("ответ «y» должен разрешить команду")
	}
}

// TestBackgroundConfirmWriteAndNetDenied — то же для записи файлов и
// сети: обе запасные ветки обязаны отказывать фоновому ходу.
func TestBackgroundConfirmWriteAndNetDenied(t *testing.T) {
	a, buf := cmdApp(t)
	setBgTurn(a, true)

	if a.confirm(tools.ConfirmReq{Kind: tools.ConfirmWrite, Path: "a.go", New: "x"}) {
		t.Error("запись файла не может быть подтверждена фоновым ходом")
	}
	if a.confirm(tools.ConfirmReq{Kind: tools.ConfirmNet, Detail: "https://example.com"}) {
		t.Error("сетевой запрос не может быть подтверждён фоновым ходом")
	}
	if n := strings.Count(out(buf), "отклонено"); n != 2 {
		t.Errorf("видимых отказов = %d, ожидалось 2:\n%s", n, out(buf))
	}
}

// TestBackgroundAskUserNoStdin — ask_user из cron-хода не читает stdin:
// модель получает пустой ответ и решает сама, строка REPL цела.
func TestBackgroundAskUserNoStdin(t *testing.T) {
	a, buf := cmdApp(t)
	a.stdin = bgStdin("да")
	setBgTurn(a, true)

	ans, err := a.askUser("какой вариант?", []string{"a", "b"})
	if err != nil {
		t.Fatalf("ask_user вернул ошибку: %v", err)
	}
	if ans != "" {
		t.Errorf("фоновой ход не мог получить ответ, получил %q", ans)
	}
	if !strings.Contains(out(buf), "решай сам") {
		t.Errorf("нужно объяснить отсутствие ответа, вывод:\n%s", out(buf))
	}
	if !a.stdin.Scan() || a.stdin.Text() != "да" {
		t.Error("stdin-строка потеряна — ask_user её украл")
	}
}

// TestInteractiveAskUserStillAsks — интерактивный ход читает ответ как
// раньше: «да» доходит до модели.
func TestInteractiveAskUserStillAsks(t *testing.T) {
	a, _ := cmdApp(t)
	a.stdin = bgStdin("да")
	setBgTurn(a, false)

	ans, err := a.askUser("какой вариант?", nil)
	if err != nil {
		t.Fatalf("ask_user вернул ошибку: %v", err)
	}
	if ans != "да" {
		t.Errorf("ответ = %q, ожидалось %q", ans, "да")
	}
}

// TestBackgroundTrustCodeDenied — фоновый ход не доверяет чужой код:
// без человека подписи нет, расширение остаётся неподключённым.
func TestBackgroundTrustCodeDenied(t *testing.T) {
	a, _ := cmdApp(t)
	a.stdin = bgStdin("y") // даже «y» из REPL не должен стать согласием
	setBgTurn(a, true)

	w := tools.ProjectCodeWarning{Kind: "ext", Name: "demo", Path: "scripts/demo.js", What: "node scripts/demo.js"}
	if a.askTrustCode(w) {
		t.Fatal("фоновой ход не может доверять чужой код")
	}
	if !a.stdin.Scan() || a.stdin.Text() != "y" {
		t.Error("stdin-строка потеряна — askTrustCode её украл")
	}
}
