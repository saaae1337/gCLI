package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"gcli/core"
)

// ---------- Пакетный (multi) режим ----------
//
// Симптом, из-за которого всё это затевалось. Модель возвращает сразу
// десяток tool_calls («прочитай вот эти пять файлов», «поправь вот эти три»),
// и агентный цикл выполняет их по одному: read_file 200 мс × 5 = секунда
// ожидания на ровном месте, причём всё это время модель простаивает, а
// пользователь смотрит на «thinking...».
//
// Отдельный инструмент на пачку решает это лучше, чем флаг параллельности
// у каждого: (1) одна операция — одна карточка в UI вместо десятка, (2) один
// сжатый результат в контексте вместо десяти отдельных сообщений, (3) можно
// общий бюджет символов на всю пачку, а не на каждый файл.
//
// Важно, чего здесь нет: гонки за общие структуры. Каждая цель — отдельный
// вызов существующего обработчика (r.hReadFile и т.п.), поэтому семафор,
// карта прочитанных файлов и журнал правок работают ровно так же, как при
// последовательных вызовах.

// Схемы инструментов multi_*.
const (
	schemaMultiRead = `{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"},"description":"Пути к файлам (до 20). Читаются параллельно, один вызов вместо N."},"offset":{"type":"integer","description":"Начальная строка (1-based) для всех файлов"},"limit":{"type":"integer","description":"Максимум строк на файл (по умолчанию 800; на всю пачку действует общий бюджет)"},"max_chars":{"type":"integer","description":"Общий бюджет символов на всю пачку (по умолчанию 40000)"}},"required":["paths"]}`

	schemaMultiEdit = `{"type":"object","properties":{"edits":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_string","new_string"]},"description":"Список правок (до 20). Применяются параллельно к РАЗНЫМ файлам; один файл — один элемент, иначе гонка."},"atomic":{"type":"boolean","description":"true — при первой ошибке не применять остальные (всё или ничего)"}},"required":["edits"]}`

	schemaMultiGrep = `{"type":"object","properties":{"pattern":{"type":"string","description":"Регулярное выражение (Go/RE2). Одно на все каталоги."},"paths":{"type":"array","items":{"type":"string"},"description":"Каталоги поиска (до 20). Если пусто — рабочий каталог"},"include":{"type":"string","description":"Фильтр имён файлов, например *.go"},"max_results":{"type":"integer","description":"Лимит совпадений на один каталог (по умолчанию 50)"},"max_total":{"type":"integer","description":"Общий лимит совпадений на всю пачку (по умолчанию 150)"}},"required":["pattern"]}`

	schemaMultiBash = `{"type":"object","properties":{"commands":{"type":"array","items":{"type":"string"},"description":"Команды (до 10). Выполняются параллельно, каждая — отдельный вызов вместо N."},"timeout_sec":{"type":"integer","description":"Таймаут на каждую команду в секундах (5-300, по умолчанию 120)"},"workdir":{"type":"string","description":"Рабочий каталог всех команд (по умолчанию текущий)"},"max_output":{"type":"integer","description":"Сколько строк вывода показать на команду (по умолчанию 200)"}},"required":["commands"]}`
)

// Лимиты пакетного режима. Задуманы как защита от двух разных бед:
// раздутия контекста модели и лавины подтверждений пользователю.
const (
	multiMaxTargets  = 20 // максимум целей в одной пачке
	multiMaxCommands = 10 // максимум команд в multi_bash
	// multiDefaultPar — параллелизм по умолчанию. Ниже, чем лимит целей:
	// десяток одновременных go test / go build выест ядра и провалится по
	// таймауту, а толку не прибавит.
	multiDefaultPar = 4
	// multiMaxPar — потолок параллелизма (аргумент parallel).
	multiMaxPar = 8
	// multiReadBudget — общий бюджет символов на пачку чтения.
	multiReadBudget = 40000
	// multiPerFileLimit — строк на файл в multi_read по умолчанию: меньше,
	// чем у одиночного read_file, потому что в пачке файлов больше.
	multiPerFileLimit = 800
)

// target — одна цель пакетной операции.
type target struct {
	label string
	run   func(ctx context.Context) (Result, error)
}

// runBatch — выполнить цели параллельно и собрать результат в порядке входа.
//
// Порядок входа, а не порядок завершения: модель читает отчёт сверху вниз и
// ожидает увидеть файлы в том порядке, в котором попросила. Если бы порядок
// определялся скоростью, одинаковый набор давал бы разный текст каждый раз,
// и сравнивать результаты двух ходов было бы невозможно.
func (r *Registry) runBatch(ctx context.Context, title string, par int, targets []target, render func([]batchItem) (string, string)) (Result, error) {
	items, err := r.runTargets(ctx, title, par, targets)
	if err != nil {
		return Result{}, err
	}
	text, summary := render(items)
	r.progress(ProgressEvent{
		Title: title,
		Label: summary,
		Done:  len(targets),
		Total: len(targets),
		Ok:    true,
		Final: true,
	})
	return Result{Text: text, Summary: summary}, nil
}

// runTargets — выполнить цели параллельно и отдать сырые результаты.
//
// Вынесено из runBatch отдельно от рендера и финального события прогресса:
// пачке с зависимостями нужно выполнить волну, посмотреть на её результаты
// (от них зависят выводы следующих субагентов и отчёт о пропусках) и лишь
// потом решать, что показывать. Свой параллелизм для волн заводить нельзя —
// это был бы второй семафор, второй счётчик прогресса и вторая защита от
// паники на один и тот же код.
func (r *Registry) runTargets(ctx context.Context, title string, par int, targets []target) ([]batchItem, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("пустая пачка: нечего выполнять")
	}
	par = core.Clamp(par, 1, multiMaxPar)

	items := make([]batchItem, len(targets))
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	// Счётчик готовности — отдельный, а не пересчёт по items: элементы
	// общего среза пишутся из разных горутий, и пересчёт давал бы и гонку,
	// и повторяющиеся номера «2/7» вместо честной последовательности.
	var done atomic.Int32
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			items[i].label = targets[i].label
			res, err := runSafe(targets[i].run, ctx)
			if err != nil {
				items[i].err = err
			} else {
				items[i].res = res
				items[i].warn = res.Error
			}
			r.progress(ProgressEvent{
				Title: title,
				Label: targets[i].label,
				Done:  int(done.Add(1)),
				Total: len(targets),
				Ok:    err == nil && res.Error == "",
				Err:   errText(res.Error, err),
			})
		}(i)
	}
	wg.Wait()
	return items, nil
}

// batchItem — результат одной цели.
type batchItem struct {
	label string
	res   Result
	err   error
	warn  string
}

// Failed — цель не выполнена.
func (it batchItem) Failed() bool { return it.err != nil }

func errText(warn string, err error) string {
	if err != nil {
		return err.Error()
	}
	return warn
}

// runSafe — вызов цели с защитой от паники.
//
// Один паникнувший файл не должен уронить весь ход агента: цель вернёт
// ошибку, остальные доработают. Это ровно то, за что платит паника без
// защиты — весь агент останавливается, а пользователь видит стек-трейс.
func runSafe(fn func(ctx context.Context) (Result, error), ctx context.Context) (res Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("внутренняя ошибка: %v", p)
		}
	}()
	return fn(ctx)
}

// countDone — сколько целей отдало результат (только для тестов/отладки:
// в горутинях используется атомарный счётчик runBatch).
func countDone(items []batchItem) int {
	n := 0
	for i := range items {
		if items[i].res.Text != "" || items[i].err != nil || items[i].warn != "" {
			n++
		}
	}
	return n
}

// progress — отдать событие прогресса подписчику (nil-безопасно).
func (r *Registry) progress(ev ProgressEvent) {
	if r == nil || r.env.OnProgress == nil {
		return
	}
	r.env.OnProgress(ev)
}

// parallelism — сколько целей выполнять одновременно.
func (r *Registry) parallelism(m map[string]any) int {
	if ArgBool(m, "sequential") {
		return 1
	}
	return core.Clamp(ArgInt(m, "parallel", multiDefaultPar), 1, multiMaxPar)
}

// ---------- multi_read ----------

// hMultiRead — прочитать пачку файлов параллельно.
func (r *Registry) hMultiRead(ctx context.Context, m map[string]any) (Result, error) {
	paths := ArgStrSlice(m, "paths")
	if len(paths) == 0 {
		// Модель часто передаёт одиночный path вместо массива. Это не ошибка
		// повода отказывать: один файл — вырожденная пачка.
		if p := ArgStr(m, "path"); p != "" {
			paths = []string{p}
		}
	}
	if len(paths) == 0 {
		return Result{}, fmt.Errorf("укажи paths — массив путей (до %d)", multiMaxTargets)
	}
	paths = dedupePaths(paths)
	if len(paths) > multiMaxTargets {
		return Result{}, fmt.Errorf("слишком много целей: %d, максимум %d за вызов — разбей на два вызова",
			len(paths), multiMaxTargets)
	}

	lim := ArgInt(m, "limit", multiPerFileLimit)
	if lim < 1 || lim > 5000 {
		lim = multiPerFileLimit
	}
	off := core.Max(1, ArgInt(m, "offset", 1))
	budget := core.Clamp(ArgInt(m, "max_chars", multiReadBudget), 2000, 200000)

	targets := make([]target, 0, len(paths))
	for _, p := range paths {
		p := p
		// Один вызов на файл: обработчик делает resolvePath, проверку на
		// двоичный файл и отметку «прочитан» — всё это нужно и в пачке.
		targets = append(targets, target{
			label: p,
			run: func(ctx context.Context) (Result, error) {
				return r.hReadFile(ctx, map[string]any{
					"path":   p,
					"offset": off,
					"limit":  lim,
				})
			},
		})
	}

	return r.runBatch(ctx, "multi_read", r.parallelism(m), targets,
		func(items []batchItem) (string, string) {
			return renderReadBatch(items, budget, r.workDir)
		})
}

// renderReadBatch — собрать текст пачки чтения в рамках общего бюджета.
//
// Бюджет делится по остатку: первые файлы получают полную квоту, и когда
// символы кончаются, остальные получают усечённый вид с явной пометкой.
// Честная асимметрия лучше молчаливого обрезания — по усечённому блоку
// видно, что файл есть и что его надо дочитать отдельно.
func renderReadBatch(items []batchItem, budget int, workDir string) (string, string) {
	var b strings.Builder
	ok, failed := 0, 0
	spent := 0
	truncated := 0

	b.WriteString(fmt.Sprintf("# multi_read: %d файлов\n\n", len(items)))
	for _, it := range items {
		switch {
		case it.Failed():
			failed++
			fmt.Fprintf(&b, "## %s\nОШИБКА: %v\n\n", it.label, it.err)
		case strings.TrimSpace(it.warn) != "":
			failed++
			fmt.Fprintf(&b, "## %s\n%v\n\n", it.label, it.warn)
		default:
			ok++
			body := it.res.Text
			rest := budget - spent
			if rest < 400 {
				truncated++
				fmt.Fprintf(&b, "## %s\n[не показан: исчерпан общий бюджет %d символов — прочитай этот файл отдельно]\n\n",
					core.RelToWD(workDir, it.label), budget)
				continue
			}
			if len([]rune(body)) > rest {
				body = clipKeepTail(body, rest)
				truncated++
				fmt.Fprintf(&b, "## %s\n%s\n[файл обрезан по общему бюджету]\n\n", core.RelToWD(workDir, it.label), body)
			} else {
				fmt.Fprintf(&b, "## %s\n%s\n\n", core.RelToWD(workDir, it.label), body)
			}
			spent += len([]rune(body))
		}
	}
	summary := fmt.Sprintf("%d прочитано, %d с ошибкой", ok, failed)
	if truncated > 0 {
		summary += fmt.Sprintf(", %d усечено", truncated)
	}
	return strings.TrimRight(b.String(), "\n"), summary
}

// clipKeepTail — обрезать текст до n символов, сохранив начало и конец.
//
// Хвост важнее середины почти всегда: в файле конец содержит сигнатуры,
// закрывающие скобки и итог, по которым модель понимает файл целиком.
func clipKeepTail(s string, n int) string {
	rn := []rune(s)
	if len(rn) <= n {
		return s
	}
	head := n / 2
	tail := n - head
	return string(rn[:head]) +
		fmt.Sprintf("\n…[обрезано: пропущено %d символов]…\n", len(rn)-head-tail) +
		string(rn[len(rn)-tail:])
}

// ---------- multi_edit ----------

// multiEdit — одна правка в аргументах multi_edit.
type multiEdit struct {
	Path    string
	Old     string
	New     string
	All     bool
	Ordinal int
}

// hMultiEdit — применить пачку правок параллельно.
func (r *Registry) hMultiEdit(ctx context.Context, m map[string]any) (Result, error) {
	raw, _ := m["edits"].([]any)
	if len(raw) == 0 {
		return Result{}, fmt.Errorf("укажи edits — массив правок (до %d)", multiMaxTargets)
	}
	if r.env.ReadOnly {
		return Result{Error: "режим «только чтение»: изменение файлов запрещено"}, nil
	}

	var edits []multiEdit
	seen := map[string]bool{}
	for i, v := range raw {
		em, ok := v.(map[string]any)
		if !ok {
			return Result{}, fmt.Errorf("edits[%d]: ожидался объект {path, old_string, new_string}", i)
		}
		e := multiEdit{
			Path:    ArgStr(em, "path"),
			Old:     ArgStr(em, "old_string"),
			New:     ArgStr(em, "new_string"),
			All:     ArgBool(em, "replace_all"),
			Ordinal: i,
		}
		if e.Path == "" || e.Old == "" {
			return Result{}, fmt.Errorf("edits[%d]: нужны path и old_string", i)
		}
		// Один файл — одна правка. Две параллельные правки одного файла
		// читали бы разные версии содержимого и одна молча затирала бы
		// другую: обе отметили бы успех, а в файле осталось бы первое.
		key := r.resolvePath(e.Path)
		if seen[key] {
			return Result{}, fmt.Errorf("файл %s встречается в edits дважды — объедини правки в одну: "+
				"параллельные правки одного файла конфликтуют", core.RelToWD(r.workDir, key))
		}
		seen[key] = true
		edits = append(edits, e)
	}
	if len(edits) > multiMaxTargets {
		return Result{}, fmt.Errorf("слишком много правок: %d, максимум %d", len(edits), multiMaxTargets)
	}

	atomic := ArgBool(m, "atomic")
	targets := make([]target, 0, len(edits))
	for _, e := range edits {
		e := e
		targets = append(targets, target{
			label: e.Path,
			run: func(ctx context.Context) (Result, error) {
				return r.editFileLabeled(map[string]any{
					"path":        e.Path,
					"old_string":  e.Old,
					"new_string":  e.New,
					"replace_all": e.All,
				}, "multi_edit")
			},
		})
	}

	// atomic требует другой раскладки: сначала ВСЕ проверки, потом запись.
	// Иначе «всё или ничего» не выполняется — половина файлов уже изменена.
	if atomic {
		return r.runAtomicEdits(ctx, edits, r.parallelism(m))
	}
	return r.runBatch(ctx, "multi_edit", r.parallelism(m), targets,
		func(items []batchItem) (string, string) {
			return renderEditBatch(items)
		})
}

// runAtomicEdits — применить правки «всё или ничего».
//
// Порядок: сначала все правки проверяются и готовятся в памяти, и только
// потом пишутся. Это единственный способ честно получить атомарность на
// уровне файловой системы: до первой записи откат — это «не делать ничего».
func (r *Registry) runAtomicEdits(ctx context.Context, edits []multiEdit, par int) (Result, error) {
	type prepared struct {
		e     multiEdit
		path  string
		old   string
		new   string
		exist bool
	}
	prep := make([]prepared, len(edits))
	var mu sync.Mutex
	var bad error
	// failed — флаг под мьютексом. Проверка «уже есть ошибка» тоже читает bad,
	// и без той же синхронизации это была бы гонка чтения с записью.
	failed := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return bad != nil
	}
	// fail записать — только под мьютексом, первая ошибка побеждает: она
	// же станет причиной отказа всей atomic-пачки.
	fail := func(e error) {
		mu.Lock()
		if bad == nil {
			bad = e
		}
		mu.Unlock()
	}

	par = core.Clamp(par, 1, multiMaxPar)
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for i := range edits {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil || failed() {
				return
			}
			// Путь — через pathArg, а не resolvePath: песочница обязана
			// проверить и цели atomic-пачки, иначе atomic:true стал бы
			// обходом песочницы «в один вызов».
			abs, err := r.pathArg(edits[i].Path)
			if err != nil {
				fail(fmt.Errorf("%s: %v", edits[i].Path, err))
				return
			}
			// «Сначала прочитай, потом правь» — то же правило, что у
			// edit_file: без него atomic:true был бы лазейкой, где
			// модель правит файл, который ни разу не открывала.
			if !wasRead(r.env.ReadFiles, abs) {
				fail(fmt.Errorf("%s: файл не прочитан — сначала read_file",
					core.RelToWD(r.workDir, abs)))
				return
			}
			p := prepared{e: edits[i], path: abs}
			cur, err := os.ReadFile(p.path)
			if err != nil {
				fail(fmt.Errorf("%s: %v", core.RelToWD(r.workDir, p.path), err))
				return
			}
			p.exist, p.old = true, string(cur)
			cnt := strings.Count(p.old, edits[i].Old)
			if cnt == 0 {
				fail(fmt.Errorf("%s: old_string не найден", core.RelToWD(r.workDir, p.path)))
				return
			}
			if cnt > 1 && !edits[i].All {
				fail(fmt.Errorf("%s: вхождений %d — нужен replace_all", core.RelToWD(r.workDir, p.path), cnt))
				return
			}
			if edits[i].All {
				p.new = strings.ReplaceAll(p.old, edits[i].Old, edits[i].New)
			} else {
				p.new = strings.Replace(p.old, edits[i].Old, edits[i].New, 1)
			}
			mu.Lock()
			prep[i] = p
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	mu.Lock()
	failure := bad
	mu.Unlock()
	if failure != nil {
		r.progress(ProgressEvent{Title: "multi_edit", Label: "откат: " + failure.Error(),
			Done: len(edits), Total: len(edits), Ok: false, Err: failure.Error(), Final: true})
		return Result{
			Text:    fmt.Sprintf("Правки не применены (atomic): %v.\nНи один файл не изменён.", failure),
			Summary: "atomic: отказ, ничего не изменено",
		}, nil
	}

	// Подтверждение — одно на всю пачку, а не на каждый файл: десять
	// последовательных вопросов «применить?» превращают любую правку в
	// макрос, который пользователь всё равно подтвердит не глядя.
	if r.env.Confirm != nil {
		var reqs []ConfirmReq
		for _, p := range prep {
			if p.path == "" {
				continue
			}
			reqs = append(reqs, ConfirmReq{
				Kind:   ConfirmWrite,
				Path:   p.path,
				Old:    p.old,
				New:    p.new,
				Detail: fmt.Sprintf("atomic-пачка из %d правок", len(prep)),
			})
		}
		if !r.confirmBatch(reqs) {
			return Result{Text: "Пакетная правка отменена пользователем", Summary: "отменено"}, nil
		}
	}

	var b strings.Builder
	applied := 0
	// Откат. atomic обещает «всё или ничего», а пока файлы пишутся по одному,
	// сбой на пятом (диск полон, файл заблокирован антивирусом) оставлял бы
	// первые четыре изменёнными — то есть обещание было бы ложью ровно в том
	// случае, когда пользователю важнее всего. Поэтому запоминаем применённое
	// и при первой неудаче возвращаем всё на место.
	//
	// Ошибки восстановления НЕ глотаем: раньше os.Remove и WriteAtomic
	// игнорировались, а отчёт затем безусловно рапортовал «файлы вернулись в
	// исходное состояние». Пользователь после такого оставался с частично
	// изменённым проектом, зная об обратном.
	var done []prepared
	// rollbackOk — довёл ли откат до конца. Иначе обещание «всё или ничего»
	// в отчёте было бы ложью ровно в том случае, когда пользователю нужнее
	// всего.
	rollbackOk := true
	rollback := func(reason error) {
		var failed []string
		for i := len(done) - 1; i >= 0; i-- {
			p := done[i]
			if !p.exist {
				if err := os.Remove(p.path); err != nil && !os.IsNotExist(err) {
					failed = append(failed, fmt.Sprintf("%s: %v", core.RelToWD(r.workDir, p.path), err))
				}
				continue
			}
			if err := core.WriteAtomic(p.path, []byte(p.old), 0o644); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", core.RelToWD(r.workDir, p.path), err))
			}
		}
		rollbackOk = len(failed) == 0
		// Журнал правок тоже врёт после отката: он уже нарисовал изменения,
		// которых не осталось. Чистим записи применённых файлов — но только
		// при успешном откате, иначе /undo «отменит» правку, которой на
		// диске уже нет.
		if rollbackOk {
			for _, p := range done {
				r.dropChange(p.path)
			}
		}
		fmt.Fprintf(&b, "- ОТКАТ: %s (изменено обратно файлов: %d из %d)\n", reason, len(done)-len(failed), len(done))
		if len(failed) > 0 {
			fmt.Fprintf(&b, "- ОТКАТ НЕ ЗАВЕРШЁН, эти файлы остались изменёнными — проверь их вручную:\n")
			for _, f := range failed {
				fmt.Fprintf(&b, "  - %s\n", f)
			}
		}
	}
	for i, p := range prep {
		if p.path == "" {
			continue
		}
		if ctx.Err() != nil {
			rollback(ctx.Err())
			break
		}
		r.record(p.path, "multi_edit")
		if err := core.WriteAtomic(p.path, []byte(p.new), 0o644); err != nil {
			fmt.Fprintf(&b, "- %s: ОШИБКА записи: %v\n", edits[i].Path, err)
			rollback(err)
			break
		}
		r.noteChange(p.path, "multi_edit", p.old, p.new, p.exist)
		done = append(done, p)
		applied++
		fmt.Fprintf(&b, "- %s: изменён\n", core.RelToWD(r.workDir, p.path))
	}
	if applied != len(edits) || len(done) != len(prep) {
		// Хотя бы одна запись не прошла — обещание «всё или ничего» нарушено,
		// отчёт обязан это сказать прямо. Откат при этом может быть неполным,
		// и об этом тоже: иначе пользователь поверит словам «изменений нет».
		head := "откат выполнен, файлы вернулись в исходное состояние"
		if !rollbackOk {
			head = "откат НЕ завершён: часть файлов осталась изменённой, см. список ниже"
		}
		return Result{
			Text:    fmt.Sprintf("# multi_edit (atomic)\n\nНЕ ПРИМЕНЕНО: %s\n%s", head, strings.TrimRight(b.String(), "\n")),
			Summary: "atomic: откат, изменений нет",
		}, nil
	}
	r.progress(ProgressEvent{Title: "multi_edit", Label: "atomic: применено",
		Done: len(edits), Total: len(edits), Ok: true, Final: true})
	return Result{
		Text:    fmt.Sprintf("# multi_edit (atomic)\n\nПрименено правок: %d из %d\n%s", applied, len(edits), strings.TrimRight(b.String(), "\n")),
		Summary: fmt.Sprintf("atomic: %d/%d применено", applied, len(edits)),
	}, nil
}

// confirmBatch — одно подтверждение на пачку правок.
func (r *Registry) confirmBatch(reqs []ConfirmReq) bool {
	if r.env.Confirm == nil || len(reqs) == 0 {
		return true
	}
	batch := ConfirmReq{
		Kind:   ConfirmWrite,
		Detail: fmt.Sprintf("пачка из %d правок: %s", len(reqs), pathsList(reqs)),
	}
	return r.env.Confirm(batch)
}

func pathsList(reqs []ConfirmReq) string {
	var names []string
	for _, q := range reqs {
		names = append(names, filepath.Base(q.Path))
	}
	return strings.Join(names, ", ")
}

// renderEditBatch — собрать отчёт по правкам.
func renderEditBatch(items []batchItem) (string, string) {
	var b strings.Builder
	ok, failed := 0, 0
	b.WriteString(fmt.Sprintf("# multi_edit: %d правок\n\n", len(items)))
	for _, it := range items {
		if it.Failed() {
			failed++
			fmt.Fprintf(&b, "- %s — ОШИБКА: %v\n", it.label, it.err)
			continue
		}
		if strings.TrimSpace(it.warn) != "" {
			failed++
			fmt.Fprintf(&b, "- %s — %s\n", it.label, it.warn)
			continue
		}
		ok++
		detail := it.res.Summary
		if detail == "" {
			detail = core.OneLine(it.res.Text)
		}
		fmt.Fprintf(&b, "- %s\n", detail)
	}
	return strings.TrimRight(b.String(), "\n"), fmt.Sprintf("%d применено, %d с ошибкой", ok, failed)
}

// ---------- multi_grep ----------

// hMultiGrep — один и тот же поиск по нескольким каталогам параллельно.
func (r *Registry) hMultiGrep(ctx context.Context, m map[string]any) (Result, error) {
	pat := ArgStr(m, "pattern")
	if pat == "" {
		return Result{}, fmt.Errorf("укажи pattern")
	}
	dirs := ArgStrSlice(m, "paths")
	if len(dirs) == 0 {
		if p := ArgStr(m, "path"); p != "" {
			dirs = []string{p}
		}
	}
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	dirs = dedupePaths(dirs)
	if len(dirs) > multiMaxTargets {
		return Result{}, fmt.Errorf("слишком много каталогов: %d, максимум %d", len(dirs), multiMaxTargets)
	}

	perDir := core.Clamp(ArgInt(m, "max_results", 50), 1, 500)
	total := core.Clamp(ArgInt(m, "max_total", 150), 1, 1000)

	targets := make([]target, 0, len(dirs))
	for _, d := range dirs {
		d := d
		targets = append(targets, target{
			label: d,
			run: func(ctx context.Context) (Result, error) {
				return r.hGrep(ctx, map[string]any{
					"pattern":     pat,
					"path":        d,
					"include":     ArgStr(m, "include"),
					"max_results": perDir,
				})
			},
		})
	}

	return r.runBatch(ctx, "multi_grep", r.parallelism(m), targets,
		func(items []batchItem) (string, string) {
			return renderGrepBatch(items, total, r.workDir)
		})
}

// renderGrepBatch — склеить совпадения из нескольких каталогов.
func renderGrepBatch(items []batchItem, total int, workDir string) (string, string) {
	var lines []string
	seen := map[string]bool{}
	dirs, errDirs := 0, 0
	for _, it := range items {
		if it.Failed() {
			errDirs++
			lines = append(lines, fmt.Sprintf("## %s — ОШИБКА: %v", it.label, it.err))
			continue
		}
		dirs++
		// hGrep отдаёт шапку вида «Найдено N совпадений в M файлах», а дальше
		// сами строки. Шапку в склейку не пускаем: слипшиеся заголовки
		// выглядят как совпадения, и модель считает их за находки.
		for _, l := range grepLines(it.res.Text) {
			if len(lines) >= total || seen[l] {
				continue
			}
			// Каталоги перекрываются (pkg и ./pkg дадут одно и то же), а
			// дубли совпадений модель принимает за разные находки.
			seen[l] = true
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return fmt.Sprintf("Совпадений нет (паттерн по %d каталогам)", dirs), "совпадений нет"
	}
	head := fmt.Sprintf("Совпадений: %d по паттерну, каталогов обработано: %d (ошибок: %d)\n",
		len(lines), dirs, errDirs)
	if len(lines) >= total {
		head = fmt.Sprintf("Совпадений: %d (показан потолок %d), каталогов: %d (ошибок: %d)\n",
			len(lines), total, dirs, errDirs)
	}
	return head + strings.Join(lines, "\n"),
		fmt.Sprintf("%d совпадений по %d каталогам", len(lines), dirs)
}

// grepLines — только строки совпадений, без шапки.
func grepLines(text string) []string {
	var out []string
	for _, l := range core.SplitLines(text) {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if l == "Совпадений нет (паттерн: …)" || strings.HasPrefix(l, "Совпадений нет") {
			continue
		}
		if strings.HasPrefix(l, "Найдено ") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// ---------- multi_bash ----------

// hMultiBash — выполнить несколько команд параллельно.
//
// Требует подтверждения пользователя на каждую команду: пачка команд не
// становится откровенно безопаснее только потому, что их много, а молчаливое
// выполнение shell без вопроса — ровно то, чего пользователь не ожидает.
func (r *Registry) hMultiBash(ctx context.Context, m map[string]any) (Result, error) {
	cmds := ArgStrSlice(m, "commands")
	if len(cmds) == 0 {
		if c := ArgStr(m, "command"); c != "" {
			cmds = []string{c}
		}
	}
	var list []string
	for _, c := range cmds {
		if strings.TrimSpace(c) != "" {
			list = append(list, c)
		}
	}
	if len(list) == 0 {
		return Result{}, fmt.Errorf("укажи commands — массив команд (до %d)", multiMaxCommands)
	}
	list = dedupeStrings(list)
	if len(list) > multiMaxCommands {
		return Result{}, fmt.Errorf("слишком много команд: %d, максимум %d", len(list), multiMaxCommands)
	}
	timeout := core.Clamp(ArgInt(m, "timeout_sec", 120), 5, 600)
	// Минимум 5, а не 10: полезный вызов — «покажи мне первые строки
	// этой длинной команды», и запрет на это заставлял бы модель
	// выкручивать max_output вместо нормального усечения.
	maxOut := core.Clamp(ArgInt(m, "max_output", 200), 5, 2000)

	targets := make([]target, 0, len(list))
	for _, c := range list {
		c := c
		args := map[string]any{"command": c, "timeout_sec": timeout}
		if wd := ArgStr(m, "workdir"); wd != "" {
			args["workdir"] = wd
		}
		targets = append(targets, target{
			label: core.OneLine(c),
			run: func(ctx context.Context) (Result, error) {
				res, err := r.hBash(ctx, args)
				if err != nil {
					return res, err
				}
				// Код возврата — часть результата, а не украшение: без него
				// модель читает пустой вывод как успех. hBash дописывает
				// метку только при ненулевом коде, поэтому нулевой добавляем
				// здесь — иначе в сводке он неотличим от неизвестного.
				//
				// Ошибку в Result.Error здесь НЕ ставим: тогда рендерер напечатал
				// бы только «ненулевой код» и выбросил сам stderr, который как
				// раз и объясняет падение. Разбираем код при сборке отчёта.
				if !strings.Contains(res.Text, "[код выхода:") && !strings.Contains(res.Text, "Таймаут") {
					res.Text += "\n[код выхода: 0]"
				}
				return res, nil
			},
		})
	}

	return r.runBatch(ctx, "multi_bash", r.parallelism(m), targets,
		func(items []batchItem) (string, string) {
			return renderBashBatch(items, maxOut)
		})
}

// renderBashBatch — собрать вывод команд.
//
// Ненулевой код выхода считается провалом: hBash отдаёт его текстом, и если
// не разобрать метку, пачка рапортует «3 успешно» про две упавшие команды —
// а это ровно тот вывод, на котором модель потом строит неверный вывод.
func renderBashBatch(items []batchItem, maxOut int) (string, string) {
	var b strings.Builder
	ok, failed := 0, 0
	for i, it := range items {
		n := i + 1
		b.WriteString(fmt.Sprintf("## [%d] %s\n", n, it.label))
		switch {
		case it.Failed():
			failed++
			fmt.Fprintf(&b, "ОШИБКА: %v\n\n", it.err)
		case strings.TrimSpace(it.warn) != "":
			failed++
			fmt.Fprintf(&b, "%s\n\n", it.warn)
		case exitCodeFrom(it.res.Text) > 0:
			// Вывод печатаем целиком (кроме усечения): именно stderr
			// объясняет, почему код ненулевой. Бросать его ценой одного
			// абзаца ошибки — значит отдать модели «упало» без причины.
			failed++
			printBashOut(&b, it.res.Text, maxOut)
		case strings.Contains(it.res.Text, "Таймаут"):
			failed++
			printBashOut(&b, it.res.Text, maxOut)
		default:
			ok++
			printBashOut(&b, it.res.Text, maxOut)
		}
	}
	sum := fmt.Sprintf("%d успешно, %d с ошибкой", ok, failed)
	if failed > 0 {
		// Разделяем два разных провала: команда не запустилась (ошибка
		// инструмента) и команда отработала, но с ненулевым кодом.
		sum = fmt.Sprintf("%d успешно, %d с ошибкой или ненулевым кодом", ok, failed)
	}
	return strings.TrimRight(b.String(), "\n"), sum
}

// printBashOut — напечатать вывод команды с усечением по числу строк.
//
// Усечение применяется к полезному выводу, а не к служебным строкам:
// метка «[код выхода: N]» и пустой хвост не должны попадать в счётчик
// «ещё N строк», иначе модель видит цифру, не совпадающую с тем, что
// она получила по-настоящему.
//
// Строки режутся ДО подсчёта остатка, иначе усечение считает уже
// обрезанный срез и остаток всегда выходит неверным.
func printBashOut(b *strings.Builder, text string, maxOut int) {
	var body, meta []string
	for _, l := range core.SplitLines(text) {
		if strings.HasPrefix(strings.TrimSpace(l), "[код выхода:") {
			meta = append(meta, l)
			continue
		}
		body = append(body, l)
	}
	// Пустой хвост от завершающего перевода строки — не строка вывода.
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	hidden := 0
	if len(body) > maxOut {
		hidden = len(body) - maxOut
		body = body[:maxOut]
	}
	out := body
	if hidden > 0 {
		out = append(out, fmt.Sprintf("…[ещё %d строк]", hidden))
	}
	out = append(out, meta...)
	fmt.Fprintf(b, "%s\n\n", strings.Join(out, "\n"))
}

// exitCodeFrom — достать код выхода из текста команды, -1 если неизвестен.
func exitCodeFrom(text string) int {
	i := strings.LastIndex(text, "[код выхода:")
	if i < 0 {
		return -1
	}
	rest := text[i+len("[код выхода:"):]
	j := strings.Index(rest, "]")
	if j < 0 {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:j]))
	if err != nil {
		return -1
	}
	return n
}

// ---------- Утилиты ----------

// dedupeStrings — убрать повторы, сохранив порядок.
//
// Повторная цель в пачке — не ошибка модели, а обычная небрежность, но
// платить за неё придётся двойным временем выполнения и двумя одинаковыми
// блоками в отчёте.
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// dedupePaths — то же, но с нормализацией: разделители, «./» в начале,
// пробелы и регистр буквы диска.
//
// «one.txt» и «./one.txt» — один и тот же файл. Если считать их разными,
// модель получает два одинаковых блока и тратит на это общий бюджет
// символов — а по лимиту 20 целей ещё и упирается в отказ.
func dedupePaths(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// Замена разделителя явная, а не filepath.ToSlash: на Linux backslash —
		// законный символ имени, и «src\a.go» жил бы как отдельный файл,
		// ломая дедупликацию в зависимости от ОС сборки.
		key := strings.ReplaceAll(s, `\`, "/")
		key = strings.TrimPrefix(key, "./")
		key = strings.TrimSuffix(key, "/")
		// Регистр сворачиваем только там, где он не различает файлы:
		// на Windows — весь путь (NTFS нечувствителен к регистру), на
		// остальных системах — только букву диска. Глобальный ToLower
		// на Linux склеивал Readme.md и readme.md в одну цель, и
		// вторая молча выпадала из пачки.
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		} else if len(key) >= 2 && key[1] == ':' && isDriveLetter(key[0]) {
			key = strings.ToLower(key[:1]) + key[1:]
		}
		if key == "" {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}
