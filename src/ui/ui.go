package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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

// Palette — семантические цвета темы.
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

// Тема одна — «УГОЛЬ» (ember): ромб-маркер, зубец результата, охра.
// Имя параметры theme в Options сохранено для совместимости: normalizeTheme
// сводит любые старые имена к единственному стилю.

// PaletteFor — палитра темы.
func PaletteFor(theme string) Palette {
	return ThemeEmber
}

// Writer — абстракция вывода (для тестов и перенаправления в stderr).
type Writer interface {
	Write(p []byte) (int, error)
}

// Options — параметры интерфейса.
type Options struct {
	Theme      string
	Unicode    bool
	Color      bool
	Animations bool
	Compact    bool
	ShowTime   bool
	Width      int
	Out        Writer
	// Colors — hex-цвета темы (пусто = цвета темы по умолчанию).
	Colors ThemeColors
	// Grade — принудительный уровень цвета (0 = определить автоматически).
	Grade ColorLevel
}

// UI — терминальный интерфейс.
type UI struct {
	mu    sync.Mutex
	opts  Options
	pal   Palette
	g     Glyphs
	out   Writer
	spins []string

	// Уровень цвета: 16 / 256 / truecolor (определяется при создании).
	grade colorLevel
	// railDepth — глубина вложенности рельса инструментов (0 — рельс выключен).
	railDepth int

	// Состояние спиннера.
	spin     bool
	spinStop chan struct{}
	spinDone chan struct{}
	spinLbl  string
	spinT0   time.Time
	// spinShimmer — непустое слово включает режим перелива: строка
	// ожидания рисуется как «( -.- ) thinking...» с цветной волной,
	// бегущей слева направо, вместо классического «кадр + метка».
	spinShimmer string

	// Потоковый markdown.
	fence  bool
	fenceL string
	pend   string
	// Текущий «блок» ответа — чтобы печатать в конце красивые рамки.
	answerOpen bool
	// Подавление обычного вывода (например, для -p).
	quiet bool
	// think — открыт ли поток размышлений: пока модель думает, на экране
	// висит «указатель», и первый кусок текста его закрывает.
	think bool
	// mascot — живёт ли маскот «Искра» (кот gcli) в интерфейсе: встречает
	// в баннере, сидит у строки ввода, крутится в спиннере, радуется
	// результату. Настраивается /mascot, config.json ("mascot") и
	// переменной GCLI_MASCOT.
	mascot bool
	// spinMascot — состояние маскота в спиннере (думает / работает).
	spinMascot MascotState
	// Фоновая жизнь кота на простое: моргает, дремлет, засыпает.
	idleOn   bool
	idleStop chan struct{}
	idleDone chan struct{}
	// idleSince — когда кот последний раз сел на пост (MascotIdleStart):
	// от него отсчитывается простой — моргание, скука, сон. Сброс при
	// каждой усадке: кот не должен «засыпать» сразу после баннера или
	// команды только потому, что старый таймер всё ещё тикает.
	idleSince time.Time
	// mascotAnchored — курсор стоит на строке приглашения, и прямо над
	// ним сидит кот со строкой состояния. Фоновая перерисовка трогает
	// экран только при поднятой привязке: любая другая печать (ответ,
	// команда, даже перевод строки) её сбрасывает, и анимация замирает
	// до следующей усадки. Иначе относительный прыжок курсора вверх
	// попадает по чужим строкам — двойные лапы и съеденные символы
	// в строке состояния из старых версий.
	mascotAnchored bool
}

// New — создать интерфейс.
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
	u := &UI{opts: opts, out: out, grade: grade, g: GlyphsFor(opts.Theme, opts.Unicode)}
	u.applyPalette()
	u.spins = SpinnerFor(opts.Theme, opts.Unicode)
	if u.opts.Width <= 0 {
		u.opts.Width = 100
	}
	return u
}

// applyPalette — пересобрать палитру под текущную тему и уровень цвета.
func (u *UI) applyPalette() {
	pal := PaletteFor(u.opts.Theme)
	if u.grade >= color256 {
		pal = upgradePalette(pal, u.opts.Theme, u.grade)
	}
	applyThemeColors(&pal, u.opts.Colors, u.grade)
	u.pal = pal
}

// ColorLevel — текущий уровень поддержки цвета.
func (u *UI) ColorLevel() ColorLevel { return ColorLevel(u.grade) }

// AnimationsOn — разрешены ли сейчас эффекты появления.
//
// Нужен вызывающему коду (например, интеграционным тестам запуска),
// чтобы проверить решение про анимации, не заглядывая в пакет ui.
// Проверяется мягкое условие (canReveal): блочный логотип есть не во
// всех темах, а появление строк — везде.
func (u *UI) AnimationsOn() bool { return u.canReveal() }

// ---------- Базовый вывод ----------

// Width — ширина терминала для вывода.
func (u *UI) Width() int {
	if u.opts.Width > 0 && u.opts.Width != 100 {
		return u.opts.Width
	}
	if w := terminalWidth(); w > 20 {
		u.opts.Width = w
		return w
	}
	return 100
}

// SetWidth — задать ширину вручную.
func (u *UI) SetWidth(w int) {
	if w > 20 {
		u.opts.Width = w
	}
}

// Theme — имя стиля оформления. Тема одна — «УГОЛЬ» (ember).
func (u *UI) Theme() string { return u.opts.Theme }

// SetMascot — включить/выключить маскота. При выключении фоновая
// жизнь кота останавливается: никакого вывода из-под выключателя.
func (u *UI) SetMascot(on bool) {
	if !on {
		u.MascotIdleStop()
	}
	u.mascot = on
}

// Mascot — живёт ли маскот в интерфейсе.
func (u *UI) Mascot() bool { return u.mascot }

// SetSpinnerMascot — задать состояние маскота в спиннере:
// MascotThink, пока модель думает, MascotWork, пока идут инструменты.
func (u *UI) SetSpinnerMascot(s MascotState) { u.spinMascot = s }

// Glyphs — текущий набор псевдографики.
func (u *UI) Glyphs() Glyphs { return u.g }

// Palette — текущая палитра.
func (u *UI) Palette() Palette { return u.pal }

// Палитра — алиас для внешнего кода, привычнее читается чем Palette().
func (u *UI) Pal() Palette { return u.pal }

// c — обернуть в цвет (если цвет выключен — вернуть как есть).
func (u *UI) c(code, s string) string {
	if code == "" || s == "" || !u.opts.Color || u.quiet {
		return s
	}
	return code + s + cReset
}

// Paint — обернуть строку произвольным кодом цвета из палитры.
// Нужен за пределами пакета (например, баннеру), где приватный c недоступен.
func (u *UI) Paint(code, s string) string { return u.c(code, s) }

// Bold — жирный.
func (u *UI) Bold(s string) string { return u.c(cBold, s) }

// Dim — приглушённый.
func (u *UI) Dim(s string) string { return u.c(cDim, s) }

// Italic — курсив.
func (u *UI) Italic(s string) string { return u.c(cItalic, s) }

// Red — красный.
func (u *UI) Red(s string) string { return u.c(u.pal.Err, s) }

// Green — зелёный.
func (u *UI) Green(s string) string { return u.c(u.pal.OK, s) }

// Yellow — жёлтый.
func (u *UI) Yellow(s string) string { return u.c(u.pal.Warn, s) }

// Blue — синий.
func (u *UI) Blue(s string) string { return u.c(u.pal.Info, s) }

// Magenta — пурпурный.
func (u *UI) Magenta(s string) string { return u.c(u.pal.Accent2, s) }

// Cyan — бирюзовый (акцент).
func (u *UI) Cyan(s string) string { return u.c(u.pal.Accent, s) }

// Gray — серый.
func (u *UI) Gray(s string) string { return u.c(u.pal.Muted, s) }

// Head — заголовок (жирный акцентом).
func (u *UI) Head(s string) string { return u.c(u.pal.Head, s) }

// Accent — основной акцент.
func (u *UI) Accent(s string) string { return u.c(u.pal.Accent, s) }

// Write — необработанная запись (с очисткой спиннера).
func (u *UI) Write(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearSpinLocked()
	u.mascotAnchored = false
	_, _ = u.out.Write([]byte(s))
}

// Print — запись с очисткой спиннера.
func (u *UI) Print(s string) { u.Write(s) }

// Printf — форматированная запись.
func (u *UI) Printf(f string, a ...any) { u.Write(fmt.Sprintf(f, a...)) }

// Println — строка + перевод строки.
func (u *UI) Println(s string) { u.Write(s + "\n") }

// PrintEmpty — пустая строка (пропускается в компактном режиме).
func (u *UI) PrintEmpty() {
	if u.opts.Compact {
		return
	}
	u.Write("\n")
}

// RawWrite — запись без очистки спиннера (для escape-последовательностей).
// Как и обычная печать, сбрасывает привязку маскота: после прямого вывода
// позиция курсора заранее неизвестна.
func (u *UI) RawWrite(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.mascotAnchored = false
	_, _ = u.out.Write([]byte(s))
}

// ClearScreen — очистить экран. Отдельный метод, а не голый RawWrite:
// после \033[H курсор прыгает в левый верхний угол, и старая привязка
// маскота стала бы ложной (кот перерисовался бы по стёртому экрану).
func (u *UI) ClearScreen() {
	u.RawWrite("\033[2J\033[H")
}

// ---------- Сообщения ----------

// Err — ошибка.
func (u *UI) Err(s string) { u.Println(" " + u.Red(u.g.Cross) + " " + s) }

// Ok — успех.
func (u *UI) Ok(s string) { u.Println(" " + u.Green(u.g.Check) + " " + s) }

// Warn — предупреждение.
func (u *UI) Warn(s string) { u.Println(" " + u.Yellow(u.g.Excl) + " " + s) }

// Info — информация (приглушённая).
func (u *UI) Info(s string) { u.Println(" " + u.Gray(u.g.Dot) + " " + s) }

// Step — шаг процесса.
func (u *UI) Step(s string) { u.Println(" " + u.Accent(u.g.Arrow) + " " + s) }

// AutoOK — строка «автопилот одобрил изменение файла».
func (u *UI) AutoOK(path string) {
	p := core.OneLine(path)
	if len([]rune(p)) > 72 {
		p = string([]rune(p)[:72]) + "…"
	}
	u.Println("  " + u.c(u.pal.OK, u.g.Check) + " " + u.Gray(u.c(u.pal.Muted, "✎ ")+p) +
		"  " + u.AutoTag())
}

// AutoTag — метка автопилота.
func (u *UI) AutoTag() string {
	return u.c(u.pal.Accent2, "автопилот")
}

// Prompt — приглашение к вводу с индикатором агентного режима.
// Ромб — тот же маркер события, что и у вызовов инструментов:
// приглашение становится полноправной строкой потока, а не отдельным
// украшением. Отступ в две колонки держит поле.
func (u *UI) Prompt(agentMode bool) {
	mark := u.emberMark(u.pal.Muted)
	if agentMode {
		mark = u.emberMark(u.pal.Accent)
	}
	u.Print("  " + mark + " ")
}

// StatusLine — нижняя панель состояния: модель, режим, токены, автопилот.
// Печатается перед приглашением к вводу, как строка статуса в Claude Code.
func (u *UI) StatusLine(items []StatusItem) {
	if u.quiet || len(items) == 0 {
		return
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		text := it.Text
		if text == "" {
			continue
		}
		switch it.Kind {
		case StatusOK:
			text = u.c(u.pal.OK, text)
		case StatusWarn:
			text = u.c(u.pal.Warn, text)
		case StatusErr:
			text = u.c(u.pal.Err, text)
		case StatusAccent:
			text = u.c(u.pal.Accent, text)
		default:
			text = u.c(u.pal.Muted, text)
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return
	}
	sep := u.c(u.pal.Muted, " · ")
	// Зубец на том же поле, что и результаты инструментов: строка
	// состояния читается как последняя строка потока, а не как
	// отдельная плашка. Над ней сидит кот (MascotPerch).
	indent := "  " + u.emberTail() + " "
	u.Println(indent + strings.Join(parts, sep))
}

// RequestEcho — эхо задачи в начале хода: «⎿ почини тесты». Как в
// Claude Code: запрос остаётся в стенке вывода, отделяя ходы друг от
// друга. В живом терминале не печатается — строка запроса и так видна
// у приглашения; эхо нужно пайпам и логам, где ввод не отображается.
func (u *UI) RequestEcho(text string) {
	if u.quiet || text == "" || isTerminal() {
		return
	}
	line := core.OneLine(text)
	if w := u.Width() - 10; w > 20 {
		line = core.Truncate(line, w)
	}
	u.Println("  " + u.emberTail() + " " + u.Gray(line))
}

// Виды элементов строки статуса.
const (
	StatusMuted = iota
	StatusAccent
	StatusOK
	StatusWarn
	StatusErr
)

// StatusItem — один элемент строки статуса.
type StatusItem struct {
	Text string
	Kind int
}

// Prompt2 — приглашение при чтении ответа пользователя.
func (u *UI) Prompt2(question, hint string) {
	if question != "" {
		u.Print("  " + u.Bold(question) + " ")
	}
	u.Print(u.Gray(hint))
}

// UsageLine — строка расхода токенов под ответом.
func (u *UI) UsageLine(d time.Duration, us core.Usage, model string) {
	parts := []string{u.c(u.pal.Muted, core.HumanDuration(d))}
	if us.PromptTokens > 0 || us.CompletionTokens > 0 {
		parts = append(parts, u.c(u.pal.Muted, "↑"+core.Kfmt(us.PromptTokens)+" ↓"+core.Kfmt(us.CompletionTokens)))
	}
	if model != "" {
		parts = append(parts, u.c(u.pal.Muted, model))
	}
	u.Println("  " + strings.Join(parts, u.c(u.pal.Muted, " · ")))
}

// SetCompact — компактный режим (без пустых строк).
func (u *UI) SetCompact(on bool) { u.opts.Compact = on }

// SetQuiet — подавить обычный вывод (машинный режим).
func (u *UI) SetQuiet(on bool) {
	u.quiet = on
	if on {
		u.opts.Animations = false
		u.opts.Color = false
	}
}

// Quiet — включён ли машинный режим.
func (u *UI) Quiet() bool { return u.quiet }

// IsTerminal — является ли stdout терминалом.
func IsTerminal() bool { return isTerminal() }

// RenderTodos — напечатать план задач.
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
	// Сделано — зелёный ромб в тон шапке плана, в работе — охра,
	// не начато — зубец. Отличаются цветом, а не только формой:
	// три похожих значка в одной колонке глаз не различает.
	markDone, markActive, markTodo := u.g.Star, u.g.Star, u.emberTickOf()
	for i, t := range todos {
		switch t.Status {
		case core.TodoCompleted:
			u.Println("   " + u.c(u.pal.OK, markDone) + " " +
				u.c(u.pal.Muted, core.StrikeOut(core.Truncate(core.OneLine(t.Content), u.Width()-10))))
		case core.TodoInProgress:
			u.Println("   " + u.c(u.pal.Accent, markActive) + " " +
				u.c(u.pal.Accent, core.Truncate(core.OneLine(t.Content), u.Width()-10)))
		default:
			u.Println("   " + u.c(u.pal.Muted, markTodo) + " " +
				u.c(u.pal.Muted, core.Truncate(core.OneLine(t.Content), u.Width()-10)))
		}
		_ = i
	}
	if active > 0 {
		u.Println("")
	}
}

// Hint — подсказка.
func (u *UI) Hint(s string) { u.Println("   " + u.Gray(s)) }

// Plain — просто строка с отступом.
func (u *UI) Plain(s string) { u.Println(s) }

// Title — заголовок раздела верхнего уровня: ромб, имя, линия вправо.
func (u *UI) Title(s string) {
	u.Println("")
	fill := u.Width() - 2 - 1 - 1 - runeLen(s) - 1
	line := ""
	if fill >= 3 {
		line = " " + u.emberRule(fill)
	}
	u.Println("  " + u.emberMark(u.pal.Accent) + " " + u.Head(s) + line)
	u.Println("")
}

// Section — заголовок раздела: ромб, имя, линия вправо до края.
func (u *UI) Section(s string) {
	u.Println("")
	fill := u.Width() - 2 - 1 - 1 - runeLen(s) - 1
	line := ""
	if fill >= 3 {
		line = " " + u.emberRule(fill)
	}
	u.Println("  " + u.emberMark(u.pal.Accent) + " " + u.Head(s) + line)
	u.Println("")
}

// KV — строка «ключ: значение» с выравниванием.
func (u *UI) KV(k, v string) {
	u.Println("  " + u.Gray(padRight(k, kvWidth)) + v)
}

// kvWidth — ширина колонки ключей в блоках «параметр: значение».
const kvWidth = 18

// KVPairs — напечатать список «ключ: значение» с общим выравниванием.
func (u *UI) KVPairs(rows [][2]string) {
	w := 0
	for _, r := range rows {
		if n := runeLen(r[0]); n > w {
			w = n
		}
	}
	// +2 — воздух перед значением, чтобы длинные ключи не слипались.
	if w += 2; w < kvWidth {
		w = kvWidth
	}
	for _, r := range rows {
		u.Println("  " + u.Gray(padRight(r[0], w)) + r[1])
	}
}

// Bar — полоса прогресса: у события есть начало (маркер ◆) и конец
// (зубец ⎿), и полоса читается как отрезок между ними. Заливка блоками
// здесь тяжелее линии.
func (u *UI) Bar(done, total int, width int) string {
	if total <= 0 {
		return ""
	}
	if width < 4 {
		width = 4
	}
	ratio := float64(done) / float64(total)
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

// SpinnerStart — запустить анимацию ожидания «кадр + метка + время».
func (u *UI) SpinnerStart(label string) {
	if !u.opts.Animations || !u.opts.Color || u.quiet {
		return
	}
	u.mu.Lock()
	if u.spin {
		u.mu.Unlock()
		return
	}
	u.spin = true
	u.spinLbl = label
	u.spinShimmer = ""
	u.spinT0 = time.Now()
	u.spinStop = make(chan struct{})
	u.spinDone = make(chan struct{})
	// Кадры спиннера: мордочка кота или «указатель» темы.
	frames := u.spins
	if u.mascot {
		frames = []string{u.mascotInline(u.spinMascot, 0), u.mascotInline(u.spinMascot, 1)}
	}
	stop, done := u.spinStop, u.spinDone
	u.mu.Unlock()

	go func() {
		defer close(done)
		i := 0
		t := time.NewTicker(90 * time.Millisecond)
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
				el := time.Since(u.spinT0).Truncate(time.Second)
				label := u.spinLbl
				// Обрезаем, чтобы не ломать строку на узком терминале.
				maxl := u.opts.Width - 14
				if maxl > 8 && runeLen(label) > maxl {
					label = truncate(label, maxl)
				}
				_, _ = u.out.Write([]byte("\r" + cReset + "\033[K" + u.pal.Accent + frame + cReset + " " + label + " " + u.pal.Muted + el.String() + cReset))
				i++
				u.mu.Unlock()
			}
		}
	}()
}

// ShimmerStart — строка ожидания с котом и переливом:
//
//	( -.- ) thinking...
//
// Слово переливается цветом от левого края к правому (shimmerWord).
// Пока маскот выключен, работает обычный спиннер с тем же словом.
// Остановка — общий SpinnerStop; любая содержательная печать через
// Write гасит строку сама (clearSpinLocked останавливает анимацию).
func (u *UI) ShimmerStart(word string, state MascotState) {
	u.SetSpinnerMascot(state)
	if !u.mascot {
		u.SpinnerStart(word)
		return
	}
	if !u.opts.Animations || !u.opts.Color || u.quiet {
		return
	}
	u.mu.Lock()
	if u.spin {
		u.mu.Unlock()
		return
	}
	u.spin = true
	u.spinLbl = ""
	u.spinShimmer = word
	u.spinMascot = state
	u.spinT0 = time.Now()
	u.spinStop = make(chan struct{})
	u.spinDone = make(chan struct{})
	stop, done := u.spinStop, u.spinDone
	u.mu.Unlock()

	go func() {
		defer close(done)
		t := time.NewTicker(90 * time.Millisecond)
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
				face := u.mascotInline(u.spinMascot, int(time.Since(u.spinT0)/(300*time.Millisecond)))
				line := "\r" + cReset + "\033[K" + face + cReset + " " +
					u.shimmerWord(u.spinShimmer, time.Since(u.spinT0).Seconds())
				_, _ = u.out.Write([]byte(line))
				u.mu.Unlock()
			}
		}
	}()
}

// SpinnerStop — остановить анимацию и очистить строку.
func (u *UI) SpinnerStop() {
	u.mu.Lock()
	if !u.spin {
		u.mu.Unlock()
		return
	}
	u.spin = false
	stop, done := u.spinStop, u.spinDone
	_, _ = u.out.Write([]byte("\r" + cReset + "\033[K"))
	u.mu.Unlock()
	close(stop)
	<-done
}

// clearSpinLocked — остановить анимацию ожидания и очистить её строку.
// Вызывается перед любой содержательной печатью: строка «thinking...»
// не должна пережить вывод, иначе она перерисуется поверх ответа.
func (u *UI) clearSpinLocked() {
	if !u.spin {
		return
	}
	u.spin = false
	stop, done := u.spinStop, u.spinDone
	_, _ = u.out.Write([]byte("\r" + cReset + "\033[K"))
	u.mu.Unlock()
	close(stop)
	<-done
	u.mu.Lock()
}

// ---------- Утилиты ширины ----------

// runeLen — длина строки в рунах.
func runeLen(s string) int { return len([]rune(s)) }

// runeWidth — грубая ширина строки в терминальных колонках
// (кириллица и CJK считаются как 1, экранированные последовательности — 0).
func runeWidth(s string) int {
	w := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		w++
	}
	return w
}

// visibleWidth — ширина строки без ANSI-последовательностей.
func visibleWidth(s string) int {
	var b strings.Builder
	b.Grow(len(s))
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inEsc {
			// ESC-последовательность заканчивается буквой (обычно 'm').
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEsc = false
			}
			continue
		}
		if c == 0x1B {
			inEsc = true
			continue
		}
		b.WriteByte(c)
	}
	return runeLen(b.String())
}

// padRight — дополнить строку (учитывая ANSI) пробелами до ширины w.
func padRight(s string, w int) string {
	pad := w - visibleWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// padLeft — дополнить слева.
func padLeft(s string, w int) string {
	pad := w - visibleWidth(s)
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

// padTo — дополнить строку пробелами до ширины w (по рунам, без учёта ANSI).
func padTo(s string, w int) string {
	if p := w - runeLen(s); p > 0 {
		return s + strings.Repeat(" ", p)
	}
	return s
}

// truncate — обрезать строку до n рун с многоточием.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

// center — центрировать строку в ширине w.
func center(s string, w int) string {
	pad := w - visibleWidth(s)
	if pad <= 0 {
		return s
	}
	left := pad / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
}

// wrap — разбить текст на строки по ширине (с учётом переносов по словам).
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
			for runeLen(word) > width {
				// Слово длиннее строки — режем по границам рун.
				r := []rune(word)
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, string(r[:width]))
				word = string(r[width:])
			}
			if line == "" {
				line = word
			} else if runeLen(line)+1+runeLen(word) <= width {
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

// wrapTo — разбить и вывести с отступом.
func (u *UI) wrapTo(s, indent string) {
	w := u.Width() - runeLen(indent) - 2
	for _, l := range wrap(s, w) {
		u.Println(indent + l)
	}
}

// atoiSafe — безопасно распарсить число.
func atoiSafe(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
