package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"gcli/core"
)

// ANSI-коды.
const (
	cReset   = "\033[0m"
	cBold    = "\033[1m"
	cDim     = "\033[2m"
	cItalic  = "\033[3m"
	cReverse = "\033[7m"
	cRed     = "\033[31m"
	cGreen   = "\033[32m"
	cYellow  = "\033[33m"
	cBlue    = "\033[34m"
	cMagenta = "\033[35m"
	cCyan    = "\033[36m"
	cGray    = "\033[90m"
	cWhite   = "\033[37m"
	cBRed    = "\033[91m"
	cBGreen  = "\033[92m"
	cBYellow = "\033[93m"
	cBBlue   = "\033[94m"
	cBMag    = "\033[95m"
	cBCyan   = "\033[96m"
)

// Palette - семантические цвета темы.
type Palette struct {
	Accent  string
	Accent2 string
	OK      string
	Warn    string
	Err     string
	Info    string
	Muted   string
	Text    string
	Head    string
	Code    string
	Link    string
	Added   string
	Removed string
	Hunk    string
}

// Тема одна - «УГОЛЬ» (ember): ромб-маркер, зубец результата, охра.
// Имя параметры theme в Options сохранено для совместимости: normalizeTheme
// сводит любые старые имена к единственному стилю.

// PaletteFor - палитра темы.
func PaletteFor(theme string) Palette {
	return ThemeEmber
}

// Writer - абстракция вывода (для тестов и перенаправления в stderr).
type Writer interface {
	Write(p []byte) (int, error)
}

// Options - параметры интерфейса.
type Options struct {
	Theme      string
	Unicode    bool
	Color      bool
	Animations bool
	Compact    bool
	ShowTime   bool
	Width      int
	Out        Writer
	// Colors - hex-цвета темы (пусто = цвета темы по умолчанию).
	Colors ThemeColors
	// Grade - принудительный уровень цвета (0 = определить автоматически).
	Grade ColorLevel
	// Panels - рисовать блоки и таблицы в скруглённых рамках (стиль
	// OpenCode). Приложение включает это всегда: рамка отделяет данные
	// от потока ответа, а без неё всё сливается в один текст.
	Panels bool
}

// sinkBox - обёртка для atomic.Pointer: хранить интерфейс напрямую в
// atomic.Value нельзя, разные конкретные типы привели бы к панике.
type sinkBox struct{ s Sink }

// UI - терминальный интерфейс.
type UI struct {
	mu    sync.Mutex
	opts  Options
	pal   Palette
	g     Glyphs
	out   Writer
	spins []string

	// sinkRef - приёмник полноэкранного интерфейса. Атомарный: вывод идёт
	// из горутин агента и субалгентов, а подключается при старте цикла.
	sinkRef atomic.Pointer[sinkBox]
	// width - ширина вывода. Атомарная по той же причине: фоновая
	// горутина может печатать, пока интерфейс меняет ширину под TUI.
	width atomic.Int64

	// Уровень цвета: 16 / 256 / truecolor (определяется при создании).
	grade colorLevel
	// railDepth - глубина вложенности рельса инструментов (0 - рельс выключен).
	railDepth int

	// Состояние спиннера.
	spin     bool
	spinStop chan struct{}
	spinDone chan struct{}
	spinLbl  string
	spinT0   time.Time
	// Строка ожидания. spinShimmer удалён: переливы — избыточная
	// самодеятельность, строка ожидания рисуется текстом.
	// spinHint - короткая подсказка справа от слова, например
	// «(Ctrl+O - показать размышления)». Показывается, только если
	// модель действительно присылает размышления, и исчезает сама,
	// как только модель перешла к ответу.
	spinHint string
	// metrics - последний снимок расхода. Нужен живым строкам (строка
	// ожидания), которые перерисовываются каждые 90 мс: там видно, как
	// растёт контекст, пока модель думает.
	metrics Metrics
	// Подавление обычного вывода (например, для -p).
	quiet bool
	// think - открыт ли поток размышлений: пока модель думает, на экране
	// висит «указатель», и первый кусок текста его закрывает.
	think bool
	// thinkPend - недовведённый хвост потока размышлений. Стрим размышлений
	// печатается построчно: живая область снизу требует, чтобы каждая печать
	// заканчивалась переводом строки.
	thinkPend string

	// ---------- Нижняя живая область (интерфейс Claude Code) ----------
	// Поле ввода в скруглённой рамке, pop-up меню команд, строка ожидания и
	// подсказки живут внизу экрана и перерисовываются при каждой печати
	// (см. live.go). Состояние защищено тем же u.mu, что и весь вывод.
	liveOn          bool
	liveRows        int
	liveInp         string
	liveCur         int
	livePlaceholder string
	liveMenu        []MenuItem
	liveMenuSel     int
	liveFooterL     string
	liveFooterR     string
	liveBusy        string
	liveMark        string
}

// New - создать интерфейс.
func New(opts Options) *UI {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	// Имя темы приводим к канону: алиасы не должны разъезжаться по веткам
	// switch внутри пакета.
	opts.Theme = normalizeTheme(opts.Theme)
	// Уровень цвета: принудительный > автоопределение > 16.
	grade := colorLevel(opts.Grade)
	if grade == colorNone {
		grade = detectColorLevel()
	}
	if !opts.Color {
		grade = colorNone
	}
	// NO_COLOR мог отключить цвет уже после того, как вызывающий выставил
	// Color: иначе спиннер, перелив и градиент печатали бы escape-коды в
	// терминал, который их не ждёт.
	if grade == colorNone {
		opts.Color = false
	}
	u := &UI{opts: opts, out: out, grade: grade, g: GlyphsFor(opts.Theme, opts.Unicode)}
	u.applyPalette()
	u.spins = SpinnerFor(opts.Theme, opts.Unicode)
	if opts.Width > 0 && opts.Width != 100 {
		u.width.Store(int64(opts.Width))
	}
	if u.opts.Width <= 0 {
		u.opts.Width = 100
	}
	return u
}

// applyPalette - пересобрать палитру под текущную тему и уровень цвета.
func (u *UI) applyPalette() {
	pal := PaletteFor(u.opts.Theme)
	if u.grade >= color256 {
		pal = upgradePalette(pal, u.opts.Theme, u.grade)
	}
	applyThemeColors(&pal, u.opts.Colors, u.grade)
	u.pal = pal
}

// SetSink - подключить или отключить приёмник полноэкранного интерфейса.
//
// Зовётся из потока запуска, а читается из горутин агента: доступ
// атомарный, поэтому позднее подключение не гоняется с первым выводом.
func (u *UI) SetSink(s Sink) {
	u.sinkRef.Store(&sinkBox{s: s})
}

// sink - текущий приёмник или nil.
func (u *UI) sink() Sink {
	if b := u.sinkRef.Load(); b != nil {
		return b.s
	}
	return nil
}

// ColorLevel - текущий уровень поддержки цвета.
func (u *UI) ColorLevel() ColorLevel { return ColorLevel(u.grade) }

// AnimationsOn - включён ли флаг анимаций в опциях.
//
// Эффекты появления удалены (строки печатаются как есть), но флаг остался
// в Options и конфиге: вызывающий код (интеграционные тесты запуска)
// проверяет решение про анимации этим методом, не заглядывая в пакет ui.
func (u *UI) AnimationsOn() bool { return u.opts.Animations && u.opts.Color }

// ---------- Базовый вывод ----------

// Width - ширина терминала для вывода.
func (u *UI) Width() int {
	if w := int(u.width.Load()); w > 0 {
		return w
	}
	if w := terminalWidth(); w > 20 {
		u.width.Store(int64(w))
		return w
	}
	return 100
}

// SetWidth - задать ширину вручную.
func (u *UI) SetWidth(w int) {
	if w > 20 {
		u.width.Store(int64(w))
	}
}

// Theme - имя стиля оформления. Тема одна - «УГОЛЬ» (ember).
func (u *UI) Theme() string { return u.opts.Theme }

// SetSpinHint - подсказка справа от слова на строке ожидания
// («(Ctrl+O - показать размышления)»).
//
// Ставится, когда модель действительно присылает размышления: до первого
// куска подсказка была бы враньём, а после него строка ожидания гаснет
// сама. Под мьютексом - строку ожидания рисует отдельная горутина.
func (u *UI) SetSpinHint(h string) {
	u.mu.Lock()
	u.spinHint = h
	u.mu.Unlock()
}

// SpinHint - текущая подсказка строки ожидания (для тестов и отладки).
func (u *UI) SpinHint() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.spinHint
}

// Glyphs - текущий набор псевдографики.
func (u *UI) Glyphs() Glyphs { return u.g }

// Palette - текущая палитра.
func (u *UI) Palette() Palette { return u.pal }

// Палитра - алиас для внешнего кода, привычнее читается чем Palette().
func (u *UI) Pal() Palette { return u.pal }

// c - обернуть в цвет (если цвет выключен - вернуть как есть).
func (u *UI) c(code, s string) string {
	if code == "" || s == "" || !u.opts.Color || u.quiet {
		return s
	}
	return code + s + cReset
}

// Paint - обернуть строку произвольным кодом цвета из палитры.
// Нужен за пределами пакета (например, баннеру), где приватный c недоступен.
func (u *UI) Paint(code, s string) string { return u.c(code, s) }

// Bold - жирный.
func (u *UI) Bold(s string) string { return u.c(cBold, s) }

// Dim - приглушённый.
func (u *UI) Dim(s string) string { return u.c(cDim, s) }

// Italic - курсив.
func (u *UI) Italic(s string) string { return u.c(cItalic, s) }

// Red - красный.
func (u *UI) Red(s string) string { return u.c(u.pal.Err, s) }

// Green - зелёный.
func (u *UI) Green(s string) string { return u.c(u.pal.OK, s) }

// Yellow - жёлтый.
func (u *UI) Yellow(s string) string { return u.c(u.pal.Warn, s) }

// Blue - синий.
func (u *UI) Blue(s string) string { return u.c(u.pal.Info, s) }

// Magenta - пурпурный.
func (u *UI) Magenta(s string) string { return u.c(u.pal.Accent2, s) }

// Cyan - бирюзовый (акцент).
func (u *UI) Cyan(s string) string { return u.c(u.pal.Accent, s) }

// Gray - серый.
func (u *UI) Gray(s string) string { return u.c(u.pal.Muted, s) }

// Head - заголовок: просто жирный, без цвета. Цветные заголовки — то,
// от чего пришлось уйти: на 16-цветных консолях они превращают интерфейс
// в «вырвиглазную» тему.
func (u *UI) Head(s string) string { return u.Bold(s) }

// Accent - основной акцент.
func (u *UI) Accent(s string) string { return u.c(u.pal.Accent, s) }

// Write - необработанная запись (с очисткой спиннера).
func (u *UI) Write(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearSpinLocked()
	u.put(s)
}

// put - единственная точка, где интерминал получает байты.
//
// Здесь, а не в каждом вызывающем, чинится невалидный UTF-8. В поток
// попадает чужой текст: вывод инструмента, содержимое файла, ответ
// модели. Файл в windows-1251 - обычное дело (выгрузка из 1С, старый
// .txt), и его байты, попав в UTF-8-консоль, дают мусор на весь вывод.
//
// Замена на U+FFFD вместо молчаливого пропуска: пропуск склеивает слова
// («п??ивет» → «ппивет»), а знак замены прямо показывает, где потерялось.
// Валидная строка возвращается как есть, без копии.
//
// Обёртка именно здесь, а не в Write, потому что Write держит блокировку
// и гасит строку ожидания, а спиннер и прогресс пишут в u.out
// напрямую. Отдельная точка нужна, чтобы ни один путь её не обошёл.
func (u *UI) put(s string) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, string(utf8.RuneError))
	}
	if sink := u.sink(); sink != nil {
		sink.Text(s)
		return
	}
	if u.liveOn {
		// Живая область внизу экрана: стереть регион, напечатать строки,
		// нарисовать регион заново (см. live.go).
		u.putLiveLocked(s)
		return
	}
	_, _ = u.out.Write([]byte(s))
}

// Print - запись с очисткой спиннера.
func (u *UI) Print(s string) { u.Write(s) }

// Printf - форматированная запись.
func (u *UI) Printf(f string, a ...any) { u.Write(fmt.Sprintf(f, a...)) }

// Println - строка + перевод строки.
//
// В терминале строка начинается с «возврата каретки»: она всегда должна
// начинаться с нулевой колонки, даже если курсор остался где-то посреди
// строки. Так случается, когда спиннер
// оставил его не там, где ожидалось, - и без этого ответ модели уезжал
// в середину экрана.
//
// В пайп управляющий символ не попадает: там вывод разбирают машины, и
// лишний «CR» в начале строки - мусор.
func (u *UI) Println(s string) { u.Write(u.lineStart() + s + "\n") }

// lineStart - начало строки для терминала.
//
// Это управляющий символ, а не текст: на экране его не видно, и в
// содержимом строки его нет.
func (u *UI) lineStart() string {
	// Живой области CR не нужен: putLive сам ставит «\r» в начале каждой строки.
	if u.liveOn {
		return ""
	}
	// Полноэкранному интерфейсу CR не нужен: у него своя сетка клеток.
	if u.sink() == nil && u.tty() {
		return "\r"
	}
	return ""
}

// tty - пишем ли мы в терминал.
func (u *UI) tty() bool {
	if u == nil || u.out == nil {
		return false
	}
	f, ok := u.out.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// PrintEmpty - пустая строка (пропускается в компактном режиме).
func (u *UI) PrintEmpty() {
	if u.opts.Compact {
		return
	}
	u.Write("\n")
}

// RawWrite - запись без очистки спиннера (для escape-последовательностей).
func (u *UI) RawWrite(s string) {
	if snk := u.sink(); snk != nil {
		// Сырой канал: OSC52 и другие управляющие последовательности
		// должны дойти до терминала как есть. Стриппер ANSI уничтожил бы
		// их, а в кадре полноэкранного интерфейса им делать нечего.
		if r, ok := snk.(interface{ Raw(string) }); ok {
			r.Raw(s)
			return
		}
		if txt := stripANSI(s); txt != "" {
			snk.Text(txt)
		}
		return
	}
	// Сырые последовательности (OSC52) идут мимо живой области напрямую:
	// это управляющие байты, а не контент, и курсор они не двигают.
	u.mu.Lock()
	defer u.mu.Unlock()
	_, _ = u.out.Write([]byte(s))
}

// ClearScreen - очистить экран терминала.
func (u *UI) ClearScreen() {
	if sink := u.sink(); sink != nil {
		sink.Clear()
		return
	}
	u.RawWrite("\033[2J\033[H")
}

// ---------- Сообщения ----------

// Err - ошибка: красный маркер события перед текстом.
func (u *UI) Err(s string) { u.Println("  " + u.c(u.pal.Err, u.g.Dot) + " " + u.Red(s)) }

// Ok - успех: зелёный маркер события.
func (u *UI) Ok(s string) { u.Println("  " + u.c(u.pal.OK, u.g.Dot) + " " + s) }

// Warn - предупреждение: янтарный маркер события.
func (u *UI) Warn(s string) { u.Println("  " + u.c(u.pal.Warn, u.g.Dot) + " " + s) }

// Info - информация: приглушённый маркер события.
func (u *UI) Info(s string) { u.Println("  " + u.c(u.pal.Muted, u.g.Dot) + " " + u.Gray(s)) }

// Step - шаг процесса.
func (u *UI) Step(s string) { u.Println("  " + u.Gray(u.g.Arrow) + " " + s) }

// AutoOK - строка «автопилот одобрил изменение файла».
func (u *UI) AutoOK(path string) {
	p := truncate(core.OneLine(path), 72)
	u.Println("  " + u.c(u.pal.OK, u.g.Check) + " " + u.Gray(u.c(u.pal.Muted, "w ")+p) +
		"  " + u.AutoTag())
}

// AutoTag - метка автопилота.
func (u *UI) AutoTag() string {
	return u.c(u.pal.Accent2, "автопилот")
}

// UserEcho - эхо запроса пользователя: «> текст». Как в Claude Code, запрос
// остаётся в стенке вывода: поле ввода стирается при отправке, и без эха
// сообщение исчезало бы с экрана. Многострочный запрос печатается целиком:
// первая строка - с маркером, продолжения - с отступом.
func (u *UI) UserEcho(text string) {
	if u.quiet || text == "" {
		return
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	u.Println(u.c(u.pal.Accent, ">") + " " + lines[0])
	for _, l := range lines[1:] {
		u.Println("  " + l)
	}
}

// Виды элементов строки статуса.
const (
	StatusMuted = iota
	StatusAccent
	StatusOK
	StatusWarn
	StatusErr
)

// StatusItem - один элемент строки статуса.
type StatusItem struct {
	Text string
	Kind int
}

// Prompt2 - приглашение при чтении ответа пользователя.
func (u *UI) Prompt2(question, hint string) {
	if u.sink() != nil {
		return
	}
	if question != "" {
		u.Print("  " + u.Bold(question) + " ")
	}
	u.Print(u.Gray(hint))
}

// UsageLine - строка расхода под ответом.
//
// Показывает то, ради чего человек ждёт ответа: сколько занял запрос,
// сколько токенов ушло, с какой скоростью и сколько теперь стоит
// контекст в процентах от лимита модели.
func (u *UI) UsageLine(m Metrics) {
	if sink := u.sink(); sink != nil {
		// В новом интерфейсе расход живёт в нижней панели, а под ответом
		// остаётся короткая подпись: время запроса и скорость вывода.
		var parts []string
		if m.Last > 0 {
			parts = append(parts, core.HumanDuration(m.Last))
		}
		if r := m.RateText(); r != "" {
			parts = append(parts, r)
		}
		if m.Prompt > 0 || m.Completion > 0 {
			parts = append(parts, "in "+core.Kfmt(m.Prompt)+" out "+core.Kfmt(m.Completion))
		}
		if len(parts) > 0 {
			sink.SetMeta(strings.Join(parts, "  |  "))
		}
		return
	}
	var parts []string
	if m.Last > 0 {
		parts = append(parts, u.c(u.pal.Muted, "запрос "+core.HumanDuration(m.Last)))
	}
	if r := m.RateText(); r != "" {
		parts = append(parts, u.c(u.pal.Accent, "вывод "+r))
	}
	if m.Prompt > 0 || m.Completion > 0 {
		parts = append(parts, u.c(u.pal.Muted, "in "+core.Kfmt(m.Prompt)+" out "+core.Kfmt(m.Completion)))
	}
	if m.Context > 0 {
		parts = append(parts, u.c(u.pal.Muted, "ctx "+m.ContextText()))
	}
	if m.CostKnown {
		parts = append(parts, u.c(u.pal.Muted, core.FormatUSD(m.Cost)))
	}
	if len(parts) == 0 {
		return
	}
	u.Println("  " + u.emberTail() + " " + strings.Join(parts, u.c(u.pal.Muted, " | ")))
}

// SetCompact - компактный режим (без пустых строк).
func (u *UI) SetCompact(on bool) { u.opts.Compact = on }

// SetQuiet - подавить обычный вывод (машинный режим).
func (u *UI) SetQuiet(on bool) {
	u.quiet = on
	if on {
		u.opts.Animations = false
		u.opts.Color = false
	}
}

// Quiet - включён ли машинный режим.
func (u *UI) Quiet() bool { return u.quiet }

// IsTerminal - является ли stdout терминалом.
func IsTerminal() bool { return isTerminal() }

// RenderTodos - напечатать план задач.
func (u *UI) RenderTodos(todos []core.Todo) {
	if len(todos) == 0 {
		u.Println("  " + u.c(u.pal.Muted, "список задач пуст"))
		return
	}
	done, active := 0, 0
	for _, t := range todos {
		if t.Status == core.TodoCompleted {
			done++
		}
		if t.Status == core.TodoInProgress {
			active++
		}
	}
	bar := u.Bar(done, len(todos), 12)
	u.Println("  " + u.emberMark(u.pal.OK) + " " + u.Bold("План") + " " + bar + " " +
		u.c(u.pal.Muted, fmt.Sprintf("%d/%d", done, len(todos))))
	u.Println("")
	// Сделано - зелёный ромб в тон шапке плана, в работе - охра,
	// не начато - зубец. Отличаются цветом, а не только формой:
	// три похожих значка в одной колонке глаз не различает.
	markDone, markActive, markTodo := u.g.Check, u.g.Arrow, u.g.Bullet
	for _, t := range todos {
		text := truncate(core.OneLine(t.Content), u.Width()-10)
		switch t.Status {
		case core.TodoCompleted:
			u.Println("   " + u.c(u.pal.OK, markDone) + " " +
				u.c(u.pal.Muted, core.StrikeOut(text)))
		case core.TodoInProgress:
			u.Println("   " + u.c(u.pal.Accent, markActive) + " " +
				u.c(u.pal.Accent, text))
		default:
			u.Println("   " + u.c(u.pal.Muted, markTodo) + " " +
				u.c(u.pal.Muted, text))
		}
	}
	if active > 0 {
		u.Println("")
	}
}

// Hint - подсказка.
func (u *UI) Hint(s string) { u.Println("   " + u.Gray(s)) }

// Plain - просто строка с отступом.
func (u *UI) Plain(s string) { u.Println(s) }

// Title - заголовок раздела верхнего уровня: жирный текст, без маркеров
// и линеек — как в Claude Code, где заголовок отличает вес, а не цвет.
func (u *UI) Title(s string) {
	u.Println("")
	u.Println("  " + u.Bold(s))
	u.Println("")
}

// Section - заголовок раздела. Историческое имя, стиль один с Title.
func (u *UI) Section(s string) {
	u.Println("")
	u.Println("  " + u.Bold(s))
	u.Println("")
}

// KV - строка «ключ: значение» с выравниванием.
func (u *UI) KV(k, v string) {
	u.Println("  " + u.Gray(padRight(k, kvWidth)) + v)
}

// kvWidth - ширина колонки ключей в блоках «параметр: значение».
const kvWidth = 18

// KVPairs - напечатать список «ключ: значение» с общим выравниванием.
func (u *UI) KVPairs(rows [][2]string) {
	w := 0
	for _, r := range rows {
		if n := cellWidth(r[0]); n > w {
			w = n
		}
	}
	// +2 - воздух перед значением, чтобы длинные ключи не слипались.
	if w += 2; w < kvWidth {
		w = kvWidth
	}
	for _, r := range rows {
		u.Println("  " + u.Gray(padRight(r[0], w)) + r[1])
	}
}

// Bar - полоса прогресса: у события есть начало (маркер *) и конец
// (зубец >), и полоса читается как отрезок между ними. Заливка блоками
// здесь тяжелее линии.
func (u *UI) Bar(done, total int, width int) string {
	if total <= 0 {
		return ""
	}
	if width < 4 {
		width = 4
	}
	ratio := float64(done) / float64(total)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	full := int(ratio * float64(width))
	rem := ratio*float64(width) - float64(full)
	fullChar := u.g.BarFull
	emptyChar := u.g.BarEmpty
	if fullChar == "" {
		fullChar = barFull
	}
	if emptyChar == "" {
		emptyChar = barEmpty
	}
	s := u.Accent(strings.Repeat(fullChar, full))
	if full < width && rem >= 0.5 {
		s += u.Accent(barPart)
		full++
	}
	if full < width {
		s += u.Gray(strings.Repeat(emptyChar, width-full))
	}
	return s
}

// ---------- Спиннер ----------
//
// Строка ожидания в стиле Claude Code: «| Pondering… (esc to interrupt · 4s
// · ↓ 1.2k tok)». С живой областью она рисуется над полем ввода; без неё -
// перерисовывается на текущей строке терминала.

// SpinnerStart - запустить анимацию ожидания «кадр + метка + время».
func (u *UI) SpinnerStart(label string) {
	if u.sink() != nil {
		return
	}
	if !u.opts.Animations || !u.opts.Color || u.quiet {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.spin {
		return
	}
	u.spin = true
	u.spinLbl = label
	u.spinHint = ""
	u.spinT0 = time.Now()
	u.spinStop = make(chan struct{})
	u.spinDone = make(chan struct{})
	frames := u.spins
	stop, done := u.spinStop, u.spinDone
	if u.liveOn {
		u.liveBusy = label
		u.liveMark = frames[0]
		u.flushLiveLocked()
	}
	go func() {
		defer close(done)
		i := 0
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				u.mu.Lock()
				if !u.spin {
					u.mu.Unlock()
					return
				}
				frame := frames[i%len(frames)]
				i++
				if u.liveOn {
					u.liveMark = frame
					u.flushLiveLocked()
					u.mu.Unlock()
					continue
				}
				label := u.spinLbl
				// Обрезаем, чтобы не ломать строку на узком терминале.
				maxl := u.opts.Width - 14
				if maxl > 8 && cellWidth(label) > maxl {
					label = truncate(label, maxl)
				}
				u.put("\r" + cReset + "\033[K" + u.c(u.pal.Accent, frame+" "+label+"…") +
					u.c(u.pal.Muted, " "+time.Since(u.spinT0).Truncate(time.Second).String()) + cReset)
				u.mu.Unlock()
			}
		}
	}()
}

// ShimmerStart - строка ожидания в стиле Claude Code: случайный глагол
// («| Pondering… (esc to interrupt · 3s)»). Аргумент word сохранён для
// совместимости вызовов и больше не влияет на вид: глагол выбирается
// случайно на каждый ход.
func (u *UI) ShimmerStart(word string) {
	u.ShimmerHint(ccVerb(), "")
}

// ShimmerHint - ShimmerStart с подсказкой справа от глагола.
//
// Подсказка нужна, когда модель молчит в размышления, а пользователь ещё не
// знает, что их можно посмотреть. Показывается только пока строка жива;
// SpinnerStop и clearSpinLocked обнуляют её вместе с анимацией.
func (u *UI) ShimmerHint(word, hint string) {
	if u.sink() != nil {
		// Полноэкранный интерфейс показывает ожидание сам.
		return
	}
	if !u.opts.Animations || !u.opts.Color || u.quiet {
		// Без анимаций и цвета перерисовки нет - но факт ожидания
		// пользователь всё равно должен увидеть. Печатаем один раз и уходим.
		u.Println("  " + word)
		return
	}
	u.mu.Lock()
	if u.spin {
		u.mu.Unlock()
		return
	}
	u.spin = true
	u.spinLbl = word
	u.spinHint = hint
	u.spinT0 = time.Now()
	u.spinStop = make(chan struct{})
	u.spinDone = make(chan struct{})
	stop, done := u.spinStop, u.spinDone
	frames := u.spins
	if u.liveOn {
		u.liveBusy = word
		u.liveMark = frames[0]
		u.flushLiveLocked()
	}
	u.mu.Unlock()

	go func() {
		defer close(done)
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				u.mu.Lock()
				if !u.spin {
					u.mu.Unlock()
					return
				}
				frame := frames[i%len(frames)]
				i++
				if u.liveOn {
					u.liveMark = frame
					u.flushLiveLocked()
					u.mu.Unlock()
					continue
				}
				// Строка ожидания без живой области: «| Pondering… (esc to
				// interrupt · 4s)». Пока модель думает, видно, сколько занято
				// контекста, и есть подсказка про Ctrl+O, если она задана.
				parts := []string{}
				if h := u.spinHint; h != "" {
					parts = append(parts, h)
				}
				parts = append(parts, time.Since(u.spinT0).Truncate(time.Second).String())
				if m := u.metrics; m.Context > 0 {
					parts = append(parts, "ctx "+m.ContextText())
				}
				line := "\r" + cReset + "\033[K" + u.c(u.pal.Accent, frame+" "+u.spinLbl+"…") +
					u.c(u.pal.Muted, " ("+strings.Join(parts, " · ")+")")
				u.put(line)
				u.mu.Unlock()
			}
		}
	}()
}

// SpinnerStop - остановить анимацию и очистить строку.
func (u *UI) SpinnerStop() {
	u.mu.Lock()
	if !u.spin {
		u.mu.Unlock()
		return
	}
	u.spin = false
	u.liveBusy = ""
	u.spinHint = ""
	stop, done := u.spinStop, u.spinDone
	if u.liveOn {
		u.flushLiveLocked()
	} else {
		u.put("\r" + cReset + "\033[K")
	}
	u.mu.Unlock()
	close(stop)
	<-done
}

// clearSpinLocked - остановить анимацию ожидания и очистить её строку.
// Вызывается перед любой содержательной печатью: строка ожидания
// не должна пережить вывод, иначе она перерисуется поверх ответа.
func (u *UI) clearSpinLocked() {
	if !u.spin {
		return
	}
	u.spin = false
	u.liveBusy = ""
	u.spinHint = ""
	stop, done := u.spinStop, u.spinDone
	if u.liveOn {
		u.flushLiveLocked()
	} else {
		u.put("\r" + cReset + "\033[K")
	}
	u.mu.Unlock()
	close(stop)
	<-done
	u.mu.Lock()
}

// ---------- Утилиты ширины ----------

// runeLen - длина строки в рунах.
func runeLen(s string) int { return len([]rune(s)) }

// visibleWidth - ширина строки без ANSI-последовательностей.
func visibleWidth(s string) int { return cellWidth(s) }

// VisibleWidth - экспорт visibleWidth: баннеру и композеру надо мерить
// строки за пределами пакета.
func VisibleWidth(s string) int { return visibleWidth(s) }

// padRight - дополнить строку (учитывая ANSI) пробелами до ширины w.
func padRight(s string, w int) string {
	pad := w - visibleWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// padLeft - дополнить слева.
func padLeft(s string, w int) string {
	pad := w - visibleWidth(s)
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

// padTo - дополнить строку пробелами до ширины w (по рунам, без учёта ANSI).
func padTo(s string, w int) string {
	if p := w - runeLen(s); p > 0 {
		return s + strings.Repeat(" ", p)
	}
	return s
}

// truncate - обрезать строку до n колонок с многоточием.
func truncate(s string, n int) string { return truncateCellsEll(s, n) }

// wrap - разбить текст на строки по ширине (с учётом переносов по словам).
//
// Считаем в колонках, а не в рунах: строка из японского текста в 40 колонок
// содержит 20 иероглифов, и обрезка по рунам давала вдвое более длинные
// строки, которые переносились уже терминалом - ломая рамки таблиц.
func wrap(text string, width int) []string {
	if width < 10 {
		width = 10
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range strings.Fields(para) {
			for cellWidth(word) > width {
				// Слово шире строки - режем по границам колонок.
				if line != "" {
					out = append(out, line)
					line = ""
				}
				cut := truncateCellsRaw(word, width)
				if cut == "" {
					// Широкий символ не помещается даже в пустую строку:
					// отступаем, иначе получился бы бесконечный цикл.
					// Целиком кластер, а не первый рун: иначе ZWJ-семья
					// распалась бы на две строки.
					cut = firstCluster(word)
				}
				out = append(out, cut)
				word = strings.TrimPrefix(word, cut)
			}
			if line == "" {
				line = word
			} else if cellWidth(line)+1+cellWidth(word) <= width {
				line += " " + word
			} else {
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
