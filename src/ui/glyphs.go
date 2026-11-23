// Package ui — терминальный интерфейс gcli: рамки, цвета,
// таблицы, карточки инструментов, диффы и потоковый markdown.
//
// Пакет умеет работать и в цвете, и без него (NO_COLOR, пайпы).
// Оформление — чистый ASCII: рамки «+ - |», статусы «ok x !», линии из
// дефисов. Декоративные юникод-символы не используются: тот же вид в
// классической консоли Windows 10/11, в linux-консоли и на macOS.
package ui

// Glyphs — набор символов оформления.
//
// Поля Unicode и набор для «юникод-терминалов» оставлены для
// совместимости сигнатур, но оба набора — один и тот же ASCII: gcli
// больше не печатает псевдографику ни в каком режиме.
type Glyphs struct {
	Unicode bool
	Sharp   bool // прямые углы вместо округлённых

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
	// RuleChar — символ сплошной линии (длинные заголовки, хвосты таблиц).
	RuleChar string
	// Mark — монограмма стиля.
	Mark string
	// Tick — «зубец» перед результатом инструмента.
	Tick string
	// Retry — знак ожидания.
	Retry string
}

// asciiGlyphs — единственный набор символов gcli: чистый ASCII.
func asciiGlyphs() Glyphs {
	return Glyphs{
		TL: "|", TR: "", BL: "|", BR: "",
		H: "-", V: "|",
		Tee: "|-", Elbow: "`-", Pipe: "|", Dash: "-", Arrow: "->",
		Bullet: "-", Check: "+", Cross: "x", Excl: "!", Star: "*", Dot: ".",
		Running: ">", Pending: "o", BarFull: "#", BarEmpty: ".",
		Separator: "|", Ellipsis: "...", Dots: "...",
		DashDot:  ".",
		RuleChar: "-",
		Mark:     "*",
		Tick:     ":",
		Retry:    "~",
	}
}

// emberGlyphs — историческое имя набора стиля «УГОЛЬ». Раньше различались
// юникод и ASCII варианты, теперь набор один и всегда ASCII.
func emberGlyphs() Glyphs {
	g := asciiGlyphs()
	g.Unicode = true
	return g
}

// emberASCIIGlyphs — то же самое (совместимость имён).
func emberASCIIGlyphs() Glyphs { return asciiGlyphs() }

// GlyphsFor — набор под текущий терминал. Стиль один — «УГОЛЬ», и всегда
// ASCII: аргумент unicodeOK оставлен в сигнатуре для совместимости.
func GlyphsFor(theme string, unicodeOK bool) Glyphs {
	return asciiGlyphs()
}

// unicodeOutput — решение «можно ли печатать юникод-глифы». С 0.9.3
// всегда ложь: декоративные символы не печатаются нигде. Переменная и
// SetUnicodeOutput оставлены для совместимости вызовов.
var unicodeOutput = false

// SetUnicodeOutput — зафиксировать решение о юникоде. Значение
// игнорируется: gcli печатает только ASCII.
func SetUnicodeOutput(bool) { unicodeOutput = false }

// UnicodeOutput — текущее решение о юникоде.
func UnicodeOutput() bool { return unicodeOutput }

// Glyph — глиф для строк вне UI-рендерера: исторически пары «✔»/«+»,
// «✖»/«x», «█»/«#», «░»/«.», «◆»/«*», «⊘»/«-». Теперь всегда ASCII-вариант.
func Glyph(mark, ascii string) string {
	return ascii
}

// Символы блоков для заполнения полосы прогресса (двухуровневый).
const (
	barFull  = "#"
	barPart  = "+"
	barEmpty = "."
)

// SpinnerFrames — кадры анимации ожидания.
var (
	// emberSpinner — классическая палочка: знак один, движение создаёт цвет.
	emberSpinner = []string{"|", "/", "-", "\\"}
	// SpinnerAscii — тот же набор: ASCII для любых терминалов.
	SpinnerAscii = []string{"|", "/", "-", "\\"}
)

// SpinnerFor — кадры ожидания под текущий терминал. Аргумент
// unicodeOK оставлен в сигнатуре для совместимости: кадры всегда ASCII.
func SpinnerFor(theme string, unicodeOK bool) []string {
	return SpinnerAscii
}
