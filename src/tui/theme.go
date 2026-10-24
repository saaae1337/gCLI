package tui

// ---------- Тема ----------
//
// Палитра продолжает «Уголь» из пакета ui: тёплый белый текст и охра на
// почти чёрном фоне, с индиго для вторичных акцентов. Значения совпадают
// с rgbEmber, чтобы текст, который приходит из старых панелей, не
// выбивался из нового интерфейса.

// Theme — цвета интерфейса.
type Theme struct {
	Bg        Color // общий фон
	Panel     Color // панели и шапка
	Panel2    Color // поле ввода, наведение
	Border    Color // рамки
	BorderDim Color // тонкие линии
	Text      Color // основной текст
	Muted     Color // подписи
	Dim       Color // совсем тихое
	Accent    Color // охра: бренд, действия
	Accent2   Color // индиго: вторичный акцент
	Gold      Color // светлая охра: цифры, искры
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
