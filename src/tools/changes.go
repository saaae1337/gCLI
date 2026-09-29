package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gcli/core"
)

// ---------- Журнал правок: changes и revert_last ----------
//
// Пока агент работает, у него нет честного ответа на вопрос «что я поменял».
// Собрать его можно только вручную, через `git status` в bash, и это стоит
// целый вызов каждый раз. Хуже — такой вызов возвращает шум: git на Windows
// сыпет «warning: LF will be replaced by CRLF» на любую команду с diff, и в
// этом шуме теряется единственная строка, ради которой звали.
//
// Здесь две вещи: журнал собственных правок (write_file / edit_file) и
// откат последней по счёту. Откат нужен не для удобства — плохая правка,
// оставленная на исходниках, обходится дороже всего остального: приходится
// либо откатывать вручную через git, либо объяснять пользователю, что надо
// сделать самому.

// Change — одна запись журнала правок.
type Change struct {
	At    time.Time
	Path  string // абсолютный путь
	Rel   string // путь относительно рабочего каталога
	Label string // какой инструмент изменил: write_file | edit_file
	// Прежнее и новое содержимое. Хранятся целиком: без них откат невозможен,
	// а «откатить последний edit_file» без потери всего файла нельзя.
	Old     string
	New     string
	Existed bool
}

// Lines — сколько строк затронуто правкой.
func (c Change) Lines() (added, removed int) {
	for _, l := range core.SplitLines(c.New) {
		if !containsLine(c.Old, l) {
			added++
		}
	}
	for _, l := range core.SplitLines(c.Old) {
		if !containsLine(c.New, l) {
			removed++
		}
	}
	return
}

func containsLine(hay, line string) bool {
	return line != "" && strings.Contains(hay, line)
}

// changeLog — журнал правок реестра.
//
// Отдельный мьютекс: журнал пишется из агентной горутины и из UI-потока
// (главный агент и субагенты делят копию реестра), а без защиты это ровно тот
// «concurrent map writes», из-за которого readMu уже существует.
type changeLog struct {
	mu   sync.Mutex
	list []Change
}

// change — ссылочное поле журнала, всегда непустое.
//
// Реестр создают не только через New: тесты, MCP-подключения и любые
// внешние потребители собирают &Registry{} вручную. При nil-журнале агент
// падал бы на первой же правке, поэтому доступ идёт только через этот
// геттер.
func (r *Registry) change() *changeLog {
	if r.log == nil {
		r.log = &changeLog{}
	}
	return r.log
}

func (l *changeLog) add(c Change) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, c)
	// Журнал растёт всю сессию, а хранит копии файлов — ограничиваем,
	// иначе большой файл, переписанный двадцать раз, съест память.
	const maxLog = 100
	if len(l.list) > maxLog {
		l.list = l.list[len(l.list)-maxLog:]
	}
}

func (l *changeLog) all() []Change {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Change, len(l.list))
	copy(out, l.list)
	return out
}

func (l *changeLog) last() (Change, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.list) == 0 {
		return Change{}, false
	}
	return l.list[len(l.list)-1], true
}

func (l *changeLog) dropLast() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.list) > 0 {
		l.list = l.list[:len(l.list)-1]
	}
}

// noteChange — записать правку в журнал. Вызывается из write_file/edit_file
// после записи на диск.
//
// Existed вычисляется по факту «файл был на диске ДО правки», а это выясняется
// только в момент чтения старого содержимого. Проверять os.Stat здесь уже
// поздно: файл к этому моменту создан, и только что созданный файл всегда
// выглядел бы как «изменённый». Из-за этого revert_last не удалял новые
// файлы, а оставлял после отката пустой файл на диске.
func (r *Registry) noteChange(p, label, old, new string, existed bool) {
	r.change().add(Change{
		At:      time.Now(),
		Path:    p,
		Rel:     core.RelToWD(r.workDir, p),
		Label:   label,
		Old:     old,
		New:     new,
		Existed: existed,
	})
}

// ---------- Git без шума ----------

// gitOut — выполнить git и вернуть ТОЛЬКО stdout.
//
// Предупреждения git вроде «LF will be replaced by CRLF» приходят в stderr и
// не имеют отношения к результату. Раньше они попадали в контекст модели
// вместе с выводом и заслоняли единственное полезное — список файлов.
func gitOut(dir string, args ...string) (string, error) {
	shell, sargs := ShellCommand("git " + strings.Join(args, " "))
	ce := exec.Command(shell, sargs...)
	ce.Dir = dir
	ce.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0", "GCLI=1")
	var stderr strings.Builder
	ce.Stderr = &stderr
	out, err := ce.Output()
	return string(out), err
}

// gitRepoRoot — корень git-репозитория ("" — не репозиторий).
func gitRepoRoot(dir string) string {
	if out, err := gitOut(dir, "rev-parse", "--show-toplevel"); err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}

// sha256Hex — короткий хеш строки для имён файлов состояния.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// hChanges — что я поменял.
func (r *Registry) hChanges(_ context.Context, m map[string]any) (Result, error) {
	var b strings.Builder

	// --- Часть 1: собственные правки. Это главное и доступно всегда. ---
	list := r.change().all()
	if path := ArgStr(m, "path"); path != "" {
		want := r.resolvePath(path)
		filtered := list[:0:0]
		for _, c := range list {
			if c.Path == want || core.RelToWD(r.workDir, c.Path) == path {
				filtered = append(filtered, c)
			}
		}
		list = filtered
	}

	b.WriteString("# Мои правки\n\n")
	if len(list) == 0 {
		b.WriteString("Журнал пуст: ни один файл через write_file / edit_file не менялся.\n")
	} else {
		added, removed := 0, 0
		for _, c := range list {
			a, r := c.Lines()
			added += a
			removed += r
		}
		fmt.Fprintf(&b, "Правок: %d, затронуто файлов: %d (+%d/-%d строк).\n\n",
			len(list), uniquePaths(list), added, removed)
		// Свежие сверху: последняя правка — самая вероятная, которую надо проверить.
		for i := len(list) - 1; i >= 0; i-- {
			c := list[i]
			a, rm := c.Lines()
			act := "изменён"
			if !c.Existed {
				act = "создан"
			} else if c.Old == "" {
				act = "создан"
			}
			// Инструмент правки виден явно: «перезаписан целиком» и «затёрт кусок»
			// — разные по последствиям ситуации, и по журналу это не угадать.
			fmt.Fprintf(&b, "- %s (%s через %s, +%d/-%d, %s)\n",
				c.Rel, act, c.Label, a, rm, core.HumanDuration(time.Since(c.At)))
		}
		if ArgBool(m, "detail") {
			for i := len(list) - 1; i >= 0 && i >= len(list)-3; i-- {
				c := list[i]
				b.WriteString(fmt.Sprintf("\n## %s\n```diff\n%s\n```\n",
					c.Rel, unifiedDiff(c.Old, c.New, c.Rel)))
			}
		}
	}

	// --- Часть 2: состояние рабочего дерева. ---
	root := gitRepoRoot(r.workDir)
	if root == "" {
		return Result{Text: strings.TrimSpace(b.String()), Summary: fmt.Sprintf("правок: %d", len(list))}, nil
	}
	if st, err := gitOut(root, "status", "--porcelain"); err == nil && strings.TrimSpace(st) != "" {
		b.WriteString("\n# Рабочее дерево (git)\n")
		for _, l := range core.SplitLines(st) {
			if strings.TrimSpace(l) != "" {
				b.WriteString(l + "\n")
			}
		}
	} else {
		b.WriteString("\nРабочее дерево чистое по git.\n")
	}
	if len(list) > 0 {
		b.WriteString("\nОткатить последнюю: revert_last (посмотри diff: changes {detail:true}).\n")
	}
	return Result{
		Text:    strings.TrimSpace(b.String()),
		Summary: fmt.Sprintf("правок: %d, файлов: %d", len(list), uniquePaths(list)),
	}, nil
}

func uniquePaths(list []Change) int {
	seen := map[string]bool{}
	for _, c := range list {
		seen[c.Path] = true
	}
	return len(seen)
}

// hRevertLast — откатить последнюю правку.
func (r *Registry) hRevertLast(_ context.Context, m map[string]any) (Result, error) {
	if r.env.ReadOnly {
		return Result{Error: "режим «только чтение»: откат запрещён"}, nil
	}
	c, ok := r.change().last()
	if !ok {
		return Result{}, fmt.Errorf("откатывать нечего: в этом ходе не было ни одной правки через write_file / edit_file")
	}
	if p := ArgStr(m, "path"); p != "" && r.resolvePath(p) != c.Path {
		return Result{}, fmt.Errorf("последняя правка — не этот файл: последним менялся %s, а указан %s", c.Rel, p)
	}

	cur, err := os.ReadFile(c.Path)
	if err != nil {
		return Result{}, fmt.Errorf("файл %s прочитан не удаётся: %v", c.Rel, err)
	}
	// Откат поверх изменённого файла затрёт чужую работу. Поэтому отказываем,
	// если содержимое уже не то, что записал агент.
	if string(cur) != c.New {
		return Result{}, fmt.Errorf("%s изменён после правки агента — откат затронет чужую работу, сначала разберись вручную", c.Rel)
	}

	diff := unifiedDiff(string(cur), c.Old, c.Rel)
	if ArgBool(m, "dry_run") || ArgBool(m, "show") {
		return Result{
			Text:    fmt.Sprintf("Откат %s (пока ничего не сделано):\n\n```diff\n%s\n```\n\nПрименить: revert_last без dry_run.", c.Rel, diff),
			Summary: "откат показан, не применён",
		}, nil
	}

	if !r.confirmWrite(c.Path, string(cur), c.Old) {
		return Result{Text: "Откат отменён пользователем", Summary: "отменено"}, nil
	}
	// Созданный файл откатываем удалением — «пустой файл» был бы мусором.
	if c.Old == "" && !c.Existed {
		if err := os.Remove(c.Path); err != nil {
			return Result{}, fmt.Errorf("не удалось удалить созданный файл %s: %v", c.Rel, err)
		}
	} else if err := core.WriteAtomic(c.Path, []byte(c.Old), 0o644); err != nil {
		return Result{}, err
	}
	r.record(c.Path, "revert_last")
	r.change().dropLast()
	return Result{
		Text: fmt.Sprintf("Откачено: %s.\n\n```diff\n%s\n```\n"+
			"Проверь результат: verify.", c.Rel, diff),
		Summary: "откат: " + c.Rel,
	}, nil
}

// unifiedDiff — грубый, но честный diff двух строк.
func unifiedDiff(old, new, name string) string {
	ol := core.SplitLines(old)
	nl := core.SplitLines(new)
	// Показываем только изменившееся место: полный diff большого файла агента
	// не нужен, достаточно понять, что именно вернётся.
	start := 0
	for start < len(ol) && start < len(nl) && ol[start] == nl[start] {
		start++
	}
	endOld, endNew := len(ol), len(nl)
	for endOld > start && endNew > start && ol[endOld-1] == nl[endNew-1] {
		endOld--
		endNew--
	}
	from := core.Max(0, start-2)
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n@@ строки с %d\n", name, name, start+1)
	for i := from; i < start && i < len(ol); i++ {
		fmt.Fprintf(&b, " %s\n", ol[i])
	}
	for i := start; i < endOld && i < len(ol); i++ {
		fmt.Fprintf(&b, "-%s\n", ol[i])
	}
	for i := start; i < endNew && i < len(nl); i++ {
		fmt.Fprintf(&b, "+%s\n", nl[i])
	}
	return strings.TrimSpace(b.String())
}

// ChangesSince — файлы, изменённые после момента t (для отчёта handoff).
func (r *Registry) ChangesSince(t time.Time) []Change {
	var out []Change
	for _, c := range r.change().all() {
		if c.At.After(t) {
			out = append(out, c)
		}
	}
	return out
}

// changeStart — момент начала работы агента (первая правка).
func (r *Registry) changeStart() time.Time {
	list := r.change().all()
	if len(list) == 0 {
		return time.Time{}
	}
	return list[0].At
}
