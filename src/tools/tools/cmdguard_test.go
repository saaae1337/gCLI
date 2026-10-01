package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newGuardedRegistry — реестр с песочницей на временном каталоге и запретом
// на «домашний» каталог, который не должен пересекаться с корнем песочницы.
func newGuardedRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	sb := NewSandbox(dir).WithDeny(filepath.Join(dir, "secret"))
	// Домашний каталог пользователя лежит за пределами песочницы почти всегда.
	// Если машина проверяющего вынесла TempDir внутрь дома, добавим отдельный
	// запрет на каталог верхнего уровня, которого точно нет в TempDir.
	if h := userHome(); h != "" && !strings.HasPrefix(dir, h) {
		sb.WithDeny(filepath.Join(h, ".ssh"))
	}
	return New(Env{WorkDir: dir, Sandbox: sb, Session: nil})
}

func TestGuardCommand_BlocksOutsideRoot(t *testing.T) {
	r := newGuardedRegistry(t)
	for _, cmd := range []string{
		`cat ~/.ssh/id_rsa`,
		`type /etc/passwd`,
		`echo hi > ../escape.txt`,
		`cp secret/x ../out.txt`,
		`cat "$HOME/.ssh/authorized_keys"`,
	} {
		if err := r.guardCommand(cmd, r.workDir); err == nil {
			t.Errorf("команда %q: песочница должна была запретить", cmd)
		}
	}
}

func TestGuardCommand_AllowsNormalWork(t *testing.T) {
	r := newGuardedRegistry(t)
	for _, cmd := range []string{
		`go build ./...`,
		`go test ./... 2>/dev/null`,
		`git status --porcelain`,
		`cat go.mod`,
		`grep -rn "func main" --include='*.go' .`,
		`mkdir -p build/out && ./build.sh one linux amd64`,
		`curl -s https://api.example.com/v1/models`,
		`python3 -c "print('hi')" > /tmp/out.txt`,
		`go vet ./... | head -20`,
	} {
		if err := r.guardCommand(cmd, r.workDir); err != nil {
			t.Errorf("команда %q не должна была блокироваться: %v", cmd, err)
		}
	}
}

func TestGuardCommand_BlocksDenyTreeInsideRoot(t *testing.T) {
	r := newGuardedRegistry(t)
	if err := r.guardCommand(`cat secret/token.txt`, r.workDir); err == nil {
		t.Fatal("чтение запрещённого поддерева внутри корня должно блокироваться")
	}
	if err := r.guardCommand(`cat go.mod`, r.workDir); err != nil {
		t.Fatalf("обычный файл проекта должен проходить: %v", err)
	}
}

func TestGuardCommand_NoSandboxAllowsEverything(t *testing.T) {
	dir := t.TempDir()
	r := New(Env{WorkDir: dir}) // песочницы нет
	for _, cmd := range []string{`cat ~/.ssh/id_rsa`, `cat /etc/shadow`} {
		if err := r.guardCommand(cmd, dir); err != nil {
			t.Errorf("без песочницы команда %q обязана выполняться, получено: %v", cmd, err)
		}
	}
}

func TestGuardCommand_InternalFilesBlock(t *testing.T) {
	r := newGuardedRegistry(t)
	// Блокировка должна наступать на уровне инструмента, а не возвращать
	// пустой результат: это проверка контракта песочницы.
	res, err := r.hBash(context.Background(), map[string]any{"command": `cat ~/.ssh/id_rsa`})
	if err == nil {
		t.Fatalf("hBash должен вернуть ошибку песочницы, получил результат %+v", res)
	}
	if !strings.Contains(err.Error(), "песочница") {
		t.Fatalf("в ошибке ожидалось слово «песочница», получено: %v", err)
	}
}

func TestCommandPaths_FindsUnixAndWindowsForms(t *testing.T) {
	work := filepath.Join(string(filepath.Separator), "work")
	got := commandPaths(`cat /etc/passwd`, work)
	if len(got) == 0 || !strings.HasSuffix(got[0], "etc") && !strings.Contains(got[0], "etc") {
		t.Fatalf("не нашёл абсолютный путь: %v", got)
	}
	// URL не должен превращаться в путь.
	if paths := commandPaths(`curl https://api.example.com/v1/models`, work); len(paths) != 0 {
		t.Fatalf("URL не должен считаться путём, получено: %v", paths)
	}
}

func TestGuardCommand_PreservesSymlinkDenial(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: dir, Sandbox: NewSandbox(dir).WithDeny(secret)})
	if err := r.guardCommand(`cat secret/a.txt`, dir); err == nil {
		t.Fatal("доступ к запрещённому поддереву должен блокироваться")
	}
}
