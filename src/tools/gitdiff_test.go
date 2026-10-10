package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- git_diff ----------

// TestGitDiffShowsUncommitted — основной случай: агент правил файл и
// хочет увидеть diff. Раньше это стоило вызова bash с подтверждением
// и возвращало шум.
func TestGitDiffShowsUncommitted(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("привет\nдобавлено\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: dir})
	res, err := r.hGitDiff(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "+добавлено") {
		t.Errorf("добавленная строка не видна:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "warning:") {
		t.Errorf("в diff попал шум git:\n%s", res.Text)
	}
}

// TestGitDiffCleanTreeSaysSo — пустой diff обязан читаться как «правок
// нет», а не как молчание: иначе агент решит, что инструмент сломан,
// и полезт проверять через bash.
func TestGitDiffCleanTreeSaysSo(t *testing.T) {
	dir := initGitRepo(t)
	r := New(Env{WorkDir: dir})
	res, err := r.hGitDiff(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Пусто") {
		t.Errorf("чистое дерево не отражено в ответе: %q", res.Text)
	}
}

// TestGitDiffRef — правки в чужих коммитах смотрятся относительно ref,
// иначе их нечем увидеть: незакоммиченного там ничего.
func TestGitDiffRef(t *testing.T) {
	dir := initGitRepo(t)
	run := gitIn(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("из второго коммита\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "b.txt")
	run("commit", "-q", "-m", "второй")

	r := New(Env{WorkDir: dir})
	res, err := r.hGitDiff(context.Background(), map[string]any{"ref": "HEAD~1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "b.txt") || !strings.Contains(res.Text, "из второго коммита") {
		t.Errorf("правки прошлого коммита не показаны:\n%s", res.Text)
	}
}

// TestGitDiffBadRefExplains — несуществующий ref обязан сказать об
// ошибке, а не вернуть пустой diff, который читается как «правок нет».
func TestGitDiffBadRefExplains(t *testing.T) {
	dir := initGitRepo(t)
	r := New(Env{WorkDir: dir})
	if _, err := r.hGitDiff(context.Background(), map[string]any{"ref": "нет-такой-ветки"}); err == nil {
		t.Fatal("несуществующий ref прошёл молча")
	}
}

// TestGitDiffOutsideRepoExplains — вне git дифф невозможен, и вместо
// отказа инструмент обязан подсказать, чем посмотреть правку.
func TestGitDiffOutsideRepoExplains(t *testing.T) {
	if gitRepoRoot(t.TempDir()) != "" {
		t.Skip("временный каталог неожиданно оказался под git")
	}
	r := New(Env{WorkDir: t.TempDir()})
	_, err := r.hGitDiff(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("вне репозитория отказ не выдан")
	}
	if !strings.Contains(err.Error(), "changes") {
		t.Errorf("в отказе нет подсказки, чем посмотреть правку: %v", err)
	}
}

// TestGitDiffRespectsSandbox — diff по файлу за пределами рабочего
// каталога обошёл бы границу песочницы.
func TestGitDiffRespectsSandbox(t *testing.T) {
	dir := initGitRepo(t)
	r := New(Env{WorkDir: dir, Sandbox: NewSandbox(dir)})
	if _, err := r.hGitDiff(context.Background(), map[string]any{"path": "../соседний/файл"}); err == nil {
		t.Fatal("путь за пределами рабочего каталога прошёл")
	}
}

// TestGitDiffStatIsSummary — stat:true обязан дать сводку, а не сам
// diff: иначе аргумент ничего не меняет и только тратит место.
func TestGitDiffStatIsSummary(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("привет\nдобавлено\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: dir})
	res, err := r.hGitDiff(context.Background(), map[string]any{"stat": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "a.txt") || !strings.Contains(res.Text, "|") {
		t.Errorf("сводка не похожа на --stat:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "```diff") {
		t.Errorf("stat вернул сам diff:\n%s", res.Text)
	}
}

// TestGitDiffNeedsNoConfirm — инструмент только читает, поэтому
// подтверждения быть не должно: иначе каждый просмотр правки
// прерывается вопросом человеку.
func TestGitDiffNeedsNoConfirm(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	tool := r.Get("git_diff")
	if tool == nil {
		t.Fatal("инструмент не зарегистрирован")
	}
	if tool.NeedsConfirm {
		t.Error("просмотр diff не должен требовать подтверждения")
	}
	if tool.Category != "read" {
		t.Errorf("категория %q, ожидалась read", tool.Category)
	}
}
