package core

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Временный файл создаётся уникальным в том же каталоге: фиксированное
	// имя path+".tmp" означало, что два параллельных писателя затирают
	// данные друг друга и один из них переименовывает чужую половину.
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// После успешного rename файла уже нет; Remove здесь — уборка на
	// любом пути ошибки.
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	// Sync до Close: без него после выключения питания на диске мог бы
	// остаться переименованный, но пустой файл.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return renameAtomic(tmp, path)
}

// renameAtomic — rename с повторами.
//
// На Windows MoveFileEx может временно вернуть «Access is denied», если
// целевой файл в этот самый момент заменяет другой писатель. Повторяем
// коротко: это не постоянная ошибка прав, а окно гонки.
func renameAtomic(tmp, path string) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * time.Millisecond)
	}
	return err
}

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
	// «..» — ровно выход на уровень выше; «..foo» — обычное имя файла
	// внутри каталога, и считать его внешним нельзя.
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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
