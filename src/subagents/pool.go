package subagents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gcli/core"
)

// Status — состояние запуска субагента.
type Status string

const (
	StatusRunning  Status = "running"
	StatusDone     Status = "done"
	StatusError    Status = "error"
	StatusCanceled Status = "canceled"
)

// Run — один запуск субагента.
type Run struct {
	ID       string
	Name     string
	Type     Type
	Task     string
	Model    string
	Status   Status
	Depth    int
	Started  time.Time
	Finished time.Time
	Turns    int
	Tools    int
	Usage    core.Usage
	Summary  string
	Full     string
	Err      string
	// Retries — сколько повторов понадобилось (слой прочности).
	Retries int
	// Reuse — отчёт получен без нового запуска (см. cache.go). Пусто ReuseNone
	// означает обычный запуск, и это видно в журнале без догадок.
	Reuse Reuse
	// ReusedFrom — имя субагента, чей отчёт переиспользован.
	ReusedFrom string

	cancel context.CancelFunc
}

// StatusText — человекочитаемый статус.
func (r *Run) StatusText() string {
	switch r.Status {
	case StatusRunning:
		return "выполняется"
	case StatusDone:
		return "готов"
	case StatusError:
		return "ошибка"
	case StatusCanceled:
		return "отменён"
	}
	return "—"
}

// statusLabel — подпись статуса для UI и сводок.
//
// Отдельный метод, а не правка StatusText: статус в базе остаётся StatusDone,
// и по нему видно, что отчёт ЕСТЬ, а вот подпись должна сказать, что он взят
// из кеша. Смешивать эти два смысла в одном поле нельзя — иначе «из кеша»
// пришлось бы выдавать за ошибку или за обычную работу.
func (r *Run) statusLabel() string {
	switch r.Reuse {
	case ReuseCache:
		return "из кеша"
	case ReuseFlight:
		return "дождался"
	}
	return r.StatusText()
}

// Elapsed — длительность выполнения.
func (r *Run) Elapsed() time.Duration {
	if r.Finished.IsZero() {
		return time.Since(r.Started)
	}
	return r.Finished.Sub(r.Started)
}

// Label — подпись для UI.
func (r *Run) Label() string {
	s := r.Name
	if s == "" {
		s = r.Type.Label()
	}
	return s
}

// Spec — описание задачи для субагента.
type Spec struct {
	Type     Type
	Task     string
	Name     string
	Model    string
	ReadOnly bool
	Depth    int
	Summary  string
	Notes    string
	// MaxTurns — лимит итераций агентного цикла (0 = значение по умолчанию).
	MaxTurns int
	// Prompt — готовый системный промпт (пользовательские агенты из
	// .gcli/agents/*.md). Непустое значение заменяет промпт типа.
	Prompt string
	// ToolsAllow — белый список инструментов (пользовательские агенты).
	// nil = набор по типу.
	ToolsAllow []string
	// RetryHint — добавка к задаче при повторной попытке (слой прочности).
	// Модели сообщает, что предыдущая попытка сорвалась и что делать иначе.
	RetryHint string
}

// Runner — функция, выполняющая задачу субагента.
// Реализуется агентом (там, где есть доступ к модели и инструментам).
type Runner func(ctx context.Context, spec Spec) (Outcome, error)

// Outcome — результат работы субагента.
type Outcome struct {
	Full    string
	Summary string
	Turns   int
	Tools   int
	Usage   core.Usage
	// Retries — сколько повторов понадобилось (0 = уложился с первого раза).
	Retries int
	// Audit — вердикт проверки отчёта по журналу доказательств.
	// Нужен слою прочности: отчёт может выглядеть идеально и при этом
	// опираться на ссылки, которые субагент никогда не открывал.
	Audit *GroundingReport
	// Reuse — откуда взят отчёт. Непустое значение означает, что работа не
	// выполнялась: её результат либо лежал в кеше, либо уже делался другим
	// субагентом. Модель обязана знать это прямо, иначе «субагент молча
	// ответил за миг» будет выглядеть как успех за нулевое время.
	Reuse Reuse
	// ReusedFrom — имя субагента-первоисточника.
	ReusedFrom string
}

// Pool — пул субагентов: запуск, лимиты, журнал.
type Pool struct {
	mu      sync.Mutex
	runs    map[string]*Run
	order   []string
	counter int
	slots   slotCounter

	runner   Runner
	maxPar   int
	maxDepth int
	enabled  bool
	workDir  string

	// cache — кеш отчётов и дедупликация идущих работ (см. cache.go).
	// Живёт в пуле, а не в глобальной переменной: кеш привязан к сессии и
	// обязан умереть вместе с ней, иначе отчёт из вчерашнего проекта прилетел
	// бы в сегодняшний с тем же ключом.
	cache *poolCache
}

// NewPool — создать пул.
func NewPool(runner Runner, opts PoolOptions) *Pool {
	p := &Pool{
		runs:     map[string]*Run{},
		runner:   runner,
		maxPar:   opts.MaxParallel,
		maxDepth: opts.MaxDepth,
		enabled:  opts.Enabled,
		workDir:  opts.WorkDir,
		cache:    newPoolCache(),
	}
	if p.maxPar <= 0 {
		p.maxPar = 3
	}
	if p.maxDepth <= 0 {
		p.maxDepth = 1
	}
	return p
}

// PoolOptions — настройки пула.
type PoolOptions struct {
	MaxParallel int
	MaxDepth    int
	Enabled     bool
	WorkDir     string
}

// Enabled — разрешены ли субагенты.
//
// enabled читается и пишется под mu: переключатель /agents on|off приходит
// из UI-потока, а Enabled зовут агентные горутины из Spawn, и без блокировки
// это data race. Блокировка безопасна: держится только вокруг чтения поля,
// Runner под mu не выполняется никогда (см. Spawn).
func (p *Pool) Enabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enabled && p.maxDepth > 0
}

// MaxDepth — максимальная глубина вложенности.
func (p *Pool) MaxDepth() int { return p.maxDepth }

// SetEnabled — включить/выключить субагентов.
func (p *Pool) SetEnabled(on bool) {
	p.mu.Lock()
	p.enabled = on
	p.mu.Unlock()
}

// Running — сколько субагентов работает сейчас.
func (p *Pool) Running() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, r := range p.runs {
		if r.Status == StatusRunning {
			n++
		}
	}
	return n
}

// Spawn — запустить субагента и дождаться результата.
//
// Повторная задача заново не запускается: если её отчёт ещё в кеше, он
// возвращается сразу, а если такая же работа уже идёт — вызов ждёт её и берёт
// тот же результат. Оба случая отмечены в Outcome и в журнале: иначе модель
// видела бы вызов без ответа и решила бы, что субагент молчал впустую.
//
// Порядок проверок неслучаен: кеш смотрится ДО захвата параллельного слота,
// потому что ожидание чужой работы не должно занимать слот вхолостую.
func (p *Pool) Spawn(ctx context.Context, spec Spec) (out Outcome, err error) {
	if !p.Enabled() {
		return Outcome{}, fmt.Errorf("субагенты отключены — включи: /agents on")
	}
	if spec.Depth > p.maxDepth {
		return Outcome{}, fmt.Errorf("достигнута максимальная глубина вложенности субагентов (%d)", p.maxDepth)
	}

	var key, runID, srcName string
	if Cacheable(spec) {
		key = CacheKey(spec)
		if e, ok := p.cache.cache.get(key); ok {
			return p.reuse(spec, e.out, ReuseCache, e.srcName, e.at)
		}
		if f, waiting := p.cache.begin(key); waiting {
			return p.joinFlight(ctx, spec, f)
		}
		// Мы ведущий этой работы. Результат публикуется в любом исходе — включая
		// ошибку и пустой отчёт: иначе ожидающие провисели бы до конца сессии,
		// не получив ни ответа, ни причины.
		defer func() {
			p.cache.finish(key, out, err, CacheableOutcome(spec, out, err), srcName, runID)
		}()
	}

	// Занимаем слот ДО запуска горутины: иначе несколько одновременных
	// вызовов успевают увидеть свободный слот и превысить лимит.
	if e := p.acquire(ctx); e != nil {
		return Outcome{}, e
	}
	defer p.release()

	name := spec.Name
	if name == "" {
		name = p.autoName(spec.Type)
	}
	run := &Run{
		ID:      core.RandID(6),
		Name:    name,
		Type:    spec.Type,
		Task:    spec.Task,
		Model:   spec.Model,
		Status:  StatusRunning,
		Depth:   spec.Depth,
		Started: time.Now(),
	}
	runID = run.ID
	srcName = name
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p.mu.Lock()
	run.cancel = cancel
	p.runs[run.ID] = run
	p.order = append(p.order, run.ID)
	p.counter++
	p.mu.Unlock()

	// runner выполняется БЕЗ mu: он работает минуты, а mu нужен UI
	// (Running/All/Cancel) и другим субагентам (acquire/release).
	out, err = p.runner(cctx, spec)

	// Итоговые поля пишутся под mu — иначе UI, читающий Run через All(),
	// одновременно с завершением субагента, ловит data race
	// (воспроизведено go test -race в subagents).
	p.mu.Lock()
	run.Finished = time.Now()
	run.Full = out.Full
	run.Summary = out.Summary
	run.Turns = out.Turns
	run.Tools = out.Tools
	run.Usage = out.Usage
	run.Retries = out.Retries
	switch {
	case err != nil:
		run.Status = StatusError
		run.Err = err.Error()
		if cctx.Err() != nil {
			run.Status = StatusCanceled
		}
	case strings.TrimSpace(out.Full) == "":
		run.Status = StatusError
		run.Err = "субагент не вернул отчёт"
		err = fmt.Errorf("субагент %s не вернул отчёт", name)
	default:
		run.Status = StatusDone
	}
	p.mu.Unlock()
	return out, err
}

// reuse — отдать сохранённый отчёт, не запуская субагента.
//
// Журнал пополняется настоящим Run, иначе вызов исчез бы из /agents и
// agent_status и выглядел бы как не отработавший. Время запуска берётся из
// кеша: отчёт получен тогда, а не только что, и за это время код могли
// изменить — притворяться свежим было бы враньём.
func (p *Pool) reuse(spec Spec, out Outcome, kind Reuse, from string, at time.Time) (Outcome, error) {
	out.Reuse = kind
	out.ReusedFrom = from
	name := spec.Name
	if name == "" {
		name = p.autoName(spec.Type)
	}
	r := &Run{
		ID:         core.RandID(6),
		Name:       name,
		Type:       spec.Type,
		Task:       spec.Task,
		Model:      spec.Model,
		Status:     StatusDone,
		Depth:      spec.Depth,
		Started:    at,
		Finished:   at,
		Turns:      out.Turns,
		Tools:      out.Tools,
		Usage:      out.Usage,
		Summary:    out.Summary,
		Full:       out.Full,
		Reuse:      kind,
		ReusedFrom: from,
	}
	p.mu.Lock()
	p.runs[r.ID] = r
	p.order = append(p.order, r.ID)
	p.counter++
	p.mu.Unlock()
	return out, nil
}

// joinFlight — дождаться уже идущей такой же работы.
//
// Отмена ожидающего ничего не говорит ведущему: та работа всё равно нужна кому-то
// ещё, и прерывать её из-за ухода одного слушателя неправильно.
func (p *Pool) joinFlight(ctx context.Context, spec Spec, f *flight) (Outcome, error) {
	out, err := waitFlight(ctx, f)
	if err != nil {
		return Outcome{}, err
	}
	// Пустой результат без ошибки означает, что ведущий ушёл, не ответив.
	// Молча вернуть его было бы хуже отказа: модель получила бы «отчёт» из
	// ничего и пошла бы дальше строить выводы на пустоте.
	if strings.TrimSpace(out.Full) == "" {
		return Outcome{}, fmt.Errorf("ведущий запуск такой же задачи не дал результата — повтори задачу")
	}
	// Отчёт свежий (его только что сделали), поэтому время — нынешнее.
	return p.reuse(spec, out, ReuseFlight, f.srcName, time.Now())
}

// slots — счётчик занятых параллельных слотов (защищён mu).
// Выделен отдельно от runs, чтобы лимит работал и до регистрации запуска.
type slotCounter struct {
	used int
}

// acquire — занять слот (с ожиданием, если все заняты).
func (p *Pool) acquire(ctx context.Context) error {
	for {
		p.mu.Lock()
		if p.slots.used < p.maxPar {
			p.slots.used++
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(60 * time.Millisecond):
		}
	}
}

// release — освободить слот.
func (p *Pool) release() {
	p.mu.Lock()
	if p.slots.used > 0 {
		p.slots.used--
	}
	p.mu.Unlock()
}

// autoName — сгенерировать имя субагента.
func (p *Pool) autoName(t Type) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	base := map[Type]string{
		TypeExplorer:   "explorer",
		TypeReviewer:   "reviewer",
		TypePlanner:    "planner",
		TypeCoder:      "coder",
		TypeTester:     "tester",
		TypeFrontend:   "frontend",
		TypeResearcher: "researcher",
		TypeDocs:       "docs",
		TypeGeneral:    "general",
		TypeCustom:     "agent",
	}[t]
	if base == "" {
		base = "agent"
	}
	n := 0
	for _, r := range p.runs {
		if r.Type == t {
			n++
		}
	}
	return fmt.Sprintf("%s-%d", base, n+1)
}

// All — все запуски (новые первыми).
//
// Возвращаются КОПИИ Run, а не живые указатели. Поля запуска меняются
// в горутине субагента до и после завершения, пока UI читает их
// (agent_status, /agents), поэтому наружу отдаётся снимок под mu.
func (p *Pool) All() []*Run {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*Run, 0, len(p.order))
	for i := len(p.order) - 1; i >= 0; i-- {
		if r := p.runs[p.order[i]]; r != nil {
			cp := *r
			cp.cancel = nil // функция отмены не должна утекать наружу
			out = append(out, &cp)
		}
	}
	return out
}

// Get — найти запуски по имени (без учёта регистра).
func (p *Pool) Get(name string) []*Run {
	var out []*Run
	for _, r := range p.All() {
		if strings.EqualFold(r.Name, name) || r.ID == name {
			out = append(out, r)
		}
	}
	return out
}

// Cancel — отменить все работающие субагенты.
func (p *Pool) Cancel() int {
	p.mu.Lock()
	var cancels []context.CancelFunc
	for _, r := range p.runs {
		if r.Status == StatusRunning && r.cancel != nil {
			cancels = append(cancels, r.cancel)
		}
	}
	p.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	return len(cancels)
}

// CacheStats — попадания, промахи и размер кеша повторов.
//
// Отдельный метод, а не поле: состояние кеша читается из тестов и сводок из
// разных горутин, и отдача снимка под mu дешевле, чем разбираться, кто и когда
// его трогал.
func (p *Pool) CacheStats() (hits, miss, size int) { return p.cache.cache.stats() }

// Summary — текстовая сводка для инструмента agent_status.
func (p *Pool) Summary() string {
	runs := p.All()
	if len(runs) == 0 {
		return "Субагенты в этой сессии не запускались.\n" +
			"Подсказка: поручи подзадачу через spawn_agent. Типы: explorer, reviewer, planner, coder, tester, frontend, researcher, docs, general" +
			" (также работают имена пользовательских агентов из .gcli/agents/*.md)."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Запусков субагентов: %d (параллельность: %d, глубина: %d)\n", len(runs), p.maxPar, p.maxDepth)
	if hits, miss, _ := p.cache.cache.stats(); hits+miss > 0 {
		fmt.Fprintf(&b, "Кеш повторов: попаданий %d, промахов %d\n", hits, miss)
	}
	for _, r := range runs {
		mark := "✔"
		switch r.Status {
		case StatusRunning:
			mark = "◆"
		case StatusError:
			mark = "✖"
		case StatusCanceled:
			mark = "⊘"
		}
		fmt.Fprintf(&b, "%s %-16s %-10s %s\n", mark, r.Label(), r.statusLabel(), core.Truncate(core.OneLine(r.Task), 60))
	}
	return b.String()
}

// Details — подробности по имени субагента.
func (p *Pool) Details(name string) string {
	runs := p.Get(name)
	if len(runs) == 0 {
		return "Субагент «" + name + "» не найден. Список: /agents"
	}
	r := runs[0]
	var b strings.Builder
	fmt.Fprintf(&b, "Субагент: %s (%s)\n", r.Label(), r.Type)
	fmt.Fprintf(&b, "Статус: %s · время: %s · ходов: %d · инструментов: %d\n",
		r.statusLabel(), core.HumanDuration(r.Elapsed()), r.Turns, r.Tools)
	if r.Reuse != ReuseNone {
		// Отдельно и явно: ходов и инструментов этот вызов не тратил, они
		// достались вместе с чужим отчётом.
		fmt.Fprintf(&b, "Отчёт переиспользован (%s) от субагента «%s» — новый запуск не выполнялся\n",
			r.Reuse, r.ReusedFrom)
	}
	fmt.Fprintf(&b, "Токены: ↑%s ↓%s\n", core.Kfmt(r.Usage.PromptTokens), core.Kfmt(r.Usage.CompletionTokens))
	fmt.Fprintf(&b, "Задача: %s\n", core.OneLine(r.Task))
	if r.Err != "" {
		fmt.Fprintf(&b, "Ошибка: %s\n", r.Err)
	}
	if r.Full != "" {
		fmt.Fprintf(&b, "\n--- Отчёт ---\n%s", r.Full)
	}
	return b.String()
}

// Table — данные для таблицы в UI.
func (p *Pool) Table() (rows [][]string) {
	runs := p.All()
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Started.After(runs[j].Started) })
	for _, r := range runs {
		rows = append(rows, []string{
			r.Label(),
			string(r.Type),
			r.statusLabel(),
			core.HumanDuration(r.Elapsed()),
			fmt.Sprintf("%d", r.Tools),
			core.Truncate(core.OneLine(r.Task), 48),
		})
	}
	return rows
}
