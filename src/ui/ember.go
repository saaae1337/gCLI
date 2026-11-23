package ui

import (
	"strings"

	"gcli/core"
)

// ---------- Стиль «Claude Code» ----------
//
// Визуальный язык — терминальный интерфейс Claude Code:
//
//  1. Разметка. Событие начинается маркером «⏺», результат — строкой ниже
//     с зубцом «⎿»:
//
//      ⏺ read_file src/ui/ui.go
//        ⎿ 481 строка · 3ms
//
//  2. Почти весь текст — обычным цветом терминала; хром (рамки, подсказки,
//     метрики) — приглушённым серым.
//
//  3. Единственный акцент — фирменный коралловый (#D97757): спиннер «✻»,
//     маркер «⏺», приглашение. Акцент не занимает слот «красный»: ошибки
//     остаются красными рядом с git diff и ls.
//
//  4. Палитра тёплая и сдержанная: никакого сине-фиолетового неона.

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

// Тёплая сдержанная палитра на тёмном фоне. Значения подобраны так, чтобы
// белый текст держал контраст, а цвета не «светились».
var rgbNight = rgbTheme{
	Accent:  [3]int{217, 119, 87},  // коралл Claude - единственный акцент
	Accent2: [3]int{194, 162, 101}, // песочный - вторичный (режимы, диффы)
	OK:      [3]int{104, 160, 99},  // спокойный зелёный
	Warn:    [3]int{210, 160, 61},  // янтарь
	Err:     [3]int{205, 92, 88},   // спокойный красный
	Info:    [3]int{107, 155, 201}, // мягкое голубое
	Muted:   [3]int{138, 132, 124}, // тёплый серый - весь хром
	Head:    [3]int{232, 228, 221}, // тёплый белый
	Code:    [3]int{214, 197, 160}, // песочный светлый (код)
	Link:    [3]int{107, 155, 201},
	Added:   [3]int{104, 160, 99},
	Removed: [3]int{205, 92, 88},
	Hunk:    [3]int{194, 162, 101},
}

// ThemeEmber - палитра для 16-цветного терминала.
//
// Как и в truecolor-варианте, акцент не занимает слот «красный»: коралл
// на 16 цветах ближе всего к жёлтому, ошибки остаются красными.
var ThemeEmber = Palette{
	Accent: cYellow, Accent2: cBYellow, OK: cGreen, Warn: cYellow, Err: cRed,
	Info: cBlue, Muted: cGray, Text: "", Head: cBold, Code: cWhite,
	Link: cBlue, Added: cGreen, Removed: cRed, Hunk: cYellow,
}

// ---------- Примитивы ----------

// ccMark - маркер события «⏺». Цвет приходит от семантики: вызов
// инструмента — акцент, успех — зелёный, ошибка — красный.
func (u *UI) ccMark(color string) string {
	return u.c(color, u.g.Dot)
}

// emberMark - историческое имя маркера события.
func (u *UI) emberMark(color string) string { return u.ccMark(color) }

// emberTickGlyph - «зубец» результата «⎿». Это запасной вариант для
// наборов, у которых Glyphs.Tick пуст.
const emberTickGlyph = "⎿"

// emberTickOf - «зубец» для текущего терминала.
//
// Символ берётся из набора псевдографики, а не пишется прямо здесь:
// в ASCII-режиме (-ascii, GCLI_ASCII) юникод-глиф печатать нельзя, и без
// подстановки терминал рисовал бы «кракозябру» на месте каждого результата.
func (u *UI) emberTickOf() string {
	if u.g.Tick != "" {
		return u.g.Tick
	}
	if u.g.Unicode {
		return emberTickGlyph
	}
	return ":-"
}

// emberTail - продолжение строки события: зубец без цвета.
func (u *UI) emberTail() string { return u.c(u.pal.Muted, u.emberTickOf()) }

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
	u.Println("  " + u.emberTail() + " " + u.Italic(u.c(u.pal.Muted, "thinking")))
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
