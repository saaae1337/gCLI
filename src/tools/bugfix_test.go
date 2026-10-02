package tools

// Регрессионные тесты багфикс-прохода 6.2.1: песочница (голое «..»,
// verify и screenshot в обход cmdguard), дедупликация путей, atomic-правки
// и подсчёт длинных строк.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGuardBareDotDot — «cp secret.txt ..» не проходит мимо песочницы:
// голый токен «..» раньше не разрешался в абсолютный путь и не проверялся.
func TestGuardBareDotDot(t *testing.T) {
	r := newGuardedRegistry(t)
	for _, cmd := range []string{
		`cp secret.txt ..`,
		`echo x > ..`,
		`cat file > ../escape`,
	} {
		if err := r.guardCommand(cmd, r.workDir); err == nil {
			t.Errorf("команда %q должна быть заблокирована (выход за корень)", cmd)
		}
	}
	// Обычная работа не задета.
	if err := r.guardCommand(`cp a.txt ./copy.txt`, r.workDir); err != nil {
		t.Errorf("копирование внутри корня не должно блокироваться: %v", err)
	}
}

// TestVerifyRunsUnderSandbox — verify подчиняется cmdguard, как bash:
// без проверки он был бы «обходом песочницы с безобидным поводом».
func TestVerifyRunsUnderSandbox(t *testing.T) {
	dir := t.TempDir()
	sb := NewSandbox(dir)
	r := New(Env{WorkDir: dir, Sandbox: sb})

	_, err := r.hVerify(context.Background(), map[string]any{"command": "cat /etc/passwd"})
	if err == nil {
		t.Fatal("verify с чтением /etc/passwd должен быть заблокирован песочницей")
	}
}

// TestVerifyCancelDoesNotTouchBaseline — отмена пользователем не записывается
// в baseline: раньше «отменено» попадало как падение, и следующий прогон
// рапортовал о фантомном «исправлено падений: 1».
func TestVerifyCancelDoesNotTouchBaseline(t *testing.T) {
	dir := t.TempDir()
	r := New(Env{WorkDir: dir, Confirm: func(ConfirmReq) bool { return false }})

	res, err := r.hVerify(context.Background(), map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("hVerify: %v", err)
	}
	if !strings.Contains(res.Text, "отменена") {
		t.Errorf("в отчёте нет отмены: %s", res.Text)
	}
	if _, err := os.Stat(baselinePath(dir)); !os.IsNotExist(err) {
		t.Error("отмена не должна создавать baseline")
	}
}

// TestVerifyTimeoutLabel — убитый по таймауту процесс помечается как
// таймаут, а не как обычный код 1.
//
// Команда выбирается под платформу: под Windows «sleep» не существует и
// падает мгновенно, а не по таймауту. Проверяется ветка runVerifyCmd,
// а не наличие sleep в системе.
func TestVerifyTimeoutLabel(t *testing.T) {
	dir := t.TempDir()
	r := New(Env{WorkDir: dir})
	slow := "sleep 5"
	if runtime.GOOS == "windows" {
		slow = "ping -n 6 127.0.0.1 >nul"
	}
	out, err := r.runVerifyCmd(context.Background(), slow, dir, 1)
	if err != nil {
		t.Fatalf("runVerifyCmd: %v", err)
	}
	if !strings.Contains(out.text, "таймаут") {
		t.Errorf("в выводе нет пометки о таймауте: %q", out.text)
	}
	// Таймаут не должен выглядеть как обычный успешный код.
	if out.exit != -1 {
		t.Errorf("код таймаута должен быть -1, получили %d", out.exit)
	}
}

// TestScreenshotBlocksInternalURL — screenshot с внутренним адресом отклоняется
// до запуска браузера: ответ внутренней сети не должен попадать в контекст.
func TestScreenshotBlocksInternalURL(t *testing.T) {
	dir := t.TempDir()
	r := New(Env{WorkDir: dir})
	for _, target := range []string{
		"http://127.0.0.1:9/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost/secret",
	} {
		_, err := r.hScreenshot(context.Background(), map[string]any{"target": target})
		if err == nil {
			t.Errorf("%s: ожидался отказ SSRF-проверки", target)
			continue
		}
		if strings.Contains(err.Error(), "headless-браузер не найден") {
			t.Errorf("%s: дошло до браузера, миновав SSRF-проверку", target)
		}
	}
}

// TestScreenshotOutUnderSandbox — путь результата режется песочницей.
func TestScreenshotOutUnderSandbox(t *testing.T) {
	dir := t.TempDir()
	r := New(Env{WorkDir: dir, Sandbox: NewSandbox(dir)})

	_, err := r.hScreenshot(context.Background(), map[string]any{
		"target": "/etc/hostname",
		"out":    filepath.Join(filepath.Dir(dir), "outside.png"),
	})
	if err == nil {
		t.Fatal("запись вне корня песочницы должна блокироваться")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "outside.png")); statErr == nil {
		t.Error("файл создан вне корня несмотря на отказ")
	}
}

// TestReadLinesFromLongLineCountsOnce — строка длиннее буфера считается
// одной строкой, а не одной строкой на каждые 64 КБ.
func TestReadLinesFromLongLineCountsOnce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "min.js")
	var b strings.Builder
	b.WriteString("head\n")
	b.WriteString(strings.Repeat("x", 200_000)) // одна гигантская строка
	b.WriteString("\ntail1\ntail2\n")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	data, _, _, err := readLinesFrom(p, 0, 2000, 0)
	if err != nil {
		t.Fatalf("readLinesFrom: %v", err)
	}
	got := strings.Count(strings.TrimSuffix(string(data), "\n"), "\n") + 1
	if got != 4 {
		t.Errorf("ожидались 4 строки, получено %d (длинная строка посчитана кусками)", got)
	}
}

// TestDedupePathsCaseHandling — на Linux регистр различает файлы; буква
// диска сворачивается везде.
func TestDedupePathsCaseHandling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("на Windows путь целиком нечувствителен к регистру")
	}
	got := dedupePaths([]string{"Readme.md", "readme.md"})
	if len(got) != 2 {
		t.Errorf("Readme.md и readme.md — разные файлы, получено %d цели: %v", len(got), got)
	}
	got = dedupePaths([]string{"C:/x.txt", "c:/x.txt"})
	if len(got) != 1 {
		t.Errorf("буква диска должна сворачиваться: %v", got)
	}
}

// TestAtomicEditRequiresRead — atomic-пачка подчиняется «сначала прочитай».
func TestAtomicEditRequiresRead(t *testing.T) {
	r, dir := newMultiReg(t)
	p := mustWriteFile(t, dir, "f.txt", "old\n")

	res, err := r.hMultiEdit(context.Background(), map[string]any{"edits": []any{
		map[string]any{"path": p, "old_string": "old", "new_string": "new"},
	}, "atomic": true})
	if err != nil {
		t.Fatalf("atomic-правка без чтения: ожидался отказ в результате, получена ошибка: %v", err)
	}
	if !strings.Contains(res.Text, "не прочитан") {
		t.Errorf("ожидался отказ «файл не прочитан», получено: %s", res.Text)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "old\n" {
		t.Errorf("файл изменён без чтения: %q", data)
	}

	// После чтения правка проходит.
	markRead(r.env.ReadFiles, p)
	if _, err := r.hMultiEdit(context.Background(), map[string]any{"edits": []any{
		map[string]any{"path": p, "old_string": "old", "new_string": "new"},
	}, "atomic": true}); err != nil {
		t.Fatalf("atomic-правка после чтения не прошла: %v", err)
	}
	data, _ = os.ReadFile(p)
	if string(data) != "new\n" {
		t.Errorf("правка не применилась: %q", data)
	}
}
