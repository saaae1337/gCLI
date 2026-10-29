package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ---------- Приложение ----------
//
// App владеет экраном, вводом и лентой. Снаружи к нему обращаются из
// горутины агента (текст, инструменты, метрики) и из main (события
// жизненного цикла). Все методы потокобезопасны: состояние под мьютексом,
// кадр рисуется сразу после изменения.

// Command — команда для подсказки в поле ввода.
type Command struct {
	Name string
	Desc string
}

// Options — параметры интерфейса.
type Options struct {
	Version  string
	Model    string
	Provider string
	WorkDir  string
	Session  string

	Commands []Command

	Color       bool
	Colors      int // 0 — truecolor, 16 или 256 — ближайшая палитра
	Unicode     bool
	HideMascot  bool
	CronEnabled bool
	Theme       Theme

	In  io.Reader
	Out io.Writer

	// Raw включает raw-режим терминала и возвращает функцию возврата.
	// nil — терминал недоступен, интерфейс не запускается.
	Raw func() (restore func(), ok bool)
	// Size — текущий размер окна в клетках.
	Size func() (w, h int)
}

// Handlers — то, что интерфейс зовёт у приложения.
type Handlers struct {
	// Submit — пользователь отправил запрос или команду. Вызывается в
	// отдельной горутине: интерфейс продолжает жить, пока идёт ход.
	Submit func(text string)
	// Interrupt — Esc или Ctrl+C во время работы.
	Interrupt func()
	// Quit — пользователь вышел.
	Quit func()
}

// Metrics — снимок расхода для нижней панели.
type Metrics struct {
	Model      string
	Provider   string
	Context    int
	ContextMax int
	Prompt     int
	Completion int
	Requests   int
	Rate       float64
	Last       time.Duration
	Session    time.Duration
	Cost       float64
	CostKnown  bool
	Autopilot  bool
	Plan       bool
}

// Choice — ответ на подтверждение.
type Choice int

const (
	ChoiceNo Choice = iota
	ChoiceYes
	ChoiceAlways
	ChoiceAll
)

// Confirm — запрос подтверждения.
type Confirm struct {
	Title  string
	Detail string
	Note   string
	Danger bool
	// Old/New — содержимое файла до и после: в окне показывается дифф.
	Old, New string
	Path     string

	AllowAlways bool
	AllowAll    bool
}

// Ask — вопрос с вариантами и свободным ответом.
type Ask struct {
	Question string
	Options  []string
	// Secret — скрыть ввод (ключи, токены).
	Secret bool
}

// App — интерфейс.
type App struct {
	opt Options
	th  Theme

	mu       sync.Mutex
	doc      Doc
	comp     *Composer
	rend     *Renderer
	rd       *Reader
	restore  func()
	started  bool
	closed   bool
	quitOnce sync.Once

	w, h  int
	busy  bool
	queue []string
	met   Metrics

	scroll   int
	palSel   int
	modal    *modal
	toast    string
	toastEnd time.Time

	tick      int
	handle    Handlers
	corners   Corners
	lastSize  time.Time
	lastDraw  time.Time
	lastCtrlC time.Time

	// Кэш раскладки: свёрстанные строки ленты.
	cache     []Line
	cacheVer  int
	cacheW    int
	cacheSpin string

	// dirty — кадр устарел, но ещё не перерисован. Все асинхронные
	// события (текст из горутин агента, метрики, инструменты) только
	// ставят флаг: кадр собирает один цикл Run. Раньше каждый метод
	// рисовал сам, и параллельные отрисовки из разных горутин
	// перемешивались в один терминал — это и было «мигание».
	dirty bool
	// activity — чем занят агент прямо сейчас («bash ls -la»);
	// показывается в рамке поля ввода вместо безликого «работаю».
	activity string
	// doneAt — когда закончился последний ход: кот празднёт пару секунд.
	doneAt time.Time
	// files — кэш списка файлов для @-дополнения, filesAt — когда собран;
	// filesBusy — сборка списка идёт в фоне (обход каталога под мьютексом
	// замораживал интерфейс и горутины агента на больших репозиториях).
	files     []string
	filesAt   time.Time
	filesBusy bool
	fileHide  string // @-токен, для которого список закрыли по Esc

	// Выделение мышью: якорь и подвижный край в клетках экрана. Текст
	// выделения снимается с последнего собранного кадра (lastScreen).
	selAnchor              [2]int
	selHead                [2]int
	selOn                  bool
	clicks                 int
	lastClick              time.Time
	lastClickX, lastClickY int

	// blockOf — блок-владелец каждой строки кэша ленты: клик по строке-
	// заголовку размышлений сворачивает или разворачивает их.
	blockOf []*block

	// lastScreen — последний собранный кадр: из него берётся текст
	// выделения для копирования.
	lastScreen *Screen
}

// modalKind — вид окна поверх ленты.
type modalKind int

const (
	modalConfirm modalKind = iota + 1
	modalAsk
)

type modal struct {
	kind   modalKind
	title  string
	detail string
	note   string
	danger bool
	diff   []diffOp
	path   string
	secret bool

	opts  []modalOpt
	sel   int
	input *Composer
	reply chan Choice
	ask   chan askResult
}

// askResult — ответ на вопрос: текст и признак «ответ дан» (Esc — отмена).
type askResult struct {
	text string
	ok   bool
}

type modalOpt struct {
	Key    rune
	Label  string
	Choice Choice
}

// New — создать интерфейс.
func New(o Options) *App {
	th := o.Theme
	if !th.Bg.On {
		th = Dark()
	}
	corners := Rounded
	if !o.Unicode {
		// ASCII-режим: без полублоков и псевдографики. Рамки храним в
		// приложении, а не в глобальной переменной: два интерфейса в
		// одном процессе не должны перекрашивать друг друга.
		corners = Corners{'+', '+', '+', '+', '-', '|'}
	}
	return &App{
		opt:     o,
		th:      th,
		corners: corners,
		comp:    NewComposer(),
		rend:    NewRenderer(o.Out, o.Color, o.Colors),
		started: false,
	}
}

// fitSize — размер экрана с запасными значениями.
//
// Нулевой размер означает, что терминал не ответил: рисовать в клетки
// нулевой ширины бессмысленно, а падать из-за этого — тем более.
func fitSize(w, h int) (int, int) {
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return w, h
}

// Run — запустить интерфейс и блокироваться до выхода.
func (a *App) Run(h Handlers) error {
	if a.opt.Raw == nil || a.opt.Size == nil {
		return fmt.Errorf("tui: терминал недоступен")
	}
	restore, ok := a.opt.Raw()
	if !ok {
		return fmt.Errorf("tui: не удалось включить raw-режим")
	}
	a.mu.Lock()
	a.restore = restore
	a.handle = h
	a.started = true
	w, hgt := fitSize(a.opt.Size())
	a.w, a.h = w, hgt
	a.mu.Unlock()

	if err := a.rend.Start(a.opt.Unicode); err != nil {
		restore()
		a.mu.Lock()
		a.started = false
		a.handle = Handlers{}
		a.restore = nil
		a.mu.Unlock()
		return err
	}
	defer func() {
		a.rend.Close()
		a.mu.Lock()
		a.started = false
		a.handle = Handlers{}
		rest := a.restore
		a.restore = nil
		a.mu.Unlock()
		if rest != nil {
			rest()
		}
	}()

	a.rd = NewReader(a.opt.In)
	defer a.rd.Close()
	a.mu.Lock()
	a.drawLocked()
	a.mu.Unlock()

	tick := time.NewTicker(110 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case ev, ok := <-a.rd.Events():
			if !ok {
				// Ввод закрылся (терминал отвалился): окно не должно
				// оставить горутину инструмента навсегда ждать ответа.
				a.mu.Lock()
				a.closed = true
				if a.modal != nil {
					a.cancelModalLocked(ChoiceNo)
				}
				a.mu.Unlock()
				return nil
			}
			a.handleEvent(ev)
		case <-tick.C:
			a.onTick()
		}
		a.mu.Lock()
		done := a.closed
		h := a.handle
		a.mu.Unlock()
		if done {
			if h.Quit != nil {
				h.Quit()
			}
			return nil
		}
	}
}

// Running — интерфейс запущен и ещё не закрыт.
func (a *App) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.started && !a.closed
}

// Close — закрыть интерфейс (можно звать из другой горутины).
func (a *App) Close() { a.Quit() }

// Quit — попросить цикл завершиться.
func (a *App) Quit() {
	a.quitOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		if a.modal != nil {
			a.cancelModalLocked(ChoiceNo)
		}
		a.mu.Unlock()
	})
}

// ---------- События ----------

// onTick — часы: ресайз, анимация, тосты.
func (a *App) onTick() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.tick++
	redraw := a.dirty
	a.dirty = false
	if a.opt.Size != nil && time.Since(a.lastSize) > 200*time.Millisecond {
		a.lastSize = time.Now()
		if w, h := a.opt.Size(); w > 0 && h > 0 {
			w, h = fitSize(w, h)
			if w != a.w || h != a.h {
				a.w, a.h = w, h
				a.rend.Invalidate()
				redraw = true
			}
		}
	}
	if a.toast != "" && time.Now().After(a.toastEnd) {
		a.toast = ""
		redraw = true
	}
	if a.busy || a.modal != nil || a.paletteOpen() || a.filePaletteOpen() {
		redraw = true
	} else if a.tick%8 == 0 {
		// Маскот моргает и на простое: редкие кадры почти ничего не пишут.
		redraw = true
	}
	if redraw {
		a.drawLocked()
	}
}

// handleEvent — клавиатура, мышь, вставка.
func (a *App) handleEvent(ev Event) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.dirty = false
	if ev.Wheel != 0 {
		// Колесо вверх приходит как -1; лента листается положительным.
		a.scrollBy(-ev.Wheel * 3)
		a.drawLocked()
		a.mu.Unlock()
		return
	}
	if a.modal != nil {
		a.modalKeyLocked(ev)
		a.drawLocked()
		a.mu.Unlock()
		return
	}
	if ev.MDown || ev.MDrag || ev.MUp {
		// Мышь: выделение перетаскиванием, клики по полю и по блокам.
		a.mouseLocked(ev)
		a.drawLocked()
		a.mu.Unlock()
		return
	}
	a.keyLocked(ev)
	a.drawLocked()
	a.mu.Unlock()
}

// keyLocked — обработка клавиши в обычном режиме.
func (a *App) keyLocked(ev Event) {
	if ev.Paste {
		a.comp.Insert(ev.Text)
		a.palSel = 0
		n := strings.Count(ev.Text, "\n") + 1
		if n > 1 {
			a.toastLocked(fmt.Sprintf("вставлено строк: %d", n))
		}
		return
	}
	if ev.Key == KeyRune {
		switch {
		case ev.Ctrl && (ev.R == 'c'):
			// Есть выделение — Ctrl+C копирует его, как в привычных
			// программах. Без выделения — прежнее поведение: прервать ход,
			// очистить ввод, выйти.
			if a.selOn {
				if txt := a.selectionText(); txt != "" {
					a.copyTextLocked(txt)
				}
				a.clearSel()
				return
			}
			a.ctrlCLocked()
			return
		case ev.Ctrl && ev.R == 'd':
			// Непустое поле — Ctrl+D отправляет (второй способ после
			// shift+enter). Пустое — выход, но только на простое: иначе сессия
			// закрылась бы посреди хода, оставив инструменты и журнал
			// недописанными.
			if !a.comp.Empty() {
				a.submitLocked()
				return
			}
			if !a.busy {
				a.closed = true
			}
			return
		case ev.Ctrl && ev.R == 'j':
			a.comp.Newline()
			return
		case ev.Ctrl && ev.R == 'v':
			// Ctrl+V: системный буфер обмена штатной утилитой (wl-paste,
			// xclip, pbpaste). Читаем вне мьютекса: утилита может отвечать
			// сотню миллисекунд, интерфейс замирать не должен.
			go func() {
				txt, ok := ReadClipboard()
				a.mu.Lock()
				defer a.mu.Unlock()
				if a.closed {
					return
				}
				if !ok || txt == "" {
					a.toastLocked("буфер обмена недоступен — вставь ctrl+shift+v")
				} else {
					a.comp.Insert(txt)
					if n := strings.Count(txt, "\n") + 1; n > 1 {
						a.toastLocked(fmt.Sprintf("вставлено строк: %d", n))
					}
				}
				a.markDirty()
			}()
			return
		case ev.Ctrl && ev.R == 'o':
			a.toggleThinkLocked()
			return
		case ev.Ctrl && ev.R == 'y':
			// Скопировать последний ответ в буфер обмена (OSC52):
			// терминалы с поддержкой забирают его сами, а привычный
			// Ctrl+Shift+C работает только там, где выделение не
			// перехвачено мышью.
			a.comp.SetText("/copy")
			a.submitLocked()
			return
		case ev.Ctrl && ev.R == 'l':
			a.rend.Invalidate()
			return
		case ev.Ctrl && ev.R == 'k':
			a.comp.KillLine()
			return
		case ev.Ctrl && ev.R == 'u':
			a.comp.KillToStart()
			return
		case ev.Ctrl && ev.R == 'w':
			a.comp.KillWord()
			return
		case ev.Ctrl && ev.R == 'a':
			a.comp.Home()
			return
		case ev.Ctrl && ev.R == 'e':
			a.comp.End()
			return
		case ev.Ctrl && ev.R == 'p':
			a.comp.HistoryPrev()
			return
		case ev.Ctrl && ev.R == 'n':
			a.comp.HistoryNext()
			return
		case ev.Ctrl:
			return
		case ev.Alt:
			return
		}
		a.comp.Insert(string(ev.R))
		a.palSel = 0
		return
	}
	palOpen := a.paletteOpen()
	fileOpen := a.filePaletteOpen()
	switch ev.Key {
	case KeyEnter:
		if ev.Shift || ev.Alt || ev.Ctrl {
			// Shift+Enter отправляет — главный аккорд: Alt+Enter занят
			// самим терминалом (в Windows Terminal это полный экран),
			// Ctrl+Enter остаётся запасным. Обычный Enter переносит
			// строку, отправке нужен отдельный аккорд.
			a.submitLocked()
			return
		}
		// Палитра открыта: первый Enter доводит команду до выбранной,
		// второй — отправляет. Иначе «/mo» + Enter уходил бы в
		// обработчик неизвестной командой.
		if palOpen {
			items := a.paletteItems()
			if len(items) > 0 && a.comp.Text() != items[a.palSel].Name {
				a.comp.SetText(items[a.palSel].Name)
				a.palSel = 0
				return
			}
			// Текст уже совпадает с выбранной командой — второй Enter
			// запускает её.
			a.submitLocked()
			return
		}
		if fileOpen {
			if a.completeFileLocked() {
				return
			}
		}
		// Слэш-команда без переносов — привычный Enter запускает её:
		// команды не бывают многострочными, а текст сообщения может.
		if text := a.comp.Text(); strings.HasPrefix(text, "/") && !strings.Contains(text, "\n") {
			a.submitLocked()
			return
		}
		// Грамотный перенос строки: хвост уезжает вниз, отступ текущей
		// строки повторяется, курсор встаёт в конец отступа. На пустом
		// поле Enter ничего не делает.
		if !a.comp.Empty() {
			a.comp.Newline()
		}
	case KeyTab:
		if fileOpen {
			a.completeFileLocked()
			return
		}
		if palOpen {
			a.completePaletteLocked()
		}
	case KeyBacktab:
		a.comp.Insert("  ")
	case KeyBackspace:
		a.comp.Backspace()
	case KeyDelete:
		a.comp.Delete()
	case KeyLeft:
		a.comp.Left()
	case KeyRight:
		a.comp.Right()
	case KeyHome:
		if ev.Ctrl {
			a.scrollBy(1 << 20)
			return
		}
		a.comp.Home()
	case KeyEnd:
		if ev.Ctrl {
			a.scroll = 0
			return
		}
		a.comp.End()
	case KeyUp:
		if fileOpen {
			a.moveFilePalette(-1)
		} else if palOpen {
			a.movePalette(-1)
		} else {
			a.comp.Up(a.composerInner())
		}
	case KeyDown:
		if fileOpen {
			a.moveFilePalette(1)
		} else if palOpen {
			a.movePalette(1)
		} else {
			a.comp.Down(a.composerInner())
		}
	case KeyPgUp:
		a.scrollBy(a.bodyHeight())
	case KeyPgDn:
		a.scrollBy(-a.bodyHeight())
	case KeyEsc:
		if fileOpen {
			if tok, ok := a.atToken(); ok {
				a.fileHide = tok
			}
			return
		}
		if palOpen {
			a.comp.Clear()
			return
		}
		if a.busy {
			a.interruptLocked()
			return
		}
		if !a.comp.Empty() {
			a.comp.Clear()
			return
		}
		a.toastLocked("ctrl+c — выход, /help — команды")
	}
}

// ctrlCLocked — Ctrl+C: остановить ход, очистить ввод, выйти.
func (a *App) ctrlCLocked() {
	if a.busy {
		a.interruptLocked()
		return
	}
	if !a.comp.Empty() {
		a.comp.Clear()
		return
	}
	// Выход по второму нажатию: Ctrl+C — привычный способ прервать
	// вывод, и случайное нажатие не должно закрывать сессию.
	if time.Since(a.lastCtrlC) < 2*time.Second {
		a.closed = true
		return
	}
	a.lastCtrlC = time.Now()
	a.toastLocked("ещё раз ctrl+c — выход")
}

// interruptLocked — прервать текущий ход.
func (a *App) interruptLocked() {
	queued := len(a.queue) > 0
	a.queue = nil
	if queued {
		a.toastLocked("останавливаю… очередь сброшена")
	} else {
		a.toastLocked("останавливаю…")
	}
	if a.handle.Interrupt != nil {
		go a.handle.Interrupt()
	}
}

// ---------- Мышь: выделение и клики ----------

// clearSel — снять выделение.
func (a *App) clearSel() {
	a.selOn = false
	a.selAnchor = [2]int{}
	a.selHead = [2]int{}
}

// selRect — нормализованный прямоугольник выделения.
func (a *App) selRect() (int, int, int, int) {
	x0, y0 := a.selAnchor[0], a.selAnchor[1]
	x1, y1 := a.selHead[0], a.selHead[1]
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	return x0, y0, x1, y1
}

// selectionText — текст выделения с последнего кадра: строки в границах
// прямоугольника, хвосты строк подрезаны. Широкие символы берутся
// целиком: клетки-продолжения пропускаются, хвост кластера дописывается.
func (a *App) selectionText() string {
	s := a.lastScreen
	if s == nil || !a.selOn {
		return ""
	}
	x0, y0, x1, y1 := a.selRect()
	if y0 == y1 && x0 == x1 {
		return ""
	}
	var rows []string
	for y := y0; y <= y1 && y < s.H; y++ {
		from, to := 0, s.W
		if y == y0 {
			from = x0
		}
		if y == y1 {
			to = x1 + 1
		}
		if to > s.W {
			to = s.W
		}
		var sb strings.Builder
		for x := from; x < to && x < s.W; x++ {
			c := s.Cells[y*s.W+x]
			if c.Cont {
				continue
			}
			r := c.R
			if r == 0 {
				r = ' '
			}
			sb.WriteRune(r)
			if c.Rest != "" {
				sb.WriteString(c.Rest)
			}
		}
		rows = append(rows, strings.TrimRight(sb.String(), " "))
	}
	for len(rows) > 0 && rows[0] == "" {
		rows = rows[1:]
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return strings.Join(rows, "\n")
}

// copyTextLocked — положить текст в системный буфер обмена (OSC52) и
// подтвердить тостом. Терминал без поддержки OSC52 молча проигнорирует
// последовательность — изнутри TUI лучше не сделать.
func (a *App) copyTextLocked(text string) {
	a.Raw(OSC52(text))
	n := len([]rune(text))
	if lines := strings.Count(text, "\n") + 1; lines > 1 {
		a.toastLocked(fmt.Sprintf("скопировано: %d строк", lines))
		return
	}
	a.toastLocked(fmt.Sprintf("скопировано: %d %s", n, plural(n, "символ", "символа", "символов")))
}

// applySelLocked — подсветить выделение в собранном кадре: у выделенных
// клеток меняются местами передний и задний план. Прямой обмен цветов,
// а не атрибут reverse: у пустых клеток цвета не заданы, reverse сделал
// бы выделение невидимым.
func (a *App) applySelLocked(s *Screen) {
	if !a.selOn || s == nil {
		return
	}
	x0, y0, x1, y1 := a.selRect()
	for y := y0; y <= y1 && y < s.H; y++ {
		from, to := 0, s.W
		if y == y0 {
			from = x0
		}
		if y == y1 {
			to = x1 + 1
		}
		if to > s.W {
			to = s.W
		}
		for x := from; x < to; x++ {
			c := &s.Cells[y*s.W+x]
			fg, bg := c.S.FG, c.S.BG
			if !fg.On {
				fg = a.th.Text
			}
			if !bg.On {
				bg = a.th.Bg
			}
			c.S.FG, c.S.BG = bg, fg
			c.S.Reverse = false
		}
	}
}

// mouseLocked — мышь: нажатие начинает выделение или ставит курсор,
// движение тянет выделение, отпускание копирует его в буфер обмена.
// Двойной клик выбирает слово, тройной — строку; клик по заголовку
// размышлений сворачивает или разворачивает их.
func (a *App) mouseLocked(ev Event) {
	x, y := ev.X-1, ev.Y-1 // SGR-мышь отдаёт координаты с единицы
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if y >= a.h {
		y = a.h - 1
	}
	if x >= a.w {
		x = a.w - 1
	}
	switch {
	case ev.MDown:
		now := time.Now()
		if now.Sub(a.lastClick) < 450*time.Millisecond &&
			absDiff(a.lastClickX, x) <= 1 && absDiff(a.lastClickY, y) <= 1 {
			a.clicks++
		} else {
			a.clicks = 1
		}
		a.lastClick, a.lastClickX, a.lastClickY = now, x, y
		a.clearSel()
		// Клик в поле ввода: поставить курсор.
		if y >= a.composerTop() {
			a.composerClickLocked(x, y)
			return
		}
		if a.clicks == 2 {
			a.selectWordLocked(x, y)
			a.selOn = true
			return
		}
		if a.clicks >= 3 {
			a.selOn = true
			a.selAnchor = [2]int{0, y}
			a.selHead = [2]int{a.w - 1, y}
			return
		}
		// Клик по заголовку размышлений — развернуть или свернуть.
		if a.toggleThinkClickLocked(x, y) {
			return
		}
		a.selOn = true
		a.selAnchor = [2]int{x, y}
		a.selHead = a.selAnchor
	case ev.MDrag:
		if !a.selOn {
			return
		}
		a.selHead = [2]int{x, y}
	case ev.MUp:
		if !a.selOn {
			return
		}
		a.selHead = [2]int{x, y}
		if txt := a.selectionText(); txt != "" {
			a.copyTextLocked(txt)
			return
		}
		a.clearSel()
	}
}

// toggleThinkClickLocked — клик по строке-заголовку размышлений.
// true — клик попал по блоку размышлений, состояние переключено.
func (a *App) toggleThinkClickLocked(x, y int) bool {
	if len(a.blockOf) != len(a.cache) || y < 2 {
		return false
	}
	row := y - 2 - a.bodyOffset()
	if row < 0 || row >= len(a.blockOf) {
		return false
	}
	b := a.blockOf[row]
	if b == nil || b.kind != blockThink {
		return false
	}
	a.doc.ToggleBlock(b)
	return true
}

// composerClickLocked — клик в поле ввода: курсор в позицию под мышью.
func (a *App) composerClickLocked(x, y int) {
	inner := a.composerInner()
	boxTop := a.composerTop()
	maxLines := a.composerHeight() - 2
	if maxLines < 1 {
		maxLines = 1
	}
	_, cy := a.comp.CursorPos(inner)
	start := 0
	if cy >= maxLines {
		start = cy - maxLines + 1
	}
	line := start + (y - boxTop - 1)
	col := x - a.composerX() - 4
	if col < 0 {
		col = 0
	}
	a.comp.SetCursor(a.comp.PosAt(inner, line, col))
}

// selectWordLocked — двойной клик: выделить слово под курсором.
func (a *App) selectWordLocked(x, y int) {
	s := a.lastScreen
	if s == nil || y < 0 || y >= s.H || x < 0 || x >= s.W {
		return
	}
	isWord := func(c Cell) bool {
		if c.Cont {
			return false
		}
		r := c.R
		if r == 0 {
			return false
		}
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
	}
	if !isWord(s.Cells[y*s.W+x]) {
		a.selAnchor = [2]int{x, y}
		a.selHead = [2]int{x, y}
		return
	}
	from := x
	for from > 0 && isWord(s.Cells[y*s.W+from-1]) {
		from--
	}
	to := x
	for to < s.W-1 && isWord(s.Cells[y*s.W+to+1]) {
		to++
	}
	a.selAnchor = [2]int{from, y}
	a.selHead = [2]int{to, y}
}

// absDiff — модуль разности: «клик рядом с прошлым» для двойного.
func absDiff(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}

// submitLocked — отправить введённое.
func (a *App) submitLocked() {
	text := a.comp.Submit()
	if strings.TrimSpace(text) == "" {
		return
	}
	a.palSel = 0
	if a.busy {
		a.queue = append(a.queue, text)
		a.toastLocked(fmt.Sprintf("в очереди: %d", len(a.queue)))
		return
	}
	a.doc.AddUser(text)
	a.scroll = 0
	a.busy = true
	h := a.handle.Submit
	if h == nil {
		a.busy = false
		return
	}
	a.launchSubmit(h, text)
}

// runSubmit — запустить обработчик в отдельной горутине. Вызывается без
// лока, поэтому a.handle читается под мьютексом: при выходе Run очищает
// handle под тем же мьютексом, и незалоченное чтение гоняло бы с ним.
func (a *App) runSubmit(text string) {
	a.mu.Lock()
	h := a.handle.Submit
	if h == nil {
		a.busy = false
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.launchSubmit(h, text)
}

// launchSubmit — горутина хода. Вызывать, когда состояние уже выставлено
// (busy=true, запрос в ленте) и handle прочитан под мьютексом.
func (a *App) launchSubmit(h func(string), text string) {
	go func() {
		defer func() {
			// Ошибка паники не должна оставить интерфейс «занятым».
			if r := recover(); r != nil {
				a.mu.Lock()
				a.doc.AddNotice(fmt.Sprintf("  внутренняя ошибка: %v", r))
				a.mu.Unlock()
			}
			a.Busy(false)
		}()
		h(text)
	}()
}

// Busy — отметить начало или конец хода.
//
// По завершении хода очередь разбирается здесь же: следующий запрос
// добавляется в ленту и запускается вне блокировки, иначе обработчик мог
// бы сразу упереться в тот же мьютекс.
func (a *App) Busy(on bool) {
	a.mu.Lock()
	if on {
		a.busy = true
		a.scroll = 0
		a.dirty = false
		a.drawLocked()
		a.mu.Unlock()
		return
	}
	a.busy = false
	a.activity = ""
	a.doneAt = time.Now()
	var next string
	if len(a.queue) > 0 {
		next = a.queue[0]
		a.queue = a.queue[1:]
		// Запрос, отправленный во время хода, должен появиться в ленте
		// тогда, когда действительно пошёл в работу, а не молча.
		a.doc.AddUser(next)
		a.scroll = 0
		a.busy = true
	}
	a.drawLocked()
	a.mu.Unlock()
	if next != "" {
		a.runSubmit(next)
	}
}

// ---------- Приём вывода ----------
//
// Все методы ниже вызываются из чужих горутин (агент, инструменты,
// cron). Они только обновляют состояние и ставят dirty: рисует один
// цикл Run. До 0.9.0 каждый метод рисовал сам — параллельные кадры
// из разных горутин и создавали мерцание.

// markDirty — кадр устарел; отрисовка произойдёт в ближайшем тике.
func (a *App) markDirty() { a.dirty = true }

// Text — добавить текст (ANSI-строки из старых панелей).
func (a *App) Text(s string) {
	if s == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.Text(s)
	a.markDirty()
}

// BeginStream — начался ответ модели.
func (a *App) BeginStream() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.BeginStream()
	a.activity = "пишу ответ"
	a.markDirty()
}

// StreamText — кусок ответа.
//
// На каждый токен рисовать нельзя: полный кадр на каждую порцию съедал
// бы процессор быстрее, чем модель печатает. Ставим dirty — тик
// интерфейса подхватит хвост через 0.1 с, заодно собрав в один кадр
// всё, что пришло от других горутин.
func (a *App) StreamText(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.StreamText(s)
	a.markDirty()
}

// EndStream — ответ закончился.
func (a *App) EndStream() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.EndStream()
	a.activity = ""
	a.markDirty()
}

// SetMeta — подпись под ответом (время, токены).
func (a *App) SetMeta(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.SetMeta(s)
	a.markDirty()
}

// BeginThink — начались размышления.
func (a *App) BeginThink() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.BeginThink()
	a.activity = "думаю"
	a.markDirty()
}

// ThinkText — кусок размышлений.
func (a *App) ThinkText(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.ThinkText(s)
	a.markDirty()
}

// EndThink — размышления закончились.
func (a *App) EndThink() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.EndThink()
	a.activity = ""
	a.markDirty()
}

// ToolStart — инструмент начал работу.
func (a *App) ToolStart(t ToolInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.ToolStart(t)
	a.activity = toolActivity(t)
	a.markDirty()
}

// ToolEnd — инструмент закончил.
func (a *App) ToolEnd(t ToolInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.ToolEnd(t)
	a.activity = ""
	a.markDirty()
}

// toolActivity — чем занят инструмент, человеческим языком:
// «read src/main.go» вместо безымянной карточки в рамке поля ввода.
func toolActivity(t ToolInfo) string {
	arg := t.Args
	if arg == "" {
		return t.Name
	}
	if i := strings.IndexAny(arg, "\r\n"); i >= 0 {
		arg = arg[:i]
	}
	if len(arg) > 42 {
		// Срез по байту разрезал руну (кириллица в аргументе) — в строке
		// активности рисовался мусорный символ. Отступаем до границы руны.
		cut := 42
		for cut > 0 && arg[cut]&0xC0 == 0x80 {
			cut--
		}
		arg = arg[:cut] + "…"
	}
	return t.Name + " " + arg
}

// SetMetrics — обновить нижнюю панель.
func (a *App) SetMetrics(m Metrics) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.met = m
	if m.Model != "" {
		a.opt.Model = m.Model
	}
	if m.Provider != "" {
		a.opt.Provider = m.Provider
	}
	a.markDirty()
}

// Toast — короткая реплика в углу подсказок.
func (a *App) Toast(s string) {
	a.mu.Lock()
	a.toastLocked(s)
	a.markDirty()
	a.mu.Unlock()
}

// Raw — управляющая последовательность в терминал мимо кадра (OSC52).
func (a *App) Raw(s string) {
	a.rend.Raw(s)
}

// SetMascot — показать или спрятать Искру на заставке.
func (a *App) SetMascot(on bool) {
	a.mu.Lock()
	a.opt.HideMascot = !on
	a.markDirty()
	a.mu.Unlock()
}

// SetCronEnabled — показать или скрыть бейдж автономных итераций в строке
// статуса. Вызывается из /cron on|off без перезапуска интерфейса.
func (a *App) SetCronEnabled(on bool) {
	a.mu.Lock()
	a.opt.CronEnabled = on
	a.markDirty()
	a.mu.Unlock()
}

// Clear — очистить ленту.
func (a *App) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.doc.Clear()
	a.scroll = 0
	a.markDirty()
}

// ToggleThink — развернуть или свернуть размышления (Ctrl+O).
func (a *App) ToggleThink() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toggleThinkLocked()
	a.markDirty()
}

// toggleThinkLocked — переключение мыслей под уже взятым мьютексом.
func (a *App) toggleThinkLocked() {
	if !a.doc.HasThink() {
		a.toastLocked("размышлений пока нет")
		return
	}
	a.doc.ToggleThink()
}

// Flash — короткое сообщение в нижней панели.
func (a *App) Flash(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toastLocked(s)
	a.markDirty()
}

func (a *App) toastLocked(s string) {
	a.toast = s
	a.toastEnd = time.Now().Add(3 * time.Second)
}

// ---------- Подтверждения и вопросы ----------

// Confirm — спросить разрешение и дождаться ответа.
func (a *App) Confirm(c Confirm) Choice {
	reply := make(chan Choice, 1)
	a.mu.Lock()
	if a.closed || !a.started || a.modal != nil {
		a.mu.Unlock()
		return ChoiceNo
	}
	m := &modal{
		kind:   modalConfirm,
		title:  c.Title,
		detail: c.Detail,
		note:   c.Note,
		danger: c.Danger,
		path:   c.Path,
		reply:  reply,
	}
	if c.Old != "" || c.New != "" {
		m.diff = diffWindow(lineDiff(c.Old, c.New), 10)
	}
	m.opts = append(m.opts, modalOpt{Key: 'y', Label: "разрешить", Choice: ChoiceYes})
	if c.AllowAlways {
		m.opts = append(m.opts, modalOpt{Key: 'a', Label: "всегда", Choice: ChoiceAlways})
	}
	if c.AllowAll {
		m.opts = append(m.opts, modalOpt{Key: 'A', Label: "все", Choice: ChoiceAll})
	}
	m.opts = append(m.opts, modalOpt{Key: 'n', Label: "отклонить", Choice: ChoiceNo})
	a.modal = m
	a.drawLocked()
	a.mu.Unlock()
	return <-reply
}

// Ask — задать вопрос; ok=false — пользователь отказался (Esc).
func (a *App) Ask(q Ask) (string, bool) {
	reply := make(chan askResult, 1)
	a.mu.Lock()
	if a.closed || !a.started || a.modal != nil {
		a.mu.Unlock()
		return "", false
	}
	m := &modal{
		kind:   modalAsk,
		title:  "Вопрос",
		detail: q.Question,
		input:  NewComposer(),
		ask:    reply,
		secret: q.Secret,
	}
	if q.Secret {
		m.title = "Секрет"
	}
	// Варианты — кнопки: выбор стрелками, ответ — Enter.
	for _, o := range q.Options {
		m.opts = append(m.opts, modalOpt{Label: o, Choice: ChoiceYes})
	}
	a.modal = m
	a.drawLocked()
	a.mu.Unlock()
	res := <-reply
	return res.text, res.ok
}

// modalKeyLocked — клавиши в модальном окне.
func (a *App) modalKeyLocked(ev Event) {
	m := a.modal
	if ev.Paste {
		if m.kind == modalAsk {
			// Поле вопроса однострочное: переводы строк в ответе сломали
			// бы рамку, поэтому склеиваем вставку в одну строку.
			m.input.Insert(strings.ReplaceAll(ev.Text, "\n", " "))
		}
		return
	}
	if ev.Key == KeyRune && ev.Ctrl && ev.R == 'c' {
		// Сначала закрываем окно, затем действуем как обычный Ctrl+C:
		// случайное нажатие в подтверждении не должно убивать сессию.
		a.cancelModalLocked(ChoiceNo)
		a.ctrlCLocked()
		return
	}
	if ev.Key == KeyEsc {
		a.cancelModalLocked(ChoiceNo)
		return
	}
	if m.kind == modalAsk {
		switch ev.Key {
		case KeyEnter:
			text := strings.TrimSpace(m.input.Text())
			if text == "" && len(m.opts) > 0 {
				text = m.opts[m.sel].Label
			}
			a.finishAskLocked(text, true)
		case KeyTab, KeyDown:
			if len(m.opts) > 0 {
				m.sel = (m.sel + 1) % len(m.opts)
			}
		case KeyBacktab, KeyUp:
			if len(m.opts) > 0 {
				m.sel = (m.sel - 1 + len(m.opts)) % len(m.opts)
			}
		case KeyBackspace:
			m.input.Backspace()
		case KeyDelete:
			m.input.Delete()
		case KeyLeft:
			m.input.Left()
		case KeyRight:
			m.input.Right()
		case KeyHome:
			m.input.Home()
		case KeyEnd:
			m.input.End()
		case KeyRune:
			if ev.Ctrl {
				switch ev.R {
				case 'a':
					m.input.Home()
				case 'e':
					m.input.End()
				case 'u':
					m.input.KillToStart()
				case 'k':
					m.input.KillLine()
				case 'w':
					m.input.KillWord()
				}
				return
			}
			if !ev.Ctrl && !ev.Alt {
				m.input.Insert(string(ev.R))
			}
		}
		return
	}
	// Подтверждение: стрелки выбирают, буквы отвечают сразу.
	switch ev.Key {
	case KeyLeft:
		m.sel = (m.sel - 1 + len(m.opts)) % len(m.opts)
		return
	case KeyRight, KeyTab:
		m.sel = (m.sel + 1) % len(m.opts)
		return
	case KeyEnter:
		if m.sel >= 0 && m.sel < len(m.opts) {
			a.finishConfirmLocked(m.opts[m.sel].Choice)
		}
		return
	case KeyRune:
		if ev.Ctrl || ev.Alt {
			return
		}
		// Точное совпадение: 'a' и 'A' — разные ответы («всегда» и «все»).
		// Русская раскладка принимается по смыслу слова, а не по позиции
		// клавиши: «н» здесь — это «нет», а не латинская y. Физическая
		// раскладка превращала бы отказ в согласие.
		r := ev.R
		switch r {
		case 'д', 'Д':
			r = 'y'
		case 'н', 'Н':
			r = 'n'
		case 'а':
			r = 'a'
		case 'А':
			r = 'A'
		}
		// «в» не мапим: первая буква и «всегда», и «все» — промах по
		// клавише превращал разовое разрешение в постоянное.
		switch r {
		case 'Y':
			r = 'y'
		case 'N':
			r = 'n'
		}
		for _, o := range m.opts {
			if o.Key != 0 && r == o.Key {
				a.finishConfirmLocked(o.Choice)
				return
			}
		}
	}
}

// finishConfirmLocked — закрыть подтверждение.
func (a *App) finishConfirmLocked(ch Choice) {
	if a.modal == nil {
		return
	}
	m := a.modal
	a.modal = nil
	if m.reply != nil {
		m.reply <- ch
	}
}

// finishAskLocked — закрыть вопрос.
func (a *App) finishAskLocked(answer string, ok bool) {
	if a.modal == nil {
		return
	}
	m := a.modal
	a.modal = nil
	if m.ask != nil {
		m.ask <- askResult{text: answer, ok: ok}
	}
}

// cancelModalLocked — отмена (Esc): «нет» или пустой ответ.
func (a *App) cancelModalLocked(ch Choice) {
	if a.modal == nil {
		return
	}
	m := a.modal
	a.modal = nil
	if m.reply != nil {
		m.reply <- ch
	}
	if m.ask != nil {
		m.ask <- askResult{ok: false}
	}
}

// ---------- Прокрутка ----------

// scrollBy — прокрутить ленту (положительное — вверх).
func (a *App) scrollBy(delta int) {
	a.scroll += delta
	if a.scroll < 0 {
		a.scroll = 0
	}
	max := 0
	if lines := len(a.cache); lines > a.bodyHeight() {
		max = lines - a.bodyHeight()
	}
	if a.scroll > max {
		a.scroll = max
	}
}

// bodyOffset — первая видимая строка ленты: 0 — прилипание к низу.
func (a *App) bodyOffset() int {
	off := len(a.cache) - a.bodyHeight() - a.scroll
	if off < 0 {
		off = 0
	}
	return off
}

// bodyHeight — высота ленты: от строки под шапкой до верха поля ввода.
func (a *App) bodyHeight() int {
	hh := a.composerTop() - 2
	if hh < 3 {
		hh = 3
	}
	return hh
}

// composerHeight — высота рамки поля ввода с содержимым.
func (a *App) composerHeight() int {
	lines := len(a.comp.Lines(a.composerInner()))
	if lines > 6 {
		lines = 6
	}
	// Витринный минимум: три строки содержимого, рамка в пять строк —
	// как на демо-GIF. На низком окне поле сжимается до одной строки.
	min := 3
	if a.h < 18 {
		min = 1
	}
	if lines < min {
		lines = min
	}
	return lines + 2
}

// composerTop — верхняя строка рамки ввода.
//
// Одна формула на отрисовку и на курсор: если считать их порознь, на
// низком терминале курсор уезжает на строку выше поля.
func (a *App) composerTop() int {
	// Витрина: между рамкой и строкой подсказок живёт пустая строка,
	// поэтому отступ снизу — три строки, а не две.
	top := a.h - 3 - a.composerHeight()
	if top < 5 {
		top = 5
	}
	// Низкий терминал: рамка поля не должна налезать на строки подсказок
	// и статуса — лучше поднять поле и поджать ленту, чем рвать рамку.
	if maxTop := a.h - 2 - a.composerHeight(); maxTop >= 1 && top > maxTop {
		top = maxTop
	}
	return top
}

// composerX — левая граница поля ввода.
//
// На широком терминале слева от поля живёт колонка мини-Искры (8 клеток):
// кот всегда на экране и меняет позу вместе с состоянием агента. На узком
// колонка исчезает — важнее место для текста.
func (a *App) composerX() int {
	if a.railWidth() > 0 {
		return a.railWidth() + 1
	}
	return 1
}

// railWidth — ширина колонки маскота: 0 — колонки нет.
func (a *App) railWidth() int {
	if a.opt.HideMascot || a.w < 66 {
		return 0
	}
	return 8
}

// composerInner — ширина текста внутри поля ввода.
func (a *App) composerInner() int {
	w := a.w - a.composerX() - 6 // рамка (2) + поля (2) + приглашение (2)
	if w < 8 {
		w = 8
	}
	return w
}

// ---------- Палитра команд ----------

// paletteItems — подходящие команды для текущего ввода.
func (a *App) paletteItems() []Command {
	text := a.comp.Text()
	if !strings.HasPrefix(text, "/") || a.comp.Len() > 40 {
		return nil
	}
	if strings.ContainsAny(text, " \n") {
		return nil
	}
	prefix := strings.ToLower(text)
	var out []Command
	for _, c := range a.opt.Commands {
		if strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			out = append(out, c)
		}
	}
	if len(out) > 12 {
		out = out[:12]
	}
	if a.palSel >= len(out) {
		a.palSel = 0
	}
	return out
}

// paletteOpen — открыта ли подсказка команд.
func (a *App) paletteOpen() bool { return len(a.paletteItems()) > 0 }

// movePalette — сдвинуть выбор в подсказке.
func (a *App) movePalette(d int) {
	n := len(a.paletteItems())
	if n == 0 {
		return
	}
	a.palSel = (a.palSel + d + n) % n
}

// completePaletteLocked — подставить выбранную команду.
func (a *App) completePaletteLocked() {
	items := a.paletteItems()
	if len(items) == 0 {
		return
	}
	if a.palSel >= len(items) {
		a.palSel = 0
	}
	a.comp.SetText(items[a.palSel].Name + " ")
	a.palSel = 0
}

// ---------- Палитра файлов (@дополнение) ----------
//
// Прикрепить файл к запросу — «@» и кусок имени: список файлов проекта
// подсказывает пути, Tab/Enter подставляет. На отправке mention разворачивается
// в содержимое файла (tools.ApplyMentions) — раньше это работало, но об
// этом никто не знал: подсказки не было, а список файлов нельзя было
// подсмотреть, не выходя из поля ввода.

// atToken — токен после «@» перед курсором: «прочитай @sr» → «sr».
func (a *App) atToken() (string, bool) {
	t := a.comp.Text()
	cur := a.comp.Cursor()
	if a.comp.Len() > 400 {
		return "", false
	}
	// Text() отдаёт байты, Cursor() считает руны: срез t[:cur] на
	// кириллице резал строку посреди руны, и @-токен после русского
	// текста не находился — подсказка файлов молча пропадала.
	// Работаем в рунах.
	r := []rune(t)
	if cur > len(r) {
		return "", false
	}
	head := string(r[:cur])
	start := strings.LastIndexAny(head, " \n\t") + 1
	tok := head[start:]
	if !strings.HasPrefix(tok, "@") || len(tok) > 100 {
		return "", false
	}
	return tok[1:], true
}

// collectFiles — файлы проекта для подсказки. Плоский список относительных
// путей: без каталогов-мусора, с разумным потолком, чтобы сборка не съела
// клавиатурную задержку на больших репозиториях.
func collectFiles(root string) []string {
	if root == "" {
		root = "."
	}
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		".gcli": true, "__pycache__": true, ".venv": true,
		"venv": true, "dist": true, "build": true, "target": true,
		".idea": true, ".vscode": true,
	}
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (skip[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			// Глубина — по пути, а не счётчиком: счётчик рос на входе в
			// каталог и никогда не убывал, так что «глубиной» было число
			// уже обойдённых каталогов. Корень съедал единицу, и в проекте
			// с шестью каталогами хвост верхнеуровневых (в самом gCLI —
			// tui/, ui/ и wcwidth/) молча выпадал из @-подсказки файлов.
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return filepath.SkipDir
			}
			if rel != "." && strings.Count(rel, string(filepath.Separator))+1 > 6 {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= 4000 {
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || rel == "." {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out
}

// collectFilesFn — точка подмены в тестах: регрессия проверяет, что
// fileItems не блокируется на обходе каталога.
var collectFilesFn = collectFiles

// refreshFilesLocked — запустить фоновую сборку списка файлов проекта.
//
// Раньше WalkDir шёл синхронно под мьютексом: тик, отрисовка и горутины
// агента (StreamText, метрики, статусы инструментов) ждали обхода всего
// репозитория, и на больших деревьях интерфейс замирал на секунды —
// пока в поле ввода оставался @-токен, обход повторялся каждые 10 секунд.
// Теперь список собирает горутина, а палитра до её завершения показывает
// прошлый (несвежий) список; по готовности его подхватит ближайший тик.
func (a *App) refreshFilesLocked() {
	if a.filesBusy {
		return // сборка уже идёт — отдаём прошлый список
	}
	a.filesBusy = true
	root := a.opt.WorkDir
	go func() {
		got := collectFilesFn(root)
		a.mu.Lock()
		a.files = got
		a.filesAt = time.Now()
		a.filesBusy = false
		a.mu.Unlock()
	}()
}

// fileItems — подходящие пути для текущего @-токена: сначала префикс,
// затем подстрока — «main» находит и src/main.go, и README.md c «main» внутри.
func (a *App) fileItems() []string {
	base, ok := a.atToken()
	if !ok {
		return nil
	}
	if a.filesAt.IsZero() || time.Since(a.filesAt) > 10*time.Second {
		a.refreshFilesLocked()
	}
	base = strings.ToLower(base)
	var pref, substr []string
	for _, f := range a.files {
		lf := strings.ToLower(f)
		if base == "" || strings.HasPrefix(lf, base) {
			pref = append(pref, f)
		} else if strings.Contains(lf, base) {
			substr = append(substr, f)
		}
		if len(pref) >= 12 {
			break
		}
	}
	out := pref
	for len(out) < 12 && len(substr) > 0 {
		out = append(out, substr[0])
		substr = substr[1:]
	}
	if len(out) > 12 {
		out = out[:12]
	}
	if a.palSel >= len(out) {
		a.palSel = 0
	}
	return out
}

// filePaletteOpen — открыт ли список файлов.
func (a *App) filePaletteOpen() bool {
	tok, ok := a.atToken()
	if !ok {
		a.fileHide = ""
		return false
	}
	if tok == a.fileHide {
		return false
	}
	return len(a.fileItems()) > 0
}

// moveFilePalette — сдвинуть выбор в списке файлов.
func (a *App) moveFilePalette(d int) {
	n := len(a.fileItems())
	if n == 0 {
		return
	}
	a.palSel = (a.palSel + d + n) % n
}

// completeFileLocked — подставить выбранный файл вместо @-токена.
// true — подставили (Enter не отправляет запрос, а доделывает путь).
func (a *App) completeFileLocked() bool {
	items := a.fileItems()
	if len(items) == 0 {
		return false
	}
	if a.palSel >= len(items) {
		a.palSel = 0
	}
	path := items[a.palSel]
	t := a.comp.Text()
	cur := a.comp.Cursor()
	// Text() отдаёт байты, Cursor() считает руны: срезы t[:cur] и
	// t[cur:] на кириллице резали посреди руны — подстановка пути
	// портила текст поля ввода невалидным UTF-8. Работаем в рунах.
	r := []rune(t)
	if cur > len(r) {
		return false
	}
	head := string(r[:cur])
	start := strings.LastIndexAny(head, " \n\t") + 1
	if !strings.HasPrefix(head[start:], "@") {
		return false
	}
	next := head[:start] + "@" + path + " " + string(r[cur:])
	a.comp.SetText(next)
	a.palSel = 0
	a.fileHide = ""
	return true
}

// ---------- Отрисовка ----------

// drawLocked — собрать кадр и вывести его. Вызывается под a.mu.
func (a *App) drawLocked() {
	if a.closed || !a.started || a.w <= 0 || a.h <= 0 {
		return
	}
	a.lastDraw = time.Now()
	s := a.buildLocked()
	a.applySelLocked(s)
	a.lastScreen = s
	a.rend.Draw(s)
	if a.modal != nil && a.modal.kind == modalAsk {
		if x, y, ok := a.askCursorLocked(); ok {
			a.rend.Cursor(x, y)
			return
		}
	}
	if a.modal != nil {
		a.rend.HideCursor()
		return
	}
	if x, y, ok := a.composerCursorLocked(); ok {
		a.rend.Cursor(x, y)
	} else {
		a.rend.HideCursor()
	}
}

// buildLocked — построить кадр.
func (a *App) buildLocked() *Screen {
	t := a.th
	s := NewScreen(a.w, a.h, t.Bg)
	a.drawHeader(s)
	bodyTop := 2
	bodyH := a.bodyHeight()
	a.drawBody(s, bodyTop, bodyH)
	a.drawFooter(s, bodyTop+bodyH)
	if a.paletteOpen() {
		a.drawPalette(s)
	} else if a.filePaletteOpen() {
		a.drawFilePalette(s)
	}
	if a.modal != nil {
		a.drawModal(s)
	}
	if !a.opt.Unicode {
		// ASCII-режим: фирменные знаки меняются на читаемые везде,
		// клетки остаются на своих местах.
		asciiFallback(s)
	}
	return s
}

// drawHeader — шапка: бренд, версия, теглайн и расход контекста —
// один в один с витриной README.
func (a *App) drawHeader(s *Screen) {
	t := a.th
	s.FillLine(0, Style{BG: t.Panel})
	x := 1
	s.Text(x, 0, "✦", Style{FG: t.Gold, BG: t.Panel})
	x += 2
	for _, seg := range wordmark(t, "gcli") {
		s.Text(x, 0, seg.Text, seg.S)
		x += StringWidth(seg.Text)
	}
	ver := strings.TrimPrefix(a.opt.Version, "v")
	if ver == "" {
		ver = "dev"
	}
	x++
	s.Text(x, 0, "v"+ver, Style{FG: t.Dim, BG: t.Panel})
	x += StringWidth("v"+ver) + 1
	s.Text(x, 0, "·", Style{FG: t.Border, BG: t.Panel})
	x += 2
	tag := "ИИ-агент в терминале"

	// Правая часть: расход контекста, как на витрине. Модель и провайдер
	// живут на заставке и в /status — шапка остаётся тонкой.
	pct := "—"
	if a.met.ContextMax > 0 {
		p := a.met.Context * 100 / a.met.ContextMax
		if p > 999 {
			p = 999
		}
		pct = fmt.Sprintf("%d%%", p)
	}
	right := "ctx " + pct
	rx := a.w - StringWidth(right) - 2
	if rx > x+6 {
		s.Text(rx, 0, right, Style{FG: t.Muted, BG: t.Panel})
	}
	// Теглайн — только если он кончается левее расхода контекста:
	// на узкой шапке строки наезжают друг на друга.
	if rx > x+StringWidth(tag)+2 {
		s.Text(x, 0, tag, Style{FG: t.Muted, BG: t.Panel})
	}
	// Тонкая линия под шапкой.
	s.FillLine(1, Style{BG: t.Bg})
	s.HLine(0, 1, a.w, '─', Style{FG: t.BorderDim, BG: t.Bg})
}

// drawBody — лента или приветствие.
func (a *App) drawBody(s *Screen, top, h int) {
	t := a.th
	if h <= 0 {
		return
	}
	if a.doc.Empty() && a.modal == nil {
		a.drawWelcome(s, top, h)
		return
	}
	w := a.w - 2
	if w < 10 {
		w = 10
	}
	a.refreshCacheLocked(w)
	lines := a.cache
	// Позиция окна: 0 — прилипание к низу.
	off := a.bodyOffset()
	for i := 0; i < h; i++ {
		idx := off + i
		if idx >= len(lines) {
			break
		}
		a.drawLine(s, 1, top+i, lines[idx], w)
	}
	// Индикатор прокрутки.
	if a.scroll > 0 {
		label := fmt.Sprintf(" ↑ %d ", a.scroll)
		x := a.w - StringWidth(label) - 2
		if x > 2 {
			s.Text(x, top+h-1, label, Style{FG: t.Gold, BG: t.Panel2, Bold: true})
		}
	}
	// Тонкая полоса прокрутки справа.
	if len(lines) > h {
		trackX := a.w - 1
		thumb := h * h / len(lines)
		if thumb < 1 {
			thumb = 1
		}
		pos := 0
		if len(lines) > h {
			pos = (len(lines) - h - a.scroll) * (h - thumb) / (len(lines) - h)
		}
		if pos < 0 {
			pos = 0
		}
		s.VLine(trackX, top, h, '│', Style{FG: t.BorderDim, BG: t.Bg})
		for i := 0; i < thumb; i++ {
			if y := top + pos + i; y < top+h {
				s.Set(trackX, y, '┃', Style{FG: t.Border, BG: t.Bg})
			}
		}
	}
}

// refreshCacheLocked — пересобрать раскладку, если она устарела.
func (a *App) refreshCacheLocked(w int) {
	spin := ""
	if a.busy && len(a.queue) == 0 {
		spin = spinner(a.tick / 2)
	}
	if a.cacheVer == a.doc.Version() && a.cacheW == w && a.cacheSpin == spin {
		return
	}
	a.cache, a.blockOf = a.doc.layout(LayoutOpts{Width: w, Theme: a.th, Spinner: spin})
	a.cacheVer = a.doc.Version()
	a.cacheW = w
	a.cacheSpin = spin
	// После смены ширины лента могла стать короче: прокрутка не должна
	// выходить за «длина кэша минус высота ленты». Клэмп к длине кэша
	// оставлял scroll > максимума: off упирался в ноль (низ ленты), а
	// счётчик «↑ N» и бегунок рисовались по завышенному scroll — три
	// противоречащих друг другу индикатора в одном кадре.
	if m := len(a.cache) - a.bodyHeight(); m > 0 {
		if a.scroll > m {
			a.scroll = m
		}
	} else {
		a.scroll = 0 // всё видно целиком — прокрутки нет
	}
}

// drawLine — нарисовать строку ленты в кадр.
func (a *App) drawLine(s *Screen, x, y int, l Line, w int) {
	if y < 0 || y >= s.H {
		return
	}
	col := x
	for _, seg := range l {
		st := seg.S
		if !st.BG.On {
			st.BG = a.th.Bg
		}
		col = s.Text(col, y, seg.Text, st)
		if col-x >= w {
			break
		}
	}
}

// drawWelcome — заставка: маскот, имя, подсказки.
func (a *App) drawWelcome(s *Screen, top, h int) {
	lines := a.welcomeLines()
	if len(lines) == 0 {
		return
	}
	y := top + (h-len(lines))/2
	if y < top {
		y = top
	}
	// Вся колонка центрируется целиком: центрировать каждую строку
	// отдельно нельзя — строки разной длины разъезжаются лесенкой.
	maxW := 0
	for _, l := range lines {
		if w := l.Width(); w > maxW {
			maxW = w
		}
	}
	x := (a.w - maxW) / 2
	if x < 1 {
		x = 1
	}
	for i, l := range lines {
		if y+i >= top+h {
			break
		}
		a.drawLine(s, x, y+i, l, a.w-x-1)
	}
}

// welcomeLines — содержимое заставки.
func (a *App) welcomeLines() []Line {
	t := a.th
	wide := !a.opt.HideMascot && a.opt.Unicode && a.w >= 86 && a.h >= 20
	state := a.mascotState()
	var left []Line
	if wide {
		art := NewScreen(24, 11, t.Bg)
		drawMascot(art, 0, 0, state, t.Bg)
		for y := 0; y < 11; y++ {
			var l Line
			for x := 0; x < 24; x++ {
				c := art.Cells[y*24+x]
				if c.Cont {
					continue
				}
				ch := c.R
				if ch == 0 {
					ch = ' '
				}
				l = append(l, Seg{Text: string(ch), S: c.S})
			}
			left = append(left, l)
		}
	}
	// Правая колонка.
	var right []Line
	brand := Line{{Text: "✦ ", S: Style{FG: t.Gold, BG: t.Bg}}}
	brand = append(brand, wordmarkOn(t, "gcli", t.Bg)...)
	brand = append(brand, Seg{Text: "  " + version(a.opt.Version), S: Style{FG: t.Dim, BG: t.Bg}})
	right = append(right, brand)
	right = append(right, Line{{Text: "агент в терминале · маскот Искра", S: Style{FG: t.Muted, BG: t.Bg}}})
	right = append(right, Line{})
	cwd := a.opt.WorkDir
	if cwd == "" {
		cwd = "."
	}
	right = append(right, Line{
		{Text: "каталог  ", S: Style{FG: t.Dim, BG: t.Bg}},
		{Text: Truncate(cwd, 52), S: Style{FG: t.Text, BG: t.Bg}},
	})
	model := a.opt.Model
	if model == "" {
		model = "не выбрана"
	}
	if a.opt.Provider != "" {
		model = a.opt.Provider + " · " + model
	}
	right = append(right, Line{
		{Text: "модель   ", S: Style{FG: t.Dim, BG: t.Bg}},
		{Text: Truncate(model, 52), S: Style{FG: t.Accent2, BG: t.Bg}},
	})
	right = append(right, Line{})
	right = append(right, Line{{Text: "с чего начать", S: Style{FG: t.Muted, BG: t.Bg, Bold: true}}})
	tips := []string{
		"почини тесты в ./src",
		"найди, где считается итог заказа",
		"сделай скриншот localhost:3000 и проверь вёрстку",
	}
	for _, tip := range tips {
		right = append(right, Line{
			{Text: "  › ", S: Style{FG: t.Accent, BG: t.Bg}},
			{Text: Truncate(tip, 58), S: Style{FG: t.Text, BG: t.Bg}},
		})
	}
	right = append(right, Line{})
	right = append(right, Line{
		{Text: "/help", S: Style{FG: t.Gold, BG: t.Bg}},
		{Text: " — команды   ", S: Style{FG: t.Dim, BG: t.Bg}},
		{Text: "shift+enter", S: Style{FG: t.Gold, BG: t.Bg}},
		{Text: " — отправить   ", S: Style{FG: t.Dim, BG: t.Bg}},
		{Text: "ctrl+o", S: Style{FG: t.Gold, BG: t.Bg}},
		{Text: " — размышления", S: Style{FG: t.Dim, BG: t.Bg}},
	})

	if !wide {
		// Узкий экран: маскот сверху, текст под ним. На низком окне
		// маскот уступает место подсказкам: обрезанная заставка хуже
		// полной, но без маскота.
		if !a.opt.HideMascot && a.opt.Unicode && a.h >= 30 {
			art := NewScreen(24, 11, t.Bg)
			drawMascot(art, 0, 0, state, t.Bg)
			for y := 0; y < 11; y++ {
				var l Line
				for x := 0; x < 24; x++ {
					c := art.Cells[y*24+x]
					if c.Cont {
						continue
					}
					ch := c.R
					if ch == 0 {
						ch = ' '
					}
					l = append(l, Seg{Text: string(ch), S: c.S})
				}
				left = append(left, l)
			}
			left = append(left, Line{})
		}
		return append(left, right...)
	}
	// Склеиваем колонки.
	var out []Line
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	for i := 0; i < n; i++ {
		var l Line
		if i < len(left) {
			l = append(l, left[i]...)
		} else {
			l = append(l, Seg{Text: strings.Repeat(" ", 24), S: Style{BG: t.Bg}})
		}
		l = append(l, Seg{Text: "   ", S: Style{BG: t.Bg}})
		if i < len(right) {
			l = append(l, right[i]...)
		}
		out = append(out, l.trimEnd())
	}
	return out
}

// mascotState — настроение Искры на заставке.
//
// Кадр заставки перерисовывается раз в ~0.9 с, поэтому окно мигания шире
// одного кадра: иначе кот «моргал» бы так быстро, что этого не заметить.
// Настроение повторяет работу: работает, думает, празднует завершение.
func (a *App) mascotState() MascotState {
	if a.busy {
		if a.activity == "думаю" {
			return MascotThink
		}
		return MascotWork
	}
	if !a.doneAt.IsZero() && time.Since(a.doneAt) < 3*time.Second {
		return MascotDone
	}
	if a.tick%60 >= 8 && a.tick%60 < 16 {
		return MascotBlink
	}
	return MascotIdle
}

// miniMascotState — поза мини-котa у поля ввода. Как у заставки, но
// blink реже: тут каждое движение перерисовывает кадр целиком.
func (a *App) miniMascotState() MascotState {
	st := a.mascotState()
	if st == MascotBlink && a.tick%60 < 12 {
		return st
	}
	if st == MascotBlink {
		return MascotIdle
	}
	return st
}

// wordmarkOn — имя продукта на заданном фоне.
func wordmarkOn(t Theme, word string, bg Color) []Seg {
	segs := wordmark(t, word)
	for i := range segs {
		segs[i].S.BG = bg
	}
	return segs
}

// version — версия с префиксом.
func version(v string) string {
	if v == "" {
		return "dev"
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// drawPalette — подсказка команд над полем ввода.
//
// Рисуется поверх нижних строк ленты: это dropdown, а не часть диалога.
func (a *App) drawPalette(s *Screen) {
	items := a.paletteItems()
	if len(items) == 0 {
		return
	}
	t := a.th
	boxW := 56
	if boxW > a.w-4 {
		boxW = a.w - 4
	}
	if boxW < 16 {
		return
	}
	// Список не должен уезжать за нижний край и закрывать поле ввода:
	// показываем окно вокруг выбранной команды, а не все подряд.
	maxItems := a.composerTop() - 4
	if maxItems > 10 {
		maxItems = 10
	}
	if maxItems < 3 {
		return
	}
	start := 0
	if a.palSel >= maxItems {
		start = a.palSel - maxItems + 1
	}
	if start > len(items)-maxItems {
		start = len(items) - maxItems
	}
	if start < 0 {
		start = 0
	}
	end := start + maxItems
	if end > len(items) {
		end = len(items)
	}
	shown := items[start:end]
	boxH := len(shown) + 2
	y := a.composerTop() - boxH
	if y < 2 {
		y = 2
	}
	x := 3
	s.Fill(x, y, boxW, boxH, Style{BG: t.Panel})
	s.Box(x, y, boxW, boxH, a.corners, Style{FG: t.Border, BG: t.Panel})
	if len(items) > maxItems {
		title := fmt.Sprintf(" команды %d–%d из %d ", start+1, end, len(items))
		s.Text(x+2, y, title, Style{FG: t.Dim, BG: t.Panel})
	}
	for i, c := range shown {
		sel := start+i == a.palSel
		st := Style{FG: t.Text, BG: t.Panel}
		if sel {
			s.Fill(x+1, y+1+i, boxW-2, 1, Style{BG: t.Panel2})
			st = Style{FG: t.Gold, BG: t.Panel2, Bold: true}
		}
		mark := "  "
		if sel {
			mark = "› "
		}
		s.Text(x+2, y+1+i, mark+c.Name, st)
		descX := x + 2 + StringWidth(mark+c.Name) + 2
		room := x + boxW - descX - 1
		if room > 4 {
			ds := Style{FG: t.Muted, BG: t.Panel}
			if sel {
				ds.BG = t.Panel2
			}
			s.Text(descX, y+1+i, Truncate(c.Desc, room), ds)
		}
	}
}

// drawFilePalette — подсказка файлов для @-дополнения. Тот же dropdown,
// что у команд: друг только список и заголовок.
func (a *App) drawFilePalette(s *Screen) {
	items := a.fileItems()
	if len(items) == 0 {
		return
	}
	t := a.th
	boxW := 56
	if boxW > a.w-4 {
		boxW = a.w - 4
	}
	if boxW < 16 {
		return
	}
	maxItems := a.composerTop() - 4
	if maxItems > 8 {
		maxItems = 8
	}
	if maxItems < 3 {
		return
	}
	start := 0
	if a.palSel >= maxItems {
		start = a.palSel - maxItems + 1
	}
	if start > len(items)-maxItems {
		start = len(items) - maxItems
	}
	if start < 0 {
		start = 0
	}
	end := start + maxItems
	if end > len(items) {
		end = len(items)
	}
	shown := items[start:end]
	boxH := len(shown) + 2
	y := a.composerTop() - boxH
	if y < 2 {
		y = 2
	}
	x := 3
	s.Fill(x, y, boxW, boxH, Style{BG: t.Panel})
	s.Box(x, y, boxW, boxH, a.corners, Style{FG: t.Border, BG: t.Panel})
	title := " файлы: tab — подставить "
	if len(items) > maxItems {
		title = fmt.Sprintf(" файлы %d–%d из %d ", start+1, end, len(items))
	}
	s.Text(x+2, y, title, Style{FG: t.Dim, BG: t.Panel})
	for i, f := range shown {
		sel := start+i == a.palSel
		st := Style{FG: t.Text, BG: t.Panel}
		if sel {
			s.Fill(x+1, y+1+i, boxW-2, 1, Style{BG: t.Panel2})
			st = Style{FG: t.Gold, BG: t.Panel2, Bold: true}
		}
		mark := "◧ "
		if sel {
			mark = "› ◧ "
		}
		s.Text(x+2, y+1+i, mark, Style{FG: t.Accent2, BG: st.BG})
		s.Text(x+2+StringWidth(mark), y+1+i, Truncate(f, boxW-StringWidth(mark)-4), st)
	}
}

// drawFooter — поле ввода, подсказки и статус.
func (a *App) drawFooter(s *Screen, top int) {
	t := a.th
	if top >= a.h {
		return
	}
	boxTop := a.composerTop()
	a.drawComposer(s, boxTop, a.composerHeight())
	// Пустая строка между рамкой и подсказками — как на витрине.
	if blank := boxTop + a.composerHeight(); blank < a.h-2 {
		s.FillLine(blank, Style{BG: t.Bg})
	}
	a.drawHints(s, a.h-2)
	a.drawStatus(s, a.h-1)
}

// drawComposer — рамка и текст ввода.
func (a *App) drawComposer(s *Screen, top, h int) {
	t := a.th
	border := t.Border
	if a.busy {
		border = Mix(t.Border, t.Accent2, 0.45)
	}
	st := Style{FG: border, BG: t.Panel}
	boxX := a.composerX()
	boxW := a.w - boxX - 2
	// Рамка.
	s.Fill(boxX, top, boxW, h, Style{BG: t.Panel})
	s.Box(boxX, top, boxW, h, a.corners, st)
	// Метка в верхней рамке: что делает агент прямо сейчас. Без активности —
	// привычное «работаю»/очередь; с активностью — конкретика: «read src/main.go».
	label := ""
	if a.busy {
		act := spinner(a.tick/2) + " работаю"
		if a.activity != "" {
			room := boxW - 24
			if room > 8 {
				act = spinner(a.tick/2) + " " + Truncate(a.activity, room)
			}
		}
		label = " " + act + " "
	} else if len(a.queue) > 0 {
		label = fmt.Sprintf(" в очереди: %d ", len(a.queue))
	}
	if label != "" {
		lx := a.w - 4 - StringWidth(label)
		if lx > boxX+3 {
			s.Text(lx, top, label, Style{FG: t.Accent2, BG: t.Panel, Bold: true})
		}
	}
	// Содержимое.
	inner := a.composerInner()
	lines := a.comp.Lines(inner)
	_, cy := a.comp.CursorPos(inner)
	maxLines := h - 2
	if maxLines < 1 {
		maxLines = 1
	}
	start := 0
	if cy >= maxLines {
		start = cy - maxLines + 1
	}
	if a.comp.Empty() {
		// Витрина: пустое поле — приглашение по центру рамки.
		py := top + 1 + (maxLines-1)/2
		s.Text(boxX+2, py, "❯ ", Style{FG: t.Accent, BG: t.Panel, Bold: true})
		s.Text(boxX+4, py, "спроси что-нибудь… shift+enter — отправить", Style{FG: t.Dim, BG: t.Panel})
	} else {
		for i := 0; i < maxLines; i++ {
			idx := start + i
			if idx >= len(lines) {
				break
			}
			y := top + 1 + i
			prompt := "  "
			pst := Style{FG: t.Dim, BG: t.Panel}
			if idx == 0 {
				prompt = "❯ "
				pst = Style{FG: t.Accent, BG: t.Panel, Bold: true}
			}
			s.Text(boxX+2, y, prompt, pst)
			s.Text(boxX+4, y, lines[idx], Style{FG: t.Text, BG: t.Panel})
		}
	}
	// Мини-Искра в левой колонке: ASCII-кот из витрины, поза по состоянию.
	if a.railWidth() > 0 {
		ph := a.tick / 2
		content := a.composerHeight() - 2
		y := top + 1
		if content > 3 {
			y += (content - 3) / 2
		}
		drawMiniCat(s, 1, y, a.miniMascotState(), ph, t)
	}
}

// drawHints — строка подсказок.
func (a *App) drawHints(s *Screen, y int) {
	if y < 0 || y >= a.h {
		return
	}
	t := a.th
	s.FillLine(y, Style{BG: t.Bg})
	left := " /help — команды · /mascot demo — все состояния Искры · shift+enter — отправить"
	if a.paletteOpen() {
		left = " ↑↓ выбор · tab вставить · esc закрыть"
	}
	if a.filePaletteOpen() {
		left = " ↑↓ файл · tab/enter подставить · esc закрыть"
	}
	if a.busy {
		left = " shift+enter в очередь · esc остановить · pgup/pgdn прокрутка"
	}
	if a.scroll > 0 {
		left = " pgup/pgdn прокрутка · ctrl+end — вниз"
	}
	s.Text(1, y, Truncate(left, a.w-2), Style{FG: t.Dim, BG: t.Bg})
	if a.toast == "" {
		return
	}
	msg := " " + a.toast + " "
	x := a.w - StringWidth(msg) - 1
	if x <= StringWidth(left)+2 {
		// Справа не помещается — сообщение важнее подсказки: показываем
		// его на всей строке вместо неё, иначе «ещё раз ctrl+c» на
		// восьмидесяти колонках не увидеть вовсе.
		s.FillLine(y, Style{BG: t.Bg})
		s.Text(1, y, Truncate(msg, a.w-2), Style{FG: t.Gold, BG: t.Panel2})
		return
	}
	s.Text(x, y, msg, Style{FG: t.Gold, BG: t.Panel2})
}

// drawStatus — нижняя панель расхода.
func (a *App) drawStatus(s *Screen, y int) {
	if y < 0 || y >= a.h {
		return
	}
	t := a.th
	s.FillLine(y, Style{BG: t.Panel})
	x := 1
	s.Text(x, y, "✦", Style{FG: t.Gold, BG: t.Panel})
	x += 2
	// Контекст: полоса и процент.
	pct := 0
	if a.met.ContextMax > 0 {
		pct = a.met.Context * 100 / a.met.ContextMax
		if pct > 999 {
			pct = 999
		}
	}
	barW := 10
	fill := 0
	if a.met.ContextMax > 0 {
		// Округление до ближайшего, а не к нулю: витринная полоса при
		// малых процентах не должна выглядеть пустой.
		fill = (2*barW*a.met.Context + a.met.ContextMax) / (2 * a.met.ContextMax)
		if fill > barW {
			fill = barW
		}
	}
	barCol := t.Accent
	if pct >= 85 {
		barCol = t.Err
	} else if pct >= 65 {
		barCol = t.Warn
	}
	for i := 0; i < barW; i++ {
		ch := '░'
		col := t.Border
		if i < fill {
			ch = '█'
			col = barCol
		}
		s.Set(x+i, y, ch, Style{FG: col, BG: t.Panel})
	}
	x += barW + 1
	ctx := fmt.Sprintf("%d%%", pct)
	if a.met.ContextMax == 0 {
		ctx = "—"
	}
	s.Text(x, y, ctx, Style{FG: t.Muted, BG: t.Panel})
	x += StringWidth(ctx) + 2
	s.Text(x, y, "·", Style{FG: t.Border, BG: t.Panel})
	x += 2
	// Метки режимов справа. Их ширина нужна до текста: на узком экране
	// текст не должен залезать под них и обрезаться в многоточие.
	badges := make([]statusBadge, 0, 3)
	if a.met.Autopilot {
		badges = append(badges, statusBadge{"автопилот", t.Warn})
	}
	if a.met.Plan {
		badges = append(badges, statusBadge{"план", t.Accent2})
	}
	if a.busy {
		badges = append(badges, statusBadge{spinner(a.tick/2) + " запрос", t.Accent})
	}
	if a.opt.CronEnabled {
		badges = append(badges, statusBadge{"cron", t.Gold})
	}
	badgeW := 0
	for _, b := range badges {
		badgeW += StringWidth(b.text) + 3
	}
	// Токены, скорость, время запроса, запросы, сессия, цена — в порядке
	// важности. Что не влезло, отбрасывается с конца, а не режется
	// многоточием посреди числа.
	parts := make([]string, 0, 6)
	if a.met.Prompt > 0 || a.met.Completion > 0 {
		parts = append(parts, fmt.Sprintf("↑%s ↓%s", kfmt(a.met.Prompt), kfmt(a.met.Completion)))
	}
	if a.met.Rate > 0 {
		parts = append(parts, rateText(a.met.Rate))
	}
	if a.met.Last > 0 {
		parts = append(parts, humanDur(a.met.Last))
	}
	if a.met.Requests > 0 {
		parts = append(parts, fmt.Sprintf("%d запр.", a.met.Requests))
	}
	if a.met.Session > 0 {
		parts = append(parts, humanDur(a.met.Session))
	}
	if a.met.CostKnown && a.met.Cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", a.met.Cost))
	}
	avail := a.w - x - 2 - badgeW
	if avail < 8 {
		avail = 8
	}
	joined := strings.Join(parts, "  ·  ")
	for len(parts) > 0 && StringWidth(joined) > avail {
		parts = parts[:len(parts)-1]
		joined = strings.Join(parts, "  ·  ")
	}
	s.Text(x, y, Truncate(joined, avail), Style{FG: t.Muted, BG: t.Panel})
	// Правая часть: режимы.
	bx := a.w - 2
	for i := len(badges) - 1; i >= 0; i-- {
		b := badges[i]
		bw := StringWidth(b.text) + 2
		bx -= bw
		if bx < x+4 {
			break
		}
		s.Text(bx, y, " "+b.text+" ", Style{FG: b.col, BG: t.Panel2})
		bx--
	}
}

// statusBadge — метка режима в нижней панели.
type statusBadge struct {
	text string
	col  Color
}

// modalGeom — геометрия окна и готовое тело.
type modalGeom struct {
	x, y, w, h int
	body       []Line
	inputRow   int // строка поля ввода (modalAsk) или -1
}

// modalLayout — собрать тело окна и посчитать рамку.
//
// Тело строится в одном месте: окно и курсор поля вопроса должны
// пользоваться одной раскладкой, иначе курсор уезжает на чужую строку.
func (a *App) modalLayout() modalGeom {
	t := a.th
	m := a.modal
	boxW := a.w - 8
	if boxW > 78 {
		boxW = 78
	}
	if boxW < 30 {
		boxW = a.w - 2
	}
	if boxW < 12 {
		boxW = a.w
	}
	inner := boxW - 4
	if inner < 4 {
		inner = 4
	}
	body := make([]Line, 0, 24)
	if m.detail != "" {
		segs := wrapSegs(Line{{Text: m.detail, S: Style{FG: t.Text, BG: t.Panel2}}}, inner)
		if len(segs) > 4 {
			segs = append(segs[:4], Line{{Text: "…", S: Style{FG: t.Dim, BG: t.Panel2}}})
		}
		body = append(body, segs...)
	}
	if m.path != "" {
		if len(body) > 0 {
			body = append(body, Line{})
		}
		body = append(body, Line{
			{Text: "◧ ", S: Style{FG: t.Gold, BG: t.Panel2}},
			{Text: Truncate(m.path, inner-2), S: Style{FG: t.Accent2, BG: t.Panel2}},
		})
	}
	if m.note != "" {
		if len(body) > 0 {
			body = append(body, Line{})
		}
		segs := wrapSegs(Line{{Text: m.note, S: Style{FG: t.Muted, BG: t.Panel2, Italic: true}}}, inner)
		if len(segs) > 3 {
			segs = append(segs[:3], Line{{Text: "…", S: Style{FG: t.Dim, BG: t.Panel2}}})
		}
		body = append(body, segs...)
	}
	if len(m.diff) > 0 {
		// Оставляем место кнопкам, подсказке и предупреждению. На низком
		// терминале лучше показать меньше диффа, чем остаться без кнопок.
		room := a.h - 4 - len(body) - 6
		if room < 3 && a.h >= 12 {
			room = 3
		}
		if room > 0 {
			ops := m.diff
			if len(ops) > room {
				ops = ops[:room]
			}
			body = append(body, Line{})
			for _, op := range ops {
				col := t.Muted
				prefix := "  "
				switch op.Kind {
				case '+':
					col = t.OK
					prefix = "+ "
				case '-':
					col = t.Err
					prefix = "- "
				}
				body = append(body, Line{{Text: prefix + Truncate(op.Text, inner-2), S: Style{FG: col, BG: t.Panel2}}})
			}
		}
	}
	if m.danger {
		body = append(body, Line{})
		body = append(body, Line{{Text: "⚠ команда может изменить или удалить данные", S: Style{FG: t.Err, BG: t.Panel2, Bold: true}}})
	}
	// Кнопки: клавиша в скобках, выбранная — инверсией. Длинный список
	// переносится по строкам: иначе дальние варианты уходили бы за рамку.
	if len(m.opts) > 0 {
		if n := len(body); n == 0 || len(body[n-1]) > 0 {
			body = append(body, Line{})
		}
		var cur Line
		curW := 0
		for i, o := range m.opts {
			label := o.Label
			if o.Key != 0 {
				label = "[" + string(o.Key) + "] " + o.Label
			}
			st := Style{FG: t.Text, BG: t.Panel}
			if i == m.sel {
				st = Style{FG: t.Bg, BG: t.Gold, Bold: true}
			}
			seg := Seg{Text: " " + label + " ", S: st}
			segW := StringWidth(seg.Text) + 2
			if curW > 0 && curW+segW > inner {
				body = append(body, cur)
				cur, curW = Line{}, 0
			}
			cur = append(cur, seg, Seg{Text: "  ", S: Style{BG: t.Panel2}})
			curW += segW
		}
		if len(cur) > 0 {
			body = append(body, cur)
		}
	}
	inputRow := -1
	if m.kind == modalAsk {
		body = append(body, Line{})
		inputRow = len(body)
		shown := m.input.Text()
		if m.secret {
			// Ключи и токены не должны оставаться на экране даже на миг:
			// показываем по точке на символ, содержимое знает только поле.
			shown = strings.Repeat("•", len([]rune(shown)))
		}
		body = append(body, Line{
			{Text: "❯ ", S: Style{FG: t.Accent, BG: t.Panel2, Bold: true}},
			{Text: shown, S: Style{FG: t.Text, BG: t.Panel2}},
		})
		hint := "enter ответить · esc отмена"
		if len(m.opts) > 0 {
			hint = "enter ответить · tab вариант · esc отмена"
		}
		body = append(body, Line{{Text: hint, S: Style{FG: t.Dim, BG: t.Panel2}}})
	} else {
		body = append(body, Line{{Text: "←→ выбор · enter подтвердить · esc отмена", S: Style{FG: t.Dim, BG: t.Panel2}}})
	}
	boxH := len(body) + 2
	if max := a.h - 2; boxH > max {
		boxH = max
	}
	if boxH < 6 {
		boxH = 6
	}
	// Если тело не помещается, режем верх: путь и кнопки важнее
	// многословного объяснения.
	for len(body) > boxH-2 {
		body = body[1:]
		if inputRow >= 0 {
			inputRow--
		}
	}
	x := (a.w - boxW) / 2
	if x < 1 {
		x = 1
	}
	y := (a.h - boxH) / 2
	if y < 1 {
		y = 1
	}
	return modalGeom{x: x, y: y, w: boxW, h: boxH, body: body, inputRow: inputRow}
}

// drawModal — окно подтверждения или вопроса.
func (a *App) drawModal(s *Screen) {
	t := a.th
	g := a.modalLayout()
	m := a.modal
	// Светлая полоса во всю ширину стирает обрывки строк, которые иначе
	// липли бы к рамке окна; на высоком экране она не шире самого окна.
	s.Dim(0.5)
	s.Fill(0, g.y-1, a.w, g.h+2, Style{BG: Darken(t.Bg, 0.5)})
	// Тень и фон.
	s.Fill(g.x+1, g.y+1, g.w, g.h, Style{BG: Darken(t.Bg, 0.7)})
	s.Fill(g.x, g.y, g.w, g.h, Style{BG: t.Panel2})
	bcol := t.Gold
	if m.danger {
		bcol = t.Err
	}
	s.Box(g.x, g.y, g.w, g.h, a.corners, Style{FG: bcol, BG: t.Panel2})
	// Заголовок на рамке.
	title := " " + Truncate(m.title, g.w-6) + " "
	if StringWidth(title) < g.w-4 {
		s.Text(g.x+3, g.y, title, Style{FG: bcol, BG: t.Panel2, Bold: true})
	}
	for i, l := range g.body {
		if i >= g.h-2 {
			break
		}
		a.drawLine(s, g.x+2, g.y+1+i, l, g.w-4)
	}
}

// composerCursorLocked — позиция курсора в поле ввода.
func (a *App) composerCursorLocked() (int, int, bool) {
	if a.comp.Empty() {
		// Пустое поле: приглашение рисуется по центру рамки, курсор к нему
		// не привязан — прятать.
		return 0, 0, false
	}
	boxTop := a.composerTop()
	boxH := a.composerHeight()
	inner := a.composerInner()
	cx, cy := a.comp.CursorPos(inner)
	maxLines := boxH - 2
	if maxLines < 1 {
		return 0, 0, false
	}
	start := 0
	if cy >= maxLines {
		start = cy - maxLines + 1
	}
	row := cy - start
	return a.composerX() + 4 + cx, boxTop + 1 + row, true
}

// askCursorLocked — курсор в поле вопроса.
func (a *App) askCursorLocked() (int, int, bool) {
	if a.modal == nil || a.modal.kind != modalAsk {
		return 0, 0, false
	}
	g := a.modalLayout()
	if g.inputRow < 0 || g.inputRow >= g.h-2 {
		return 0, 0, false
	}
	return g.x + 4 + a.modal.input.CursorX(), g.y + 1 + g.inputRow, true
}

// CursorX — колонка курсора в однострочном поле.
func (c *Composer) CursorX() int {
	x := 0
	for i := 0; i < c.cur && i < len(c.text); {
		cw, n := nextClusterW(c.text, i)
		x += cw
		i += n
	}
	return x
}

// ---------- Мелкие форматтеры ----------

// kfmt — короткое число: 12, 1.2k, 3.4M.
func kfmt(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// rateText — скорость вывода.
func rateText(r float64) string {
	if r < 10 {
		return fmt.Sprintf("%.1f tok/s", r)
	}
	return fmt.Sprintf("%d tok/s", int(r+0.5))
}

// humanDur — длительность для панели.
func humanDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dмс", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fс", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dм%02dс", m, s)
}
