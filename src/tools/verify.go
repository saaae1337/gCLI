package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gcli/core"
)

// ---------- Верификация правок ----------
//
// Самая частая причина плохой работы агента — не отсутствие знаний, а
// отсутствие проверки. Правка сделана, тесты не запущены, сбой всплывает
// позже и уже на чужом коде. В промпте это была одна строка («после правок
// запускай сборку/тесты»), чего недостаточно: агент не знает, какой именно
// командой проверять этот проект, и обычно не догадывается.
//
// Инструмент решает ровно эту задачу и делает три вещи, которых нет в
// привычных агентах:
//
//  1. находит команду проверки проекта автоматически (по файлам проекта),
//     а не по догадке модели;
//  2. запускает её и возвращает НЕ сырой лог, а разбор: что именно упало,
//     в каких файлах и с какими строками;
//  3. сравнивает результат с предыдущим прогоном — то есть показывает,
//     стала ли сборка хуже из-за конкретной правки, а не просто «красно».
//
// Пункт 3 — то, чего нет ни в Claude Code, ни в Codex: не «тесты упали»,
// а «эти 3 теста упали после того, как ты изменил вот этот файл».

// VerifyKind — что предлагает верифицировать.
type VerifyKind struct {
	// Name — короткое имя для UI.
	Name string
	// Cmd — команда запуска.
	Cmd string
	// Files — какие файлы проекта на неё указывают.
	Files []string
}

// verifyCandidates — правила определения команды проверки.
//
// Порядок важен: сначала самые строгие и быстрые проверки, потом общие.
// Совпадение по наличию файла дешевле, чем запуск команды наугад.
var verifyCandidates = []VerifyKind{
	{"go-тесты", "go test ./...", []string{"go.mod"}},
	{"go-сборка", "go build ./...", []string{"go.mod"}},
	{"rust-тесты", "cargo test", []string{"Cargo.toml"}},
	{"rust-проверка", "cargo clippy --all-targets", []string{"Cargo.toml"}},
	{"python-тесты", "python -m pytest -q", []string{"pytest.ini", "pyproject.toml", "setup.py", "conftest.py"}},
	{"pytest-по-умолчанию", "python -m pytest -q", []string{"tests", "test"}},
	{"js-тесты", "npm test --silent", []string{"package.json"}},
	{"ts-проверка", "npx tsc --noEmit", []string{"tsconfig.json"}},
	{"make", "make test", []string{"Makefile"}},
	{"just", "just test", []string{"justfile"}},
	{"php-тесты", "vendor/bin/phpunit", []string{"phpunit.xml"}},
	{"ruby-тесты", "bundle exec rspec", []string{".rspec"}},
	{"dotnet-тесты", "dotnet test", []string{"*.csproj", "*.sln"}},
	{"cmake-сборка", "cmake --build build", []string{"CMakeLists.txt"}},
}

// DetectVerify — определить, чем проверять этот проект.
//
// Возвращает несколько кандидатов: выбрать один — дело агента, который
// видит задачу. Порог доверия — файл маркера должен существовать.
func DetectVerify(workDir string) []VerifyKind {
	var out []VerifyKind
	seen := map[string]bool{}
	for _, c := range verifyCandidates {
		if seen[c.Cmd] {
			continue
		}
		for _, f := range c.Files {
			if existsGlob(workDir, f) {
				out = append(out, c)
				seen[c.Cmd] = true
				break
			}
		}
	}
	// Ветка всегда проверяется самой первой: незакоммиченная работа —
	// главный источник «оно у меня работало».
	if gitRepo(workDir) {
		out = append([]VerifyKind{{"git-ветка", "git status --porcelain", []string{".git"}}}, out...)
	}
	return out
}

// existsGlob — существует ли путь (простая поддержка * в имени).
func existsGlob(workDir, name string) bool {
	if strings.ContainsAny(name, "*?") {
		matches, err := filepath.Glob(filepath.Join(workDir, name))
		return err == nil && len(matches) > 0
	}
	st, err := os.Stat(filepath.Join(workDir, name))
	return err == nil && !st.IsDir()
}

func gitRepo(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, ".git"))
	return err == nil
}

// ---------- Разбор вывода проверки ----------

// Failure — одно конкретное падение.
type Failure struct {
	Where  string // файл:строка или имя теста
	What   string // суть в одну строку
	Raw    string
	Output string // весь блок, если он небольшой
}

// VerifyReport — разобранный результат проверки.
type VerifyReport struct {
	Command  string
	OK       bool
	ExitCode int
	Elapsed  time.Duration
	// Passed / Failed — счётчики, если их удалось распознать.
	Passed int
	Failed int
	// Failures — конкретные падения.
	Failures []Failure
	// NewFailures — падения, которых не было в прошлом прогоне.
	NewFailures []Failure
	// Fixed — сколько паений исчезло с прошлого прогона.
	Fixed int
	// Baseline — был ли прошлый прогон для сравнения.
	HadBaseline bool
	// Diagnostics — разобранные сообщения компилятора.
	Diagnostics []Diag
	// Raw — сырой вывод (обрезанный).
	Raw string
}

// Diag — диагностика компилятора.
type Diag struct {
	File string
	Line int
	Col  int
	Msg  string
	Tool string // компилятор или «тест»
}

// reGoDiag — ошибка компилятора Go: file.go:12:3: message.
var reGoDiag = regexp.MustCompile(`^([^\s:]+\.go):(\d+)(?::(\d+))?:\s*(.*)$`)

// reTestFail — падающий тест.
//
// В шаблоне нет голого «FAIL»: у Go это «--- FAIL:», у pytest — «FAILED»,
// у jest/vitest — «✖». Голое «FAIL» и «FAIL\tgcli/tools\t1.2s» — сводки
// прогона, и разбирать их как падения нельзя: агент начнёт «чинить» то, что
// никогда не ломалось. Отсекаем по маркеру, а не по виду имени — иначе под
// нож попадут законные pytest-имена вида tests/test_foo.py::test_bar.
var reTestFail = regexp.MustCompile(`^\s*(?:---\s*FAIL|FAILED|ERROR|✖|×)\s*[:\s]\s*(\S+)`)

// reTestMark — тот же маркер, но без захвата имени. Нужен, чтобы отрезать
// префикс и взять имя целиком: у Go в имени тайминг («TestFoo (0.00s)»),
// у pytest — путь вместе с классом.
var reTestMark = regexp.MustCompile(`^\s*(?:---\s*FAIL|FAILED|ERROR|✖|×)\s*[:\s]*`)

// reCounters — сводные счётчики вида «ok ... 5 passed» или pytest.
var reCounters = regexp.MustCompile(`(\d+)\s+(passed|failed|ok|FAIL|PASS)`)

// ParseVerify — разобрать вывод команды проверки.
//
// Разбор сознательно неполный: задача не в том, чтобы заменить тестовый
// раннер, а в том, чтобы агент получил адресные подсказки вместо простыни
// логов на 15 000 символов, где ошибка теряется.
func ParseVerify(cmd string, exitCode int, elapsed time.Duration, out string) VerifyReport {
	rep := VerifyReport{Command: cmd, OK: exitCode == 0, ExitCode: exitCode, Elapsed: elapsed}

	lines := core.SplitLines(out)
	// Блоки падений группируются: заголовок FAIL и следующие за ним строки.
	var cur *Failure
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		// Тело падения go-теста — «    foo_test.go:10: got 3, want 5» — похоже на
		// диагностику компилятора, но им не является. Отступ и означает, что
		// это продолжение блока, а не новая ошибка сборки.
		if m := reGoDiag.FindStringSubmatch(trimmed); m != nil && !strings.HasPrefix(trimmed, "FAIL") && l == trimmed {
			d := Diag{File: m[1], Msg: strings.TrimSpace(m[4])}
			fmt.Sscanf(m[2], "%d", &d.Line)
			if m[3] != "" {
				fmt.Sscanf(m[3], "%d", &d.Col)
			}
			d.Tool = "компилятор"
			rep.Diagnostics = append(rep.Diagnostics, d)
			continue
		}
		if m := reTestFail.FindStringSubmatch(l); m != nil {
			// Имя берём по всей строке после маркера: у Go в нём тайминг
			// («TestFoo (0.00s)»), у pytest — путь и класс теста.
			name := strings.TrimSpace(reTestMark.ReplaceAllString(l, ""))
			if i := strings.Index(name, ":"); i >= 0 {
				name = strings.TrimSpace(name[i+1:])
			}
			if name != "" {
				rep.Failed++
				rep.Failures = append(rep.Failures, Failure{Where: name, What: oneLineAfterColon(l), Raw: l})
				// Указатель именно на элемент среза: append копирует значение,
				// и дописывание тела падения в отдельную копию терялось бы —
				// в отчёте оставалось «(0.00s)» без причины.
				cur = &rep.Failures[len(rep.Failures)-1]
				continue
			}
		}
		// Тело падения — строки с отступом после заголовка. Отступ сверяется
		// целиком, иначе после gofmt / на табах «    foo_test.go:10: …» не
		// попадёт в блок и падение останется без причины.
		if cur != nil && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && strings.TrimSpace(l) != "" {
			cur.Output += l + "\n"
		} else {
			cur = nil
		}
	}
	// Заголовок падения отвечает на вопрос КАКОЕ, а тело — ПОЧЕМУ. Без
	// этого шага в отчёт уходит «TestFoo (0.00s)», и чинить падение придётся
	// вслепую: ни «got 3, want 5», ни файла с ошибкой агент не увидит.
	for i := range rep.Failures {
		if reason := failureReason(rep.Failures[i].Output); reason != "" {
			rep.Failures[i].What = reason
		}
	}

	// Счётчики из сводных строк (pytest, jest, go test -v).
	for _, l := range lines {
		for _, m := range reCounters.FindAllStringSubmatch(l, -1) {
			var n int
			fmt.Sscanf(m[1], "%d", &n)
			switch strings.ToUpper(m[2]) {
			case "PASSED", "OK":
				if n > rep.Passed {
					rep.Passed = n
				}
			case "FAILED", "FAIL":
				if n > rep.Failed {
					rep.Failed = n
				}
			}
		}
	}

	// Диагностика без FAIL-блоков: сборка не прошла, тесты даже не начались.
	if len(rep.Failures) == 0 && exitCode != 0 {
		if len(rep.Diagnostics) > 0 {
			for _, d := range rep.Diagnostics {
				rep.Failures = append(rep.Failures, Failure{
					Where:  fmt.Sprintf("%s:%d", d.File, d.Line),
					What:   d.Msg,
					Output: d.Msg,
				})
			}
		} else {
			// Ничего не распознали — отдаём хвост вывода, где обычно суть.
			rep.Failures = append(rep.Failures, Failure{
				Where:  "вывод команды",
				What:   core.Truncate(core.OneLine(strings.Join(tail(lines, 12), "\n")), 200),
				Output: strings.Join(tail(lines, 30), "\n"),
			})
		}
	}

	rep.Raw = core.TruncateUTF8(out, 6000, 3000)
	return rep
}

func oneLineAfterColon(s string) string {
	if i := strings.Index(s, ":"); i >= 0 && i+1 < len(s) {
		return core.Truncate(strings.TrimSpace(s[i+1:]), 160)
	}
	return core.Truncate(core.OneLine(s), 160)
}

// failureReason — самая полезная строка тела падения.
//
// Тело почти всегда начинается с «file_test.go:12: got 3, want 5»: адрес и
// ожидаемое значение. Именно её агент должен увидеть первой, а не заголовок
// «TestFoo (0.00s)», который не говорит ничего.
func failureReason(output string) string {
	for _, l := range core.SplitLines(output) {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		// Отбрасываем рамки и трассировки — они только разбавляют причину.
		if strings.HasPrefix(t, "---") || strings.HasPrefix(t, "===") {
			continue
		}
		return core.Truncate(t, 200)
	}
	return ""
}

func tail(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// Diff — сравнить текущий прогон с предыдущим.
//
// Смысл: «красно» бесполезно, «стало хуже вот из-за этого» — полезно.
// Сравнение по ключу «где+что», чтобы не среагировать на простое переименование.
func (rep *VerifyReport) Diff(prev *VerifyReport) {
	if prev == nil {
		return
	}
	rep.HadBaseline = true
	old := map[string]bool{}
	for _, f := range prev.Failures {
		old[failureKey(f)] = true
	}
	cur := map[string]bool{}
	for _, f := range rep.Failures {
		cur[failureKey(f)] = true
	}
	for _, f := range rep.Failures {
		if !old[failureKey(f)] {
			rep.NewFailures = append(rep.NewFailures, f)
		}
	}
	for _, f := range prev.Failures {
		if !cur[failureKey(f)] {
			rep.Fixed++
		}
	}
}

func failureKey(f Failure) string {
	return core.OneLine(f.Where) + "|" + core.Truncate(core.OneLine(f.What), 60)
}

// Text — отчёт для контекста модели.
func (r *VerifyReport) Text() string {
	var b strings.Builder
	verdict := "✔ Проверка пройдена"
	if !r.OK {
		verdict = "✖ Проверка провалена"
	}
	fmt.Fprintf(&b, "%s — %s (%s, код выхода %d)\n", verdict, r.Command, core.HumanDuration(r.Elapsed), r.ExitCode)
	if r.Passed > 0 || r.Failed > 0 {
		fmt.Fprintf(&b, "Тестов: успешно %d, упало %d.\n", r.Passed, r.Failed)
	}

	// Что изменилось с прошлого прогона. Это главный вывод отчёта, и он не
	// зависит от кода выхода: «исправилось» может быть и при красном прогоне,
	// если до правки падений было больше.
	if r.HadBaseline {
		switch {
		case len(r.NewFailures) > 0:
			fmt.Fprintf(&b, "\n⚠ НОВЫЕ падения (их не было до последней правки) — %d:\n", len(r.NewFailures))
			for i, f := range r.NewFailures {
				if i >= 12 {
					fmt.Fprintf(&b, "…ещё %d\n", len(r.NewFailures)-12)
					break
				}
				fmt.Fprintf(&b, "  • %s\n    %s\n", f.Where, core.Truncate(f.What, 200))
			}
			if r.Fixed > 0 {
				fmt.Fprintf(&b, "При этом исправлено: %d. Итог: стало хуже.\n", r.Fixed)
			} else {
				b.WriteString("Итог: стало хуже.\n")
			}
			b.WriteString("Это твоя регрессия: чини её, не пробуя команды подряд.\n")
		case r.Fixed > 0:
			fmt.Fprintf(&b, "По сравнению с прошлым прогоном исправлено падений: %d.\n", r.Fixed)
			if r.OK {
				b.WriteString("Правка работает.\n")
			} else {
				fmt.Fprintf(&b, "Правка сдвинула в сторону успеха, но осталось падений: %d.\n", len(r.Failures))
			}
		default:
			b.WriteString("Состояние не изменилось: падений столько же, сколько было до правки.\n")
		}
	}

	if r.OK {
		// Предупреждение честное только когда вывод действительно пустой.
		// «go test ./...» без -v печатает лишь «ok пакет 0.4s» и счётчиков
		// тестов не содержит — ругаться на такой вывод нельзя, иначе
		// инструмент сам себе противоречит на каждом зелёном прогоне.
		if r.Passed == 0 && r.Failed == 0 && len(r.Diagnostics) == 0 && strings.TrimSpace(r.Raw) == "" {
			b.WriteString("Внимание: команда завершилась с кодом 0 и не напечатала ни строчки — " +
				"похоже, она ничего не проверяет. Убедись, что команда осмысленная.\n")
		}
		return strings.TrimSpace(b.String())
	}

	if len(r.Failures) > 0 {
		fmt.Fprintf(&b, "\nЧто именно не так (%d):\n", len(r.Failures))
		for i, f := range r.Failures {
			if i >= 10 {
				fmt.Fprintf(&b, "…ещё %d\n", len(r.Failures)-10)
				break
			}
			fmt.Fprintf(&b, "  • %s\n    %s\n", f.Where, core.Truncate(f.What, 220))
		}
	}
	if len(r.Diagnostics) > 0 {
		b.WriteString("\nДиагностика компилятора:\n")
		for i, d := range r.Diagnostics {
			if i >= 10 {
				fmt.Fprintf(&b, "…ещё %d\n", len(r.Diagnostics)-10)
				break
			}
			fmt.Fprintf(&b, "  %s:%d:%d — %s\n", d.File, d.Line, d.Col, core.Truncate(d.Msg, 160))
		}
	}
	return strings.TrimSpace(b.String())
}

// ---------- Инструмент ----------

// baselinePath — файл предыдущего прогона (для diff между проверками).
//
// Файл привязан к каталогу проекта: один общий baseline на все репозитории
// приводил бы к выдуманным «новым падениям» — прогон в чужом проекте вдруг
// оказывался бы «сравнением» с падениями соседнего.
func baselinePath(workDir string) string {
	home := core.Home()
	sum := sha256.Sum256([]byte(filepath.Clean(workDir)))
	return filepath.Join(home, "verify", hex.EncodeToString(sum[:8])+".json")
}

// baselineData — сохранённый прошлый результат.
type baselineData struct {
	Command  string    `json:"command"`
	Failures []Failure `json:"failures"`
	Passed   int       `json:"passed"`
	Failed   int       `json:"failed"`
	At       time.Time `json:"at"`
}

func loadBaseline(path string) *baselineData {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var b baselineData
	if err := json.Unmarshal(data, &b); err != nil {
		return nil
	}
	return &b
}

func saveBaseline(path string, rep *VerifyReport) {
	b := baselineData{Command: rep.Command, Passed: rep.Passed, Failed: rep.Failed, At: time.Now()}
	for _, f := range rep.Failures {
		b.Failures = append(b.Failures, Failure{Where: f.Where, What: f.What})
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return
	}
	_ = core.WriteAtomic(path, data, 0o644)
}

func toReport(b *baselineData) *VerifyReport {
	if b == nil {
		return nil
	}
	return &VerifyReport{Command: b.Command, Failures: b.Failures, Passed: b.Passed, Failed: b.Failed}
}

// hVerify — проверить, что правки работают.
func (r *Registry) hVerify(ctx context.Context, m map[string]any) (Result, error) {
	// Режим 1: предложить, чем проверять (дешёвый, ничего не запускает).
	if ArgBool(m, "suggest") || (ArgStr(m, "command") == "" && ArgBool(m, "list")) {
		cands := DetectVerify(r.workDir)
		if len(cands) == 0 {
			return Result{
				Text: "Не нашёл в проекте признаков известного стека (go.mod, package.json, Cargo.toml, " +
					"pyproject.toml, Makefile…). Укажи команду проверки явно: {\"command\": \"...\"}.",
				Summary: "команда проверки не найдена",
			}, nil
		}
		var b strings.Builder
		b.WriteString("Чем проверить этот проект (в порядке полезности):\n")
		for _, c := range cands {
			fmt.Fprintf(&b, "- %s: %s  # найдено по %s\n", c.Name, c.Cmd, strings.Join(c.Files, ", "))
		}
		b.WriteString("\nПроверяй ПОСЛЕ каждой серии правок, а не в конце: дешевле чинить, пока помнишь, что менял.")
		return Result{Text: b.String(), Summary: fmt.Sprintf("найдено проверок: %d", len(cands))}, nil
	}

	cmd := strings.TrimSpace(ArgStr(m, "command"))
	if cmd == "" {
		// Команда не указана — берём лучшего кандидата сами.
		cands := DetectVerify(r.workDir)
		if len(cands) == 0 {
			return Result{}, fmt.Errorf("не знаю, чем проверять этот проект — укажи command или запусти с list=true")
		}
		cmd = cands[0].Cmd
	}

	timeout := core.Clamp(ArgInt(m, "timeout_sec", 300), 10, 1800)
	runDir := r.workDir
	if wd := ArgStr(m, "workdir"); wd != "" {
		runDir = r.resolvePath(wd)
	}

	// Команды проверки читают и меняют кэш сборки, но пользователь запускал
	// бы их вручную без подтверждения — поэтому подтверждения не спрашиваем,
	// а результат всё равно показываем в UI как обычный вызов.
	out, err := r.runVerifyCmd(ctx, cmd, runDir, timeout)
	rep := ParseVerify(cmd, out.exit, out.elapsed, out.text)
	// Ошибка запуска — это тоже результат проверки, и разбирать её надо ДО
	// сравнения с базовой линией. Иначе несуществующая команда уйдёт в
	// baseline как «всё хорошо», и следующий прогон покажет «падений не
	// изменилось» вместо внятного «команда не найдена».
	if err != nil && rep.ExitCode == 0 {
		rep.OK = false
		rep.ExitCode = -1
		rep.Failures = append(rep.Failures, Failure{Where: "запуск команды", What: err.Error()})
	}
	rep.Diff(toReport(loadBaseline(baselinePath(r.workDir))))
	if !ArgBool(m, "no_baseline") {
		// no_baseline — режим «посмотреть, что сломалось», не портим линию.
		saveBaseline(baselinePath(r.workDir), &rep)
	}

	summary := "проверка ок"
	if !rep.OK {
		summary = fmt.Sprintf("проверка упала: %d", len(rep.Failures))
		if len(rep.NewFailures) > 0 {
			summary += fmt.Sprintf(" (новых: %d)", len(rep.NewFailures))
		}
	}
	return Result{Text: rep.Text(), Summary: summary}, nil
}

type verifyOut struct {
	text    string
	exit    int
	elapsed time.Duration
}

// runVerifyCmd — выполнить команду проверки.
func (r *Registry) runVerifyCmd(ctx context.Context, cmd, dir string, timeout int) (verifyOut, error) {
	// Защита от nil: инструмент могут вызвать вне агентного цикла (тесты,
	// будущие вызовы из UI), а context.WithTimeout(nil, …) паникует.
	if ctx == nil {
		ctx = context.Background()
	}
	if r.env.Confirm != nil {
		ok := r.env.Confirm(ConfirmReq{Kind: ConfirmExec, Detail: cmd, Reason: "проверка результата"})
		if !ok {
			return verifyOut{text: "[проверка отменена пользователем]"}, fmt.Errorf("отменено пользователем")
		}
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	shell, sargs := ShellCommand(cmd)
	ce := exec.CommandContext(cctx, shell, sargs...)
	ce.Dir = dir
	ce.Env = append(os.Environ(), "GCLI=1", "CI=1")
	t0 := time.Now()
	data, err := ce.CombinedOutput()
	out := verifyOut{text: string(data), elapsed: time.Since(t0)}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out.exit = ee.ExitCode()
			return out, nil
		}
		if cctx.Err() == context.DeadlineExceeded {
			out.text += fmt.Sprintf("\n[таймаут %ds]", timeout)
			out.exit = -1
			return out, nil
		}
		return out, err
	}
	return out, nil
}

// VerifyHint — короткая подсказка для системного промпта: чем этот проект
// проверяется. Экономит модели вызов на подбор команды.
func VerifyHint(workDir string) string {
	cands := DetectVerify(workDir)
	if len(cands) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cands))
	for i, c := range cands {
		if i >= 3 {
			break
		}
		parts = append(parts, c.Cmd)
	}
	return "Проверка этого проекта: " + strings.Join(parts, " | ") +
		". Инструмент verify сам разберёт ошибки и покажет, что сломалось из-за последней правки."
}
