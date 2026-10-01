package core

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store — пути всех данных gcli в ~/.gcli (или $GCLI_HOME).
type Store struct{ Root string }

// Home — каталог данных. Переопределяется переменной GCLI_HOME.
func Home() string {
	if h := strings.TrimSpace(os.Getenv("GCLI_HOME")); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ".gcli"
	}
	return filepath.Join(h, ".gcli")
}

// NewStore — хранилище по умолчанию.
func NewStore() *Store { return &Store{Root: Home()} }

// Каталоги внутри хранилища.
func (s *Store) Sessions() string     { return filepath.Join(s.Root, "sessions") }
func (s *Store) Checkpoints() string  { return filepath.Join(s.Root, "checkpoints") }
func (s *Store) Exports() string      { return filepath.Join(s.Root, "exports") }
func (s *Store) Skills() string       { return filepath.Join(s.Root, "skills") }
func (s *Store) Extensions() string   { return filepath.Join(s.Root, "extensions") }
func (s *Store) Agents() string       { return filepath.Join(s.Root, "agents") }
func (s *Store) Log() string          { return filepath.Join(s.Root, "gcli.log") }
func (s *Store) ConfigPath() string   { return filepath.Join(s.Root, "config.json") }
func (s *Store) GlobalMemory() string { return filepath.Join(s.Root, "GCLI.md") }

// Ensure — создать все каталоги хранилища.
func (s *Store) Ensure() {
	for _, d := range []string{s.Root, s.Sessions(), s.Checkpoints(), s.Exports(), s.Skills(), s.Extensions(), s.Agents()} {
		_ = os.MkdirAll(d, 0o755)
	}
}

// WriteAtomic — записать файл атомарно (через временный + rename),
// чтобы прерванный процесс не оставил половину JSON.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readFileHelper — чтение файла (используется в тестах).
func readFileHelper(path string) ([]byte, error) { return os.ReadFile(path) }

// AbsPath — абсолютный путь относительно рабочего каталога.
//
// Корректно обрабатывает Windows-пути: «C:\gcli\src» и «C:/gcli/src»
// уже абсолютны и не должны склеиваться с workDir.
func AbsPath(workDir, p string) string {
	if p == "" {
		p = "."
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	// Windows-путь с буквой диска на не-Windows системе (и наоборот).
	if len(p) > 1 && p[1] == ':' {
		return filepath.Clean(p)
	}
	// Короткое имя диска вида «C:foo» (относительно текущего каталога диска).
	return filepath.Clean(filepath.Join(workDir, p))
}

// RelToWD — путь относительно рабочего каталога, если он внутри него.
func RelToWD(workDir, p string) string {
	rel, err := filepath.Rel(workDir, p)
	if err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// SkipDirs — каталоги, которые не обходятся при поиске.
var SkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".gcli": true,
	".idea": true, ".vscode": true, "__pycache__": true, ".venv": true,
	"venv": true, "target": true, "dist": true, ".next": true,
	".cache": true, ".gradle": true, ".mypy_cache": true, ".pytest_cache": true,
}

// ShouldSkipDir — нужно ли пропустить каталог при обходе.
func ShouldSkipDir(name string) bool { return SkipDirs[name] }

// SortedEntries — содержимое каталога: сначала каталоги, потом файлы, по имени.
func SortedEntries(dir string) ([]os.DirEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(ents, func(i, j int) bool {
		if ents[i].IsDir() != ents[j].IsDir() {
			return ents[i].IsDir()
		}
		return ents[i].Name() < ents[j].Name()
	})
	return ents, nil
}

// DirSize — суммарный размер каталога в байтах (для /status).
func DirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// IsDirEmpty — пуст ли каталог.
func IsDirEmpty(dir string) bool {
	ents, err := os.ReadDir(dir)
	return err != nil || len(ents) == 0
}
