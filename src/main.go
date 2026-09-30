// gcli — ИИ-агент в терминале.
//
// Агент не просто отвечает, а выполняет задачи: правит файлы, запускает
// команды, ищет в интернете, ведёт план работ и делегирует подзадачи
// субагентам. Работает с любым провайдером — от Z.ai GLM и OpenRouter
// до локальной Ollama. Только стандартная библиотека Go.
//
// Сборка:  go build -o gcli .
// Запуск:  ./gcli                    интерактивный режим
//
//	./gcli -p "запрос"         один запрос и выход
//	./gcli -c                  продолжить последнюю сессию
//	./gcli -r 2                возобновить сессию №2
//	./gcli --help              справка
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"time"

	"gcli/agent"
	"gcli/core"
	"gcli/providers"
	"gcli/subagents"
	"gcli/tools"
	"gcli/ui"
)

// buildVersion — переопределяется при сборке:
// -ldflags "-X main.buildVersion=5.0.1".
var buildVersion = ""

func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	return core.Version
}

// app — всё состояние приложения.
type app struct {
	repo    *core.Repo
	ui      *ui.UI
	store   *core.Store
	workDir string
	stdin   *stdinReader

	registry *providers.Registry
	client   *providers.Client
	prov     *providers.Provider
	model    string

	tools  *tools.Registry
	memory *tools.Memory
	pool   *subagents.Pool

	sess *core.Session

	stream *ui.Stream

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	notes   []string
	quiet   bool

	// lastTurnFailed — предыдущий ход завершился ошибкой: на второй
	// ошибке подряд кот не хандрит, а сердится.
	lastTurnFailed bool

	// lastAgent — агент текущего хода. Нужен инструменту self_status,
	// чтобы агент видел свой настоящий системный промпт, а не базовый.
	lastAgent *agent.Agent

	// setupNotes — замечания при старте, показываются под баннером одной
	// группой. Снимок startupNotes на момент последней сборки реестра.
	setupNotes []string

	// startupNotes — то, что не относится к реестру и должно печататься под
	// баннером: например, включённая песочница. Список реестра (setupNotes)
	// buildTools пересобирает начисто, а эти замечания при пересборке
	// терялись бы: после /sandbox on пользователь не увидел бы слова
	// «Песочница: пути ограничены…».
	startupNotes []string

	// sandbox — граница файловой системы для инструментов. По умолчанию
	// выключена: пользователь обычно работает в своём проекте и лишний
	// вопрос «а можно ли выйти из каталога?» только мешает. Но в чужом
	// репозитории (клон, распакованный архив) песочница — единственное,
	// что не даст агенту унести ключи из ~/.gcli.
	sandbox *tools.Sandbox

	// Размышления модели. По умолчанию они НЕ печатаются: в ходе работы
	// они шумят, а читать их заранее всё равно нельзя. Но они копятся в
	// reasonBuf целиком, и Ctrl+O показывает их на лету либо выводит
	// уже накопленные задним числом.
	//
	// reasonLive — воля пользователя, она переживает ходы: нажал
	// «показать» — значит показывать дальше, пока не свернёт. Остальное —
	// состояние текущего хода, обнуляется в resetReason.
	reasonMu  sync.Mutex
	reasonBuf strings.Builder
	// reasonPrint — порядок печати. Стриминг и Ctrl+O печатают в один и тот
	// же буфер, и без этого мьютекса они перемешиваются: Ctrl+O напечатает
	// накопленное, а пришедший следом кусок допишется перед ним, и отметка
	// «показано всё» съест кусок, который на экране так и не появился.
	// Порядок блокировок всегда reasonPrint → reasonMu → мьютекс UI,
	// обратный порядок приводит к дедлоку со строкой ожидания.
	reasonPrint sync.Mutex
	reasonLive  bool // пользователь хочет видеть размышления (после Ctrl+O)
	reasonShown bool // размышления уже показывались в этом ходу
	reasonOpen  bool // на экране открыт поток размышлений (нужен перевод строки)
	reasonAny   bool // модель вообще присылала размышления в этом ходу
	// reasonHinted — подсказка про Ctrl+O уже показана в этом ходу.
	reasonHinted bool
	// reasonPrintedLen — сколько байт буфера уже показано на экране.
	// Нужно, чтобы задним числом не напечатать один блок дважды.
	reasonPrintedLen int
}

// reasonHint — подсказка на строке ожидания.
const reasonHint = "(Ctrl+O — показать размышления)"

func main() {
	flag.Usage = usage
	var (
		flagPrompt       = flag.String("p", "", "один запрос: выполнить и выйти")
		flagResume       = flag.Int("r", 0, "возобновить сессию по номеру из /sessions")
		flagContinue     = flag.Bool("c", false, "продолжить последнюю сессию")
		flagModel        = flag.String("model", "", "модель (например glm-4.6)")
		flagProv         = flag.String("provider", "", "провайдер: zai | openrouter | openai | anthropic | ollama | свой id")
		flagAgent        = flag.String("agent", "", "агентный режим: on | off")
		flagYolo         = flag.Bool("yolo", false, "не спрашивать подтверждений (кроме опасных команд)")
		flagSandbox      = flag.String("sandbox", "", "песочница файлов: on | off (ограничить доступ рабочим каталогом)")
		flagAutopilot    = flag.String("autopilot", "", "автопилот: on | off (сам одобряет безопасные действия)")
		flagAutopilotAll = flag.String("autopilot-all", "", "автопилот повышенного риска: on | off (одобряет всё)")
		flagSetup        = flag.Bool("setup", false, "мастер настройки: провайдер → ключ → модель")
		flagVersion      = flag.Bool("v", false, "версия и платформа")
		flagJSON         = flag.Bool("json", false, "машиночитаемый вывод (только ответ, без UI)")
		flagColor        = flag.String("color", "", "уровень цвета: auto | 16 | 256 | truecolor | none")
		flagNoColor      = flag.Bool("no-color", false, "без цвета")
		flagUnicode      = flag.Bool("ascii", false, "только ASCII-псевдографика")
		flagCompact      = flag.Bool("compact", false, "компактный вывод")
		flagSubagent     = flag.String("subagent", "", "разрешить субагентов: on | off")
	)
	flag.Parse()

	if *flagVersion {
		fmt.Println("gcli v" + version() + " · " + runtime.GOOS + "/" + runtime.GOARCH + " · " + runtime.Version())
		return
	}

	a := &app{workDir: must(os.Getwd())}
	a.repo = core.Open()
	a.store = a.repo.Store
	a.store.Ensure()

	a.setupUI(*flagNoColor, *flagUnicode, *flagCompact, *flagJSON, *flagColor)
	a.quiet = *flagJSON

	a.registry = providers.Build(a.repo.Cfg)
	a.client = providers.NewClient()
	a.prov = a.registry.Pick(*flagProv)
	a.model = providers.ResolveModel(a.prov, *flagModel, a.repo.Cfg.Model)

	// Флаги переопределяют конфиг.
	if *flagSubagent != "" {
		a.repo.Cfg.Subagents = *flagSubagent != "off"
	}
	// Песочница включается флагом или переменной окружения. Сначала
	// собираем Env: без него buildTools не увидит песочницу.
	a.sandbox = a.setupSandbox(*flagSandbox)
	if a.repo.Cfg.Compact || *flagCompact {
		a.ui.SetCompact(true)
	}

	a.memory = tools.NewMemory(a.workDir, a.store)
	a.buildTools()
	a.buildPool()
	a.applyMascot()

	a.stdin = newStdin()

	// Сессия.
	a.sess = a.loadInitialSession(*flagResume, *flagContinue)
	switch *flagAgent {
	case "on":
		a.sess.AgentMode = true
	case "off":
		a.sess.AgentMode = false
	}
	if *flagYolo {
		a.sess.Perms.BashAll = true
		a.sess.Perms.FileWrite = true
	}
	// Автопилот: сначала конфиг, затем флаги (флаги сильнее конфига).
	if a.repo.Cfg.Autopilot {
		a.sess.Perms.Autopilot = true
	}
	if a.repo.Cfg.AutopilotAll {
		a.sess.Perms.Autopilot = true
		a.sess.Perms.AutopilotAll = true
	}
	if s := strings.ToLower(strings.TrimSpace(*flagAutopilot)); s != "" {
		a.setAutopilot(s == "on" || s == "вкл" || s == "1" || s == "true")
	}
	if s := strings.ToLower(strings.TrimSpace(*flagAutopilotAll)); s != "" {
		on := s == "on" || s == "вкл" || s == "1" || s == "true"
		a.setAutopilotAll(on)
	}
	a.sess.Provider = a.prov.ID
	a.sess.Model = a.model
	defer a.saveSession()

	a.installSignalHandler()

	// Режим одного запроса (CI/скрипты).
	if *flagPrompt != "" {
		if *flagJSON {
			a.ui.SetQuiet(true)
		}
		if err := a.turn(*flagPrompt); err != nil {
			if !a.quiet {
				a.ui.Err(err.Error())
			}
			os.Exit(1)
		}
		return
	}

	if !a.quiet {
		a.banner()
	}
	if *flagSetup {
		a.runSetup()
	}
	a.repl()
}

func must[T any](v T, err error) T {
	if err != nil {
		fmt.Fprintln(os.Stderr, "gcli:", err)
		os.Exit(1)
	}
	return v
}

// ---------- Анимации ----------

// animEnv — входные данные для решения «включать ли анимации».
// Вынесено в структуру, чтобы правило приоритетов можно было проверить
// тестом, не подменяя окружение и не читая ~/.gcli/config.json.
type animEnv struct {
	Color     bool  // терминал умеет цвет
	Machine   bool  // -json: машинный вывод
	ASCII     bool  // -ascii
	UnicodeOK bool  // терминал умеет псевдографику
	NoAnimEnv bool  // выставлена GCLI_NO_ANIM
	Cfg       *bool // ключ "animations" в config.json (nil = не задан)
}

// animsEnabled — правило включения анимаций.
//
// Приоритет от сильного к слабому:
//
//	GCLI_NO_ANIM      — принудительное «выключить всё»;
//	-json, ASCII      — вывод машинный или кракозябрный;
//	config.json       — пользовательская настройка;
//	автоопределение   — цвет и настоящий терминал.
//
// Раньше config.json стоял выше GCLI_NO_ANIM, и переменная окружения молча
// не работала: у кого в конфиге стояло "animations": true, тот получал
// анимации вопреки явному GCLI_NO_ANIM=1.
func animsEnabled(e animEnv) bool {
	if e.NoAnimEnv {
		return false
	}
	if e.Machine || e.ASCII || !e.UnicodeOK || !e.Color {
		return false
	}
	if e.Cfg != nil {
		return *e.Cfg
	}
	return true
}

// colorEnv — входные данные для решения «включать ли цвет».
type colorEnv struct {
	TTY        bool   // stdout — терминал
	VT         bool   // Windows: включён режим виртуального терминала
	NoColor    bool   // -no-color
	Machine    bool   // -json
	NoColorEnv bool   // выставлена NO_COLOR
	Mode       string // -color <уровень> или GCLI_COLOR ("" = авто)
}

// resolveColor — автоопределение цвета и уровня.
//
// Уровень: флаг -color важнее GCLI_COLOR, тот важнее автоопределения.
// Явно запрошенный уровень принудительно включает окраску, даже если
// терминал не определился как tty (так работает Windows Terminal,
// где isTerminal() иногда врёт).
//
// Решение вынесено в чистую функцию: анимации считаются ПОСЛЕ него и
// обязаны видеть итоговый color. Считать анимации раньше — значит молча
// выключить эффекты при -color true.
func resolveColor(e colorEnv) (color bool, grade ui.ColorLevel) {
	color = e.TTY && e.VT && !e.NoColor && !e.NoColorEnv && !e.Machine
	grade = ui.ColorNone
	lvl, ok := ui.ParseColorLevel(e.Mode)
	if !ok {
		return color, grade
	}
	grade = ui.ColorLevel(lvl)
	if grade != ui.ColorNone && !e.NoColor && !e.Machine {
		color = true
	}
	return color, grade
}

// setupUI — создать интерфейс с учётом возможностей терминала.
func (a *app) setupUI(noColor, ascii, compact, machine bool, colorMode string) {
	vtOK := ui.EnableVT()
	tty := isTerminal()
	unicodeOK := !ascii && unicodeSupported()

	// Уровень цвета: флаг -color важнее GCLI_COLOR, тот важнее автоопределения.
	mode := colorMode
	if mode == "" {
		mode = os.Getenv("GCLI_COLOR")
	}
	color, grade := resolveColor(colorEnv{
		TTY:        tty,
		VT:         vtOK,
		NoColor:    noColor,
		Machine:    machine,
		NoColorEnv: os.Getenv("NO_COLOR") != "",
		Mode:       mode,
	})

	width := 0
	if !tty {
		width = 100
	}

	// Анимации — строго после решения о цвете: они требуют цвета.
	animations := animsEnabled(animEnv{
		Color:     color,
		Machine:   machine,
		ASCII:     ascii,
		UnicodeOK: unicodeOK,
		NoAnimEnv: os.Getenv("GCLI_NO_ANIM") != "",
		Cfg:       a.repo.Cfg.Animations,
	})

	a.ui = ui.New(ui.Options{
		Theme:      "ember", // стиль один — «УГОЛЬ»
		Unicode:    unicodeOK,
		Color:      color,
		Animations: animations,
		Compact:    compact,
		ShowTime:   a.cfgShowTime(),
		Width:      width,
		Grade:      grade,
	})
}

func (a *app) cfgShowTime() bool {
	if a.repo != nil {
		return a.repo.Cfg.ShowTime
	}
	return false
}

// loadInitialSession — определить стартовую сессию по флагам.
func (a *app) loadInitialSession(resume int, cont bool) *core.Session {
	if resume > 0 {
		ss := a.repo.ListSessions()
		if resume <= len(ss) {
			if s, err := a.repo.LoadSession(ss[resume-1].ID); err == nil {
				return s
			}
		}
		if !a.quiet {
			a.ui.Warn(fmt.Sprintf("сессия №%d не найдена — создаю новую", resume))
		}
	}
	if cont {
		ss := a.repo.ListSessions()
		if len(ss) > 0 {
			if s, err := a.repo.LoadSession(ss[0].ID); err == nil {
				return s
			}
		}
	}
	return a.repo.NewSession(a.prov.ID, a.model, a.workDir)
}

func (a *app) saveSession() {
	if a.sess == nil {
		return
	}
	sessMu.Lock()
	a.sess.Updated = time.Now()
	_ = a.repo.SaveSession(a.sess)
	sessMu.Unlock()
}

// buildTools — собрать реестр инструментов.
func (a *app) buildTools() {
	// Замечания относятся к конкретной сборке реестра. Без сброса повторная
	// сборка (смена песочницы, /ext trust) дописывала бы их в конец старого
	// списка, и пользователь увидел бы предупреждение о коде, который уже
	// подтвердили. Замечания самого запуска (песочница) переживают сборку.
	a.setupNotes = append([]string(nil), a.startupNotes...)
	env := tools.Env{
		WorkDir:    a.workDir,
		ReadFiles:  map[string]bool{},
		Sandbox:    a.sandbox,
		Depth:      0,
		MaxDepth:   core.Clamp(a.repo.Cfg.SubMaxDepth, 1, 3),
		HTTPClient: tools.NewHTTPClient(),
		SkillsOff:  a.repo.Cfg.SkillsOff,
		Record:     a.recordCheckpoint,
		Confirm:    a.confirm,
		Session:    a,
		OnTodo:     a.onTodos,
		OnNote:     a.onNote,
		Spawn:      a.spawnAgent,
		Agents:     a.agentsInfo,
		Ask:        a.askUser,
		Self:       a.selfReport,
		OnProgress: a.onProgress,
		// Доверие к коду из проекта. Хранилище всегда есть, даже если файл
		// согласий пуст: тогда «нет согласия» и «хранилища нет» — одно и то же.
		Trust: tools.NewTrustStore(a.store.Root),
	}
	a.tools = tools.New(env)
	a.tools.RegisterSkills()
	a.tools.RegisterVision()
	if a.repo.Cfg.Subagents {
		a.tools.RegisterSubagentTools()
	}
	a.tools.RegisterExtTools()
	if n, warns := a.tools.RegisterMCP(); n > 0 && !a.quiet {
		a.setupNotes = append(a.setupNotes, fmt.Sprintf("MCP: подключено инструментов — %d", n))
	} else {
		for _, w := range warns {
			a.setupNotes = append(a.setupNotes, w)
		}
	}
	a.reportPendingCode()
}

// reportPendingCode — сказать при старте, что код из проекта ждёт подтверждения.
//
// Молчать нельзя: иначе выглядит так, будто расширений и MCP-серверов просто
// нет, а на самом деле их инструменты не подключены — и агент будет
// уверенно говорить «такого инструмента у меня нет».
func (a *app) reportPendingCode() {
	if a.quiet {
		return
	}
	pending := a.tools.ScanProjectCode()
	if len(pending) == 0 {
		return
	}
	names := make([]string, 0, len(pending))
	for _, w := range pending {
		names = append(names, w.Label())
	}
	kind := "/ext trust"
	if len(pending) == 1 && pending[0].Kind == "mcp" {
		kind = "/mcp trust"
	}
	a.setupNotes = append(a.setupNotes, fmt.Sprintf(
		"код из проекта ждёт подтверждения: %s (подтвердить: %s <имя>)",
		core.Truncate(strings.Join(names, ", "), 70), kind))
}

// setupSandbox — решить, включать ли песочницу, и собрать её.
//
// Три источника, по убыванию приоритета: флаг -sandbox, переменная
// GCLI_SANDBOX, значение из конфига. Пока режим не задан явно, песочница
// выключена: ломать привычное поведение без просьбы нельзя, а вот для
// чужого репозитория её включают руками (-sandbox on) или переменной
// окружения, чтобы не набирать флаг каждый раз.
func (a *app) setupSandbox(flagVal string) *tools.Sandbox {
	mode := strings.ToLower(strings.TrimSpace(flagVal))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(os.Getenv("GCLI_SANDBOX")))
	}
	if mode == "" {
		if a.repo.Cfg.Sandbox {
			mode = "on"
		} else {
			return nil
		}
	}
	if mode == "off" || mode == "0" || mode == "false" || mode == "выкл" {
		return nil
	}

	sb := tools.NewSandbox(a.workDir).WithDeny(tools.DefaultDeny()...)
	// Каталог данных gcli закрываем всегда, даже когда он внутри проекта:
	// там config.json с ключами API, и песочница не должна превращаться в
	// возможность его прочитать.
	if home := a.store.Root; home != "" {
		sb.WithDeny(home)
	}
	if !a.quiet {
		a.startupNotes = append(a.startupNotes,
			"Песочница: пути ограничены рабочим каталогом, секреты закрыты (выход: /sandbox off)")
	}
	return sb
}

// buildPool — собрать пул субагентов.
func (a *app) buildPool() {
	// Runner оборачивается слоем прочности: временный обрыв сети и
	// негодный (пустой/обрезанный) отчёт — это не повод оставлять
	// главного агента без результата. Раньше такие сбои доходили до
	// него как есть, и он получал обрывок вместо ответа.
	runner := a.runSubagent
	if a.repo.Cfg.Subagents {
		rs := subagents.DefaultResilience()
		if n := a.repo.Cfg.SubRetries; n > 0 {
			rs.Attempts = core.Clamp(n, 1, 3)
		}
		runner = subagents.Wrap(runner, rs)
	}
	a.pool = subagents.NewPool(runner, subagents.PoolOptions{
		MaxParallel: core.Clamp(a.repo.Cfg.SubMaxPar, 1, 8),
		MaxDepth:    core.Clamp(a.repo.Cfg.SubMaxDepth, 1, 3),
		Enabled:     a.repo.Cfg.Subagents,
		WorkDir:     a.workDir,
	})
}

// installSignalHandler — Ctrl+C: прервать генерацию, повторное — выход.
func (a *app) installSignalHandler() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		for range ch {
			a.mu.Lock()
			c := a.cancel
			running := a.running
			a.mu.Unlock()
			if running && c != nil {
				c()
				continue
			}
			if !a.quiet {
				a.ui.Println("")
				a.ui.Info("до связи!")
			}
			a.saveSession()
			os.Exit(130)
		}
	}()
}

// SetTodos/Todos — интерфейс сессии для инструментов.
func (a *app) SetTodos(todos []tools.TodoItem) {
	sessMu.Lock()
	a.sess.Todos = a.sess.Todos[:0]
	for _, t := range todos {
		a.sess.Todos = append(a.sess.Todos, core.Todo{Content: t.Content, Status: t.Status})
	}
	sessMu.Unlock()
	a.saveSession()
}

func (a *app) Todos() []tools.TodoItem {
	src := a.sessionTodos()
	out := make([]tools.TodoItem, 0, len(src))
	for _, t := range src {
		out = append(out, tools.TodoItem{Content: t.Content, Status: t.Status})
	}
	return out
}

// sessionTodos — копия плана для безопасного чтения из UI-потока.
func (a *app) sessionTodos() []core.Todo {
	sessMu.Lock()
	defer sessMu.Unlock()
	out := make([]core.Todo, len(a.sess.Todos))
	copy(out, a.sess.Todos)
	return out
}

func (a *app) onTodos(todos []tools.TodoItem) {
	if a.quiet {
		return
	}
	a.ui.RenderTodos(a.sessionTodos())
}

func (a *app) onNote(title, body string) {
	a.mu.Lock()
	a.notes = append(a.notes, title+": "+core.Truncate(core.OneLine(body), 200))
	if len(a.notes) > 30 {
		a.notes = a.notes[len(a.notes)-30:]
	}
	a.mu.Unlock()
	a.saveSession()
}

func (a *app) notesText() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return subagents.Notes(a.notes)
}

// ---------- Размышления: копятся молча, показывает пользователь ----------
//
// Правила показа, коротко:
//
//	состояние           meaning
//	reasonLive          пользователь хочет видеть размышления (Ctrl+O)
//	reasonOpen          на экране открыт поток (маркер напечатан)
//	reasonPrintedLen    сколько байт буфера уже на экране
//
// Печать всегда под reasonPrint: ею пользуются и стриминг, и Ctrl+O.
// reasonOpen держим честным — он равен «маркер напечатан и ещё не закрыт»,
// а не «пользователь что-то просил»: строка ожидания могла уже погаснуть
// сама, и тогда ThinkChunk в ThinkMarker поставит лишний заголовок.

// onReason — кусок размышлений модели.
//
// Показ по умолчанию выключен. Куски всегда копятся в буфере (иначе показать
// их задним числом было бы нечем), а на экран попадают только если
// пользователь включил показ по Ctrl+O. Показать накопленное целиком —
// работа toggleReason; здесь мы дописываем кусок в буфер и, если показ уже
// включён, печатаем его в открытый поток.
//
// Метод зовётся из горутины стриминга, поэтому всё состояние — под
// reasonMu, порядок печати — под reasonPrint.
func (a *app) onReason(s string) {
	if s == "" || a.quiet {
		return
	}
	a.reasonPrint.Lock()
	defer a.reasonPrint.Unlock()

	a.reasonMu.Lock()
	a.reasonBuf.WriteString(s)
	a.reasonAny = true
	live := a.reasonLive
	open := a.reasonOpen
	a.reasonMu.Unlock()

	if !live {
		// Пользователь ещё не просил показывать: молча копим и подсказываем,
		// что посмотреть можно. Подсказку ставим даже при свернутом показе —
		// свернул он сам, значит знает про Ctrl+O.
		a.hintReason()
		return
	}
	if !open {
		// Пользователь нажал Ctrl+O до первого куска: поток ещё не открыт,
		// печатать в него рано — сначала маркер, иначе мысль уйдёт в строку
		// ответа без заголовка.
		a.openThinkStream()
	}
	a.ui.ThinkChunk(s)
	a.markReasonPrinted(len(s))
}

// openThinkStream — открыть на экране поток размышлений: погасить строку
// ожидания и напечатать маркер события. Дальше текст печатается через
// ThinkChunk, и перевод строки закрывает ThinkEnd.
//
// reasonPrint здесь уже взят: блокировки строго в порядке
// reasonPrint → reasonMu → UI, поэтому вложиться в свой же мьютекс нельзя.
func (a *app) openThinkStream() {
	a.reasonMu.Lock()
	already := a.reasonOpen
	a.reasonOpen = true
	a.reasonMu.Unlock()
	if already {
		return
	}
	a.ui.ThinkMarker()
}

// closeThinkStream — свернуть открытый на экране поток размышлений.
//
// Модель перешла к ответу (или к следующей итерации с инструментами):
// закрываем поток переводом строки, иначе ответ допишется в хвост мысли.
// Накопленное не выбрасываем — Ctrl+O покажет его снова целиком.
//
// Здесь reasonPrint не нужен: перевод строки ничего не печатает из буфера,
// а закрыть поток после живого куска нельзя — кусок приходит из другой
// горутины, и «свернуть» против неё бессмысленно.
func (a *app) closeThinkStream() {
	a.reasonMu.Lock()
	open := a.reasonOpen
	a.reasonOpen = false
	a.reasonMu.Unlock()
	if open && !a.quiet {
		a.ui.ThinkEnd()
	}
}

// hintReason — показать на строке ожидания подсказку про Ctrl+O, один раз.
func (a *app) hintReason() {
	a.reasonMu.Lock()
	if a.reasonHinted || a.reasonShown {
		a.reasonMu.Unlock()
		return
	}
	a.reasonHinted = true
	a.reasonMu.Unlock()
	a.ui.SetSpinHint(reasonHint)
}

// markReasonPrinted — отметить, что в конец буфера показано n байт.
func (a *app) markReasonPrinted(n int) {
	a.reasonMu.Lock()
	defer a.reasonMu.Unlock()
	a.reasonPrintedLen += n
}

// reasonText — накопленные размышления текущего хода.
func (a *app) reasonText() string {
	a.reasonMu.Lock()
	defer a.reasonMu.Unlock()
	return a.reasonBuf.String()
}

// toggleReason — Ctrl+O: показать размышления или свернуть их обратно.
//
// Первое нажатие показывает накопленное и включает показ дальше по ходу,
// повторное сворачивает поток и останавливает показ. Накопленное при этом
// не выбрасывается: «скрыть» значит «убрать с глаз», а не «выбросить».
func (a *app) toggleReason() {
	if a.quiet {
		return
	}
	// Ждём конца текущей печати: иначе Ctrl+O напечатает накопленное, а
	// пришедший следом кусок допишется в начало потока, а отметка
	// «показано N байт» окажется оптимистичной.
	a.reasonPrint.Lock()
	defer a.reasonPrint.Unlock()

	a.reasonMu.Lock()
	a.reasonShown = true
	hasAny := a.reasonAny
	open := a.reasonOpen
	// Модель ещё думает: переключаем показ. Уже открытый поток на экране —
	// повод свернуть. Показывать нечего — значит нажали на середине первой
	// секунды: тогда показ включаем, иначе человек так и прождёт всю мысль.
	show := !hasAny || !a.reasonLive || !open
	if !hasAny {
		// Показать нечего: воля пользователя сохраняется (он ждёт мыслей),
		// но поток на экране не открываем — печатать всё равно нечего.
		a.reasonLive = true
		a.reasonMu.Unlock()
		a.ui.Info("размышлений пока нет — модель ещё думает")
		return
	}
	if show {
		a.reasonLive = true
		fresh := ""
		// Печатаем только невыведенное: повторное Ctrl+O не должен
		// печатать заново то, что уже висело на экране.
		full := a.reasonBuf.String()
		if from := a.reasonPrintedLen; from < len(full) {
			fresh = full[from:]
			a.reasonPrintedLen = len(full)
		}
		a.reasonOpen = true
		a.reasonMu.Unlock()
		if !open {
			// Поток мог быть ещё не открыт: маркер нужен здесь, иначе
			// ThinkHidden откроет его сам и заголовок встанет не туда.
			a.ui.ThinkMarker()
		}
		if fresh != "" {
			a.ui.ThinkHidden(fresh)
		}
		return
	}
	a.reasonLive, a.reasonOpen = false, false
	a.reasonMu.Unlock()
	// Перевод строки обязателен: иначе ответ модели допишется в хвост мысли.
	a.ui.ThinkEnd()
}

// reasonShownAll — показывали ли уже весь накопленный блок в этом ходу
// (чтобы не печатать один и тот же текст дважды).
func (a *app) reasonShownAll() bool {
	a.reasonMu.Lock()
	defer a.reasonMu.Unlock()
	return a.reasonAny && a.reasonPrintedLen >= a.reasonBuf.Len()
}

// resetReason — новый ход: буфер и состояние хода обнуляются, воля
// пользователя (reasonLive) сохраняется.
func (a *app) resetReason() {
	a.reasonMu.Lock()
	a.reasonBuf.Reset()
	a.reasonShown = false
	a.reasonAny = false
	a.reasonHinted = false
	a.reasonOpen = false
	a.reasonPrintedLen = 0
	a.reasonMu.Unlock()
}

// turnWatcher — следить за горячими клавишами, пока идёт ход.
//
// Единственный читатель keys: вне хода канал просто копит байты (см.
// stdinReader.Keys), поэтому Ctrl+O, нажатый между ходами, не теряется и не
// съедает первую букву запроса. Работает и в -p режиме, где stdin свободен.
func (a *app) turnWatcher(done <-chan struct{}) {
	if a.stdin == nil {
		return
	}
	for {
		select {
		case <-done:
			return
		case b, ok := <-a.stdin.Keys():
			if !ok {
				return
			}
			if b == ctrlO {
				a.toggleReason()
			}
		}
	}
}

// turn — один ход: пользовательский ввод → агентный цикл.
func (a *app) turn(text string) error {
	if os.Getenv("GCLI_DEBUG_BODY") != "" {
		fmt.Fprintf(os.Stderr, "TURN IN: %q (len=%d)\n", text, len(text))
	}
	text = tools.ApplyMentions(a.workDir, text)
	if a.sess.Title == "" {
		a.sess.Title = core.Truncate(core.OneLine(text), 60)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.cancel = cancel
	a.running = true
	a.mu.Unlock()
	// Размышления нового хода начинаются с чистого листа; включённость
	// показа (Ctrl+O) при этом сохраняется — она настройка пользователя,
	// а не состояние хода.
	a.resetReason()
	defer func() {
		a.mu.Lock()
		a.running = false
		a.cancel = nil
		a.mu.Unlock()
		cancel()
		a.saveSession()
	}()

	ag := a.newAgent(ctx, false)
	// Кот работает: строка «thinking...» с его мордочкой переливается
	// под эхом задачи. Первый delta гасит строку, конец хода — тоже.
	// Поза кота над строкой состояния не трогаем: после Enter курсор
	// уехал вниз, и перерисовка «вслепую» попадала по чужим строкам.
	if !a.quiet {
		a.ui.MascotIdleStop()
		state, word := ui.MascotThink, "thinking..."
		if a.repo.Cfg.PlanMode {
			state, word = ui.MascotPensive, "planning..."
		}
		a.ui.RequestEcho(text)
		a.ui.SetSpinnerMascot(state)
		a.ui.ShimmerStart(word, state)
	}
	// Ctrl+O работает весь ход: слушаем клавиши и с самого начала, иначе
	// показать размышления первой секунды ожидания было бы нечем.
	stopKeys := make(chan struct{})
	keysDone := make(chan struct{})
	go func() {
		defer close(keysDone)
		a.turnWatcher(stopKeys)
	}()
	defer func() {
		close(stopKeys)
		<-keysDone
	}()
	err := ag.Run(ctx, text)
	a.ui.SpinnerStop()
	a.ui.SetSpinnerMascot(ui.MascotWork)
	return err
}

// newAgent — собрать агента с UI-обработчиками.
func (a *app) newAgent(ctx context.Context, quiet bool) *agent.Agent {
	stream := a.ui.NewStream()
	a.stream = stream
	thinking := false

	ag := agent.New(agent.Deps{
		Providers: a.client,
		Provider:  a.prov,
		Registry:  a.tools,
		Session:   a,
		Model:     a.model,
		Think:     providers.ThinkState(a.repo.Cfg),
		Memory:    a.memory.Collect,
		SkillsPrompt: func() string {
			return a.tools.SkillsPromptBlock()
		},
		MaxIters:    a.maxIters(),
		AutoCompact: a.autoCompactLimit(),
		Parallel:    a.repo.Cfg.ParallelTools,
		Quiet:       quiet || a.quiet,
		OnDelta: func(s string) {
			// Первый кусок ответа — маскот умолкает и уступает место тексту.
			a.ui.SpinnerStop()
			if thinking {
				if !quiet && !a.quiet {
					a.ui.ThinkEnd()
				}
				thinking = false
			}
			// Ответ пошёл — поток размышлений закрыт. Дальше модель
			// размышляет уже по ходу инструментов (в agent-режиме), и
			// Ctrl+O снова покажет накопленное.
			a.closeThinkStream()
			stream.Write(s)
		},
		OnReason:    a.onReason,
		OnToolStart: a.onToolStart,
		OnToolDone:  a.onToolDone,
		OnUsage:     a.onUsage,
	}, a.workDir)
	ag.WithAgentMode(a.sess.AgentMode)
	ag.Notes = a.notesText()
	ag.OnNote = a.onNote
	a.mu.Lock()
	a.lastAgent = ag
	a.mu.Unlock()
	if a.repo.Cfg.PlanMode {
		ag.System = planModeSystem
	}
	_ = ctx
	return ag
}

func (a *app) maxIters() int {
	if a.repo.Cfg.MaxIters > 0 {
		return core.Clamp(a.repo.Cfg.MaxIters, 1, 200)
	}
	return agent.DefaultMaxIters
}

func (a *app) autoCompactLimit() int {
	if a.repo.Cfg.AutoCompact > 0 {
		return a.repo.Cfg.AutoCompact
	}
	// По умолчанию — 70% мягкого лимита контекста модели.
	return a.prov.CtxSoftLimit()
}

// onProgress — живой прогресс пакетных операций (multi_*, spawn_agents).
//
// События приходят из горутий инструментов, поэтому здесь только перевод
// типа и передача в UI: тот под своим мьютексом сам разберётся с порядком и
// с гашением строки ожидания. Накапливать события в app здесь нельзя —
// список состояния сессии тоже пишется из разных горутин.
func (a *app) onProgress(ev tools.ProgressEvent) {
	if a.ui == nil {
		return
	}
	a.ui.Progress(ui.ProgressUpdate{
		Title: ev.Title,
		Label: ev.Label,
		Done:  ev.Done,
		Total: ev.Total,
		Ok:    ev.Ok,
		Err:   ev.Err,
		Final: ev.Final,
	})
}

// onToolStart — показать карточку инструмента.
func (a *app) onToolStart(tc core.ToolCall, tool *tools.Tool) {
	a.sess.Stats.RecordTool(false)
	cat := tool.Category
	if cat == "" {
		cat = "exec"
	}
	if a.quiet {
		return
	}
	a.ui.ToolStart(ui.ToolCall{
		Name: tc.Name,
		Args: shortArgs(tc),
		Kind: cat,
	})
}

// onToolDone — показать результат инструмента.
func (a *app) onToolDone(tc core.ToolCall, tool *tools.Tool, res tools.Result, err error, elapsed time.Duration) {
	status := "ok"
	detail := res.Summary
	if err != nil {
		status = "fail"
		detail = err.Error()
	} else if res.Error != "" {
		status = "fail"
		detail = res.Error
	} else if strings.Contains(res.Summary, "отклонено") || strings.Contains(res.Summary, "отменено") {
		status = "denied"
	}
	if status == "fail" {
		a.sess.Stats.RecordTool(true)
	}
	if a.quiet {
		return
	}
	if detail == "" {
		detail = "готово"
	}
	lines := 0
	if res.Text != "" {
		lines = len(core.SplitLines(res.Text))
	}
	a.ui.ToolEnd(ui.ToolCall{
		Name:    tc.Name,
		Kind:    tool.Category,
		Detail:  detail,
		Status:  status,
		Elapsed: elapsed,
		Lines:   lines,
	})
}

func (a *app) onUsage(u core.Usage, d time.Duration) {
	// Метрики считаются всегда: на них построены /status и /usage.
	a.sess.Stats.RecordRequest(u, d)
	if a.quiet {
		return
	}
	if u.PromptTokens > 0 || u.CompletionTokens > 0 {
		a.ui.UsageLine(d, u, a.model)
	}
}

// shortArgs — короткая подпись аргументов инструмента для карточки.
func shortArgs(tc core.ToolCall) string {
	m := tools.ParseArgs(tc.Args)
	for _, k := range []string{"command", "path", "pattern", "query", "url", "title", "type", "thought", "input"} {
		if v := tools.ArgStr(m, k); v != "" {
			return core.Truncate(core.OneLine(v), 90)
		}
	}
	if ts := tools.ParseTodos(m); len(ts) > 0 {
		return fmt.Sprintf("%d задач", len(ts))
	}
	if s := tools.ArgStr(m, "task"); s != "" {
		return core.Truncate(core.OneLine(s), 90)
	}
	if s := tools.ArgStr(m, "old_string"); s != "" {
		return core.Truncate(core.OneLine(s), 60)
	}
	return ""
}

// repl — цикл ввода.
//
// Блок «кот + строка состояния» печатается не на каждой итерации, а
// только там, где выше появился новый вывод: после баннера, команды или
// хода. Пустой Enter лишь перерисовывает приглашение — иначе скроллбэк
// забивается одинаковыми котами.
func (a *app) repl() {
	header := true
	for {
		if !a.quiet {
			if header {
				a.ui.Println("")
				// Кот усаживается НАД строкой состояния и живёт на простое:
				// моргает, скучает, засыпает. На любой ввод — замирает.
				a.ui.MascotPerch()
				a.ui.StatusLine(a.statusItems())
				header = false
				// Якорь анимации — только после усадки: курсор стоит на
				// строке приглашения, и перерисовка знает, где кот.
				a.ui.Prompt(a.sess.AgentMode)
				a.ui.MascotIdleStart()
			} else {
				// Перепечатка приглашения после Enter: курсор уехал на
				// строку ниже, старая привязка устарела — кот замирает
				// до следующей усадки. Иначе «слепой» прыжок курсора
				// вверх попадал по строке состояния.
				a.ui.Prompt(a.sess.AgentMode)
			}
		}
		if !a.stdin.Scan() {
			a.ui.MascotIdleStop()
			if !a.quiet {
				a.ui.Println("")
				if a.ui.Mascot() {
					a.mascotBye()
				} else {
					a.ui.Info("до связи!")
				}
			}
			// MCP-серверы — дочерние процессы: гасим перед выходом.
			a.tools.MCPShutdown()
			a.saveSession()
			return
		}
		line := a.stdin.Text()
		// Перенос строки обратным слэшем.
		for strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") && !strings.HasSuffix(line, "\\\\") {
			line = strings.TrimRight(line, " \t")[:len(strings.TrimRight(line, " \t"))-1] + "\n"
			if !a.stdin.Scan() {
				break
			}
			line += a.stdin.Text()
		}
		line = strings.TrimSpace(line)
		// Любой ввод — кот замолкает немедленно, ещё до разбора строки.
		// После Enter терминал уже перевёл курсор на новую строку, и
		// слепая перерисовка успела бы попасть по чужим строкам, пока
		// команда/ход не вызвали MascotIdleStop сами. Раньше команды не
		// останавливали фоновую анимацию до первой печати — окно гонки.
		a.ui.MascotIdleStop()
		if line == "" {
			// Пустой ввод: приглашение уже перерисовано в начале цикла,
			// кот и статус остаются на месте.
			continue
		}
		if strings.HasPrefix(line, "/") {
			if !a.handleCommand(line) {
				a.saveSession()
				return
			}
			header = true // команда печатала вывод — кота сажаем заново
			continue
		}
		if strings.HasPrefix(line, "#") {
			// Заметка в память проекта (GCLI.md) — приём из Claude Code.
			a.appendMemoryNote(line)
			header = true
			continue
		}
		err := a.turn(line)
		if err != nil && !a.quiet {
			a.ui.Err(err.Error())
		}
		if !a.quiet {
			// Итог хода глазами кота: мурчание/подмиг при успехе,
			// фырканье при ошибке, «опять?!» — на второй подряд.
			a.mascotResult(err == nil)
		}
		header = true // ход печатал вывод — кота сажаем заново
		a.mu.Lock()
		a.lastTurnFailed = err != nil
		a.mu.Unlock()
	}
}

// statusItems — элементы нижней строки состояния (стиль Claude Code).
// Порядок повторяет референс: модель · состояние · контекст · режимы.
func (a *app) statusItems() []ui.StatusItem {
	items := []ui.StatusItem{
		{Text: a.model, Kind: ui.StatusMuted},
		{Text: "idle", Kind: ui.StatusAccent},
		{Text: core.Kfmt(core.EstimateContext(a.sess.Messages)) + " ctx", Kind: ui.StatusMuted},
	}
	if a.sess.AgentMode {
		items = append(items, ui.StatusItem{Text: "agent", Kind: ui.StatusAccent})
	} else {
		items = append(items, ui.StatusItem{Text: "chat", Kind: ui.StatusMuted})
	}
	if a.repo.Cfg.PlanMode {
		items = append(items, ui.StatusItem{Text: "plan", Kind: ui.StatusWarn})
	}
	if a.sess.Perms.AutopilotAll {
		items = append(items, ui.StatusItem{Text: "autopilot: all", Kind: ui.StatusErr})
	} else if a.sess.Perms.Autopilot {
		items = append(items, ui.StatusItem{Text: "autopilot", Kind: ui.StatusAccent})
	} else if a.sess.Perms.BashAll {
		items = append(items, ui.StatusItem{Text: "yolo", Kind: ui.StatusWarn})
	}
	if !a.prov.HasKey() {
		items = append(items, ui.StatusItem{Text: "нет ключа — /setup", Kind: ui.StatusErr})
	}
	return items
}

// osMascotEnv — значение GCLI_MASCOT для решения о маскоте.
func osMascotEnv() string { return os.Getenv("GCLI_MASCOT") }

// unicodeSupported — умеет ли терминал показывать псевдографику.
func unicodeSupported() bool {
	if os.Getenv("GCLI_ASCII") != "" {
		return false
	}
	enc := strings.ToLower(os.Getenv("LC_ALL") + os.Getenv("LC_CTYPE") + os.Getenv("LANG"))
	if enc != "" {
		if strings.Contains(enc, "utf-8") || strings.Contains(enc, "utf8") {
			return true
		}
		return false
	}
	// На Windows 10+ и в современных терминалах — да.
	return true
}

func isTerminal() bool { return ui.IsTerminal() }
