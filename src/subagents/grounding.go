package subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ---------- Заземление отчётов на факты ----------
//
// Проблема, ради которой всё это затевалось: субагенты работают «50/50».
// Разбирая причины, видно две разные болезни, и лечатся они по-разному.
//
// Первая — галлюцинации. Отчёт субагента это свободный текст, и единственный
// способ узнать, правда ли там что-то, — поверить модели на слово. Просьба в
// промпте «каждый факт с ссылкой файл:строка» не работает: модель отлично
// знает, как выглядит правдоподобная ссылка, и сочиняет её, даже не открывая
// файл. AssessReport проверяет форму отчёта (пустой ли он, не обрезан ли), но
// не содержимое.
//
// Вторая — бесполезная работа. Даже с честным отчётом субагент может потратить
// двадцать ходов и вернуть пересказ, ради которого главному агенту дешевле
// прочитать три файла самому.
//
// Лечится это тем, что субагент перестаёт быть «ещё одним промптом» и
// становится задачей с доказуемым результатом. Инструменты, которые он
// вызывает, и есть его доказательства: если агент не читал pool.go, он не мог
// знать, что написал в нём 440 строк.
//
// Важно, что доказательства берутся не из аргументов вызовов, а из ТЕКСТА
// результатов, и это принципиально:
//
//   - grep возвращает строки вида «путь:строка: текст» — значит известны
//     точные номера строк, где что-то нашлось;
//   - read_file возвращает хвост вида «[показано строк: 40 начиная с 120]» или
//     «[файл закончился на строке 440]» — значит известен ТОЧНЫЙ диапазон
//     прочитанного, а не «файл открывался».
//
// Поэтому ссылка «pool.go:42» проверяется честно: строка 42 вне диапазона
// 120–160, который субагент реально читал, — значит он её не видел, и ссылка
// сочинена. Проверка детерминированная, это обычный Go-код: модель не может
// «договориться» с валидатором.

// EvidenceKind — откуда взялось доказательство.
type EvidenceKind string

const (
	// EvFile — файл прочитан целиком или частями (read_file, multi_read, inspect).
	EvFile EvidenceKind = "file"
	// EvGrep — по содержимому искали (grep, multi_grep).
	EvGrep EvidenceKind = "grep"
	// EvList — просматривали каталог (list_dir, glob).
	EvList EvidenceKind = "list"
	// EvExec — выполнялась команда (bash, multi_bash).
	EvExec EvidenceKind = "exec"
	// EvWeb — загружали страницу (web_fetch).
	EvWeb EvidenceKind = "web"
	// EvWrite — файл изменён (write_file, edit_file).
	EvWrite EvidenceKind = "write"
)

// Evidence — одно доказательство: что субагент реально делал.
type Evidence struct {
	Kind EvidenceKind
	Path string // путь к файлу
	From int    // начало прочитанного диапазона строк (EvFile)
	To   int    // конец диапазона
	Hits int    // сколько совпадений нашло grep (EvGrep)
	Pat  string // что искали (EvGrep)
	Cmd  string // команда (EvExec)
	OK   bool   // инструмент отработал без ошибки
	URL  string // адрес страницы (EvWeb)
}

// lineRange — включительный диапазон прочитанных строк.
type lineRange struct{ from, to int }

// fileRec — что известно об одном файле.
type fileRec struct {
	// full — файл прочитан целиком (инструмент сообщил про EOF).
	full   bool
	ranges []lineRange  // отсортированные, непересекающиеся
	hits   map[int]bool // строки, найденные grep
	total  int          // последняя известная длина файла
}

// covers — попадает ли строка в зону, которую субагент видел.
func (f *fileRec) covers(line int) bool {
	if f == nil || line <= 0 {
		return false
	}
	if f.hits[line] {
		return true
	}
	// Файл прочитан целиком, значит известна и его длина. Ссылка за последнюю
	// строку — такая же выдумка, как ссылка в непрочитанный кусок: строки с
	// таким номером в файле нет. total == 0 означает, что длина неизвестна
	// (хвост чтения не распознан) — тогда не придираемся.
	if f.full {
		return f.total <= 0 || line <= f.total
	}
	// Диапазоны отсортированы и слиты, поэтому достаточно одного прохода.
	lo, hi := 0, len(f.ranges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case line < f.ranges[mid].from:
			hi = mid - 1
		case line > f.ranges[mid].to:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// addRange — добавить диапазон с последующим слиянием пересечений.
func (f *fileRec) addRange(from, to int) {
	if from <= 0 {
		return
	}
	if to < from {
		to = from
	}
	f.ranges = append(f.ranges, lineRange{from, to})
	if to > f.total {
		f.total = to
	}
	f.merge()
}

// merge — слить пересекающиеся и соседние диапазоны.
//
// Без слияния пятнадцать чтений одного файла подряд дадут пятнадцать записей,
// и проверка каждой ссылки станет линейной по истории чтения. Со слитыми
// диапазонами их всегда немного, а проверка — двоичным поиском.
func (f *fileRec) merge() {
	if len(f.ranges) < 2 {
		return
	}
	sort.Slice(f.ranges, func(i, j int) bool {
		if f.ranges[i].from != f.ranges[j].from {
			return f.ranges[i].from < f.ranges[j].from
		}
		return f.ranges[i].to < f.ranges[j].to
	})
	out := f.ranges[:1]
	for _, r := range f.ranges[1:] {
		last := &out[len(out)-1]
		if r.from <= last.to+1 { // соседние тоже сливаем: 1-10 и 11-20 — это 1-20
			if r.to > last.to {
				last.to = r.to
			}
			continue
		}
		out = append(out, r)
	}
	f.ranges = out
}

// Grounding — журнал доказательств одного запуска субагента.
//
// Потокобезопасен: пишет хук OnToolDone из горутины агентного цикла, читает
// проверка отчёта из той же горутины, UI может читать параллельно.
type Grounding struct {
	mu      sync.Mutex
	files   map[string]*fileRec // ключ — нормализованный путь
	items   []Evidence
	workDir string

	// Потолки. Они не влияют на вердикт, а только ограничивают размер
	// приложения к отчёту: субагент с двумястами вызовами не должен
	// превращать отчёт в протокол.
	maxFiles, maxItems, maxHits int
}

// Лимиты журнала доказательств.
const (
	groundMaxFiles = 250
	groundMaxItems = 400
	// groundMaxHits — потолок по числу строк, запомненных из grep. Их может
	// быть тысячи, а нужны они только для подтверждения ссылок отчёта.
	groundMaxHits = 4000
)

// NewGrounding — новый журнал доказательств.
// workDir — корень проекта: без него нельзя отличить «файла нет» от «файл
// есть, но субагент его не открывал».
func NewGrounding(workDir string) *Grounding {
	return &Grounding{
		files:    map[string]*fileRec{},
		workDir:  workDir,
		maxFiles: groundMaxFiles,
		maxItems: groundMaxItems,
		maxHits:  groundMaxHits,
	}
}

// Observe — зафиксировать выполненный вызов инструмента.
//
// args — сырой JSON аргументов, resText — текст результата, ok — отработал ли
// инструмент без ошибки. Ошибка доказательством не является: сказать «в файле
// вот это» после неудачного grep субагент не мог.
func (g *Grounding) Observe(tool, args, resText string, ok bool) {
	if g == nil {
		return
	}
	a := parseArgs(args)
	var evs []Evidence
	switch tool {
	case "read_file", "multi_read", "inspect":
		evs = g.fileEvidence(a, resText, ok)
	case "grep", "multi_grep":
		evs = g.grepEvidence(a, resText, ok)
	case "glob", "list_dir":
		evs = listEvidence(a, ok)
	case "bash", "multi_bash", "job":
		evs = execEvidence(a, ok)
	case "web_fetch":
		evs = webEvidence(a, ok)
	case "write_file", "edit_file", "multi_edit":
		evs = writeEvidence(a, ok)
	default:
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for _, ev := range evs {
		if len(g.items) >= g.maxItems {
			return
		}
		g.items = append(g.items, ev)
	}
}

// rec — запись или создание сведений о файле.
func (g *Grounding) rec(path string) *fileRec {
	if r, ok := g.files[path]; ok {
		return r
	}
	if len(g.files) >= g.maxFiles {
		return nil
	}
	r := &fileRec{hits: map[int]bool{}}
	g.files[path] = r
	return r
}

// reReadShown — хвост read_file с числом показанных строк.
var reReadShown = regexp.MustCompile(`\[показано строк: (\d+) начиная с (\d+)`)

// reReadEOF — хвост read_file, дошедшего до конца файла. Кроме последней
// строки он сообщает, сколько строк показано: чтение с offset=120 до EOF
// закрывает не весь файл, а только хвост, и подтверждать им строки 1-119
// нельзя.
var reReadEOF = regexp.MustCompile(`\[файл закончился на строке (\d+)(?:, показано строк: (\d+))?`)

// reGrepHit — строка вывода grep: «путь:строка: текст».
//
// Флаг (?m) обязателен: без него «^» в Go RE2 означает начало всего текста, а
// не начало строки, и все совпадения кроме первой терялись бы. На одном и том
// же отчёте вердикт зависел бы от того, попал ли текст вывода в результат
// первым совпадением или нет.
var reGrepHit = regexp.MustCompile(`(?m)^([^\s:][^:]*\.[A-Za-z0-9]{1,8}):(\d+):`)

// fileEvidence — разобрать чтение файла: какие строки субагент реально видел.
func (g *Grounding) fileEvidence(a map[string]any, resText string, ok bool) []Evidence {
	if !ok {
		return nil
	}
	var out []Evidence
	add := func(p string, from, to int, full bool) {
		path := normPath(p)
		if path == "" {
			return
		}
		g.mu.Lock()
		r := g.rec(path)
		if r != nil {
			if full {
				r.full = true
			}
			if to > 0 {
				r.addRange(from, to)
			}
		}
		g.mu.Unlock()
		ev := Evidence{Kind: EvFile, Path: path, From: from, To: to, OK: true}
		if full {
			ev.To = to
		}
		out = append(out, ev)
	}

	// multi_read передаёт paths, read_file — path.
	var paths []string
	for _, p := range toList(a["paths"]) {
		if s := strings.TrimSpace(toStr(p)); s != "" {
			paths = append(paths, s)
		}
	}
	if s := strings.TrimSpace(toStr(a["path"])); s != "" {
		paths = append(paths, s)
	}
	if len(paths) == 0 {
		return nil
	}

	// Диапазон из хвоста результата — источник истины: аргументы могли быть
	// обрезаны инструментом, а хвост всегда описывает, что реально показано.
	shown, from, eof, eofShown := 0, 0, 0, 0
	if m := reReadShown.FindStringSubmatch(resText); m != nil {
		shown, _ = strconv.Atoi(m[1])
		from, _ = strconv.Atoi(m[2])
	}
	if m := reReadEOF.FindStringSubmatch(resText); m != nil {
		eof, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			eofShown, _ = strconv.Atoi(m[2])
		}
	}

	// multi_read отдаёт хвост для каждого файла, а файлы бывают разной длины.
	// Если хвостов столько же, сколько путей, сопоставляем их по порядку:
	// иначе первый EOF приписывался всем файлам, и короткий файл «подтверждал»
	// строки, которых в нём нет.
	type readTail struct{ eof, shown, from int }
	var eofTails []readTail
	for _, m := range reReadEOF.FindAllStringSubmatch(resText, -1) {
		n, _ := strconv.Atoi(m[1])
		sh := 0
		if m[2] != "" {
			sh, _ = strconv.Atoi(m[2])
		}
		start := 0
		if sh > 0 {
			start = n - sh + 1
			if start < 1 {
				start = 1
			}
		}
		eofTails = append(eofTails, readTail{eof: n, shown: sh, from: start})
	}
	var tails []readTail
	for _, m := range reReadShown.FindAllStringSubmatch(resText, -1) {
		n, _ := strconv.Atoi(m[1])
		f, _ := strconv.Atoi(m[2])
		tails = append(tails, readTail{shown: n, from: f})
	}

	// offset/limit из аргументов — запасной путь, если хвост не распознан.
	if from == 0 {
		from = atoiDefault(a["offset"], 1)
	}

	for i, p := range paths {
		switch {
		case len(eofTails) == len(paths) && eofTails[i].eof > 0:
			t := eofTails[i]
			if t.from > 1 {
				add(p, t.from, t.eof, false)
			} else {
				add(p, 1, t.eof, true)
			}
		case len(tails) == len(paths) && tails[i].shown > 0:
			add(p, tails[i].from, tails[i].from+tails[i].shown-1, false)
		case eof > 0:
			start := 1
			if eofShown > 0 {
				start = eof - eofShown + 1
				if start < 1 {
					start = 1
				}
			}
			if start > 1 {
				add(p, start, eof, false)
			} else {
				add(p, 1, eof, true)
			}
		case shown > 0:
			add(p, from, from+shown-1, false)
		default:
			// Хвост не распознан (другой формат, усечение). Считаем, что
			// файл открывали целиком с первой строки: это мягкая оценка,
			// лучше лишнее подтверждение, чем ложное обвинение в галлюцинации.
			add(p, 1, 0, true)
		}
	}
	return out
}

// grepEvidence — поиск по содержимому: известны точные строки совпадений.
func (g *Grounding) grepEvidence(a map[string]any, resText string, ok bool) []Evidence {
	pat := strings.TrimSpace(toStr(a["pattern"]))
	if !ok {
		return nil
	}
	ev := Evidence{Kind: EvGrep, Pat: pat, OK: true}
	g.mu.Lock()
	for _, m := range reGrepHit.FindAllStringSubmatch(resText, -1) {
		path := normPath(m[1])
		if path == "" {
			continue
		}
		line, err := strconv.Atoi(m[2])
		if err != nil || line <= 0 {
			continue
		}
		if r := g.rec(path); r != nil && len(r.hits) < g.maxHits {
			r.hits[line] = true
		}
		ev.Hits++
		// В сводке интересует первый файл: остальные видны через Hits, а путь
		// нужен как указание, где искали.
		if ev.Path == "" {
			ev.Path = path
		}
	}
	g.mu.Unlock()
	// Совпадений нет — поиск всё равно доказательство отрицания: субагент
	// проверил и не нашёл. Это ценно не меньше, чем находка.
	if ev.Path == "" {
		ev.Path = normPath(toStr(a["path"]))
	}
	return []Evidence{ev}
}

// listEvidence — каталог просмотрен: доказывает существование пути.
func listEvidence(a map[string]any, ok bool) []Evidence {
	pattern := strings.TrimSpace(toStr(a["pattern"]))
	path := strings.TrimSpace(toStr(a["path"]))
	if pattern == "" && path == "" {
		return nil
	}
	ev := Evidence{Kind: EvList, OK: ok}
	ev.Path = normPath(firstNonEmpty(path, pattern))
	if ev.Path != "" {
		ev.Pat = pattern
	}
	return []Evidence{ev}
}

// execEvidence — команда: доказательство о состоянии проекта в момент запуска.
func execEvidence(a map[string]any, ok bool) []Evidence {
	cmd := strings.TrimSpace(toStr(a["command"]))
	if cmd == "" {
		var parts []string
		for _, c := range toList(a["commands"]) {
			if s := strings.TrimSpace(toStr(c)); s != "" {
				parts = append(parts, s)
			}
		}
		cmd = strings.Join(parts, " ; ")
	}
	if cmd == "" {
		return nil
	}
	return []Evidence{{Kind: EvExec, Cmd: coreTruncate(cmd, 200), OK: ok}}
}

// webEvidence — загруженная страница.
func webEvidence(a map[string]any, ok bool) []Evidence {
	u := strings.TrimSpace(toStr(a["url"]))
	if u == "" {
		return nil
	}
	return []Evidence{{Kind: EvWeb, URL: u, OK: ok}}
}

// writeEvidence — изменённый файл.
func writeEvidence(a map[string]any, ok bool) []Evidence {
	var out []Evidence
	add := func(p string) {
		if p = normPath(p); p != "" {
			out = append(out, Evidence{Kind: EvWrite, Path: p, OK: ok})
		}
	}
	add(toStr(a["path"]))
	for _, p := range toList(a["paths"]) {
		add(toStr(p))
	}
	return out
}

// ---------- Проверка отчёта ----------

// Problems — адресный список того, что в отчёте не подтверждено.
//
// Именно этот список уходит в добивку. Общий повтор «попробуй ещё раз» почти
// бесполезен: модель снова пишет тот же текст, ссылаясь на те же строки, потому
// что не понимает, в чём именно ошиблась. Список мест — это конкретная
// обратная связь: «открой вот это и либо подтверди, либо убери из отчёта».
//
// Места дедуплицируются дважды. Точное совпадение («a.go:3» дважды в отчёте)
// снимается напрямую, а голый путь фантома отбрасывается, если уже назван
// его же ссылка с номером строки: «ghost.go» рядом с «ghost.go:15» — это одно
// и то же место, и в подсказке оно занимает строку попусту, отвлекая от
// реально новых проблем.
func (r *GroundingReport) Problems() []string {
	if r == nil {
		return nil
	}
	var out []string
	seen := make(map[string]bool, len(r.Unsupported)+len(r.Phantoms))
	for _, c := range r.Unsupported {
		if c.Where == "" || seen[c.Where] {
			continue
		}
		seen[c.Where] = true
		// Путь ссылка тоже помечен: им помечается фантом этого же файла.
		if p := normPath(c.Path); p != "" {
			seen[p] = true
		}
		out = append(out, c.Where)
	}
	for _, p := range r.Phantoms {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Claim — утверждение отчёта о конкретном месте в коде.
type Claim struct {
	Where     string // как написано в отчёте
	Path      string // путь без номера строки
	Line      int    // номер строки, 0 если не указан
	Supported bool   // подтверждено доказательствами
	Why       string // причина, если не подтверждено
}

// Risk — уровень риска утверждения.
type Risk string

const (
	// RiskNone — подтверждено.
	RiskNone Risk = ""
	// RiskLine — ссылка на строку, которую субагент не читал.
	RiskLine Risk = "строка не открывалась"
	// RiskUnread — файл упомянут, но не открыт.
	RiskUnread Risk = "файл не открывался"
	// RiskPhantom — упомянут файл, которого нет на диске.
	RiskPhantom Risk = "такого файла нет"
)

// GroundingReport — вердикт по отчёту.
type GroundingReport struct {
	Claims      []Claim
	Checked     int
	Supported   int
	Unsupported []Claim
	// Phantoms — файлы, которых нет на диске: самая грубая галлюцинация.
	Phantoms []string
	// Unread — файлы, которые существуют, но субагент их не открывал.
	Unread []string
	// FilesRead — сколько файлов субагент реально прочитал.
	FilesRead int
	// Covered — сколько ссылок попало в зону чтения.
	Covered int
	// Total — сколько строк субагент видел суммарно.
	Total int
	// Trustworthy — отчёт можно принимать без оговорок.
	Trustworthy bool
}

// Score — доля подтверждённых ссылок, 0..1.
func (r *GroundingReport) Score() float64 {
	if r == nil || r.Checked == 0 {
		return 1 // проверять нечего — не придираемся
	}
	return float64(r.Supported) / float64(r.Checked)
}

// ReFileLine — ссылка вида «путь:строка» или «путь:строка-строка».
//
// Классы символов — Unicode, а не ASCII, и это не украшение: русские отчёты
// сплошь ссылаются на файлы с кириллическими именами (`docs/Проверка.md:12`),
// и ASCII-регулярка их просто не видела. Молча не найденная ссылка выглядит
// как подтверждённая, то есть фантом проходил проверку.
//
// Расширение остаётся ASCII намеренно: `строка.Итог` не должен разбираться
// как ссылка, а настоящие расширения всегда латиницей.
var ReFileLine = regexp.MustCompile(`([\p{L}\p{N}_][\p{L}\p{N}_./\\-]*\.[A-Za-z0-9]{1,8})\s*:\s*(\d+)(?:\s*[-–—]\s*(\d+))?`)

// reFileOnly — упоминание файла без номера строки.
var reFileOnly = regexp.MustCompile(`[\p{L}\p{N}_][\p{L}\p{N}_./\\-]*\.[A-Za-z0-9]{1,8}`)

// Audit — сверить отчёт с журналом доказательств.
func (g *Grounding) Audit(report string) *GroundingReport {
	out := &GroundingReport{Trustworthy: true}
	if g == nil {
		return out
	}

	g.mu.Lock()
	files := make(map[string]*fileRec, len(g.files))
	for k, v := range g.files {
		if v == nil {
			files[k] = nil
			continue
		}
		// Глубокая копия: Audit работает уже без mu, а Observe продолжает
		// менять ranges/hits/total из горутины агентного цикла. Поверхностная
		// копия map'а оставляла общие *fileRec и давала data race.
		cp := &fileRec{full: v.full, total: v.total}
		cp.ranges = append([]lineRange(nil), v.ranges...)
		if len(v.hits) > 0 {
			cp.hits = make(map[int]bool, len(v.hits))
			for line := range v.hits {
				cp.hits[line] = true
			}
		}
		files[k] = cp
	}
	items := append([]Evidence(nil), g.items...)
	g.mu.Unlock()

	out.FilesRead = len(files)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
		// Суммарный объём прочитанного: грубая мера глубины исследования.
		// Для прочитанного целиком файла ranges — это тот же самый диапазон
		// 1..total, поэтому суммировать их вместе с total значит считать
		// каждую строку дважды.
		if r := files[p]; r != nil {
			if r.full {
				out.Total += maxInt(r.total, 1)
				continue
			}
			for _, rg := range r.ranges {
				out.Total += rg.to - rg.from + 1
			}
		}
	}
	// Сортировка ради детерминизма: вердикт на одном и том же отчёте не
	// должен зависеть от порядка обхода map'а.
	sort.Strings(paths)

	// Списки собираются в обоих проходах: ссылка «utils/helper.go:15» после
	// удаления из текста не попадёт во второй проход, а несуществующий файл
	// должен быть найден именно в первом.
	// Шумный вид («1.2.3», «т.д», домен) отсеивается, но только если такого пути
	// реально нет на диске. Порядок важен: сначала факт, потом эвристика.
	// Каталог с точкой в имени (`docs.go/main.go`) неотличим от домена по виду,
	// и единственный честный способ их различить — спросить у файловой системы.
	phantoms, unread := map[string]bool{}, map[string]bool{}

	// 1. Ссылки с номером строки.
	seen := map[string]bool{}
	for _, m := range ReFileLine.FindAllStringSubmatch(report, -1) {
		path := normPath(m[1])
		if path == "" || (isNoiseToken(path) && !g.exists(path)) {
			continue
		}
		line, err := strconv.Atoi(m[2])
		if err != nil || line <= 0 {
			continue
		}
		// «a.go:10-20» — утверждение обо всём диапазоне. Проверять только
		// начало мало: субагент мог прочитать строку 10 и выдумать хвост до 20.
		last := line
		if m[3] != "" {
			if end, err := strconv.Atoi(m[3]); err == nil && end > last {
				last = end
			}
		}
		where := path + ":" + m[2]
		if m[3] != "" {
			where += "-" + m[3]
		}
		if seen[where] {
			continue
		}
		seen[where] = true

		cl := Claim{Where: where, Path: path, Line: line}
		if rec := lookup(files, path); rec != nil {
			if rec.covers(line) && rec.covers(last) {
				cl.Supported = true
			} else {
				cl.Why = string(RiskLine) + " (читались " + describe(rec) + ")"
			}
		} else {
			// Файл не открывали. Различаем «назван наугад» и «несуществующий»:
			// второе — самая грубая галлюцинация и главный повод не доверять
			// остальным утверждениям отчёта.
			cl.Why = string(RiskUnread)
			if g.exists(path) {
				unread[path] = true
			} else {
				cl.Why = string(RiskPhantom)
				phantoms[path] = true
			}
		}
		out.Claims = append(out.Claims, cl)
	}

	// 2. Файлы без номера строки.
	rest := ReFileLine.ReplaceAllString(report, " ")
	for _, m := range reFileOnly.FindAllString(rest, -1) {
		path := normPath(m)
		if path == "" || seen[path] || (isNoiseToken(path) && !g.exists(path)) {
			continue
		}
		seen[path] = true
		if lookup(files, path) != nil {
			continue // читал — всё в порядке
		}
		// glob/list_dir тоже считаем знанием о существовании: субагент видел
		// файл в листинге, даже если не открывал содержимое.
		if g.sawInListing(items, path) {
			continue
		}
		if g.exists(path) {
			unread[path] = true
		} else {
			phantoms[path] = true
		}
	}
	out.Phantoms = sortedKeys(phantoms)
	out.Unread = sortedKeys(unread)

	for _, cl := range out.Claims {
		out.Checked++
		if cl.Supported {
			out.Supported++
		} else {
			out.Unsupported = append(out.Unsupported, cl)
		}
	}
	out.Covered = out.Supported
	out.Trustworthy = len(out.Unsupported) == 0 && len(out.Phantoms) == 0
	return out
}

// lookup — найти сведения о файле с учётом нечёткого совпадения путей.
func lookup(files map[string]*fileRec, path string) *fileRec {
	if r, ok := files[path]; ok {
		return r
	}
	for p, r := range files {
		if sameFile(p, path) {
			return r
		}
	}
	return nil
}

// sawInListing — видел ли субагент файл в листинге каталога.
func (g *Grounding) sawInListing(items []Evidence, path string) bool {
	for _, e := range items {
		if e.Kind != EvList && e.Kind != EvGrep {
			continue
		}
		if e.Path == path || sameFile(e.Path, path) {
			return true
		}
	}
	return false
}

// exists — есть ли файл на диске (относительно корня проекта или абсолютно).
func (g *Grounding) exists(path string) bool {
	if path == "" {
		return false
	}
	cands := []string{path}
	if !filepath.IsAbs(path) {
		if g.workDir != "" {
			cands = append(cands, filepath.Join(g.workDir, filepath.FromSlash(path)))
		}
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// describe — человекочитаемый перечень зон чтения файла.
func describe(r *fileRec) string {
	if r.full {
		return "файл целиком"
	}
	if len(r.ranges) == 0 {
		return "файл не открывался"
	}
	var parts []string
	for i, rg := range r.ranges {
		if i >= 3 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%d-%d", rg.from, rg.to))
	}
	return "строки " + strings.Join(parts, ", ")
}

// ---------- Приложение к отчёту ----------

// Report — приложить к отчёту блок проверки.
//
// Это обратный канал: главный агент видит, что подтверждено инструментами, а
// что нет, и может перепроверить сомнительное сам. Блок появляется, только
// когда есть ОГОВОРКА, то есть когда хоть что-то не подтвердилось. Отчёт без
// замечаний остаётся чистым: подтверждать исправность работы субагента в
// каждом отчёте — значит засорять контекст главного агента новостями, которых
// он не просил.
func (g *Grounding) Report(rep *GroundingReport) string {
	if rep == nil {
		return ""
	}
	// Нечего проверять и нечего подтверждать.
	if rep.Checked == 0 && len(rep.Phantoms) == 0 && len(rep.Unread) == 0 {
		return ""
	}
	// Проверять было нечего: ни одной ссылки «файл:строка» и ни одного фантома.
	// Вердикт «чисто» о фантомах не сообщает, а блок был бы пустым шумом.
	//
	// Проверка именно на оба условия: фантомный файл без номера строки — это
	// тоже неподтверждённое утверждение, и про него тоже нужно сказать.
	if rep.Checked == 0 && len(rep.Phantoms) == 0 {
		return ""
	}
	// Всё подтверждено: оговорок нет, блок не нужен.
	if rep.Trustworthy {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n---\n## Проверка отчёта (сделано автоматически)\n")
	if rep.FilesRead > 0 {
		fmt.Fprintf(&b, "- Субагент прочитал %d файл(ов), около %d строк.\n", rep.FilesRead, rep.Total)
	}
	if rep.Checked > 0 {
		fmt.Fprintf(&b, "- Ссылок «файл:строка» проверено: %d, подтверждено чтением: %d.\n", rep.Checked, rep.Supported)
	}
	if len(rep.Unsupported) > 0 {
		fmt.Fprintf(&b, "- ⚠ НЕ подтверждено (%d) — субагент не открывал эти места:\n", len(rep.Unsupported))
		for _, c := range rep.Unsupported {
			fmt.Fprintf(&b, "  - %s (%s)\n", c.Where, c.Why)
		}
	}
	if len(rep.Phantoms) > 0 {
		fmt.Fprintf(&b, "- ⚠ Упомянуты файлы, которых нет на диске: %s\n", coreTruncate(strings.Join(rep.Phantoms, ", "), 200))
	}
	if len(rep.Unread) > 0 {
		fmt.Fprintf(&b, "- Упомянуты, но не открыты: %s\n", coreTruncate(strings.Join(rep.Unread, ", "), 200))
	}
	if !rep.Trustworthy {
		b.WriteString("- Вывод: не доверяй этим утверждениям без проверки.\n")
	}
	return b.String()
}

// ---------- Сопоставление путей ----------

// normPath — привести путь из аргумента инструмента или отчёта к единому виду.
//
// Сравнивать «как написано» нельзя: модель пишет `pool.go`, инструмент отдаёт
// `subagents/pool.go`, третий источник — `./subagents/pool.go`.
func normPath(p string) string {
	s := strings.TrimSpace(p)
	s = strings.Trim(s, `"'«»`)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "file://")
	s = filepath.ToSlash(s)
	for strings.HasPrefix(s, "./") {
		s = s[2:]
	}
	return s
}

// sameFile — один и тот же файл, записанный по-разному.
//
// Ведущий каталог — самая частая причина расхождения: отчёт говорит про
// `pool.go`, инструмент читает `subagents/pool.go`.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a) {
		return true
	}
	// Один — путь внутри другого каталога: сравниваем хвост из двух частей.
	ta, tb := tail2(a), tail2(b)
	return ta == tb && strings.Contains(ta, "/")
}

func tail2(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return p
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// isNoiseToken — техническая муть, которую не стоит считать ссылкой.
func isNoiseToken(p string) bool {
	if p == "" || !strings.Contains(p, ".") {
		return true
	}
	// Адреса и домены — не пути проекта. Без этой проверки ссылка на
	// `https://example.com/spec.md` или на `docs.example.com/guide.html`
	// превращалась бы в «файл, которого нет на диске».
	if isAddr(p) {
		return true
	}
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	i := strings.LastIndex(base, ".")
	if i <= 0 {
		return true
	}
	name, ext := base[:i], base[i+1:]
	if isAllDigits(name) { // 1.2.3
		return true
	}
	switch strings.ToLower(ext) {
	case "md", "go", "js", "ts", "tsx", "jsx", "json", "yaml", "yml", "toml",
		"py", "rs", "java", "kt", "c", "h", "cpp", "hpp", "cs", "rb", "php",
		"sh", "css", "html", "xml", "sql", "txt", "log", "env", "lock", "mod", "sum":
		return false
	}
	return len(ext) <= 2 // «т.д», «и.о» и прочее
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isAddr — это веб-адрес или домен, а не путь в проекте.
//
// Регулярка вытаскивает из `https://example.com/spec.md` кусок
// `https//example.com/spec.md`: двоеточие вне классов символов, слэши внутри.
// Отсекаем по двум признакам: схема (`//` после имени схемы) и домен в первой
// компоненте пути. У файлов точка стоит в конце компоненты — `utils.go` — а у
// домена в середине, поэтому `docs.go/main.go` адресом не считается.
func isAddr(p string) bool {
	s := strings.ToLower(p)
	if strings.Contains(s, "://") || strings.Contains(s, "//") {
		return true
	}
	head, _, nested := strings.Cut(s, "/")
	return nested && strings.Contains(head, ".") && !strings.HasSuffix(head, ".")
}

// ---------- Мелкие помощники ----------

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func coreTruncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseArgs(args string) map[string]any {
	var m map[string]any
	if args == "" {
		return map[string]any{}
	}
	if err := json.Unmarshal([]byte(args), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func toStr(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(s)
	}
	return ""
}

func toList(v any) []any {
	switch l := v.(type) {
	case []any:
		return l
	case string:
		return []any{l}
	}
	return nil
}

func atoiDefault(v any, def int) int {
	if s := strings.TrimSpace(toStr(v)); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return def
}
