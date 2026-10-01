package ui

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ---------- Уровни цвета ----------
//
// Терминалы различаются по возможностям: где-то только 8 базовых ANSI-цвет,
// где-то 256, где-то 24-битный truecolor. Уровень определяется автоматически,
// а пользователь может задать его флагом -color (16|256|truecolor|auto).

type colorLevel int

const (
	colorNone colorLevel = iota // NO_COLOR, пайп, -no-color
	color16                     // базовые ANSI-цвета
	color256                    // xterm-256
	colorRGB                    // 24-битный truecolor
)

// ColorLevel — уровень поддержки цвета.
type ColorLevel int

// Уровни цвета в виде констант для /status и /doctor.
const (
	ColorNone = ColorLevel(colorNone)
	Color16   = ColorLevel(color16)
	Color256  = ColorLevel(color256)
	ColorRGB  = ColorLevel(colorRGB)
)

func (l ColorLevel) String() string {
	switch l {
	case Color256:
		return "256"
	case ColorRGB:
		return "truecolor"
	case Color16:
		return "16"
	default:
		return "none"
	}
}

// ParseColorLevel — разобрать значение флага -color / переменной GCLI_COLOR.
// Второй результат false означает «значение не задано, определить автоматически».
func ParseColorLevel(s string) (ColorLevel, bool) {
	lvl, ok := parseColorLevel(s)
	return ColorLevel(lvl), ok
}

// parseColorLevel — внутренняя разборка уровня цвета.
func parseColorLevel(s string) (colorLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return colorNone, false // не задано — определим сами
	case "none", "off", "no", "0":
		return colorNone, true
	case "16", "ansi", "basic":
		return color16, true
	case "256", "xterm", "xterm-256", "xterm-256color", "256color":
		return color256, true
	case "truecolor", "24bit", "rgb", "24":
		return colorRGB, true
	}
	return colorNone, false
}

// detectColorLevel — определить уровень цвета окружения.
func detectColorLevel() colorLevel {
	if os.Getenv("NO_COLOR") != "" {
		return colorNone
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COLORTERM"))) {
	case "truecolor", "24bit":
		return colorRGB
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TERM"))) {
	case "xterm-256color", "screen-256color", "alacritty", "wezterm", "foot", "kitty", "tmux-256color":
		return color256
	}
	// Windows Terminal и современный conhost умеют 256 цветов.
	if os.Getenv("WT_SESSION") != "" || os.Getenv("ConEmuANSI") == "ON" {
		return color256
	}
	if runtime.GOOS == "windows" && os.Getenv("TERM_PROGRAM") == "" {
		return color256
	}
	return color16
}

// ---------- Построение кодов ----------

func fg256(n int) string { return "\033[38;5;" + itoa(n) + "m" }

func fgRGB(r, g, b int) string {
	return "\033[38;2;" + itoa(r) + ";" + itoa(g) + ";" + itoa(b) + "m"
}

// blendRGB — линейная интерполяция между двумя цветами.
func blendRGB(a, b [3]int, t float64) [3]int {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return [3]int{
		a[0] + int(float64(b[0]-a[0])*t),
		a[1] + int(float64(b[1]-a[1])*t),
		a[2] + int(float64(b[2]-a[2])*t),
	}
}

// rgbTo256 — приблизить truecolor-цвет к палитре xterm-256.
// Сначала считаем ошибку для куба 6×6×6 и для серой шкалы, берём лучшую.
func rgbTo256(c [3]int) int {
	r, g, b := c[0], c[1], c[2]
	ri, gi, bi := nearestC6(r), nearestC6(g), nearestC6(b)
	cubeErr := sq(r-ri*51) + sq(g-gi*51) + sq(b-bi*51)
	gray := (r + g + b) / 3
	g24 := clampInt((gray-8)*24/247, 0, 23)
	grayErr := sq(r-(8+g24*10)) + sq(g-(8+g24*10)) + sq(b-(8+g24*10))
	if grayErr < cubeErr {
		return 232 + g24
	}
	return 16 + 36*ri + 6*gi + bi
}

func nearestC6(v int) int {
	if v < 48 {
		return 0
	}
	if v < 115 {
		return 1
	}
	return clampInt((v-35)/40, 0, 5)
}

func sq(v int) int { return v * v }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// nearestBasic — ближайший из 8 базовых ANSI-цветов.
func nearestBasic(c [3]int) string {
	bases := [][3]int{
		{0, 0, 0}, {205, 49, 49}, {13, 188, 121}, {229, 229, 16},
		{36, 114, 200}, {188, 63, 188}, {17, 168, 205}, {229, 229, 229},
	}
	best, bestErr := "", 1<<30
	for i, b := range bases {
		e := sq(c[0]-b[0]) + sq(c[1]-b[1]) + sq(c[2]-b[2])
		if e < bestErr {
			bestErr, best = e, []string{cBlack, cRed, cGreen, cYellow, cBlue, cMagenta, cCyan, cWhite}[i]
		}
	}
	return best
}

const cBlack = "\033[30m"

// hexToRGB — разобрать "#rrggbb" или "rrggbb".
func hexToRGB(h string) ([3]int, bool) {
	h = strings.TrimPrefix(strings.TrimSpace(h), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseInt(h[i*2:i*2+2], 16, 32)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = int(v)
	}
	return out, true
}

// ---------- Цветовая тема в truecolor ----------

// rgbTheme — семантические цвета темы в 24-битном виде.
type rgbTheme struct {
	Accent, Accent2         [3]int
	OK, Warn, Err, Info     [3]int
	Muted, Head, Code, Link [3]int
	Added, Removed, Hunk    [3]int
}

// rgbThemeFor — цвета «УГЛЯ» в truecolor. Параметр оставлен для
// совместимости сигнатур; стиль один.
func rgbThemeFor(theme string) rgbTheme {
	return rgbEmber
}

// codeFor — код ANSI для truecolor-цвета на текущем уровне.
func codeFor(c [3]int, grade colorLevel) string {
	switch grade {
	case colorRGB:
		return fgRGB(c[0], c[1], c[2])
	case color256:
		return fg256(rgbTo256(c))
	default:
		return nearestBasic(c)
	}
}

// upgradePalette — обогатить 16-цветную палитру кодами 256/rgb.
func upgradePalette(base Palette, theme string, grade colorLevel) Palette {
	if grade < color256 {
		return base
	}
	rt := rgbThemeFor(theme)
	base.Accent = codeFor(rt.Accent, grade)
	base.Accent2 = codeFor(rt.Accent2, grade)
	base.OK = codeFor(rt.OK, grade)
	base.Warn = codeFor(rt.Warn, grade)
	base.Err = codeFor(rt.Err, grade)
	base.Info = codeFor(rt.Info, grade)
	base.Muted = codeFor(rt.Muted, grade)
	base.Head = codeFor(rt.Head, grade)
	base.Code = codeFor(rt.Code, grade)
	base.Link = codeFor(rt.Link, grade)
	base.Added = codeFor(rt.Added, grade)
	base.Removed = codeFor(rt.Removed, grade)
	base.Hunk = codeFor(rt.Hunk, grade)
	return base
}

// ---------- Пользовательские цвета темы ----------

// ThemeColors — опциональные цвета темы (hex) из config.json.
// Пустая строка означает «взять цвет темы по умолчанию».
type ThemeColors struct {
	Accent, Accent2, OK, Warn, Err, Muted, Text, Code, Link string
}

// applyThemeColors — наложить пользовательские цвета на палитру.
func applyThemeColors(p *Palette, tc ThemeColors, grade colorLevel) {
	set := func(dst *string, hex string) {
		if hex == "" {
			return
		}
		c, ok := hexToRGB(hex)
		if !ok {
			return
		}
		*dst = codeFor(c, grade)
	}
	set(&p.Accent, tc.Accent)
	set(&p.Accent2, tc.Accent2)
	set(&p.OK, tc.OK)
	set(&p.Warn, tc.Warn)
	set(&p.Err, tc.Err)
	set(&p.Muted, tc.Muted)
	set(&p.Head, tc.Text)
	set(&p.Code, tc.Code)
	set(&p.Link, tc.Link)
}

// ---------- Градиенты ----------

// Gradient — раскрасить строку градиентом от одного цвета к другому.
func (u *UI) Gradient(s string, from, to [3]int) string {
	if s == "" || u.quiet || u.grade < color256 {
		return s
	}
	r := []rune(s)
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i, ch := range r {
		if ch == ' ' {
			b.WriteRune(' ')
			continue
		}
		t := 0.0
		if len(r) > 1 {
			t = float64(i) / float64(len(r)-1)
		}
		b.WriteString(codeFor(blendRGB(from, to, t), u.grade))
		b.WriteRune(ch)
	}
	b.WriteString(cReset)
	return b.String()
}

// ApplyHexes — применить пользовательские hex-цвета к теме «на лету».
func (u *UI) ApplyHexes(tc ThemeColors) {
	if u.grade == colorNone {
		return
	}
	u.opts.Colors = tc
	u.applyPalette()
}
