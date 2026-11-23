package ui

import (
	"strings"

	"gcli/core"
)

// ---------- Стиль «Claude Code» ----------
//
// Визуальный язык — терминальный интерфейс Claude Code, копия без
// самодеятельности:
//
//  1. Разметка. Событие начинается маркером «*», результат — строкой ниже
//     с отступом:
//
//      * read_file src/ui/ui.go
//        481 строка · 3ms
//
//  2. Почти весь текст — обычным цветом терминала; хром (рамки, подсказки,
//     метрики) — нейтральным серым, без тёплых оттенков.
//
//  3. Единственный акцент — коралловый Claude (#D97757) — только на живых
//     элементах: приглашение «>», спиннер, маркер вызова, выбор в меню.
//     Заголовки, таблицы и текст акцента не получают.
//
//  4. Никаких градиентов, переливов и рамок вокруг данных.

// themeEmber - каноническое имя темы. Историческое значение сохранено:
// старые config.json читаются без миграций.
const themeEmber = "ember"

// normalizeTheme - свести имя темы к канону. Раньше тем было несколько
// (orbit, claude, nord, matrix, mono); что бы ни стояло в старом
// config.json, оформление одно.
func normalizeTheme(theme string) string {
	return themeEmber
}

// ---------- Палитра ----------

// Нейтральная палитра: серые без тёплого подмеса, никакого «песка» на
// коде и заголовках. Такой набор одинаково спокойно смотрится и на тёмном,
// и на светлом фоне терминала.
var rgbNight = rgbTheme{
	Accent:  [3]int{217, 119, 87},  // коралл Claude - единственный акцент
	Accent2: [3]int{150, 150, 150}, // нейтральный серый (вторичные бейджи)
	OK:      [3]int{104, 160, 99},  // спокойный зелёный
	Warn:    [3]int{210, 160, 61},  // янтарь
	Err:     [3]int{205, 92, 88},   // спокойный красный
	Info:    [3]int{107, 155, 201}, // мягкое голубое
	Muted:   [3]int{136, 136, 136}, // нейтральный серый - весь хром
	Head:    [3]int{255, 255, 255}, // не используется: Head рисуется жирным
	Code:    [3]int{156, 156, 156}, // нейтральный серый (инлайн-код)
	Link:    [3]int{107, 155, 201},
	Added:   [3]int{104, 160, 99},
	Removed: [3]int{205, 92, 88},
	Hunk:    [3]int{210, 160, 61},
}

// ThemeEmber - палитра для 16-цветного терминала.
//
// Акцент - белый: ярко-жёлтый был «вырвиглазным» на классических консолях.
// Заголовок - просто жирный, инлайн-код - серым, ошибки остаются красными.
var ThemeEmber = Palette{
	Accent: cWhite, Accent2: cWhite, OK: cGreen, Warn: cYellow, Err: cRed,
	Info: cBlue, Muted: cGray, Text: "", Head: cBold, Code: cGray,
	Link: cBlue, Added: cGreen, Removed: cRed, Hunk: cYellow,
}

// ---------- Примитивы ----------

// ccMark - маркер события «*». Цвет приходит от семантики: вызов
// инструмента — акцент, успех — зелёный, ошибка — красный.
func (u *UI) ccMark(color string) string {
	return u.c(color, u.g.Dot)
}

// emberMark - историческое имя маркера события.
func (u *UI) emberMark(color string) string { return u.ccMark(color) }

// emberTickGlyph - «зубец» результата. Пусто: зубец — лишний символ,
// результат читается по отступу под вызовом.
const emberTickGlyph = ""

// emberTickOf - «зубец» для текущего терминала. Всегда пуст: результат
// инструмента печатается с отступом, без декоративного символа.
func (u *UI) emberTickOf() string {
	return ""
}

// emberTail - продолжение строки события. Пусто (см. emberTickOf).
func (u *UI) emberTail() string { return "" }

// emberRule - тонкая линия-разделитель.
func (u *UI) emberRule(width int) string {
	if width < 1 {
		width = 1
	}
	return u.c(u.pal.Muted, strings.Repeat(u.g.RuleChar, width))
}

// emberRuleTail - хвост заголовка: линия впритык к правому краю.
func (u *UI) emberRuleTail(width int) string {
	if width < 1 {
		return ""
	}
	return u.c(u.pal.Muted, " "+strings.Repeat(u.g.RuleChar, width-1))
}

// emberStatusGlyph - иконка статуса результата.
func (u *UI) emberStatusGlyph(status string) (string, string) {
	switch status {
	case "fail", "error":
		return u.g.Cross, u.pal.Err
	case "denied":
		return "-", u.pal.Warn
	case "skip":
		return "-", u.pal.Muted
	default:
		return u.g.Check, u.pal.OK
	}
}

// ---------- Размышления ----------

// ThinkMarker - напечатать маркер открытия потока размышлений и погасить
// строку ожидания.
//
// Отдельный метод, потому что маркер нужен в трёх местах: перед первым
// куском вслепую, по Ctrl+O (поток открывается посреди хода) и при
// разовом показе накопленного.
func (u *UI) ThinkMarker() {
	u.SpinnerStop()
	u.think = true
	if sink := u.sink(); sink != nil {
		sink.BeginThink()
		return
	}
	u.Println("    " + u.Italic(u.c(u.pal.Muted, "thinking")))
}

// ThinkChunk - принять кусок размышлений и напечатать его в потоке.
//
// Поток открывает не ThinkChunk, а ThinkMarker: иначе первый кусок, пришедший
// после Ctrl+O посреди хода, начинался бы без заголовка и выглядел бы как
// обычный ответ модели. Если маркера не было - печатаем его здесь, чтобы
// поток не потерялся.
//
// Печать приглушённая - размышления фон хода, и обычным цветом они
// перетягивали бы внимание с ответа модели.
func (u *UI) ThinkChunk(s string) {
	if s == "" {
		return
	}
	if sink := u.sink(); sink != nil {
		sink.ThinkText(s)
		return
	}
	if !u.think {
		u.ThinkMarker()
	}
	// Стрим размышлений печатается построчно: живая область снизу требует,
	// чтобы каждая печать заканчивалась переводом строки (иначе курсор
	// остаётся внутри региона и перерисовка ломается).
	u.thinkPend += s
	for {
		i := strings.IndexByte(u.thinkPend, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(u.thinkPend[:i], "\r")
		u.thinkPend = u.thinkPend[i+1:]
		if line != "" {
			u.Print(u.c(u.pal.Muted, line))
		}
		u.Print("\n")
	}
}

// ThinkEnd - закрыть поток размышлений.
//
// Перевод строки обязателен: иначе ответ модели допишется в хвост мысли.
// Если размышлений так и не было, перевод не нужен - ничего не нарисовано.
func (u *UI) ThinkEnd() {
	if sink := u.sink(); sink != nil {
		sink.EndThink()
		u.think = false
		return
	}
	if u.thinkPend != "" {
		line := strings.TrimSuffix(strings.TrimRight(u.thinkPend, "\r"), "\n")
		if strings.TrimSpace(line) != "" {
			u.Print(u.c(u.pal.Muted, line))
		}
		u.thinkPend = ""
	}
	u.Print("\n")
	if u.think {
		u.SpinnerStop()
		u.think = false
	}
}

// ThinkHidden - размышления скрыты и показаны разово, одним блоком.
//
// Это режим по умолчанию: пока модель думает, на экране висит только строка
// ожидания, а текст мыслей копится в буфере приложения. Пользователь
// открывает его сам, нажатием Ctrl+O.
func (u *UI) ThinkHidden(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if sink := u.sink(); sink != nil {
		sink.BeginThink()
		sink.ThinkText(text)
		sink.EndThink()
		return
	}
	if !u.think {
		u.ThinkMarker()
	}
	// Весь блок одной строкой: размышления - это поток мысли, у него нет
	// абзацной структуры, и перенос по словам рвёт фразы на куски.
	for _, l := range wrap(core.OneLine(text), u.Width()-6) {
		u.Println(u.c(u.pal.Muted, l))
	}
}
