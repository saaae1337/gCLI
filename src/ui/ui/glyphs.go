// Package ui — терминальный интерфейс gcli: псевдографика, цвета,
// таблицы, карточки инструментов, диффы и потоковый markdown.
//
// Пакет умеет работать и в цвете, и без него (NO_COLOR, пайпы),
// и в юникоде, и в чистом ASCII — наборы символов выбираются автоматически.
package ui

import "strings"

// Glyphs — набор псевдографики с учётом unicode/ascii.
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
	// Mark — монограмма стиля (ромб «◆»).
	Mark string
	// Tick — «зубец» перед результатом инструмента.
	// У Claude Code такой же приём, но там глиф не входит в общий набор и
	// печатается прямо в коде. Здесь он часть темы, поэтому в ASCII-режиме
	// подставляется ASCII-вариант, а не «кракозябра».
	Tick string
	// Retry — знак ожидания: «↻» (указатель) или ASCII «~».
	Retry string
}

// emberGlyphs — набор псевдографики стиля «УГОЛЬ» (см. ember.go).
// В ASCII-режиме у каждого символа есть запасной вариант.
func emberGlyphs() Glyphs {
	return Glyphs{
		Unicode: true,
		TL:      "│", TR: "", BL: "│", BR: "",
		H: "─", V: "│",
		Tee: "├", Elbow: "└", Pipe: "│", Dash: "─", Arrow: "→",
		Bullet: "•", Check: "✔", Cross: "✖", Excl: "▲", Star: "◆", Dot: "·",
		Running: "◆", Pending: "◇", BarFull: "▬", BarEmpty: "·",
		Separator: "·", Ellipsis: "…", Dots: "…",
		DashDot:  "·",
		RuleChar: "─",
		Mark:     "◆",
		Tick:     "⎿",
		Retry:    "↻",
	}
}

// emberASCIIGlyphs — тот же стиль для терминалов без юникода.
func emberASCIIGlyphs() Glyphs {
	return Glyphs{
		Unicode: false,
		TL:      "|", TR: "", BL: "|", BR: "",
		H: "-", V: "|",
		Tee: "|-", Elbow: "`-", Pipe: "|", Dash: "-", Arrow: "->",
		Bullet: "-", Check: "+", Cross: "x", Excl: "!", Star: "*", Dot: ".",
		Running: ">", Pending: "o", BarFull: "#", BarEmpty: ".",
		Separator: ".", Ellipsis: "...", Dots: "...",
		DashDot:  ".",
		RuleChar: "-",
		Mark:     "*",
		Tick:     ">-",
		Retry:    "~",
	}
}

// GlyphsFor — набор под текущий терминал. Стиль один — «УГОЛЬ»,
// различается только поддержка юникода.
func GlyphsFor(theme string, unicodeOK bool) Glyphs {
	if unicodeOK {
		return emberGlyphs()
	}
	return emberASCIIGlyphs()
}

// Символы блоков для заполнения полосы прогресса (двухуровневый).
const (
	barFull  = "█"
	barPart  = "▌"
	barEmpty = "░"
)

// SpinnerFrames — кадры анимации ожидания.
var (
	// emberSpinner — «указатель» «↻»: знак один, движение создаёт цвет.
	emberSpinner = []string{"↻", "↺"}
	// SpinnerAscii — для терминалов без юникода.
	SpinnerAscii = []string{"|", "/", "-", "\\"}
)

// SpinnerFor — кадры ожидания под текущий терминал.
func SpinnerFor(theme string, unicodeOK bool) []string {
	if !unicodeOK {
		return SpinnerAscii
	}
	return emberSpinner
}

// ToolIcon — иконка инструмента (двуручные эмодзи в терминале не везде есть,
// поэтому используем простые символы).
type ToolIcon struct {
	Glyph string
	Name  string
}

var toolIcons = map[string]ToolIcon{
	"read_file":    {"▤", "read"},
	"write_file":   {"✎", "write"},
	"edit_file":    {"✎", "edit"},
	"list_dir":     {"▤", "list"},
	"glob":         {"◎", "glob"},
	"grep":         {"⌕", "grep"},
	"bash":         {"⚡", "shell"},
	"web_search":   {"⌕", "search"},
	"web_fetch":    {"⇩", "fetch"},
	"todo_write":   {"☑", "todo"},
	"think":        {"◈", "think"},
	"load_skill":   {"◐", "skill"},
	"spawn_agent":  {"◆", "subagent"},
	"agent_status": {"◈", "agents"},
	"ask_user":     {"?", "ask"},
	"task_note":    {"▣", "note"},
}

// IconFor — иконка инструмента с запасным вариантом.
func IconFor(name string) ToolIcon {
	if i, ok := toolIcons[name]; ok {
		return i
	}
	// Расширения: имена с префиксом x_ (добавлен из-за коллизии имён).
	if strings.HasPrefix(name, "x_") {
		return ToolIcon{"⚡", "ext"}
	}
	return ToolIcon{"·", "tool"}
}
