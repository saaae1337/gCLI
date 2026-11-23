package tui

// ---------- Тема ----------
//
// Палитра «Ночной город»: холодный синий акцент и фиолет на тёмном
// сине-чёрном фоне — язык современных терминальных редакторов (концепт
// OpenCode). Цвета — единственное оформление, которое отличает терминалы:
// всё остальное — чистый ASCII, который одинаково рисуют cmd.exe на
// Windows 10/11, linux-консоль и Terminal.app на macOS. Никаких
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
	Accent    Color // синий: бренд, метки ответа
	Accent2   Color // фиолет: метки пользователя
	Gold      Color // светлое золото: цифры, выделение
	OK        Color
	Warn      Color
	Err       Color
	Info      Color
	User      Color // текст запроса пользователя
	Sel       Color // выделение в списках
}

// Dark — тема по умолчанию.
func Dark() Theme {
	return Theme{
		Bg:        Hex("#0E1016"),
		Panel:     Hex("#141824"),
		Panel2:    Hex("#1A2030"),
		Border:    Hex("#2B3245"),
		BorderDim: Hex("#1F2536"),
		Text:      Hex("#E4E9F4"),
		Muted:     Hex("#8A93AB"),
		Dim:       Hex("#5A6478"),
		Accent:    Hex("#7AA2F7"),
		Accent2:   Hex("#BB9AF7"),
		Gold:      Hex("#E0C079"),
		OK:        Hex("#9ECE6A"),
		Warn:      Hex("#E0AF68"),
		Err:       Hex("#F7768E"),
		Info:      Hex("#7DCFFF"),
		User:      Hex("#E4E9F4"),
		Sel:       Hex("#24304A"),
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
	// Метки ленты: у каждого вида сообщения свой знак.
	// you/gcli — текстовые метки, инструменты — «*», размышления — «~».
	glyphTool  = "*"
	glyphThink = "~"
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
