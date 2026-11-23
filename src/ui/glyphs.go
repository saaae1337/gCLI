// Package ui — терминальный интерфейс gcli: цвета, карточки инструментов,
// диффы, потоковый markdown и нижняя живая область (поле ввода).
//
// Визуальный язык — терминальный интерфейс Claude Code: скруглённая рамка
// поля ввода «╭ ─ ╮», маркер события «⏺», зубец результата «⎿», спиннер
// «✻». Оформление сдержанное: почти весь текст — обычным цветом терминала,
// хром — приглушённым серым, единственный акцент — фирменный коралловый.
//
// Пакет умеет работать и в цвете, и без него (NO_COLOR, пайпы). Если
// терминал не умеет юникод (-ascii, GCLI_ASCII, консоль без UTF-8), весь
// набор глифов заменяется чистым ASCII: тот же вид в классической консоли
// Windows 10/11, в linux-консоли и на macOS.
package ui

// Glyphs — набор символов оформления.
//
// Два набора: юникодный (Claude Code: ╭ ⏺ ⎿ ✻) и ASCII-запаска для
// терминалов без UTF-8 (рамки «+», маркер «*», зубец «:`»). Поля и
// сигнатуры исторические, смысл прежний.
type Glyphs struct {
	Unicode bool
	Sharp   bool // прямые углы вместо округлённых (ASCII-набор)

	// Рамки.
	TL, TR, BL, BR, H, V string
	// Ветки дерева.
	Tee, Elbow, Pipe, Dash, Arrow string
	// Маркеры.
	Bullet, Check, Cross, Excl, Star, Dot string
	// Активность.
	Running, Pending, BarFull, BarEmpty string
	// Прочее.
	Separator, Ellipsis, Dots string

	// DashDot — символ пунктирной линии (мягкий разделитель).
	DashDot string
	// RuleChar — символ сплошной линии (заголовки, хвосты таблиц).
	RuleChar string
	// Mark — монограмма стиля (брендовый «✻»).
	Mark string
	// Tick — «зубец» перед результатом инструмента («⎿»).
	Tick string
	// Retry — знак ожидания.
	Retry string
}

// ccGlyphs — юникодный набор: визуальный язык Claude Code.
func ccGlyphs() Glyphs {
	return Glyphs{
		Unicode: true,
		TL:      "╭", TR: "╮", BL: "╰", BR: "╯",
		H: "─", V: "│",
		Tee: "├", Elbow: "└", Pipe: "│", Dash: "─", Arrow: "→",
		Bullet: "•", Check: "✓", Cross: "×", Excl: "!", Star: "✻", Dot: "⏺",
		Running: "✻", Pending: "·", BarFull: "█", BarEmpty: "·",
		Separator: "·", Ellipsis: "…", Dots: "·",
		DashDot:  "·",
		RuleChar: "─",
		Mark:     "✻",
		Tick:     "⎿",
		Retry:    "…",
	}
}

// asciiGlyphs — ASCII-запаска: терминал без юникода рисует то же самое
// другими знаками (классическая консоль Windows, пайп, -ascii).
func asciiGlyphs() Glyphs {
	return Glyphs{
		Sharp: true,
		TL:    "+", TR: "+", BL: "+", BR: "+",
		H: "-", V: "|",
		Tee: "|-", Elbow: "`-", Pipe: "|", Dash: "-", Arrow: "->",
		Bullet: "-", Check: "+", Cross: "x", Excl: "!", Star: "*", Dot: "*",
		Running: ">", Pending: "o", BarFull: "#", BarEmpty: ".",
		Separator: "|", Ellipsis: "...", Dots: "...",
		DashDot:  ".",
		RuleChar: "-",
		Mark:     "*",
		Tick:     ":",
		Retry:    "~",
	}
}

// emberGlyphs — историческое имя юникодного набора.
func emberGlyphs() Glyphs { return ccGlyphs() }

// emberASCIIGlyphs — то же самое (совместимость имён).
func emberASCIIGlyphs() Glyphs { return asciiGlyphs() }

// GlyphsFor — набор под текущий терминал: юникод Claude Code, если терминал
// умеет UTF-8, иначе ASCII-запаска.
func GlyphsFor(theme string, unicodeOK bool) Glyphs {
	if unicodeOK {
		return ccGlyphs()
	}
	return asciiGlyphs()
}

// unicodeOutput — решение «можно ли печатать юникод-глифы». Ставится при
// старте приложения: оно знает про -ascii, GCLI_ASCII и кодировку консоли.
var unicodeOutput = false

// SetUnicodeOutput — зафиксировать решение о юникоде.
func SetUnicodeOutput(v bool) { unicodeOutput = v }

// UnicodeOutput — текущее решение о юникоде.
func UnicodeOutput() bool { return unicodeOutput }

// Glyph — глиф для строк вне UI-рендерера: юникод, если можно, иначе ascii.
func Glyph(mark, ascii string) string {
	if unicodeOutput {
		return mark
	}
	return ascii
}

// Символы блоков для заполнения полосы прогресса (двухуровневый).
const (
	barFull  = "#"
	barPart  = "+"
	barEmpty = "."
)

// SpinnerFrames — кадры анимации ожидания: набор Claude Code
// (восьмилучевые звёздочки) и ASCII-палочка для терминалов без юникода.
var (
	ccSpinner = []string{"✻", "✽", "✶", "✳", "✢"}
	// SpinnerAscii — тот же набор для любых терминалов.
	SpinnerAscii = []string{"|", "/", "-", "\\"}
)

// SpinnerFor — кадры ожидания под текущий терминал.
func SpinnerFor(theme string, unicodeOK bool) []string {
	if unicodeOK {
		return ccSpinner
	}
	return SpinnerAscii
}
