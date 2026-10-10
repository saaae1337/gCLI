package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------- gitOut не должен пускать через оболочку ----------

// initGitRepo — временный репозиторий с одним коммитом.
func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не в PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v не отработал: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("привет\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "первый")
	return dir
}

// TestGitOutDoesNotRunShell — аргументы git не должны попадать в
// оболочку.
//
// gitOut собирал команду строкой и звал bash -c, поэтому любой символ
// с особым смыслом в аргументе превращался в команду. Сейчас все
// вызовы передают константы, и мина не рвалась — но она стояла под
// любым будущим аргументом из данных: путём файла или именем ветки,
// которые приходят от модели.
func TestGitOutDoesNotRunShell(t *testing.T) {
	dir := initGitRepo(t)
	// Метка относительная: `ce.Dir` и так равен dir, а абсолютный путь
	// на Windows содержит обратные слэши, которые оболочка съедает
	// экранированием. С таким payload инъекция исполнилась бы, и тест
	// проверял бы не защиту, а собственную полезную нагрузку.
	payload := "HEAD; echo pwned > pwned.txt"

	out, err := gitOut(dir, "log", "--oneline", "-1", payload)
	if err == nil && strings.Contains(out, "pwned") {
		t.Fatalf("аргумент исполнился как команда:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "pwned.txt")); statErr == nil {
		t.Fatalf("инъекция исполнилась: создан %s/pwned.txt", dir)
	}
}

// TestGitOutRejectsBadRefHonestly — негодный аргумент обязан дать ошибку
// от git, а не молчаливую пустую выдачу: вызывающий должен отличать
// «репозиторий чист» от «не с чем сравнивать».
func TestGitOutRejectsBadRefHonestly(t *testing.T) {
	dir := initGitRepo(t)
	if _, err := gitOut(dir, "log", "--oneline", "такой-ветки-нет"); err == nil {
		t.Fatal("несуществующая ветка не дала ошибки")
	}
}

// TestGitOutWorksNormally — после отказа от оболочки обычные вызовы
// обязаны работать: это проверка, что починили не «всё», а ровно
// инъекцию.
func TestGitOutWorksNormally(t *testing.T) {
	dir := initGitRepo(t)
	out, err := gitOut(dir, "log", "--oneline", "-1")
	if err != nil {
		t.Fatalf("обычный вызов git сломан: %v", err)
	}
	if !strings.Contains(out, "первый") {
		t.Fatalf("вывод git неожиданный: %q", out)
	}
}

// TestGitOutIgnoresPagerAndKeepsEnv — gitOut задаёт GIT_PAGER=cat и
// GCLI=1, и это не должно потеряться при переходе на прямой exec:
// иначе git откроет вывод в less и зависнет в неинтерактивном ходе.
func TestGitOutIgnoresPagerAndKeepsEnv(t *testing.T) {
	dir := initGitRepo(t)
	if runtime.GOOS == "windows" {
		t.Skip("на Windows проверка через env не воспроизводится")
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := gitOut(dir, "log", "--oneline", "-1")
	if err != nil || !strings.Contains(out, "первый") {
		t.Fatalf("прямой вызов git не работает: err=%v out=%q", err, out)
	}
}
