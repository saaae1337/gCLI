package core

// Теневые снимки проекта.
//
// Идея: в <проект>/.gcli/snapshots.git живёт отдельный «bare»-репозиторий,
// в который на каждый ход человека коммитится состояние рабочих файлов.
// Это даёт то, чего не даёт /undo по чекпоинтам: откат проекта к любому
// прошлому ходу целиком — включая удаления, переименования и правки,
// сделанные bash-командами, а не инструментами.
//
// Почему git, а не копии каталогов: git сам дедуплицирует содержимое
// (снимки исходников стоят килобайты, а не мегабайты на ход), не зависит
// от ОС и уже есть на машине любого, кто работает с кодом. Если git не
// найден в PATH — снапшоты тихо отключаются: это страховка, а не критический
// путь, и ломать из-за неё ход нельзя.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot — запись о снимке проекта.
type Snapshot struct {
	Hash    string    `json:"hash"`    // полный хеш коммита
	Time    time.Time `json:"time"`    // когда снят
	Subject string    `json:"subject"` // тема хода, ради которого снят
	Files   int       `json:"files"`   // файлов в снимке
}

// snapshotExcludes — что не тащить в теневой репозиторий.
//
// .gcli исключён целиком, а не только snapshots.git: mission.json и
// mission_state.json — состояние агента, а не исходники, и воскрешать
// их откатом к прошлому ходу нельзя (иначе /revert «чинит» файлы и
// одновременно ломает миссию). Зависимости и сборки восстанавливать
// тоже бессмысленно — они воспроизводятся заново.
var snapshotExcludes = []string{
	".git",
	".gcli/",
	"node_modules/",
	"__pycache__/",
	".venv/",
	"venv/",
	"target/",
	"dist/",
	"vendor/",
	"builds/",
}

var errNoGit = errors.New("git не найден в PATH — снапшоты недоступны")

// SnapshotsGitDir — каталог теневого репозитория проекта.
func SnapshotsGitDir(workDir string) string {
	return filepath.Join(workDir, ".gcli", "snapshots.git")
}

// SnapshotsAvailable — есть ли git в PATH. Без него вся механика
// отключается (см. заголовок файла).
func SnapshotsAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// guardSnapshotWorkDir — отказаться снимать каталоги, в которых снимок
// либо бессмыслен, либо опасен: корень диска и домашний каталог.
// Человек может запустить gcli оттуда случайно, а «git add -A» по всему
// домашнему каталогу — это часы работы и гигабайты мусора.
func guardSnapshotWorkDir(workDir string) error {
	wd := filepath.Clean(workDir)
	if wd == "" || wd == "." {
		return errors.New("снапшоты: пустой рабочий каталог")
	}
	vol := filepath.VolumeName(wd)
	if vol != "" && wd == vol+string(os.PathSeparator) {
		return errors.New("снапшоты: рабочий каталог — корень диска, снимок не делается")
	}
	if vol == "" && wd == string(os.PathSeparator) {
		return errors.New("снапшоты: рабочий каталог — корень диска, снимок не делается")
	}
	if h, err := os.UserHomeDir(); err == nil && filepath.Clean(h) == wd {
		return errors.New("снапшоты: рабочий каталог — домашний каталог, снимок не делается")
	}
	return nil
}

// runGit — выполнить команду git над теневым репозиторием.
//
// GIT_CONFIG_GLOBAL/SYSTEM сброшены в /dev/null: пользовательский
// глобальный конфиг не должен ломать механику (gpgsign, hooksPath,
// autocrlf) — всё нужное репозиторий настраивает сам при создании.
func runGit(workDir string, timeout time.Duration, args ...string) (string, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", errNoGit
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return string(out), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}

func gitShadow(workDir string, timeout time.Duration, args ...string) (string, error) {
	// --git-dir/--work-tree только через «=»: git не принимает их
	// в раздельной форме (см. usage в его выводе).
	full := append([]string{
		"--git-dir=" + SnapshotsGitDir(workDir),
		"--work-tree=" + workDir,
	}, args...)
	return runGit(workDir, timeout, full...)
}

// EnsureSnapshotsRepo — создать теневой репозиторий, если его ещё нет.
// Идемпотентно: существующий не трогает.
func EnsureSnapshotsRepo(workDir string) error {
	if err := guardSnapshotWorkDir(workDir); err != nil {
		return err
	}
	gitDir := SnapshotsGitDir(workDir)
	if fi, err := os.Stat(filepath.Join(gitDir, "HEAD")); err == nil && !fi.IsDir() {
		return nil // уже настроен
	}
	if !SnapshotsAvailable() {
		return errNoGit
	}
	_ = os.MkdirAll(filepath.Join(workDir, ".gcli"), 0o755)
	if _, err := runGit(workDir, 60*time.Second, "init", "--bare", "--quiet", gitDir); err != nil {
		return err
	}
	// Автоприпись автора и настройки: без user.name/email git откажется
	// коммитить, а глобальный конфиг мы выключили выше.
	for _, kv := range [][2]string{
		{"user.name", "gcli snapshots"},
		{"user.email", "gcli@localhost"},
		{"commit.gpgsign", "false"},
		{"core.autocrlf", "false"},
		{"gc.auto", "0"}, // авто-сборка мусора на каждый ход недопустима
	} {
		_, _ = gitShadow(workDir, 30*time.Second, "config", kv[0], kv[1])
	}
	// Исключения — в info/exclude теневого репозитория, а не в .gitignore
	// проекта: gcli не должен писать мусор в пользовательские файлы.
	_ = os.MkdirAll(filepath.Join(gitDir, "info"), 0o755)
	_ = os.WriteFile(filepath.Join(gitDir, "info", "exclude"),
		[]byte("# gcli snapshots: служебные и тяжёлые каталоги\n"+
			strings.Join(snapshotExcludes, "\n")+"\n"), 0o644)
	return nil
}

// snapshotInfo — собрать описание коммита по хешу.
func snapshotInfo(workDir, hash string) (Snapshot, error) {
	out, err := gitShadow(workDir, 30*time.Second, "log", "-1", "--pretty=format:%H%x1f%aI%x1f%s", hash)
	if err != nil {
		return Snapshot{}, err
	}
	parts := strings.Split(strings.TrimSpace(out), "\x1f")
	if len(parts) != 3 {
		return Snapshot{}, fmt.Errorf("снапшоты: неожиданный вывод git log для %s", Truncate(hash, 8))
	}
	t, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		t = time.Time{}
	}
	n := 0
	if ls, err := gitShadow(workDir, 60*time.Second, "ls-files"); err == nil {
		n = countNonEmpty(ls)
	}
	return Snapshot{Hash: parts[0], Time: t, Subject: parts[2], Files: n}, nil
}

func countNonEmpty(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// TakeSnapshot — зафиксировать текущее состояние рабочих файлов.
// Возвращает took=false, если с прошлого снимка ничего не изменилось:
// пустые коммиты на каждый ход раздули бы список без пользы.
//
// Ошибка — не повод валить ход: вызывающий (turn) обязан пережить её тихо.
func TakeSnapshot(workDir, subject string) (Snapshot, bool, error) {
	if err := EnsureSnapshotsRepo(workDir); err != nil {
		return Snapshot{}, false, err
	}
	if _, err := gitShadow(workDir, 600*time.Second, "add", "-A", "--"); err != nil {
		return Snapshot{}, false, err
	}
	changed, err := gitShadow(workDir, 120*time.Second, "diff", "--cached", "--name-only")
	if err != nil {
		return Snapshot{}, false, err
	}
	if strings.TrimSpace(changed) == "" {
		return Snapshot{}, false, nil
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = "ход"
	}
	subject = "ход: " + Truncate(OneLine(subject), 60)
	if _, err := gitShadow(workDir, 600*time.Second, "commit", "--quiet", "-m", subject); err != nil {
		return Snapshot{}, false, err
	}
	snap, err := snapshotInfo(workDir, "HEAD")
	if err != nil {
		return Snapshot{}, true, err
	}
	return snap, true, nil
}

// ListSnapshots — все снимки от старого к новому.
//
// --all: в список попадают и коммиты на ветке keep/pre-revert — точка,
// из которой ушли последним откатом. Без неё «отменить откат» было бы
// нечем: git log от HEAD после reset --hard новые снимки теряет.
func ListSnapshots(workDir string) ([]Snapshot, error) {
	if err := EnsureSnapshotsRepo(workDir); err != nil {
		return nil, err
	}
	// Нет ни одного коммита — не ошибка, а просто пустой список.
	if _, err := gitShadow(workDir, 30*time.Second, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return []Snapshot{}, nil
	}
	out, err := gitShadow(workDir, 60*time.Second,
		"log", "--all", "--reverse", "--date-order",
		"--pretty=format:%H%x1f%aI%x1f%s")
	if err != nil {
		return nil, err
	}
	res := []Snapshot{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\x1f")
		if len(parts) != 3 {
			continue
		}
		t, err := time.Parse(time.RFC3339, parts[1])
		if err != nil {
			t = time.Time{}
		}
		res = append(res, Snapshot{Hash: parts[0], Time: t, Subject: parts[2]})
	}
	return res, nil
}

// RestoreSnapshot — откатить рабочие файлы к снимку hash.
//
// Откат сам должен быть откатываем, поэтому: (1) текущее состояние
// коммитится, если грязное; (2) HEAD помечается веткой keep/pre-revert —
// она появится в /snapshots, и повторный /revert вернёт как было.
//
// Файлы, которые никогда не попадали в снимки (не трекались), reset
// не трогает — честно сообщаем об этом в docs, но гарантируем: всё, что
// агент менял через инструменты и команды, откатывается.
func RestoreSnapshot(workDir, hash string) (Snapshot, error) {
	if err := EnsureSnapshotsRepo(workDir); err != nil {
		return Snapshot{}, err
	}
	hash = strings.TrimSpace(hash)
	if _, err := gitShadow(workDir, 30*time.Second,
		"rev-parse", "--verify", "--quiet", hash+"^{commit}"); err != nil {
		return Snapshot{}, fmt.Errorf("снимок %s не найден", Truncate(hash, 8))
	}
	_, _, _ = TakeSnapshot(workDir, "перед откатом")
	// Уникальная ветка на каждый откат, а не одна переиспользуемая:
	// --force стёр бы предыдущую точку, и состояние «два отката назад»
	// стало бы недостижимым. Ветка — просто 40 байт указателя.
	keep := "keep/pre-revert-" + time.Now().Format("20060102-150405") + "-" + RandID(4)
	_, _ = gitShadow(workDir, 30*time.Second, "branch", "--force", keep, "HEAD")
	cur, err := gitShadow(workDir, 30*time.Second, "rev-parse", "HEAD")
	if err == nil && strings.TrimSpace(cur) == hash {
		// Уже там: reset не нужен, но снимок вернуть надо.
		return snapshotInfo(workDir, hash)
	}
	if _, err := gitShadow(workDir, 600*time.Second, "reset", "--hard", hash); err != nil {
		return Snapshot{}, err
	}
	return snapshotInfo(workDir, hash)
}
