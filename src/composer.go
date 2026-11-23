package main

import (
	"fmt"
	"strings"
	"sync"

	"gcli/ui"
)

// ---------- Композер: поле ввода в стиле Claude Code ----------
//
// Поле ввода живёт в нижней области экрана (ui.Live*): скруглённая рамка,
// курсор-блок, pop-up меню команд по «/», история по стрелкам, вставка
// многострочного текста плейсхолдером. Ход агентом идёт в отдельной
// горутине, поэтому печатать и править текст можно прямо во время ответа:
// Esc прерывает, Enter ставит сообщение в очередь.
//
// Единственный читатель stdin - stdinReader; композер - единственный
// потребитель событий. Вопросы подтверждений (askLine) обслуживаются тем
// же циклом через канал askCh: второй читатель stdin не нужен.

// askReq - запрос строки у человека (подтверждения, секреты, ask_user).
type askReq struct {
	question string
	secret   bool
	options  []string
	reply    chan string // пустая строка = отказ
}

// composer - состояние поля ввода.
type composer struct {
	a  *app
	ui *ui.UI

	// Поле ввода.
	text []rune
	cur  int // позиция курсора в рунах

	// История поданных строк.
	hist      []string
	histIdx   int // -1 = вне истории
	histDraft string

	// Pop-up меню команд.
	menu    []ui.MenuItem
	menuSel int

	// Плейсхолдеры вставленного многострочного текста.
	pastes map[string]string
	pasteN int

	// Очередь сообщений на время занятого хода.
	pending  []string
	inflight bool

	// Служебное.
	quit      bool
	exitArm   bool // Ctrl+C уже нажат один раз с пустым полем
	askSecret bool // рисовать точки вместо символов
	// live - рисовать ли живую область: только в raw-режиме терминала.
	// В пайпах и CI регион рисовать нечем и не нужно - ввод построчный.
	live   bool
	askCh  chan askReq
	redraw chan struct{}
	done   chan struct{}
	once   sync.Once
}

// newComposer - собрать композер и включить живую область.
func newComposer(a *app) *composer {
	c := &composer{
		a:      a,
		ui:     a.ui,
		pastes: map[string]string{},
		askCh:  make(chan askReq, 4),
		redraw: make(chan struct{}, 8),
		done:   make(chan struct{}),
	}
	c.live = a.stdin.raw.Load()
	if c.live {
		c.ui.LiveStart(c.placeholder())
		c.ui.LiveFooter("? for shortcuts", a.footerRight())
	}
	return c
}

// placeholderTry - подсказки пустого поля, как в Claude Code.
var placeholderTry = []string{
	`Try "/help"`,
	`Try "исправь падающий тест"`,
	`Try "объясни, что делает этот проект"`,
	`Try "/status"`,
}

// liveMenuCap - потолок отфильтрованного меню (регион показывает шесть).
const liveMenuCap = 9

// ---------- Цикл событий ----------

// run - главный цикл композера: читает события stdin и обслуживает
// вопросы подтверждений. Блокируется до выхода.
func (c *composer) run() {
	defer c.once.Do(func() { close(c.done) })
	for !c.quit {
		select {
		case ev, ok := <-c.a.stdin.Events():
			if !ok {
				c.quit = true
				return
			}
			c.handle(ev)
		case req := <-c.askCh:
			c.serveAsk(req)
		case <-c.redraw:
			c.refresh()
		}
	}
}

// Done - канал завершения цикла (для синхронизации в тестах).
func (c *composer) Done() <-chan struct{} { return c.done }

// Stop - потребовать завершения цикла (из slash-команды выхода).
func (c *composer) Stop() {
	c.quit = true
	c.wake()
}

// wake - разбудить цикл перерисовкой.
func (c *composer) wake() {
	select {
	case c.redraw <- struct{}{}:
	default:
	}
}

// refresh - перерисовать регион и строку подсказок с актуальными метриками.
func (c *composer) refresh() {
	if !c.live {
		return
	}
	c.ui.LiveFooter(c.footerLeft(), c.a.footerRight())
	c.ui.LiveInput(c.displayText(), c.cur)
}

// footerLeft - левая часть строки подсказок.
func (c *composer) footerLeft() string {
	if len(c.menu) > 0 {
		return "enter выбрать · tab дополнить · esc закрыть"
	}
	if c.exitArm {
		return "press ctrl+c again to exit"
	}
	return "? for shortcuts"
}

// displayText - текст поля ввода как он рисуется: у секретных вопросов
// каждый символ заменяется точкой.
func (c *composer) displayText() string {
	if c.askSecret {
		return strings.Repeat("•", len(c.text))
	}
	return string(c.text)
}

// ---------- Обработка событий ----------

// handle - одно событие ввода.
func (c *composer) handle(ev termEvent) {
	defer c.sync() // после каждого события пересчитать меню и перерисовать

	switch ev.kind {
	case evEOF:
		c.quit = true
		return
	case evLine:
		// Cooked-режим (пайп): строки приходят готовыми.
		line := strings.TrimSpace(ev.line)
		if line == "" {
			return
		}
		c.submit(line)
		return
	case evPaste:
		c.insertPaste(ev.runes)
		return
	case evRune:
		c.insertRune(ev.r)
		return
	case evKey:
		c.handleKey(ev.key)
	}
}

// handleKey - клавиши.
func (c *composer) handleKey(k keyID) {
	switch k {
	case keyEnter:
		c.onEnter()
	case keyShiftEnter:
		c.insert([]rune{'\n'})
	case keyBackspace:
		if c.cur > 0 {
			c.text = append(c.text[:c.cur-1], c.text[c.cur:]...)
			c.cur--
		}
	case keyDelete:
		if c.cur < len(c.text) {
			c.text = append(c.text[:c.cur], c.text[c.cur+1:]...)
		}
	case keyLeft:
		if c.cur > 0 {
			c.cur--
		}
	case keyRight:
		if c.cur < len(c.text) {
			c.cur++
		}
	case keyHome:
		c.cur = 0
	case keyEnd:
		c.cur = len(c.text)
	case keyCtrlA:
		c.cur = 0
	case keyCtrlE:
		c.cur = len(c.text)
	case keyCtrlU:
		c.text = c.text[:0]
		c.cur = 0
	case keyCtrlK:
		c.text = c.text[:c.cur]
	case keyCtrlW:
		c.deleteWord()
	case keyUp:
		c.onUp()
	case keyDown:
		c.onDown()
	case keyTab:
		c.completeMenu()
	case keyBackTab:
		if len(c.menu) > 0 {
			c.menuSel--
			if c.menuSel < 0 {
				c.menuSel = len(c.menu) - 1
			}
			c.pushMenu()
		}
	case keyEsc:
		c.onEsc()
	case keyCtrlC:
		c.onCtrlC()
	case keyCtrlD:
		if len(c.text) == 0 && !c.inflight {
			c.quit = true
			return
		}
		if c.cur < len(c.text) {
			c.text = append(c.text[:c.cur], c.text[c.cur+1:]...)
		}
	case keyCtrlL:
		c.clearScreen()
	case keyCtrlO:
		c.a.ctrlO()
		c.refresh()
	case keyPageUp, keyPageDown, keyCtrlR, keyCtrlT, keyCtrlN, keyCtrlP:
		// Резерв: поиск по истории и прочее - не задействовано.
	}
}

// insertRune - печатный символ в позицию курсора.
func (c *composer) insertRune(r rune) {
	if r == '\r' || r == '\n' {
		c.insert([]rune{'\n'})
		return
	}
	c.insert([]rune{r})
}

// insert - вставить руны в позицию курсора.
func (c *composer) insert(rs []rune) {
	if len(rs) == 0 {
		return
	}
	out := make([]rune, 0, len(c.text)+len(rs))
	out = append(out, c.text[:c.cur]...)
	out = append(out, rs...)
	out = append(out, c.text[c.cur:]...)
	c.text = out
	c.cur += len(rs)
	c.exitArm = false
}

// deleteWord - Ctrl+W: стереть слово перед курсором.
func (c *composer) deleteWord() {
	i := c.cur
	for i > 0 && c.text[i-1] == ' ' {
		i--
	}
	for i > 0 && c.text[i-1] != ' ' && c.text[i-1] != '\n' {
		i--
	}
	c.text = append(c.text[:i], c.text[c.cur:]...)
	c.cur = i
}

// onUp/onDown - по меню или по истории/строкам многострочного ввода.
func (c *composer) onUp() {
	if len(c.menu) > 0 {
		c.menuSel--
		if c.menuSel < 0 {
			c.menuSel = len(c.menu) - 1
		}
		c.pushMenu()
		return
	}
	c.moveLine(-1)
}

func (c *composer) onDown() {
	if len(c.menu) > 0 {
		c.menuSel = (c.menuSel + 1) % len(c.menu)
		c.pushMenu()
		return
	}
	c.moveLine(1)
}

// moveLine - вертикальное движение курсора: внутри многострочного текста -
// по строкам, на однострочном поле Up/Down листают историю (вверх с первой
// строки уходит в историю, вниз с последней возвращается).
func (c *composer) moveLine(d int) {
	if c.textIsSingle() || (d < 0 && c.cur == 0) || (d > 0 && c.cur == len(c.text)) {
		if c.textIsSingle() {
			c.histMove(d)
			return
		}
		c.histMove(d)
		return
	}
	start, _, col := c.lineBounds()
	var otherStart, otherEnd int
	if d < 0 {
		otherEnd = start - 1 // символ \n над текущей строкой
		otherStart = prevLineStart(c.text, otherEnd)
	} else {
		end := nextLineEnd(c.text, c.cur)
		otherStart = end + 1
		otherEnd = nextLineEnd(c.text, otherStart)
	}
	otherLen := otherEnd - otherStart
	if col > otherLen {
		col = otherLen
	}
	c.cur = otherStart + col
}

// textIsSingle - в поле одна строка (без переводов).
func (c *composer) textIsSingle() bool {
	for _, r := range c.text {
		if r == '\n' {
			return false
		}
	}
	return true
}

// lineBounds - границы строки под курсором и колонка.
func (c *composer) lineBounds() (start, end, col int) {
	start = 0
	for i := c.cur - 1; i >= 0; i-- {
		if c.text[i] == '\n' {
			start = i + 1
			break
		}
	}
	end = len(c.text)
	for i := c.cur; i < len(c.text); i++ {
		if c.text[i] == '\n' {
			end = i
			break
		}
	}
	return start, end, c.cur - start
}

func prevLineStart(text []rune, from int) int {
	for i := from - 1; i >= 0; i-- {
		if text[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

func nextLineEnd(text []rune, from int) int {
	for i := from; i < len(text); i++ {
		if text[i] == '\n' {
			return i
		}
	}
	return len(text)
}

// histMove - листать историю (d=1 - назад, -1 - вперёд).
func (c *composer) histMove(d int) {
	if len(c.hist) == 0 {
		return
	}
	if c.histIdx == -1 {
		if d <= 0 {
			return
		}
		c.histDraft = string(c.text)
		c.histIdx = len(c.hist) - 1
	} else {
		c.histIdx += d
		if c.histIdx < 0 {
			c.histIdx = -1
			c.set(c.histDraft)
			return
		}
		if c.histIdx >= len(c.hist) {
			c.histIdx = len(c.hist) - 1
		}
	}
	c.set(c.hist[c.histIdx])
}

// set - заменить текст поля целиком.
func (c *composer) set(s string) {
	c.text = []rune(s)
	c.cur = len(c.text)
}

// onEnter - Enter: выбрать пункт меню или отправить текст.
func (c *composer) onEnter() {
	// Перенос строки обратным слэшем: строка, кончающаяся на «\», склеивается.
	if len(c.menu) == 0 && c.cur > 0 && c.text[c.cur-1] == '\\' &&
		(c.cur < 2 || c.text[c.cur-2] != '\\') {
		c.text[c.cur-1] = '\n'
		return
	}
	if len(c.menu) > 0 && c.menuSel < len(c.menu) {
		c.set(c.menu[c.menuSel].Name + " ")
		c.pushMenu()
		return
	}
	line := c.expandPastes(string(c.text))
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	c.hist = append(c.hist, line)
	if len(c.hist) > 200 {
		c.hist = c.hist[len(c.hist)-200:]
	}
	c.histIdx = -1
	c.histDraft = ""
	c.text = c.text[:0]
	c.cur = 0
	c.submit(line)
}

// onEsc - Esc: закрыть меню, прервать занятой ход, иначе ничего.
func (c *composer) onEsc() {
	if len(c.menu) > 0 {
		c.menu = nil
		c.menuSel = 0
		c.pushMenu()
		return
	}
	if c.inflight {
		c.a.interruptTurn()
	}
}

// onCtrlC - Ctrl+C: стереть текст; с пустым полем прервать ход; дважды
// подряд с пустым полем - выйти.
func (c *composer) onCtrlC() {
	if len(c.text) > 0 {
		c.text = c.text[:0]
		c.cur = 0
		c.exitArm = false
		return
	}
	if c.inflight {
		if c.exitArm {
			c.quit = true
			return
		}
		c.a.interruptTurn()
		c.exitArm = true
		return
	}
	if c.exitArm {
		c.quit = true
		return
	}
	c.exitArm = true
}

// clearScreen - Ctrl+L: очистить экран и перерисовать баннер с регионом.
func (c *composer) clearScreen() {
	if c.live {
		c.ui.LiveStop()
	}
	c.ui.ClearScreen()
	c.a.banner()
	if c.live {
		c.ui.LiveStart(c.placeholder())
	}
	c.refresh()
}

// placeholder - подсказка пустого поля.
func (c *composer) placeholder() string {
	return placeholderTry[len(c.hist)%len(placeholderTry)]
}

// ---------- Меню команд ----------

// sync - пересчитать меню и перерисовать регион (после каждого события).
func (c *composer) sync() {
	c.syncMenu()
	c.refresh()
}

// syncMenu - пересчитать список команд по текущему тексту.
func (c *composer) syncMenu() {
	items := c.filterMenu()
	if sameMenu(c.menu, items) {
		return
	}
	c.menu = items
	if c.menuSel >= len(items) {
		c.menuSel = 0
	}
	c.ui.LiveMenu(items, c.menuSel)
}

// pushMenu - отрисовать меню с текущим выбором.
func (c *composer) pushMenu() {
	c.ui.LiveMenu(c.menu, c.menuSel)
}

// filterMenu - пункты меню, подходящие под первое слово ввода.
func (c *composer) filterMenu() []ui.MenuItem {
	s := string(c.text)
	if s == "" || len(s) > 80 {
		return nil
	}
	// Меню живёт, только пока вводится сама команда: пробел или перевод
	// строки закрывают его (аргументы уже не часть имени).
	if strings.ContainsAny(s, " \t\n") {
		return nil
	}
	tok := strings.ToLower(strings.TrimPrefix(s, "/"))
	if tok == "" {
		return nil
	}
	all := c.a.menuItems()
	var out []ui.MenuItem
	for _, it := range all {
		name := strings.ToLower(strings.TrimPrefix(it.Name, "/"))
		if name == tok {
			continue // точное совпадение: меню не нужно, Enter отправит сам
		}
		if strings.HasPrefix(name, tok) {
			out = append(out, it)
			if len(out) >= liveMenuCap {
				break
			}
		}
	}
	return out
}

// completeMenu - Tab: дополнить ввод выбранным пунктом.
func (c *composer) completeMenu() {
	if len(c.menu) == 0 {
		return
	}
	c.set(c.menu[c.menuSel].Name + " ")
	c.syncMenu()
}

// sameMenu - совпадают ли списки меню.
func sameMenu(a, b []ui.MenuItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Desc != b[i].Desc {
			return false
		}
	}
	return true
}

// ---------- Вставка ----------

// insertPaste - bracketed paste: многострочный текст сворачивается в
// плейсхолдер « [Pasted text #1 +N lines] », однострочный вставляется как есть.
func (c *composer) insertPaste(rs []rune) {
	text := strings.TrimSuffix(string(rs), "\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	lines := strings.Count(text, "\n") + 1
	if lines <= 1 {
		c.insert([]rune(strings.ReplaceAll(text, "\r", "")))
		return
	}
	c.pasteN++
	id := c.pasteN
	ph := fmt.Sprintf(" [Pasted text #%d +%d lines] ", id, lines)
	c.pastes[ph] = strings.ReplaceAll(text, "\r", "")
	c.insert([]rune(ph))
}

// expandPastes - развернуть плейсхолдеры вставок перед отправкой.
func (c *composer) expandPastes(s string) string {
	for ph, content := range c.pastes {
		if strings.Contains(s, ph) {
			s = strings.Replace(s, ph, content, 1)
			delete(c.pastes, ph)
		}
	}
	return s
}

// ---------- Отправка ----------

// submit - поставить строку в работу: занято - в очередь, свободно - горутина.
func (c *composer) submit(line string) {
	if line == "" {
		return
	}
	if c.inflight {
		c.pending = append(c.pending, line)
		return
	}
	c.inflight = true
	go func() {
		c.a.handleSubmit(line)
		c.finishJob()
	}()
}

// finishJob - ход или команда завершились: снять занятость, взять очередь.
func (c *composer) finishJob() {
	c.inflight = false
	if len(c.pending) > 0 {
		next := c.pending[0]
		c.pending = c.pending[1:]
		c.inflight = true
		go func() {
			c.a.handleSubmit(next)
			c.finishJob()
		}()
	}
	c.wake()
}

// handleSubmit - разбор поданной строки (slash / заметка / ход агента).
// Выполняется в горутине композера, поэтому может печатать и спрашивать.
func (a *app) handleSubmit(text string) {
	// Любой ввод пользователя сбрасывает таймер автономных итераций.
	a.cronTouch()
	if strings.HasPrefix(text, "/") {
		if !a.handleCommand(text) {
			if c := a.composerRef(); c != nil {
				c.Stop()
			}
		}
		a.ui.SetMetrics(a.metrics())
		return
	}
	if strings.HasPrefix(text, "#") {
		a.appendMemoryNote(text)
		a.ui.SetMetrics(a.metrics())
		return
	}
	if err := a.turn(text); err != nil && !a.quiet {
		a.ui.Err(err.Error())
	}
	a.ui.SetMetrics(a.metrics())
}

// interruptTurn - Esc/Ctrl+C во время хода: отменить текущий контекст.
func (a *app) interruptTurn() {
	a.mu.Lock()
	c := a.cancel
	a.mu.Unlock()
	if c != nil {
		c()
	}
}

// ---------- Вопросы у терминала ----------

// ask - задать человеку вопрос через композер. Выполняется из горутины
// хода: цикл композера обслужит запрос и вернёт ответ.
func (c *composer) ask(question string, secret bool, options []string) (string, bool) {
	if c.quit {
		return "", false
	}
	req := askReq{question: question, secret: secret, options: options, reply: make(chan string, 1)}
	select {
	case c.askCh <- req:
	case <-c.done:
		return "", false
	}
	select {
	case text := <-req.reply:
		return text, true
	case <-c.done:
		return "", false
	}
}

// serveAsk - показать вопрос и читать ответ в поле ввода.
func (c *composer) serveAsk(req askReq) {
	// Печать вопроса идёт в стенку вывода: над полем ввода, как
	// подтверждение в Claude Code.
	c.a.ui.Println("")
	c.a.ui.Println("  " + c.a.ui.Accent(c.a.ui.Glyphs().Dot) + " " + req.question)
	for i, opt := range req.options {
		c.a.ui.Println("    " + c.a.ui.Gray(fmt.Sprintf("%d.", i+1)) + " " + opt)
	}
	if len(req.options) > 0 {
		c.a.ui.Gray("номер варианта или свой ответ, esc - отказ")
	}
	c.askSecret = req.secret
	c.text = c.text[:0]
	c.cur = 0
	if c.live {
		c.ui.LiveFooter("enter - ответить · esc - отказ", c.a.footerRight())
	}
	defer func() {
		c.askSecret = false
		c.refresh()
	}()

	buf := []rune{}
	for {
		ev, ok := <-c.a.stdin.Events()
		if !ok {
			req.reply <- ""
			return
		}
		switch ev.kind {
		case evEOF:
			req.reply <- ""
			return
		case evLine:
			req.reply <- strings.TrimSpace(ev.line)
			return
		case evPaste:
			for _, r := range ev.runes {
				if r != '\n' && r != '\r' {
					buf = append(buf, r)
				}
			}
		case evRune:
			if ev.r != '\n' && ev.r != '\r' {
				buf = append(buf, ev.r)
			}
		case evKey:
			switch ev.key {
			case keyEnter:
				req.reply <- strings.TrimSpace(string(buf))
				return
			case keyEsc:
				req.reply <- ""
				return
			case keyBackspace:
				if len(buf) > 0 {
					buf = buf[:len(buf)-1]
				}
			case keyCtrlU:
				buf = buf[:0]
			case keyCtrlC:
				req.reply <- ""
				return
			}
		}
		c.text = buf
		c.cur = len(buf)
		if c.live {
			c.ui.LiveInput(c.displayText(), c.cur)
		} else {
			// Пайп: вопрос без региона - эхо-подсказка, ответ одной строкой.
			c.a.ui.Print(".")
		}
	}
}
