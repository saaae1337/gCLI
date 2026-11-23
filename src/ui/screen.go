package ui

import (
	"fmt"
	"strings"
	"time"
)

// ---------- Полноэкранный интерфейс (альтернативный буфер) ----------
//
// Режим Claude Code: программа владеет всем экраном. Транскрипт хранится
// логическими строками, перелицовывается под текущую ширину окна и
// перерисовывается целиком при каждом изменении - вводе, выводе, работе
// спиннера и, главное, при изменении размера окна. Именно поэтому
// интерфейс «масштабируется»: после resize вся стенка перепечатывается
// под новую ширину, поле ввода растёт и сжимается вместе с окном, а
// нижняя зона всегда стоит у нижней границы экрана.
//
// Слои кадра снизу вверх:
//
//      pop-up меню команд (если открыто)
//      строка ожидания (если агент занят)
//      поле ввода в скруглённой рамке
//      строка подсказок
//
// Всё, что выше, - вьюпорт транскрипта: последние строки стенки,
// прижатые к низу; PgUp/PgDn листают назад.
//
// Блокировки: экран не заводит собственный мьютекс - всё состояние
// меняется и читается под u.mu. Горутина-тикер раз в 80 мс
// перерисовывает кадр, если он помечен грязным или изменился размер
// окна: опрос размера заменяет SIGWINCH и одинаково работает на всех
// платформах.

// screenMaxLines - потолок транскрипта в экране: старые строки
// выбрасываются, память не растёт без границ за долгую сессию.
const screenMaxLines = 6000

// screenTick - период кадра.
const screenTick = 80 * time.Millisecond

// Screen - состояние полноэкранного рендера.
type Screen struct {
	active bool
	stop   chan struct{}

	// Транскрипт: логические строки. rows не хранится: перелицовка
	// пересчитывается при смене wrapW (дёшево - раз на resize).
	lines []scrLine

	// Ширина, под которую перелицован кэш rows в scrLine.
	wrapW int

	// Незавершённый хвост: put() без перевода строки ждёт продолжения.
	pending string

	// Сдвиг вьюпорта назад: 0 - следовать за низом; новые строки
	// возвращают режим слежения.
	scroll int

	// Проба размера при последнем кадре: изменение размера - повод
	// перерисоваться, даже если dirty не стоял.
	lastW, lastH int

	dirty     bool
	lastPaint time.Time
}

// scrLine - логическая строка транскрипта и кэш её перелицовки.
type scrLine struct {
	text string
	rows []string
}

// screenTestForce - тестовый крюк: считать альтернативный буфер
// доступным без настоящего терминала.
var screenTestForce = false

// screenSupported - можно ли включить полноэкранный режим: вывод в
// терминал и escape-последовательности (на Windows - включённый VT).
func (u *UI) screenSupported() bool {
	if !screenTestForce && !u.tty() {
		return false
	}
	return EnableVT()
}

// ScreenStart - включить полноэкранный режим. Возвращает false, если
// терминал его не поддерживает (пайп, консоль без VT) - тогда вызывающий
// код остаётся на живой области в прокрутке.
func (u *UI) ScreenStart() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.scr != nil && u.scr.active {
		return true
	}
	if !u.screenSupported() {
		return false
	}
	s := &Screen{active: true, stop: make(chan struct{}), wrapW: -1}
	u.scr = s
	u.screenOn.Store(true)
	u.putEscLocked("\x1b[?1049h\x1b[H\x1b[2J")
	u.screenDirtyLocked()
	go screenTicker(u, s.stop)
	return true
}

// ScreenStop - выключить полноэкранный режим и вернуть терминал.
func (u *UI) ScreenStop() {
	u.mu.Lock()
	s := u.scr
	if s == nil || !s.active {
		u.mu.Unlock()
		return
	}
	s.active = false
	u.scr = nil
	u.screenOn.Store(false)
	u.putEscLocked("\x1b[?25h\x1b[2J\x1b[?1049l")
	u.mu.Unlock()
	close(s.stop)
}

// ScreenActive - включён ли полноэкранный режим.
func (u *UI) ScreenActive() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.scr != nil && u.scr.active
}

// screenOnLocked - полноэкранный режим активен (вызов под u.mu).
func (u *UI) screenOnLocked() bool {
	return u.scr != nil && u.scr.active
}

// screenTicker - кадр каждые screenTick: перерисовка по dirty или смене
// размера окна.
func screenTicker(u *UI, stop <-chan struct{}) {
	t := time.NewTicker(screenTick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			u.mu.Lock()
			if !u.screenOnLocked() {
				u.mu.Unlock()
				return
			}
			w, h := u.frameSize()
			if u.scr.dirty || w != u.scr.lastW || h != u.scr.lastH {
				u.paintScreenLocked()
			}
			u.mu.Unlock()
		}
	}
}

// putEscLocked - сырые escape-последовательности мимо кадра (вход/выход
// из альтернативного буфера, сам кадр). Вызов под u.mu.
func (u *UI) putEscLocked(s string) {
	_, _ = u.out.Write([]byte(s))
}

// screenDirtyLocked - пометить кадр грязным (вызов под u.mu).
func (u *UI) screenDirtyLocked() {
	if u.screenOnLocked() {
		u.scr.dirty = true
	}
}

// screenKick - перерисовать немедленно, если прошлый кадр был давно;
// иначе тикер сделает это за нас. Вызывается под u.mu после мутации.
func (u *UI) screenKick() {
	if !u.screenOnLocked() {
		return
	}
	if time.Since(u.scr.lastPaint) < 25*time.Millisecond {
		u.scr.dirty = true
		return
	}
	u.paintScreenLocked()
}

// ScreenClear - очистить транскрипт (Ctrl+L, /clear).
func (u *UI) ScreenClear() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.screenOnLocked() {
		return
	}
	u.scr.lines = nil
	u.scr.pending = ""
	u.scr.scroll = 0
	u.screenDirtyLocked()
	u.screenKick()
}

// ScreenScroll - листать вьюпорт (d > 0 - назад по истории).
func (u *UI) ScreenScroll(d int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.screenOnLocked() {
		return
	}
	u.scr.scroll += d
	if max := len(u.scr.lines); u.scr.scroll > max {
		u.scr.scroll = max
	}
	if u.scr.scroll < 0 {
		u.scr.scroll = 0
	}
	u.screenDirtyLocked()
	u.screenKick()
}

// putScreenLocked - строка транскрипта. Вызывается из put() под u.mu.
func (u *UI) putScreenLocked(s string) {
	if !u.screenOnLocked() {
		return
	}
	s = u.scr.pending + s
	u.scr.pending = ""
	if !strings.HasSuffix(s, "\n") {
		// Незавершённая строка: завершённая часть - в транскрипт,
		// хвост ждёт продолжения в следующей печати.
		if i := strings.LastIndexByte(s, '\n'); i >= 0 {
			u.scr.appendLinesLocked(s[:i+1])
			u.scr.pending = s[i+1:]
		} else {
			u.scr.pending = s
		}
		u.screenDirtyLocked()
		u.screenKick()
		return
	}
	u.scr.appendLinesLocked(s)
	u.scr.scroll = 0 // новые строки возвращают режим слежения за низом
	u.screenDirtyLocked()
	u.screenKick()
}

// appendLinesLocked - дописать завершённые строки в транскрипт.
func (s *Screen) appendLinesLocked(text string) {
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		s.lines = append(s.lines, scrLine{text: l})
	}
	if n := len(s.lines); n > screenMaxLines {
		s.lines = s.lines[n-screenMaxLines:]
	}
}

// frameSize - размер кадра: явные опции (фиксированная ширина из
// конфига, тесты) иначе текущий размер терминала.
func (u *UI) frameSize() (int, int) {
	w := u.Width()
	h := u.Height()
	return w, h
}

// paintScreenLocked - собрать и напечатать кадр целиком. Вызывается
// под u.mu.
func (u *UI) paintScreenLocked() {
	s := u.scr
	if s == nil || !s.active {
		return
	}
	s.dirty = false
	s.lastPaint = time.Now()

	w, h := u.frameSize()
	if w < 12 || h < 4 {
		// Терминал слишком мал: рисовать нечего, курсор прячем.
		u.putEscLocked("\x1b[?25l")
		return
	}
	s.lastW, s.lastH = w, h

	// Перелицовка под новую ширину (первый кадр: wrapW=-1).
	if s.wrapW != w {
		for i := range s.lines {
			s.lines[i].rows = wrapStyledRow(s.lines[i].text, w)
		}
		s.wrapW = w
	}

	// Нижняя зона: меню, строка ожидания, поле ввода, подсказки.
	menuRows := u.liveMenuRows(w)
	busyRows := []string{}
	if u.liveBusy != "" {
		busyRows = append(busyRows, u.liveBusyLine(w))
	}
	boxRows, caretRow, caretCol := u.screenBoxRows(w)
	footer := u.liveFooterLine(w)

	chrome := len(menuRows) + len(busyRows) + len(boxRows)
	if footer != "" {
		chrome++
	}
	avail := h - chrome
	for avail < 1 {
		// Экран мал: сжимаем нижнюю зону от наименее важного к важному.
		switch {
		case len(menuRows) > 0:
			menuRows = nil
		case len(busyRows) > 0:
			busyRows = nil
		case footer != "":
			footer = ""
		case len(boxRows) > 3:
			// В рамке остаётся первая строка ввода: верх, контент, низ.
			boxRows = append(boxRows[:1], boxRows[len(boxRows)-1])
		default:
			avail = 0
		}
		if avail == 0 {
			break
		}
		chrome = len(menuRows) + len(busyRows) + len(boxRows)
		if footer != "" {
			chrome++
		}
		avail = h - chrome
		if avail >= 1 {
			break
		}
	}
	if avail < 0 {
		avail = 0
	}
	if caretRow >= len(boxRows) {
		caretRow, caretCol = -1, 0
	}

	// Вьюпорт: последние avail строк транскрипта со сдвигом scroll.
	if s.scroll > len(s.lines) {
		s.scroll = len(s.lines)
	}
	end := len(s.lines) - s.scroll
	if end > len(s.lines) {
		end = len(s.lines)
	}
	start := end - avail
	if start < 0 {
		start = 0
	}

	// Кадр: абсолютное позиционирование на каждую строку - не зависит
	// от переносов и предыдущего содержимого экрана.
	var b strings.Builder
	b.WriteString("\x1b[?25l")
	row := 0
	for i := start; i < end; i++ {
		for _, l := range s.lines[i].rows {
			row++
			b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
			b.WriteString(l)
		}
	}
	// Рамка поля ввода начинается со следующей строки.
	for _, l := range menuRows {
		row++
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
		b.WriteString(l)
	}
	for _, l := range busyRows {
		row++
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
		b.WriteString(l)
	}
	boxTop := row + 1 // экранный (1-based) номер верхней границы рамки
	for _, l := range boxRows {
		row++
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
		b.WriteString(l)
	}
	if footer != "" {
		row++
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
		b.WriteString(footer)
	}
	// Всё ниже последней строки кадра - подчистить.
	b.WriteString("\x1b[J")

	// Курсор - в позицию ввода.
	if caretRow >= 0 {
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", boxTop+caretRow, caretCol))
	} else {
		b.WriteString("\x1b[?25h")
	}
	u.putEscLocked(b.String())
}

// wrapStyledRow - разбить одну строку (возможно, с ANSI) на экранные
// строки ширины w по колонкам терминала.
func wrapStyledRow(l string, w int) []string {
	if l == "" {
		return []string{""}
	}
	l = validUTF8(l)
	if cellWidth(l) <= w {
		return []string{l}
	}
	var out []string
	rest := l
	for cellWidth(rest) > w {
		cut := truncateCellsRaw(rest, w)
		if cut == "" {
			// Широкий кластер не помещается: берём его целиком,
			// иначе цикл не сдвинется.
			_, sz := nextCluster(rest, 0)
			if sz <= 0 || sz > len(rest) {
				sz = 1
			}
			cut = rest[:sz]
		}
		// Стараемся резать по пробелу, но не отдаём под это больше
		// половины строки. В цветной строке пробел может оказаться
		// внутри escape-последовательности - такую режем строго
		// по границе кластера.
		if !hasESC(cut) {
			if idx := strings.LastIndexByte(cut, ' '); idx > 0 &&
				cellWidth(cut[:idx]) >= w/2 {
				cut = cut[:idx]
			}
		}
		out = append(out, cut)
		rest = rest[len(cut):]
		for len(rest) > 0 && rest[0] == ' ' {
			rest = rest[1:]
		}
	}
	if rest != "" || out == nil {
		out = append(out, rest)
	}
	return out
}

// screenBoxRows - строки поля ввода (вся рамка) и позиция курсора.
//
//	caretRow - индекс строки в boxRows (0 - верхняя граница рамки);
//	caretCol - 1-based колонка экрана; caretRow < 0 - курсор скрыт.
//
// Курсор - настоящий курсор терминала, поставленный в позицию ввода:
// блочный символ-каретка не рисуется, двойного курсора нет.
func (u *UI) screenBoxRows(w int) (rows []string, caretRow, caretCol int) {
	caretRow, caretCol = -1, 0
	inner := w - 4 // между «│  » и «  │»
	if inner < 6 {
		inner = 6
	}
	colW := inner - 4 // ширина текста после префикса «> »
	if colW < 2 {
		colW = 2
	}

	bar := strings.Repeat(u.g.H, w-3)
	rows = append(rows, " "+u.c(u.pal.Muted, u.g.TL+bar+u.g.TR))

	text := u.liveInp
	if text == "" && u.livePlaceholder != "" {
		// Пустое поле: подсказка приглушённым, курсор - в начало текста.
		body := "  " + u.truncateRow(u.c(u.pal.Muted, u.livePlaceholder), colW)
		rows = append(rows, u.boxRow(body, inner))
		rows = append(rows, " "+u.c(u.pal.Muted, u.g.BL+bar+u.g.BR))
		return rows, 1, textCol()
	}
	if text == "" {
		rows = append(rows, u.boxRow("  ", inner))
		rows = append(rows, " "+u.c(u.pal.Muted, u.g.BL+bar+u.g.BR))
		return rows, 1, textCol()
	}

	cur := u.liveCur
	runes := []rune(text)
	if cur < 0 {
		cur = 0
	}
	if cur > len(runes) {
		cur = len(runes)
	}

	// Логические строки ввода: text[start:end] без перевода строки.
	type inLine struct {
		text  string
		start int // индекс строки в рунах всего ввода
	}
	var lns []inLine
	start := 0
	for i, r := range runes {
		if r == '\n' {
			lns = append(lns, inLine{string(runes[start:i]), start})
			start = i + 1
		}
	}
	lns = append(lns, inLine{string(runes[start:]), start})

	// Развёртка в визуальные строки; попутно - позиция курсора.
	prefix := "> " // первая строка, продолжения - «  »
	for i, ln := range lns {
		if i > 0 {
			prefix = "  "
		}
		// Перенос по колонкам терминала (не по рунам): на узком поле
		// широкие слова режутся честно, рамка не уезжает за край.
		wr := wrapStyledRow(ln.text, colW)
		if len(wr) == 0 {
			wr = []string{""}
		}
		if caretRow < 0 && cur >= ln.start && cur <= ln.start+len([]rune(ln.text)) {
			// Курсор в этой логической строке.
			rem := cur - ln.start
			for j, r := range wr {
				rw := cellWidth(r)
				if rem <= rw {
					caretRow = len(rows) + j
					caretCol = textCol() + rem
					break
				}
				rem -= rw + 1 // склеивающий пробел уходит на стыке строк
			}
			if caretRow < 0 {
				// Курсор за последней строкой переноса - в её конец.
				caretRow = len(rows) + len(wr) - 1
				caretCol = textCol() + cellWidth(wr[len(wr)-1])
			}
		}
		for _, r := range wr {
			rows = append(rows, u.boxRow(prefix+r, inner))
		}
	}
	rows = append(rows, " "+u.c(u.pal.Muted, u.g.BL+bar+u.g.BR))
	return rows, caretRow, caretCol
}

// textCol - 1-based колонка начала текста внутри рамки: « │  » слева
// (4 колонки) плюс префикс «> »/«  » (2 колонки).
func textCol() int { return 7 }
