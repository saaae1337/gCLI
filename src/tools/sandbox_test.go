package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newSandboxForTest — песочница на временном каталоге с закрытым «секретом».
func newSandboxForTest(t *testing.T) (root, secret string, sb *Sandbox) {
	t.Helper()
	root = t.TempDir()
	secret = filepath.Join(root, "secrets")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	sb = NewSandbox(root).WithDeny(secret)
	return root, secret, sb
}

func TestSandboxNilIsDisabled(t *testing.T) {
	var sb *Sandbox
	if sb.Enabled() {
		t.Fatal("нулевая песочница должна считаться выключенной")
	}
	if err := sb.Check(`C:\Windows\system32`); err != nil {
		t.Fatalf("выключенная песочница не должна ничего запрещать: %v", err)
	}
	// nil-получатель: без него /sandbox off и /permissions падали бы.
	if got := sb.Roots(); got != nil {
		t.Fatalf("Roots() на nil = %v, ожидался nil", got)
	}
}

func TestSandboxNoRootsDisabled(t *testing.T) {
	sb := NewSandbox()
	if sb.Enabled() {
		t.Fatal("песочница без корней должна считаться выключенной")
	}
	if err := sb.Check(t.TempDir()); err != nil {
		t.Fatalf("без корней пути не ограничены: %v", err)
	}
}

func TestSandboxAllowsInsideAndBlocksOutside(t *testing.T) {
	root, _, sb := newSandboxForTest(t)

	if err := sb.Check(filepath.Join(root, "src", "main.go")); err != nil {
		t.Fatalf("путь внутри корня должен проходить: %v", err)
	}
	if err := sb.Check(root); err != nil {
		t.Fatalf("сам корень должен проходить: %v", err)
	}

	outside := filepath.Join(filepath.Dir(root), "соседний-проект", "config.json")
	err := sb.Check(outside)
	if err == nil {
		t.Fatalf("путь вне корня должен запрещаться: %s", outside)
	}
	if !strings.Contains(err.Error(), "песочница") {
		t.Fatalf("в ошибке должно быть слово «песочница», было: %v", err)
	}
}

// TestSandboxPrefixNotStringPrefix — классическая ошибка HasPrefix по путям:
// «<root>-secret» не должен считаться лежащим внутри «<root>».
func TestSandboxPrefixNotStringPrefix(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "проект")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sb := NewSandbox(root)

	neighbour := root + "-secret"
	if err := sb.Check(neighbour); err == nil {
		t.Fatalf("путь %s — соседний, а не внутри %s: должен быть запрещён", neighbour, root)
	}
}

// TestSandboxDenyBeatsAllow — запрет сильнее разрешения: закрытый каталог
// внутри корня остаётся закрытым.
func TestSandboxDenyBeatsAllow(t *testing.T) {
	root, secret, sb := newSandboxForTest(t)

	target := filepath.Join(secret, "config.json")
	if err := sb.Check(target); err == nil {
		t.Fatalf("%s внутри закрытого каталога должен быть запрещён", target)
	}
	// Обход через .. не должен помочь.
	dotdot := filepath.Join(root, "secrets", "..", "secrets", "config.json")
	if err := sb.Check(dotdot); err == nil {
		t.Fatalf("обход через .. не должен обходить запрет: %s", dotdot)
	}
	// Обычный файл рядом при этом доступен.
	if err := sb.Check(filepath.Join(root, "secrets.txt")); err != nil {
		t.Fatalf("похожее имя не должно считаться закрытым: %v", err)
	}
}

// TestSandboxSymlinkEscape — ссылка внутри корня не должна выпускать наружу.
func TestSandboxSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("симлинки на Windows требуют прав разработчика")
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("ключ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("симлинки недоступны: %v", err)
	}

	sb := NewSandbox(root)
	if err := sb.Check(filepath.Join(root, "link")); err == nil {
		t.Fatal("каталог-ссылка наружу должен быть запрещён")
	}
	if err := sb.Check(filepath.Join(root, "link", "secret.txt")); err == nil {
		t.Fatal("файл под ссылкой наружу должен быть запрещён")
	}
	if err := sb.Check(filepath.Join(root, "link", "новый-файл.go")); err == nil {
		t.Fatal("несуществующий файл под ссылкой тоже должен быть запрещён")
	}
}

// TestSandboxAllowsMissingFile — песочница не должна ломать создание файлов:
// write_file обращается к пути, которого ещё нет.
func TestSandboxAllowsMissingFile(t *testing.T) {
	root, _, sb := newSandboxForTest(t)
	p := filepath.Join(root, "новый", "файл.go")
	if err := sb.Check(p); err != nil {
		t.Fatalf("будущий файл внутри корня должен проходить: %v", err)
	}
}

// TestSandboxDenyLinkInside — ссылка на закрытый каталог внутри корня.
func TestSandboxDenyLinkInside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("симлинки на Windows требуют прав разработчика")
	}
	root, secret, sb := newSandboxForTest(t)
	if err := os.WriteFile(filepath.Join(secret, "id_rsa"), []byte("ключ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "шорткат")); err != nil {
		t.Skipf("симлинки недоступны: %v", err)
	}
	if err := sb.Check(filepath.Join(root, "шорткат", "id_rsa")); err == nil {
		t.Fatal("ссылка на закрытый каталог должна оставаться закрытой")
	}
}

// ---------- Интеграция с инструментами ----------

// TestRegistryPathArgSandbox — все инструменты, принимающие путь, идут через
// pathArg: режем один раз в одном месте.
func TestRegistryPathArgSandbox(t *testing.T) {
	work := t.TempDir()
	r := New(Env{WorkDir: work, Sandbox: NewSandbox(work)})

	if _, err := r.pathArg("src/main.go"); err != nil {
		t.Fatalf("путь внутри рабочего каталога должен проходить: %v", err)
	}
	if _, err := r.pathArg(filepath.Join(filepath.Dir(work), "чужая")); err == nil {
		t.Fatal("путь вне рабочего каталога должен запрещаться")
	}
	// Без песочницы всё разрешено — режим по умолчанию не ломаем.
	plain := New(Env{WorkDir: work})
	if _, err := plain.pathArg(filepath.Join(work, "..", "сосед")); err != nil {
		t.Fatalf("без песочницы пути не ограничены: %v", err)
	}
}

// TestHGlobStaysInRoot — glob с абсолютным шаблоном раньше обходил весь диск.
func TestHGlobStaysInRoot(t *testing.T) {
	work := t.TempDir()
	sub := filepath.Join(work, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: work, Sandbox: NewSandbox(work)})

	res, err := r.hGlob(nil, map[string]any{"pattern": filepath.Join(sub, "*.go")})
	if err != nil {
		t.Fatalf("glob по абсолютному шаблону: %v", err)
	}
	if !strings.Contains(res.Text, "main.go") {
		t.Fatalf("ожидался main.go в результате, получено: %s", res.Text)
	}

	// Корнем обхода не должен стать весь диск: в тексте ошибки это видно.
	_, err = r.hGlob(nil, map[string]any{
		"pattern": filepath.Join(filepath.Dir(work), "нигде", "*.go"),
	})
	if err == nil {
		t.Fatal("glob вне рабочего каталога должен запрещаться песочницей")
	}
}

// TestSandboxSymlinkAlwaysResolved — ссылка ВНУТРИ корня, указывающая наружу,
// раньше проходила: буквальный путь «внутри корня», и Check возвращал успех
// до всякого разыменования. Проверка обязана идти по реальному адресу.
func TestSandboxSymlinkAlwaysResolved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("симлинки на Windows требуют прав разработчика")
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "config.json"), []byte(`{"api_key":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Skipf("симлинки недоступны: %v", err)
	}
	sb := NewSandbox(root)
	if err := sb.Check(filepath.Join(root, "out", "config.json")); err == nil {
		t.Fatal("путь под ссылкой наружу обязан запрещаться, даже если он начинается внутри корня")
	}
	// Обычная работа внутри корня не должна сломаться.
	if err := sb.Check(filepath.Join(root, "обычный.go")); err != nil {
		t.Fatalf("файл внутри корня должен проходить: %v", err)
	}
}

// TestHGrepSkipsDeniedSubtree — grep проверяет песочницей каждый файл, а не
// только корень обхода: широкий корень (домашний каталог) иначе отдавал бы
// содержимое закрытого поддерева в совпадения.
func TestHGrepSkipsDeniedSubtree(t *testing.T) {
	work := t.TempDir()
	secret := filepath.Join(work, "secrets")
	if err := os.MkdirAll(secret, 0o700); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(work, "src")
	if err := os.MkdirAll(open, 0o755); err != nil {
		t.Fatal(err)
	}
	const needle = "BEGINKEY"
	if err := os.WriteFile(filepath.Join(secret, "id_rsa"), []byte(needle), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(open, "ok.txt"), []byte(needle), 0o644); err != nil {
		t.Fatal(err)
	}
	sb := NewSandbox(work).WithDeny(secret)
	r := New(Env{WorkDir: work, Sandbox: sb})

	res, err := r.hGrep(nil, map[string]any{"pattern": needle, "path": ".", "max_results": 100})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(res.Text, "ok.txt") {
		t.Fatalf("ожидалось совпадение в открытом файле: %s", res.Text)
	}
	if strings.Contains(res.Text, "id_rsa") {
		t.Fatalf("содержимое закрытого поддерева попало в выдачу:\n%s", res.Text)
	}
}

// TestHBashWorkdirSandbox — workdir команды — тоже путь от модели.
func TestHBashWorkdirSandbox(t *testing.T) {
	work := t.TempDir()
	r := New(Env{WorkDir: work, Sandbox: NewSandbox(work)})
	_, err := r.hBash(nil, map[string]any{
		"command": "echo hi",
		"workdir": filepath.Join(work, ".."),
	})
	if err == nil {
		t.Fatal("workdir вне рабочего каталога должен запрещаться")
	}
}
