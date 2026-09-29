package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gcli/core"
)

// Схемы инструментов (JSON Schema для function calling).
const (
	schemaRead = `{"type":"object","properties":{"path":{"type":"string","description":"Путь к файлу (относительный или абсолютный)"},"offset":{"type":"integer","description":"Начальная строка (1-based)"},"limit":{"type":"integer","description":"Максимум строк (по умолчанию 2000)"}},"required":["path"]}`

	schemaWrite = `{"type":"object","properties":{"path":{"type":"string","description":"Путь к файлу"},"content":{"type":"string","description":"Полное содержимое файла"}},"required":["path","content"]}`

	schemaEdit = `{"type":"object","properties":{"path":{"type":"string","description":"Путь к файлу"},"old_string":{"type":"string","description":"Точный фрагмент для замены (с отступами)"},"new_string":{"type":"string","description":"Новый фрагмент"},"replace_all":{"type":"boolean","description":"Заменить все вхождения (по умолчанию только первое)"}},"required":["path","old_string","new_string"]}`

	schemaList = `{"type":"object","properties":{"path":{"type":"string","description":"Каталог (по умолчанию текущий)"},"depth":{"type":"integer","description":"Глубина рекурсии (по умолчанию 1)"}}}`

	schemaGlob = `{"type":"object","properties":{"pattern":{"type":"string","description":"Шаблон, например src/**/*.go или *.md"},"max_results":{"type":"integer","description":"Лимит результатов (по умолчанию 200)"}},"required":["pattern"]}`

	schemaGrep = `{"type":"object","properties":{"pattern":{"type":"string","description":"Регулярное выражение (Go/RE2)"},"path":{"type":"string","description":"Каталог поиска (по умолчанию текущий)"},"include":{"type":"string","description":"Фильтр имён файлов, например *.go"},"max_results":{"type":"integer","description":"Лимит результатов (по умолчанию 50)"}},"required":["pattern"]}`

	schemaBash = `{"type":"object","properties":{"command":{"type":"string","description":"Команда для выполнения в shell"},"timeout_sec":{"type":"integer","description":"Таймаут в секундах (5-300, по умолчанию 60)"},"workdir":{"type":"string","description":"Рабочий каталог команды (по умолчанию текущий)"}},"required":["command"]}`

	schemaSearch = `{"type":"object","properties":{"query":{"type":"string","description":"Поисковый запрос"},"max_results":{"type":"integer","description":"Сколько результатов (по умолчанию 8)"}},"required":["query"]}`

	schemaFetch = `{"type":"object","properties":{"url":{"type":"string","description":"URL страницы"},"max_chars":{"type":"integer","description":"Максимум символов текста (по умолчанию 8000)"}},"required":["url"]}`

	schemaTodo = `{"type":"object","properties":{"todos":{"type":"array","items":{"type":"object","properties":{"content":{"type":"string","description":"Суть задачи"},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"required":["content","status"]}}},"required":["todos"]}`

	schemaThink = `{"type":"object","properties":{"thought":{"type":"string","description":"Ход размышления: план, гипотезы, проверка предположений, разбор задачи"}},"required":["thought"]}`

	schemaNote = `{"type":"object","properties":{"title":{"type":"string","description":"Короткий заголовок заметки"},"body":{"type":"string","description":"Текст заметки — факт, вывод, договорённость"}},"required":["title","body"]}`

	schemaRemember = `{"type":"object","properties":{"action":{"type":"string","enum":["write","forget","read"],"description":"write — запомнить факт между сессиями; forget — забыть по подстроке; read — показать всю память (по умолчанию)"},"fact":{"type":"string","description":"Текст факта — для write и поиск для forget"},"note":{"type":"string","description":"Почему факт важен (для write). Помеченные факты попадают в промпт в первую очередь и не протухают."}}}`

	schemaSelf = `{"type":"object","properties":{"section":{"type":"string","description":"Часть отчёта: контекст, расход, итерации, заметки, субагенты, лимиты. Пусто — весь отчёт."}}}`

	schemaVerify = `{"type":"object","properties":{"command":{"type":"string","description":"Команда проверки. Пусто — определить автоматически по файлам проекта"},"suggest":{"type":"boolean","description":"Только показать варианты проверки, ничего не запускать"},"list":{"type":"boolean","description":"То же, что suggest"},"timeout_sec":{"type":"integer","description":"Таймаут в секундах (по умолчанию 300)"},"workdir":{"type":"string","description":"Каталог запуска"},"no_baseline":{"type":"boolean","description":"Не обновлять базовую линию: посмотреть, что сломано, не потеряв предыдущее состояние"}}}`

	schemaSpawn = `{"type":"object","properties":{"type":{"type":"string","description":"Тип субагента или имя своего агента из .gcli/agents/*.md. Типы: explorer — карта кода (только чтение); reviewer — ревью и баги (чтение); planner — план реализации (чтение); coder — реализация; tester — тесты; frontend — верстка с проверкой по скриншотам; researcher — веб-исследование; docs — документация; general — универсал"},"task":{"type":"string","description":"Чёткая задача для субагента: что сделать и что вернуть"},"name":{"type":"string","description":"Имя субагента (необязательно)"},"model":{"type":"string","description":"Модель субагента (необязательно, по умолчанию текущая)"},"read_only":{"type":"boolean","description":"Запретить любые изменения файлов (по умолчанию true для explorer/reviewer/planner/researcher)"}},"required":["type","task"]}`

	schemaAgents = `{"type":"object","properties":{"action":{"type":"string","enum":["status","list","result"],"description":"status — сводка; list — список запусков; result — итог субагента по имени"},"name":{"type":"string","description":"Имя субагента для action=result"}}}`

	schemaAsk = `{"type":"object","properties":{"question":{"type":"string","description":"Вопрос пользователю, требующий решения"},"options":{"type":"array","items":{"type":"string"},"description":"Варианты ответа, если их немного"}},"required":["question"]}`
)

// registerBuiltins — все встроенные инструменты.
func (r *Registry) registerBuiltins() {
	r.register("read_file", "Прочитать текстовый файл. Возвращает содержимое с номерами строк. Читай перед edit_file.",
		schemaRead, "read", false, r.hReadFile)
	r.register("write_file", "Создать файл или полностью перезаписать. Перед вызовом пользователю показывается diff.",
		schemaWrite, "write", true, r.hWriteFile)
	r.register("edit_file", "Точечная замена фрагмента в файле. old_string должен точно совпадать с содержимым (с отступами). Сначала read_file.",
		schemaEdit, "write", true, r.hEditFile)
	r.register("list_dir", "Список файлов и папок в каталоге.", schemaList, "read", false, r.hListDir)
	r.register("glob", "Найти файлы по шаблону (поддерживается **).", schemaGlob, "read", false, r.hGlob)
	r.register("grep", "Поиск по регулярному выражению в файлах проекта.", schemaGrep, "read", false, r.hGrep)
	r.register("bash", "Выполнить shell-команду и получить вывод. Не запускай интерактивные команды. Требует подтверждения пользователя.",
		schemaBash, "exec", true, r.hBash)
	r.register("web_search", "Поиск в интернете. Возвращает заголовки, ссылки и сниппеты.", schemaSearch, "net", false, r.hWebSearch)
	r.register("web_fetch", "Скачать страницу и извлечь текст.", schemaFetch, "net", false, r.hWebFetch)
	r.register("todo_write", "Вести план многошаговой задачи: создай список, обновляй статусы in_progress/completed по ходу работы.",
		schemaTodo, "plan", false, r.hTodoWrite)
	r.register("think", "Приватное размышление: разложи сложную задачу на шаги, проверь гипотезы, спланируй действия перед другими инструментами. Ничего не меняет.",
		schemaThink, "think", false, r.hThink)
	r.register("task_note", "Записать важный факт, вывод или договорённость в память задачи — будет доступно субагентам и в /export.",
		schemaNote, "plan", false, r.hNote)
	r.register("remember", "Долговременная память, переживает сессии: action=write запомнить факт о проекте (fact, note — почему важен); action=forget забыть по подстроке; action=read показать всё. Используй для дорогих выводов: команды сборки, грабли, договорённости.",
		schemaRemember, "plan", false, r.hRemember)
	r.register("self_status", "Посмотреть на самого себя: заполнение контекста и порог сжатия, расход токенов, оставшиеся итерации, заметки, субагенты, фактические лимиты инструментов. Звони, когда не уверен в бюджете или не понимаешь, что уже было сделано.",
		schemaSelf, "think", false, r.hSelfStatus)
	r.register("verify", "Проверить, что правки реально работают. Без command: определит, чем проверять этот проект (по go.mod, package.json, Cargo.toml…) и разберёт ошибки. Сравнивает с прошлым прогоном и показывает НОВЫЕ падения — то есть что сломалось именно из-за последней правки.",
		schemaVerify, "exec", false, r.hVerify)
}

// ---------- Инструменты чтения ----------

// hReadFile — чтение файла с нумерацией строк.
func (r *Registry) hReadFile(_ context.Context, m map[string]any) (Result, error) {
	raw := ArgStr(m, "path")
	if raw == "" {
		return Result{}, fmt.Errorf("укажи path")
	}
	p := r.resolvePath(raw)
	data, err := os.ReadFile(p)
	if err != nil {
		return Result{}, err
	}
	markRead(r.env.ReadFiles, p)

	if core.IsBinary(data) {
		return Result{
			Text:    fmt.Sprintf("[двоичный файл: %s, %s]", p, core.HumanSize(len(data))),
			Summary: "двоичный файл, " + core.HumanSize(len(data)),
		}, nil
	}

	lines := core.SplitLines(string(data))
	if len(lines) == 0 {
		return Result{Text: fmt.Sprintf("Файл: %s (пустой)", p), Summary: "пустой файл"}, nil
	}

	off := core.Max(1, ArgInt(m, "offset", 1))
	lim := ArgInt(m, "limit", 2000)
	if lim < 1 {
		lim = 2000
	}
	if lim > 5000 {
		lim = 5000
	}

	var b strings.Builder
	total := 0
	for i := off - 1; i < len(lines) && total < lim; i++ {
		l := lines[i]
		if len([]rune(l)) > 2000 {
			l = string([]rune(l)[:2000]) + " …"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, l)
		total++
	}
	note := ""
	if off > 1 || off-1+total < len(lines) {
		note = fmt.Sprintf("\n[показано строк: %d из %d; далее используй offset=%d]", total, len(lines), off+total)
	}
	rel := core.RelToWD(r.workDir, p)
	return Result{
		Text:    fmt.Sprintf("Файл: %s (%d строк)\n\n%s%s", p, len(lines), b.String(), note),
		Summary: fmt.Sprintf("%s — %d строк", rel, len(lines)),
	}, nil
}

// hListDir — листинг каталога (с необязательной рекурсией).
func (r *Registry) hListDir(_ context.Context, m map[string]any) (Result, error) {
	raw := ArgStr(m, "path")
	if raw == "" {
		raw = "."
	}
	p := r.resolvePath(raw)
	depth := core.Clamp(ArgInt(m, "depth", 1), 1, 4)

	type row struct {
		name string
		dir  bool
		size int64
	}
	var rows []row
	total := 0
	truncated := false

	var walk func(dir string, level int) error
	walk = func(dir string, level int) error {
		ents, err := core.SortedEntries(dir)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if total >= 500 {
				truncated = true
				return nil
			}
			if e.IsDir() && core.ShouldSkipDir(e.Name()) {
				continue
			}
			full := filepath.Join(dir, e.Name())
			r := row{name: full, dir: e.IsDir()}
			if !e.IsDir() {
				if info, err := e.Info(); err == nil {
					r.size = info.Size()
				}
			}
			rows = append(rows, r)
			total++
			if e.IsDir() && level < depth {
				if err := walk(full, level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(p, 1); err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{Text: fmt.Sprintf("Каталог %s пуст", core.RelToWD(r.workDir, p)), Summary: "пусто"}, nil
	}

	var b strings.Builder
	for _, row := range rows {
		rel := core.RelToWD(r.workDir, row.name)
		if row.dir {
			fmt.Fprintf(&b, "  %s\n", rel+string(os.PathSeparator))
		} else {
			fmt.Fprintf(&b, "  %s  (%s)\n", rel, core.HumanSize(int(row.size)))
		}
	}
	note := ""
	if truncated {
		note = fmt.Sprintf("\n\n[показаны первые 500 записей из большего числа]")
	}
	rel := core.RelToWD(r.workDir, p)
	return Result{
		Text:    fmt.Sprintf("Каталог: %s (%d записей)\n%s%s", p, total, b.String(), note),
		Summary: fmt.Sprintf("%s — %d записей", rel, total),
	}, nil
}

// ---------- Запись файлов ----------

// hWriteFile — полная перезапись файла.
func (r *Registry) hWriteFile(_ context.Context, m map[string]any) (Result, error) {
	raw := ArgStr(m, "path")
	if raw == "" {
		return Result{}, fmt.Errorf("укажи path")
	}
	if r.env.ReadOnly {
		return Result{Error: "режим «только чтение»: изменение файлов запрещено"}, nil
	}
	p := r.resolvePath(raw)
	content := ArgStr(m, "content")

	var old string
	if b, err := os.ReadFile(p); err == nil {
		old = string(b)
	}
	if old == content {
		return Result{Text: "Файл не изменился — запись отменена (содержимое идентично)", Summary: "без изменений"}, nil
	}
	if !r.confirmWrite(p, old, content) {
		return Result{Text: "Изменение отменено пользователем", Summary: "отменено"}, nil
	}
	r.record(p, "write_file")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Result{}, err
	}
	if err := core.WriteAtomic(p, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	markRead(r.env.ReadFiles, p)
	rel := core.RelToWD(r.workDir, p)
	return Result{
		Text:    fmt.Sprintf("Записано: %s (%d строк, %s)", rel, len(core.SplitLines(content)), core.HumanSize(len(content))),
		Summary: fmt.Sprintf("записан %s (%d строк)", rel, len(core.SplitLines(content))),
	}, nil
}

// hEditFile — точечная замена фрагмента.
func (r *Registry) hEditFile(_ context.Context, m map[string]any) (Result, error) {
	raw := ArgStr(m, "path")
	if raw == "" {
		return Result{}, fmt.Errorf("укажи path")
	}
	if r.env.ReadOnly {
		return Result{Error: "режим «только чтение»: изменение файлов запрещено"}, nil
	}
	p := r.resolvePath(raw)
	if !wasRead(r.env.ReadFiles, p) {
		return Result{}, fmt.Errorf("сначала прочитай файл через read_file: %s", core.RelToWD(r.workDir, p))
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Result{}, err
	}
	content := string(data)
	old := ArgStr(m, "old_string")
	if old == "" {
		return Result{}, fmt.Errorf("укажи old_string")
	}
	if old == "" {
		return Result{}, fmt.Errorf("old_string пустой")
	}
	newS := ArgStr(m, "new_string")

	cnt := strings.Count(content, old)
	if cnt == 0 {
		return Result{}, fmt.Errorf("old_string не найден в %s — проверь точность (пробелы, отступы); перечитай файл", core.RelToWD(r.workDir, p))
	}
	replAll := ArgBool(m, "replace_all")
	if cnt > 1 && !replAll {
		return Result{}, fmt.Errorf("найдено %d вхождений old_string; возьми более длинный уникальный фрагмент или передай replace_all=true", cnt)
	}

	var updated string
	replaced := 1
	if replAll {
		updated = strings.ReplaceAll(content, old, newS)
		replaced = cnt
	} else {
		updated = strings.Replace(content, old, newS, 1)
	}
	if !r.confirmWrite(p, content, updated) {
		return Result{Text: "Изменение отменено пользователем", Summary: "отменено"}, nil
	}
	r.record(p, "edit_file")
	if err := core.WriteAtomic(p, []byte(updated), 0o644); err != nil {
		return Result{}, err
	}
	rel := core.RelToWD(r.workDir, p)
	return Result{
		Text:    fmt.Sprintf("Файл обновлён: %s (замен: %d)", rel, replaced),
		Summary: fmt.Sprintf("%s — замен: %d", rel, replaced),
	}, nil
}

// confirmWrite — подтверждение записи (дифф показывает вызывающий агент).
func (r *Registry) confirmWrite(path, oldContent, newContent string) bool {
	if r.env.Confirm == nil {
		return true
	}
	return r.env.Confirm(ConfirmReq{
		Kind: ConfirmWrite,
		Path: path,
		Old:  oldContent,
		New:  newContent,
	})
}

// record — чекпоинт перед изменением.
func (r *Registry) record(path, label string) {
	if r.env.Record != nil {
		r.env.Record(path, label)
	}
}

// ---------- glob / grep ----------

var errStopWalk = fmt.Errorf("стоп-обход")

// GlobToRegexp — превратить glob-шаблон в регулярное выражение.
func GlobToRegexp(pat string) string {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(pat)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '*' {
			if i+1 < len(runes) && runes[i+1] == '*' {
				b.WriteString(".*")
				i++
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		} else if r == '?' {
			b.WriteString("[^/]")
		} else if r == '[' {
			// Класс символов [abc] / [!abc].
			j := i + 1
			if j < len(runes) && (runes[j] == '!' || runes[j] == '^') {
				j++
			}
			for j < len(runes) && runes[j] != ']' {
				j++
			}
			if j < len(runes) {
				cls := string(runes[i : j+1])
				cls = strings.Replace(cls, "!", "^", 1)
				b.WriteString(cls)
				i = j
			} else {
				b.WriteString(`\[`)
			}
		} else {
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return b.String()
}

// hGlob — поиск файлов по шаблону.
func (r *Registry) hGlob(_ context.Context, m map[string]any) (Result, error) {
	pat := ArgStr(m, "pattern")
	if pat == "" {
		return Result{}, fmt.Errorf("укажи pattern")
	}
	// Модель может писать и src/ui, и src\ui — приводим к единому виду.
	normPat := strings.ReplaceAll(pat, "\\", "/")
	re, err := compileGlob(normPat)
	if err != nil {
		return Result{}, fmt.Errorf("плохой шаблон: %v", err)
	}

	maxRes := core.Clamp(ArgInt(m, "max_results", 200), 1, 2000)

	// Определяем корень обхода: часть до первого wildcard.
	root := "."
	if isAbs(normPat) {
		root = filepath.VolumeName(r.resolvePath(pat))
		if root == "" {
			root = "/"
		} else {
			root += string(filepath.Separator)
		}
	} else if idx := strings.IndexAny(normPat, "*?["); idx > 0 {
		if d := filepath.Dir(normPat[:idx]); d != "." && d != "" {
			root = d
		}
	}
	rootAbs := r.resolvePath(root)

	var out []string
	steps := 0
	walkErr := filepath.WalkDir(rootAbs, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		steps++
		if steps > 300000 || len(out) >= maxRes {
			return errStopWalk
		}
		if d.IsDir() {
			if path != rootAbs && core.ShouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		// Матчим по нормализованному пути, чтобы работало и с \, и с /.
		candidate := core.RelToWD(r.workDir, path)
		if isAbs(normPat) {
			candidate = path
		}
		candidate = strings.ReplaceAll(candidate, "\\", "/")
		if re.MatchString(candidate) {
			out = append(out, core.RelToWD(r.workDir, path))
		}
		return nil
	})
	if walkErr != nil && walkErr != errStopWalk {
		return Result{}, walkErr
	}
	if len(out) == 0 {
		return Result{Text: fmt.Sprintf("По шаблону %s ничего не найдено", pat), Summary: "не найдено"}, nil
	}
	sort.Strings(out)
	return Result{
		Text:    fmt.Sprintf("Найдено %d файлов по шаблону %s:\n%s", len(out), pat, strings.Join(out, "\n")),
		Summary: fmt.Sprintf("%d файлов", len(out)),
	}, nil
}

// hGrep — поиск по регулярному выражению.
func (r *Registry) hGrep(_ context.Context, m map[string]any) (Result, error) {
	pat := ArgStr(m, "pattern")
	if pat == "" {
		return Result{}, fmt.Errorf("укажи pattern")
	}
	re, err := compileRegexp(pat)
	if err != nil {
		return Result{}, fmt.Errorf("плохое регулярное выражение (Go/RE2): %v", err)
	}
	root := r.workDir
	if rp := ArgStr(m, "path"); rp != "" {
		root = r.resolvePath(rp)
	}
	incl := ArgStr(m, "include")
	var inclRe *regexp.Regexp
	if incl != "" {
		inclRe, err = compileGlob(incl)
		if err != nil {
			return Result{}, fmt.Errorf("плохой include-шаблон: %v", err)
		}
	}
	maxRes := core.Clamp(ArgInt(m, "max_results", 50), 1, 500)

	var out []string
	nfiles := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if len(out) >= maxRes {
			return errStopWalk
		}
		if d.IsDir() {
			if core.ShouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if inclRe != nil && !inclRe.MatchString(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > 2<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || core.IsBinary(data) {
			return nil
		}
		nfiles++
		rel := core.RelToWD(r.workDir, path)
		for i, line := range core.SplitLines(string(data)) {
			if re.MatchString(line) {
				l := line
				if len([]rune(l)) > 240 {
					l = string([]rune(l)[:240]) + "…"
				}
				out = append(out, fmt.Sprintf("%s:%d: %s", rel, i+1, l))
				if len(out) >= maxRes {
					return errStopWalk
				}
			}
		}
		return nil
	})
	if len(out) == 0 {
		return Result{Text: fmt.Sprintf("Совпадений нет (паттерн: %s)", pat), Summary: "совпадений нет"}, nil
	}
	trunc := ""
	if len(out) >= maxRes {
		trunc = fmt.Sprintf(" (показаны первые %d)", maxRes)
	}
	return Result{
		Text:    fmt.Sprintf("Найдено %d совпадений в %d файлах%s:\n%s", len(out), nfiles, trunc, strings.Join(out, "\n")),
		Summary: fmt.Sprintf("%d совпадений", len(out)),
	}, nil
}

func isAbs(p string) bool {
	return strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':')
}
