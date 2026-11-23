package tui

// ---------- Тема ----------
//
// Палитра «Уголь»: тёплый белый текст и охра на почти чёрном фоне.
// Цвета — единственное оформление, которое отличает терминалы: всё
// остальное — чистый ASCII, который одинаково рисуют cmd.exe на
// Windows 10/11, linux-консоли и Terminal.app на macOS. Никаких
// псевдографических символов, полублоков и брайль-точек: рамки из
// «+ - |», спиннер из «| / - \», статусы словами и знаками «ok x !».

// Theme — цвета интерфейса.
type Theme struct {
	Bg        Color // общий фон
	Panel     Color // панели: шапка и статус
	Panel2    Color // поле ввода, окна
	Border    Color // рамки
	BorderDim Color // тонкие линии
	Text      Color // основной текст
	Muted     Color // подписи
	Dim       Color // совсем тихое
	Accent    Color // охра: бренд, действия
	Accent2   Color // индиго: вторичный акцент
	Gold      Color // светлая охра: цифры, выделение
	OK        Color
	Warn      Color
	Err       Color
	Info      Color
	User      Color // фон запроса пользователя
	Sel       Color // выделение в списках
}

// Dark — тема по умолчанию.
func Dark() Theme {
	return Theme{
		Bg:        Hex("#0E0D0B"),
		Panel:     Hex("#161411"),
		Panel2:    Hex("#1D1A16"),
		Border:    Hex("#37312A"),
		BorderDim: Hex("#26221D"),
		Text:      Hex("#EEE9E3"),
		Muted:     Hex("#8A8478"),
		Dim:       Hex("#5C574F"),
		Accent:    Hex("#D08A3E"),
		Accent2:   Hex("#8C94F5"),
		Gold:      Hex("#E8C078"),
		OK:        Hex("#7EBE8A"),
		Warn:      Hex("#E2B75C"),
		Err:       Hex("#DB6A6E"),
		Info:      Hex("#7DC4E8"),
		User:      Hex("#1A1713"),
		Sel:       Hex("#2A241C"),
	}
}

// ---------- ASCII-глифы ----------
//
// Все символы оформления собраны в одном месте: если здесь ASCII, то
// ASCII и на экране. Диапазон U+0000..U+007F — единственное, что
// гарантированно рисует любой терминал, включая классическую консоль
// Windows без смены шрифта.

const (
	// Рамки: плюс, дефис, вертикальная черта.
	glyphTL = '+'
	glyphTR = '+'
	glyphBL = '+'
	glyphBR = '+'
	glyphH  = '-'
	glyphV  = '|'
	// Спиннер: классический поворот палочки.
	// Запятая в конце обязательна — иначе следующий const продолжит ряд.
	spinnerFrames = "|/-\\"
	// Статусы инструментов и прочие отметки.
	glyphOK     = "ok"
	glyphFail   = "x"
	glyphDenied = "!"
	glyphMore   = "..."
	// Стрелка прокрутки и маркер выбора.
	glyphScroll = '^'
	glyphCursor = '>'
	// Приглашение поля ввода.
	glyphPrompt = '>'
	// Тело развернутых размышлений: каждая строка с чертой.
	glyphQuote = '|'
	// Секретный ввод: по звёздочке на символ.
	glyphSecret = '*'
)

// spinner — кадр ожидания по номеру тика.
func spinner(tick int) string {
	if len(spinnerFrames) == 0 {
		return "-"
	}
	i := tick % len(spinnerFrames)
	return string(spinnerFrames[i])
}

// ---------- Строки со стилями ----------

// Line — одна строка стилизованного текста для doc-блоков.
type Line []Seg

// Seg — кусок строки с одним стилем.
type Seg struct {
	Text string
	S    Style
}

// Text — плоский текст строки.
func (l Line) Text() string {
	var out string
	for _, s := range l {
		out += s.Text
	}
	return out
}

// Width — ширина строки.
func (l Line) Width() int {
	w := 0
	for _, s := range l {
		w += StringWidth(s.Text)
	}
	return w
}

// trimEnd — убрать пробелы в конце.
func (l Line) trimEnd() Line {
	for len(l) > 0 {
		last := l[len(l)-1]
		trimmed := trimRightSpaces(last.Text)
		if trimmed == last.Text {
			break
		}
		if trimmed == "" {
			l = l[:len(l)-1]
			continue
		}
		l[len(l)-1].Text = trimmed
		break
	}
	return l
}

func trimRightSpaces(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == ' ' {
		i--
	}
	return s[:i]
}
