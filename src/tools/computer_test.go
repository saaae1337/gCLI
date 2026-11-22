package tools

// Тесты взаимодействия с компьютером и stdin bash.
//
// Платформенные ветки (pbpaste, notify-send, screencapture) в тестах не
// вызываются: они зависят от окружения, и зелёный тест не должен
// означать «на машине тестировщика стоял xclip». Проверяются чистые
// части: разбор аргументов, экранирование, честные ошибки при отсутствии
// системных утилит, stdin-канал bash.

import (
	"context"
	"strings"
	"testing"
)

// TestBashStdin — данные из input попадают в stdin команды: без них
// модель была бы вынуждена городить heredoc и экранировать содержимое.
func TestBashStdin(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	res, err := r.hBash(context.Background(), map[string]any{
		"command": "cat",
		"input":   "привет из stdin",
	})
	if err != nil {
		t.Fatalf("bash с stdin упал: %v", err)
	}
	if !strings.Contains(res.Text, "привет из stdin") {
		t.Errorf("stdin не дошёл до команды: %q", res.Text)
	}
	if res.ExitCode != 0 {
		t.Errorf("код выхода %d, ожидался 0", res.ExitCode)
	}
}

// TestBashInputTooLarge — потолок input: гигантская вставка в stdin
// уложила бы процесс и контекст.
func TestBashInputTooLarge(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	_, err := r.hBash(context.Background(), map[string]any{
		"command": "cat",
		"input":   strings.Repeat("x", bashInputLimit+1),
	})
	if err == nil || !strings.Contains(err.Error(), "слишком большой") {
		t.Fatalf("ожидалась ошибка про большой input, получено: %v", err)
	}
}

// TestBashExitCodeStructured — код выхода приходит структурным полем,
// а не только текстовой меткой: сводка multi_bash не должна зависеть от
// формата вывода.
func TestBashExitCodeStructured(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	res, err := r.hBash(context.Background(), map[string]any{"command": "exit 3"})
	if err != nil {
		t.Fatalf("bash упал: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode %d, ожидался 3", res.ExitCode)
	}
	if bashExitCode(res) != 3 {
		t.Errorf("bashExitCode %d, ожидался 3", bashExitCode(res))
	}
	// Фолбэк: результат без структурного поля читает текст.
	old := res
	old.ExitCode = 0
	if bashExitCode(old) != 3 {
		t.Errorf("фолбэк на текст не отработал: %d", bashExitCode(old))
	}
}

// TestClipboardValidation — неправильные аргументы отсекаются до
// обращения к системе.
func TestClipboardValidation(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	if _, err := r.hClipboard(context.Background(), map[string]any{"action": "пере"}); err == nil {
		t.Error("неизвестный action принят")
	}
	if _, err := r.hClipboard(context.Background(), map[string]any{"action": "write"}); err == nil {
		t.Error("write без text принят")
	}
}

// TestClipboardWriteMissingTools — при отсутствии системных утилит
// ошибка честно называет, что установить. На машине с утилитами запись
// просто succeeds — тогда тест пропускается.
func TestClipboardWriteMissingTools(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	res, err := r.hClipboard(context.Background(), map[string]any{"action": "write", "text": "x"})
	if err != nil {
		if !strings.Contains(err.Error(), "буфер обмена недоступен") && !strings.Contains(err.Error(), "powershell") {
			t.Fatalf("ошибка без подсказки: %v", err)
		}
		return
	}
	if !strings.Contains(res.Text, "записано") {
		t.Errorf("запись отработала, но не отчиталась: %q", res.Text)
	}
}

// TestQuoteHelpers — экранирование для AppleScript и PowerShell:
// кавычка внутри текста уведомления не должна ломать скрипт.
func TestQuoteHelpers(t *testing.T) {
	if got := quoteApple(`сказал "привет"`); got != `"сказал \"привет\""` {
		t.Errorf("quoteApple: %q", got)
	}
	if got := psQuote(`it's ok`); got != `'it''s ok'` {
		t.Errorf("psQuote: %q", got)
	}
}

// TestOpenRejectsNonHTTP — открываются только http/https: схему telnet://
// или javascript:// в браузер пользователя не отдаём.
func TestOpenRejectsNonHTTP(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	for _, target := range []string{"javascript:alert(1)", "ftp://example.com", "file:///etc/passwd"} {
		if _, err := r.hOpen(context.Background(), map[string]any{"target": target}); err == nil {
			t.Errorf("%q принят без ошибки", target)
		}
	}
}

// TestNotifyValidation — уведомление без заголовка не отправляется.
func TestNotifyValidation(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	if _, err := r.hNotify(context.Background(), map[string]any{"message": "x"}); err == nil {
		t.Error("notify без title принят")
	}
}
