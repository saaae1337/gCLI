package tui

import "strings"

// ---------- Поле ввода ----------
//
// Многострочный редактор: Enter отправляет, Ctrl+J (и Shift+Enter, если
// терминал его присылает) переносит строку. Курсор двигается по видимым
// строкам с учётом переноса — «вверх» не промахивается мимо длинной
// строки, как в однострочных приглашениях.

// Composer — состояние редактора.
type Composer struct {
	text []rune
	cur  int
	hist []string
	hpos int // -1 — не листаем историю
	// draft — что было набрано до входа в историю.
	draft string
}

// NewComposer — пустой редактор.
func NewComposer() *Composer {
	return &Composer{hpos: -1}
}

// Empty — пусто ли поле.
func (c *Composer) Empty() bool { return len(c.text) == 0 }

// Text — текущий текст.
func (c *Composer) Text() string { return string(c.text) }

// Len — длина в рунах.
func (c *Composer) Len() int { return len(c.text) }

// Cursor — позиция курсора в рунах.
func (c *Composer) Cursor() int { return c.cur }

// SetText — заменить текст (курсор в конец).
func (c *Composer) SetText(s string) {
	s = sanitizeInput(s)
	c.text = []rune(s)
	c.cur = len(c.text)
	c.hpos = -1
}

// Clear — очистить поле.
func (c *Composer) Clear() {
	c.text = c.text[:0]
	c.cur = 0
	c.hpos = -1
	c.draft = ""
}

// Insert — вставить текст в позицию курсора.
func (c *Composer) Insert(s string) {
	s = sanitizeInput(s)
	if s == "" {
		return
	}
	add := []rune(s)
	next := make([]rune, 0, len(c.text)+len(add))
	next = append(next, c.text[:c.cur]...)
	next = append(next, add...)
	next = append(next, c.text[c.cur:]...)
	c.text = next
	c.cur += len(add)
	c.hpos = -1
}

// Backspace — удалить символ слева.
func (c *Composer) Backspace() {
	if c.cur == 0 {
		return
	}
	c.text = append(c.text[:c.cur-1], c.text[c.cur:]...)
	c.cur--
	c.hpos = -1
}

// Delete — удалить символ справа.
func (c *Composer) Delete() {
	if c.cur >= len(c.text) {
		return
	}
	c.text = append(c.text[:c.cur], c.text[c.cur+1:]...)
	c.hpos = -1
}

// Left — на символ влево.
func (c *Composer) Left() {
	if c.cur > 0 {
		c.cur--
	}
}

// Right — на символ вправо.
func (c *Composer) Right() {
	if c.cur < len(c.text) {
		c.cur++
	}
}

// Home — в начало логической строки.
func (c *Composer) Home() {
	for c.cur > 0 && c.text[c.cur-1] != '\n' {
		c.cur--
	}
}

// End — в конец логической строки.
func (c *Composer) End() {
	for c.cur < len(c.text) && c.text[c.cur] != '\n' {
		c.cur++
	}
}

// KillLine — удалить от курсора до конца строки (Ctrl+K).
func (c *Composer) KillLine() {
	end := c.cur
	for end < len(c.text) && c.text[end] != '\n' {
		end++
	}
	c.text = append(c.text[:c.cur], c.text[end:]...)
	c.hpos = -1
}

// KillToStart — удалить от начала строки до курсора (Ctrl+U).
func (c *Composer) KillToStart() {
	start := c.cur
	for start > 0 && c.text[start-1] != '\n' {
		start--
	}
	c.text = append(c.text[:start], c.text[c.cur:]...)
	c.cur = start
	c.hpos = -1
}

// KillWord — удалить слово слева (Ctrl+W).
func (c *Composer) KillWord() {
	i := c.cur
	for i > 0 && c.text[i-1] == ' ' {
		i--
	}
	for i > 0 && c.text[i-1] != ' ' && c.text[i-1] != '\n' {
		i--
	}
	c.text = append(c.text[:i], c.text[c.cur:]...)
	c.cur = i
	c.hpos = -1
}

// visualRanges — границы видимых строк (индексы рун).
//
// Перенос по ширине; если рядом с границей есть пробел, рвём по нему,
// чтобы слова не разрезались посередине без нужды.
func (c *Composer) visualRanges(width int) [][2]int {
	if width < 1 {
		width = 1
	}
	var out [][2]int
	start := 0
	w := 0
	lastSpace := -1
	for i, r := range c.text {
		if r == '\n' {
			out = append(out, [2]int{start, i})
			start = i + 1
			w = 0
			lastSpace = -1
			continue
		}
		rw := RuneWidth(r)
		if w+rw > width && i > start {
			br := i
			// Пробел недалеко от края — переносим по нему.
			if lastSpace > start && i-lastSpace <= width/3 {
				br = lastSpace + 1
			}
			out = append(out, [2]int{start, br})
			start = br
			w = 0
			for k := start; k < i; k++ {
				w += RuneWidth(c.text[k])
			}
			lastSpace = -1
			for k := start; k < i; k++ {
				if c.text[k] == ' ' {
					lastSpace = k
				}
			}
		}
		w += rw
		if r == ' ' {
			lastSpace = i
		}
	}
	out = append(out, [2]int{start, len(c.text)})
	return out
}

// Lines — видимые строки текста.
func (c *Composer) Lines(width int) []string {
	ranges := c.visualRanges(width)
	out := make([]string, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, strings.ReplaceAll(string(c.text[r[0]:r[1]]), "\t", "    "))
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// CursorPos — позиция курсора (колонка, строка) в видимых строках.
func (c *Composer) CursorPos(width int) (int, int) {
	ranges := c.visualRanges(width)
	line := len(ranges) - 1
	for i, r := range ranges {
		if c.cur < r[1] {
			line = i
			break
		}
		if c.cur == r[1] && i == len(ranges)-1 {
			line = i
		}
	}
	x := 0
	if line < len(ranges) {
		start := ranges[line][0]
		for k := start; k < c.cur && k < len(c.text); k++ {
			x += RuneWidth(c.text[k])
		}
	}
	return x, line
}

// Up — вверх по видимым строкам; на первой строке — история.
func (c *Composer) Up(width int) {
	ranges := c.visualRanges(width)
	x, line := c.CursorPos(width)
	if line == 0 {
		c.HistoryPrev()
		return
	}
	target := ranges[line-1]
	c.cur = target[0]
	col := 0
	for k := target[0]; k < target[1]; k++ {
		rw := RuneWidth(c.text[k])
		if col+rw > x {
			break
		}
		col += rw
		c.cur = k + 1
	}
}

// Down — вниз по видимым строкам; на последней — история.
func (c *Composer) Down(width int) {
	ranges := c.visualRanges(width)
	x, line := c.CursorPos(width)
	if line >= len(ranges)-1 {
		c.HistoryNext()
		return
	}
	target := ranges[line+1]
	c.cur = target[0]
	col := 0
	for k := target[0]; k < target[1]; k++ {
		rw := RuneWidth(c.text[k])
		if col+rw > x {
			break
		}
		col += rw
		c.cur = k + 1
	}
}

// HistoryPrev — предыдущая строка истории.
func (c *Composer) HistoryPrev() {
	if len(c.hist) == 0 {
		return
	}
	if c.hpos == -1 {
		c.draft = string(c.text)
		c.hpos = len(c.hist)
	}
	if c.hpos > 0 {
		c.hpos--
		c.text = []rune(c.hist[c.hpos])
		c.cur = len(c.text)
	}
}

// HistoryNext — следующая строка истории.
func (c *Composer) HistoryNext() {
	if c.hpos == -1 {
		return
	}
	if c.hpos < len(c.hist)-1 {
		c.hpos++
		c.text = []rune(c.hist[c.hpos])
		c.cur = len(c.text)
		return
	}
	c.hpos = -1
	c.text = []rune(c.draft)
	c.cur = len(c.text)
}

// Submit — забрать текст и очистить поле (с записью в историю).
func (c *Composer) Submit() string {
	s := strings.TrimRight(string(c.text), " \n")
	if strings.TrimSpace(s) != "" {
		if len(c.hist) == 0 || c.hist[len(c.hist)-1] != s {
			c.hist = append(c.hist, s)
			if len(c.hist) > 200 {
				c.hist = c.hist[len(c.hist)-100:]
			}
		}
	}
	c.Clear()
	return s
}

// sanitizeInput — вставка без управляющих символов.
func sanitizeInput(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 32 || r == 0x7f:
			// мусор из буфера обмена
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
