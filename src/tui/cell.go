package tui

import (
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"gcli/wcwidth"
)

// ---------- Цвет и стиль ----------

// Color — цвет в truecolor. On=false — «не задан».
type Color struct {
	R, G, B uint8
	On      bool
}

// RGB — цвет из компонентов.
func RGB(r, g, b uint8) Color { return Color{R: r, G: g, B: b, On: true} }

// Hex — цвет из строки "#rrggbb" (или "rrggbb").
func Hex(s string) Color {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return Color{}
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return Color{}
	}
	return RGB(uint8(v>>16), uint8(v>>8), uint8(v))
}

// Mix — линейная смесь двух цветов (t=0 → a, t=1 → b).
func Mix(a, b Color, t float64) Color {
	if !a.On {
		return b
	}
	if !b.On {
		return a
	}
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return RGB(l(a.R, b.R), l(a.G, b.G), l(a.B, b.B))
}

// Lighten — осветлить цвет к белому на t.
func Lighten(c Color, t float64) Color { return Mix(c, RGB(255, 255, 255), t) }

// Darken — затемнить цвет к чёрному на t.
func Darken(c Color, t float64) Color { return Mix(c, RGB(0, 0, 0), t) }

// Dim — приглушить цвета всего экрана (фон открытого окна).
func (s *Screen) Dim(t float64) {
	for i := range s.Cells {
		c := &s.Cells[i]
		if c.S.FG.On {
			c.S.FG = Darken(c.S.FG, t)
		}
		if c.S.BG.On {
			c.S.BG = Darken(c.S.BG, t)
		}
	}
}

// Style — оформление клетки.
type Style struct {
	FG, BG                                        Color
	Bold, Dim, Italic, Underline, Strike, Reverse bool
}

// FGc — стиль с другим цветом текста.
func (s Style) FGc(c Color) Style { s.FG = c; return s }

// BGc — стиль с другим фоном.
func (s Style) BGc(c Color) Style { s.BG = c; return s }

// With — стиль с включёнными атрибутами (по имени).
func (s Style) With(attrs ...string) Style {
	for _, a := range attrs {
		switch a {
		case "b":
			s.Bold = true
		case "d":
			s.Dim = true
		case "i":
			s.Italic = true
		case "u":
			s.Underline = true
		case "s":
			s.Strike = true
		case "r":
			s.Reverse = true
		}
	}
	return s
}

// ---------- Экран ----------

// Cell — одна клетка экрана.
type Cell struct {
	R    rune
	Rest string // хвост графического кластера: ZWJ, модификаторы, вторая руна флага
	S    Style
	Cont bool // вторая половина широкого символа
}

// Screen — кадр: сетка клеток.
type Screen struct {
	W, H  int
	Cells []Cell
	Bg    Color
}

// NewScreen — пустой экран заданного размера.
func NewScreen(w, h int, bg Color) *Screen {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	s := &Screen{W: w, H: h, Bg: bg, Cells: make([]Cell, w*h)}
	s.Clear()
	return s
}

// Clear — заполнить фоном.
func (s *Screen) Clear() {
	base := Cell{R: ' ', S: Style{BG: s.Bg}}
	for i := range s.Cells {
		s.Cells[i] = base
	}
}

// In — координаты внутри экрана.
func (s *Screen) In(x, y int) bool { return x >= 0 && y >= 0 && x < s.W && y < s.H }

// Set — поставить одиночный символ.
func (s *Screen) Set(x, y int, r rune, st Style) {
	s.setCluster(x, y, wcwidth.RuneWidth(r), r, "", st)
}

// setCluster — поставить графический кластер шириной w клеток.
//
// base — базовый символ, rest — его продолжения (ZWJ, модификаторы,
// вторая руна флага). Продолжения занимают ноль клеток на экране, но
// обязаны попасть в вывод вместе с базой: терминал рисует кластер одной
// картинкой, и если хвост потерять, на его месте останется мусор.
func (s *Screen) setCluster(x, y, w int, base rune, rest string, st Style) {
	if !s.In(x, y) {
		return
	}
	if w <= 0 {
		return
	}
	if w > 2 {
		w = 2
	}
	// Затираем широкий символ, у которого мы сейчас отнимаем половину.
	if s.Cells[y*s.W+x].Cont && x > 0 {
		s.Cells[y*s.W+x-1] = Cell{R: ' ', S: st}
	}
	if w == 2 {
		if x+1 >= s.W {
			// Широкий символ не помещается в последнюю клетку — пробел.
			s.Cells[y*s.W+x] = Cell{R: ' ', S: st}
			return
		}
		s.Cells[y*s.W+x] = Cell{R: base, Rest: rest, S: st}
		s.Cells[y*s.W+x+1] = Cell{R: 0, S: st, Cont: true}
		return
	}
	// Если на этом месте была первая половина широкого символа, вторую
	// нужно погасить: иначе она останется висеть отдельной клеткой.
	if x+1 < s.W && s.Cells[y*s.W+x+1].Cont {
		s.Cells[y*s.W+x+1] = Cell{R: ' ', S: st}
	}
	s.Cells[y*s.W+x] = Cell{R: base, Rest: rest, S: st}
}

// Char — поставить один символ, не трогая остальное.
func (s *Screen) Char(x, y int, r rune, st Style) { s.Set(x, y, r, st) }

// Text — нарисовать строку, вернуть следующую колонку.
//
// Строка разбирается по графическим кластерам: составной символ (флаг,
// ZWJ-последовательность, эмодзи с тоном кожи) занимает ровно столько
// клеток, сколько рисует терминал, и целиком хранится в базовой клетке.
// Управляющие символы и ANSI-разметка в кадр не попадают.
func (s *Screen) Text(x, y int, text string, st Style) int {
	text = wcwidth.ValidUTF8(text)
	for i := 0; i < len(text); {
		if text[i] == 0x1b {
			i += wcwidth.SkipANSI(text, i)
			continue
		}
		w, size := wcwidth.ClusterAt(text, i)
		if size <= 0 {
			break
		}
		base, bsz := utf8.DecodeRuneInString(text[i:])
		if base == '\n' || base == '\r' || base == '\t' {
			i += size
			continue
		}
		if w <= 0 {
			i += size
			continue
		}
		if x+w > s.W {
			break
		}
		rest := ""
		if size > bsz {
			rest = text[i+bsz : i+size]
		}
		s.setCluster(x, y, w, base, rest, st)
		x += w
		i += size
	}
	return x
}

// Fill — заполнить прямоугольник фоном (и пробелами).
func (s *Screen) Fill(x, y, w, h int, st Style) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			s.Set(xx, yy, ' ', st)
		}
	}
}

// HLine — горизонтальная линия.
func (s *Screen) HLine(x, y, w int, r rune, st Style) {
	for i := 0; i < w; i++ {
		s.Set(x+i, y, r, st)
	}
}

// VLine — вертикальная линия.
func (s *Screen) VLine(x, y, h int, r rune, st Style) {
	for i := 0; i < h; i++ {
		s.Set(x, y+i, r, st)
	}
}

// Corners — набор символов рамки.
type Corners struct {
	TL, TR, BL, BR, H, V rune
}

// Rounded — скруглённая рамка.
var Rounded = Corners{'╭', '╮', '╰', '╯', '─', '│'}

// Square — обычная рамка.
var Square = Corners{'┌', '┐', '└', '┘', '─', '│'}

// Box — нарисовать рамку (без заливки).
func (s *Screen) Box(x, y, w, h int, c Corners, st Style) {
	if w < 2 || h < 2 {
		return
	}
	s.HLine(x+1, y, w-2, c.H, st)
	s.HLine(x+1, y+h-1, w-2, c.H, st)
	s.VLine(x, y+1, h-2, c.V, st)
	s.VLine(x+w-1, y+1, h-2, c.V, st)
	s.Set(x, y, c.TL, st)
	s.Set(x+w-1, y, c.TR, st)
	s.Set(x, y+h-1, c.BL, st)
	s.Set(x+w-1, y+h-1, c.BR, st)
}

// FillLine — заполнить строку целиком фоном.
func (s *Screen) FillLine(y int, st Style) {
	if y < 0 || y >= s.H {
		return
	}
	s.Fill(0, y, s.W, 1, st)
}

// Plain — текст кадра без стилей (для тестов и отладки).
func (s *Screen) Plain() string {
	var b strings.Builder
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			c := s.Cells[y*s.W+x]
			if c.Cont {
				continue
			}
			if c.R == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(c.R)
			if c.Rest != "" {
				b.WriteString(c.Rest)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ---------- Отрисовка в терминал ----------

// Renderer — вывод экрана с минимальными обновлениями: в терминал уходят
// только изменившиеся клетки, а не весь кадр.
type Renderer struct {
	w      io.Writer
	color  bool
	colors int
	prev   *Screen
	curX   int
	curY   int
	shown  bool
	last   Style
	ready  bool
}

// NewRenderer — создать рендерер.
//
// colors — глубина цвета: 0 — truecolor, 16 или 256 — ближайшая палитра.
// Одни терминалы (macOS Terminal.app) не понимают 24-битные коды и молча
// игнорируют цвет; тогда интерфейс потерял бы акценты и границы.
func NewRenderer(w io.Writer, color bool, colors int) *Renderer {
	return &Renderer{w: w, color: color, colors: colors, last: Style{}, curX: -1, curY: -1}
}

// Start — перейти в полноэкранный режим.
func (r *Renderer) Start(unicode bool) error {
	r.prev = nil
	r.ready = false
	// 1049 — альтернативный экран; 25 — спрятать курсор; 2004 — вставка;
	// 1000/1006 — колесо мыши и его координаты.
	seq := "\x1b[?1049h\x1b[?25l\x1b[?2004h\x1b[?1000h\x1b[?1006h"
	// В крайнем случае (нет VT) последовательности просто не сработают.
	if _, err := io.WriteString(r.w, seq); err != nil {
		return err
	}
	r.ready = true
	return nil
}

// Close — вернуть терминал в исходное состояние.
func (r *Renderer) Close() error {
	if !r.ready {
		return nil
	}
	r.ready = false
	_, err := io.WriteString(r.w, "\x1b[0m\x1b[?1000l\x1b[?1006l\x1b[?2004l\x1b[?25h\x1b[?1049l")
	return err
}

// Invalidate — перерисовать всё заново (после ресайза или выхода из
// внешней программы).
func (r *Renderer) Invalidate() { r.prev = nil }

// Draw — вывести изменившиеся клетки кадра и вернуть позицию курсора.
func (r *Renderer) Draw(s *Screen) {
	if !r.ready || s == nil {
		return
	}
	var b strings.Builder
	resized := r.prev == nil || r.prev.W != s.W || r.prev.H != s.H
	if resized {
		b.WriteString("\x1b[0m\x1b[2J")
		// Нулевые клетки заведомо отличаются от нарисованных, поэтому
		// первый кадр заливается целиком.
		r.prev = &Screen{W: s.W, H: s.H, Bg: s.Bg, Cells: make([]Cell, s.W*s.H)}
	}
	for y := 0; y < s.H; y++ {
		// Находим изменившийся отрезок строки.
		minX, maxX := -1, -1
		for x := 0; x < s.W; x++ {
			i := y*s.W + x
			a := s.Cells[i]
			var old Cell
			if i < len(r.prev.Cells) {
				old = r.prev.Cells[i]
			}
			if a != old {
				if minX < 0 {
					minX = x
				}
				maxX = x
			}
		}
		if minX < 0 {
			continue
		}
		// Широкий символ на границе: расширяем отрезок, чтобы не
		// перерисовать половину.
		for minX > 0 && s.Cells[y*s.W+minX].Cont {
			minX--
		}
		if maxX+1 < s.W && s.Cells[y*s.W+maxX+1].Cont {
			maxX++
		}
		r.moveTo(&b, minX, y)
		var cur Style
		for x := minX; x <= maxX; x++ {
			c := s.Cells[y*s.W+x]
			if c.Cont {
				continue
			}
			if c.S != cur {
				r.sgr(&b, c.S)
				cur = c.S
			}
			ch := c.R
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
			// Хвост кластера печатается вместе с базой: терминал
			// рисует всю последовательность одной картинкой и
			// сдвигает курсор ровно на w клеток.
			if c.Rest != "" {
				b.WriteString(c.Rest)
			}
		}
		for x := minX; x <= maxX; x++ {
			r.prev.Cells[y*s.W+x] = s.Cells[y*s.W+x]
		}
	}
	if b.Len() > 0 {
		_, _ = io.WriteString(r.w, b.String())
	}
	r.curX, r.curY = -1, -1
}

// moveTo — поставить курсор (1-based в ANSI).
func (r *Renderer) moveTo(b *strings.Builder, x, y int) {
	b.WriteString("\x1b[")
	b.WriteString(strconv.Itoa(y + 1))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(x + 1))
	b.WriteByte('H')
}

// Cursor — показать курсор в позиции (x,y) и вернуть true.
func (r *Renderer) Cursor(x, y int) {
	if !r.ready {
		return
	}
	if x == r.curX && y == r.curY && r.shown {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b[")
	b.WriteString(strconv.Itoa(y + 1))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(x + 1))
	b.WriteByte('H')
	if !r.shown {
		b.WriteString("\x1b[?25h")
		r.shown = true
	}
	_, _ = io.WriteString(r.w, b.String())
	r.curX, r.curY = x, y
}

// HideCursor — спрятать курсор.
func (r *Renderer) HideCursor() {
	if !r.ready || !r.shown {
		return
	}
	_, _ = io.WriteString(r.w, "\x1b[?25l")
	r.shown = false
	r.curX, r.curY = -1, -1
}

// sgr — escape-последовательность стиля.
func (r *Renderer) sgr(b *strings.Builder, st Style) {
	b.WriteString("\x1b[0")
	if st.Bold {
		b.WriteString(";1")
	}
	if st.Dim {
		b.WriteString(";2")
	}
	if st.Italic {
		b.WriteString(";3")
	}
	if st.Underline {
		b.WriteString(";4")
	}
	if st.Reverse {
		b.WriteString(";7")
	}
	if st.Strike {
		b.WriteString(";9")
	}
	if r.color {
		if st.FG.On {
			r.writeColor(b, st.FG, false)
		}
		if st.BG.On {
			r.writeColor(b, st.BG, true)
		}
	}
	b.WriteByte('m')
}

// writeColor — один цвет в SGR с учётом глубины палитры.
func (r *Renderer) writeColor(b *strings.Builder, c Color, bg bool) {
	switch r.colors {
	case 16:
		n := ansi16(c)
		base := 30
		if bg {
			base = 40
		}
		if n >= 8 {
			base += 60
			n -= 8
		}
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(base + n))
	case 256:
		if bg {
			b.WriteString(";48;5;")
		} else {
			b.WriteString(";38;5;")
		}
		b.WriteString(strconv.Itoa(xterm256(c)))
	default:
		if bg {
			b.WriteString(";48;2;")
		} else {
			b.WriteString(";38;2;")
		}
		b.WriteString(strconv.Itoa(int(c.R)))
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(int(c.G)))
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(int(c.B)))
	}
}

// xterm256 — ближайший цвет палитры xterm-256: серая шкала для почти
// серых, куб 6×6×6 для остальных.
func xterm256(c Color) int {
	r, g, bl := int(c.R), int(c.G), int(c.B)
	gray := (r + g + bl) / 3
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	if abs(r-gray) < 10 && abs(g-gray) < 10 && abs(bl-gray) < 10 {
		if gray < 8 {
			return 16
		}
		if gray > 238 {
			return 231
		}
		n := (gray - 8 + 5) / 10
		if n > 23 {
			n = 23
		}
		return 232 + n
	}
	q := func(v int) int {
		switch {
		case v < 48:
			return 0
		case v < 115:
			return 1
		default:
			return (v - 35) / 40
		}
	}
	return 16 + 36*q(r) + 6*q(g) + q(bl)
}

// ansi16 — ближайший из 16 базовых цветов ANSI.
//
// Для насыщенных цветов важен оттенок, а не расстояние в RGB: тёмно-
// оранжевый акцент должен остаться жёлтым, а не стать серым. Серые и
// почти серые раскладываются по яркости, чтобы границы и фон не слились.
func ansi16(c Color) int {
	r, g, b := int(c.R), int(c.G), int(c.B)
	maxc := r
	if g > maxc {
		maxc = g
	}
	if b > maxc {
		maxc = b
	}
	minc := r
	if g < minc {
		minc = g
	}
	if b < minc {
		minc = b
	}
	if maxc-minc < 40 || maxc < 60 {
		lum := (299*r + 587*g + 114*b) / 1000
		switch {
		case lum < 40:
			return 0 // чёрный
		case lum < 110:
			return 8 // серый
		case lum < 190:
			return 7 // серебро
		default:
			return 15 // белый
		}
	}
	n := 1 // красный
	switch {
	case r >= g && r >= b:
		switch {
		case b > g+30 && b*2 > r:
			n = 5 // пурпурный
		case g*2 > r:
			n = 3 // жёлтый
		}
	case g >= r && g >= b:
		n = 2 // зелёный
		if b > r+30 {
			n = 6 // бирюзовый
		}
	default:
		n = 4 // синий
		switch {
		case r > g+30 && r*2 > b:
			n = 5 // пурпурный
		case g > r+30:
			n = 6 // бирюзовый
		}
	}
	if maxc >= 180 {
		n += 8 // яркий
	}
	return n
}
