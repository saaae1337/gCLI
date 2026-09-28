package ui

import "strings"

// ---------- Стиль «УГОЛЬ» ----------
//
// Тема появилась после разбора оформления Claude Code. Там нашлись удачные
// решения — плоская вёрстка, событийный маркер, «зубец» результата, тёплый
// акцент, оторванный от слота ошибки — и одна неприятная мелочь: фирменный
// глиф маркера U+23FA в Linux и Windows рисуется обычной точкой.
//
// Поэтому стиль gcli берёт идею, но не копирует букву в букву:
//
//  1. Разметка — как у Claude Code, без рамок. Событие начинается с маркера,
//     результат идёт строкой ниже, с «зубцом»:
//
//      ◆ read_file  src/ui/ui.go
//        ⎿ 481 строка · 3ms
//
//  2. Маркер события — ромб «◆» вместо ⏺ (U+23FA). Ромб рисуется
//     одинаково везде и не зависит от шрифта. Плата за это: в отличие от
//     круга «⏺» он не сливается с буллетом списка.
//
//  3. Акцент — охра, и он НЕ занимает слот «красный». У Claude Code
//     фирменный цвет уехал в bright-red, а настоящая ошибка осталась
//     красной: `git diff` и `ls` в том же терминале выглядят согласованно.
//     Здесь то же: ошибка — киноварь, акцент — охра.
//
//  4. Второй акцент (индиго) не пересекается со «слотом ошибки» и на 16
//     цветах не сливается с охрой.
//
//  5. Приглушённый приём «указатель» вместо «летающих» стрелок: у каждого
//     основного цвета есть двойник послабее, и анимация живёт на этой паре.

// themeEmber — каноническое имя темы. Стиль один: ромб-маркер «◆»,
// «зубец» результата «⎿», плоская вёрстка, охра и индиго.
const themeEmber = "ember"

// normalizeTheme — свести имя темы к канону. Раньше тем было несколько
// (orbit, claude, nord, matrix, mono) и функция разбирала алиасы; после
// унификации остаётся одно правило: что бы ни стояло в старом config.json
// («theme": "orbit", "claude", «mono»…), оформление — «УГОЛЬ».
func normalizeTheme(theme string) string {
	return themeEmber
}

// ---------- Палитра ----------

// Уголь и охра. Значения подобраны так, чтобы белый текст поверх них держал
// контраст, а сами цвета не «светились» на тёмном фоне.
var rgbEmber = rgbTheme{
	Accent:  [3]int{203, 132, 58},  // охра — основной акцент
	Accent2: [3]int{140, 148, 245}, // индиго — вторичный акцент
	OK:      [3]int{126, 190, 138}, // зелёный травы
	Warn:    [3]int{226, 183, 92},  // охра светлее
	Err:     [3]int{219, 106, 110}, // киноварь
	Info:    [3]int{125, 196, 232}, // голубое
	Muted:   [3]int{122, 130, 146}, // камень
	Head:    [3]int{238, 233, 227}, // тёплый белый
	Code:    [3]int{232, 178, 118}, // охра светлее
	Link:    [3]int{125, 196, 232}, // холодный голубой
	Added:   [3]int{126, 190, 138},
	Removed: [3]int{219, 106, 110},
	Hunk:    [3]int{140, 148, 245},
}

// ThemeEmber — палитра для 16-цветного терминала.
//
// Ключевая идея, взятая по смыслу из Claude Code: фирменный цвет не должен
// занимать слот «красный». Иначе окраска события и окраска ошибки выглядят
// как одно и то же. Здесь ошибка — красный, акцент — ярко-оранжевый.
var ThemeEmber = Palette{
	Accent: cBYellow, Accent2: cBMag, OK: cGreen, Warn: cBYellow, Err: cRed,
	Info: cBBlue, Muted: cGray, Text: "", Head: cBold, Code: cBYellow,
	Link: cBBlue, Added: cGreen, Removed: cRed, Hunk: cBMag,
}

// ---------- Примитивы ----------

// emberMark — маркер события (вызов инструмента, шаг, раздел). Цвет приходит
// от категории инструмента: у каждого свой оттенок, и глаз считывает тип
// вызова не читая названия.
func (u *UI) emberMark(color string) string {
	return u.c(color, u.g.Star)
}

// emberTickGlyph — «зубец» перед результатом. Читается как отпечаток
// инструмента: вызов отмечен ромбом, след от него — «зубцом».
// Это запасной вариант для наборов, у которых Glyphs.Tick пуст.
const emberTickGlyph = "⎿"

// emberTickOf — «зубец» для текущего терминала.
//
// Символ берётся из набора псевдографики, а не пишется прямо здесь:
// в ASCII-режиме (-ascii, GCLI_ASCII) глиф «⎿» печатать нельзя, и без
// подстановки терминал рисовал бы «кракозябру» на месте каждого результата.
func (u *UI) emberTickOf() string {
	if u.g.Tick != "" {
		return u.g.Tick
	}
	if u.g.Unicode {
		return emberTickGlyph
	}
	return ">-"
}

// emberTick — «зубец» перед результатом. Приём взят из Claude Code, но здесь
// он всегда приглушён: результат — фон, а не событие.
func (u *UI) emberTick() string { return u.c(u.pal.Muted, u.emberTickOf()) }

// emberTail — продолжение строки события: маркер без цвета.
func (u *UI) emberTail() string { return u.c(u.pal.Muted, u.emberTickOf()) }

// emberRule — тонкая линия-разделитель в полосу: мягче рамки, но заметнее
// пунктира, который в этом стиле занят разделителем колонок.
func (u *UI) emberRule(width int) string {
	if width < 1 {
		width = 1
	}
	return u.c(u.pal.Muted, strings.Repeat(u.g.RuleChar, width))
}

// emberRuleTail — хвост заголовка: линия впритык к правому краю.
func (u *UI) emberRuleTail(width int) string {
	if width < 1 {
		return ""
	}
	return u.c(u.pal.Muted, " "+strings.Repeat(u.g.RuleChar, width-1))
}

// emberStatusGlyph — иконка статуса результата.
func (u *UI) emberStatusGlyph(status string) (string, string) {
	switch status {
	case "fail", "error":
		return u.g.Cross, u.pal.Err
	case "denied":
		return "⊘", u.pal.Warn
	case "skip":
		return "⊘", u.pal.Muted
	default:
		return u.g.Check, u.pal.OK
	}
}

// ---------- Размышления ----------

// ThinkStart — открыть поток приватных размышлений агента.
//
// Пока модель думает, на экране висит строка ожидания с котом:
// «( -.- ) thinking...» с цветной волной. Если маскот выключен —
// обычный «указатель» темы. Статичная строка «── размышления ──» для
// этого плоха: она выглядит заголовком блока кода и остаётся висеть,
// когда размышления уже кончились.
func (u *UI) ThinkStart() {
	if u.think {
		return
	}
	u.think = true
	if u.mascot {
		u.ShimmerStart("thinking...", MascotThink)
		return
	}
	u.SpinnerStart(u.g.Dash + " размышления")
}

// ThinkChunk — принять кусок размышлений и напечатать его в потоке.
//
// Первый кусок закрывает строку ожидания и печатает маркер события:
// дальше текст идёт сам. Печать приглушённая — размышления фон хода, и
// обычным цветом они перетягивали бы внимание с ответа модели.
func (u *UI) ThinkChunk(s string) {
	if s == "" {
		return
	}
	if u.think {
		u.SpinnerStop()
		u.think = false
		u.Println("  " + u.emberMark(u.pal.Muted) + " " + u.c(u.pal.Muted, u.g.Dash+" размышления"))
	}
	u.Print(u.c(u.pal.Muted, s))
}

// ThinkEnd — закрыть поток размышлений.
//
// Перевод строки обязателен: иначе ответ модели допишется в хвост мысли.
// Если размышлений так и не было, перевод не нужен — ничего не нарисовано.
func (u *UI) ThinkEnd() {
	if u.think {
		u.SpinnerStop()
		u.think = false
		return
	}
	u.Println("")
}

// ---------- Своё оформление блоков ----------

// emberBlock — блок в стиле «УГОЛЬ»: ромб-маркер и линия вправо, без рамки.
//
//	◆ Заголовок ─────────────────
//	  тело
//	  ...
//
// Форма заимствована у Claude Code, но с поправкой на собственный примитив:
// линия не рамка (тело не «заперто» в прямоугольник) и не стержень (нет
// вертикали, которая превращалась бы в рельс вызовов). Зато по заголовку
// сразу видно, где начинается самостоятельный блок.
func (u *UI) emberBlock(content string, o BlockOpts) {
	var b strings.Builder

	// Маркер один — ромб темы: без дополнительной иконки, которая
	// удваивала символ («◆ ◆ Навыки»).
	title := o.Title
	plain := StripANSI(title)

	// Тело смещено на четыре знакоместа: маркер, пробел и ещё пара пробелов
	// на ступень вправо. Ширина считается по той же ступени.
	inner := u.Width() - 4
	if inner < 8 {
		inner = 8
	}
	lines := u.contentLines(content, inner)

	if plain != "" {
		mark := u.emberMark(u.pal.Accent)
		head := u.Head(plain)
		if o.Accent {
			head = u.c(u.pal.Accent, plain)
		}
		used := 2 + 1 + 1 + runeLen(plain)
		avail := u.Width() - used
		switch {
		case o.Subtitle != "" && avail > runeLen(o.Subtitle)+8:
			// Подзаголовок прижат к правому краю, хвост — линией.
			fill := avail - runeLen(o.Subtitle)
			b.WriteString("  " + mark + " " + head +
				u.emberRuleTail(fill) + o.Subtitle + "\n")
		case o.Subtitle == "" && avail >= 12:
			b.WriteString("  " + mark + " " + head + " " + u.emberRule(avail-1) + "\n")
		case o.Subtitle != "":
			b.WriteString("  " + mark + " " + head + "\n")
			b.WriteString("    " + u.c(u.pal.Muted, o.Subtitle) + "\n")
		default:
			b.WriteString("  " + mark + " " + head + "\n")
		}
	}

	for _, l := range lines {
		b.WriteString("    " + l + "\n")
	}
	if o.Footer != "" {
		b.WriteString("    " + u.c(u.pal.Muted, o.Footer) + "\n")
	}
	b.WriteString("\n")
	u.emit(b.String())
}

// emberTable — таблица в стиле «УГОЛЬ»: колонки разделяет точка,
// как в Claude Code. Сплошной вертикальный разделитель здесь только шумит:
// данные и так читаются по ширине столбцов.
func (u *UI) emberTable(cols []Column, rows [][]string, o BlockOpts) {
	sizes := make([]int, len(cols))
	copy(sizes, tableWidths(cols, rows))
	squeeze := func() {
		total := 0
		for _, w := range sizes {
			total += w + 2
		}
		over := total - (u.Width() - 2)
		for over > 0 {
			widest, wi := 0, -1
			for i, w := range sizes {
				if w > 6 && w > widest {
					widest, wi = w, i
				}
			}
			if wi < 0 {
				break
			}
			sizes[wi]--
			over--
		}
	}
	squeeze()

	var out strings.Builder
	// Маркер один — ромб темы: без дополнительной иконки, которая
	// удваивала символ («◆ ◆ Навыки»).
	title := o.Title
	plain := StripANSI(title)
	if plain != "" {
		head := u.Head(plain)
		used := 2 + 1 + 1 + runeLen(plain)
		avail := u.Width() - used
		if avail >= 12 {
			head = head + " " + u.emberRule(avail-1)
		}
		out.WriteString("  " + u.emberMark(u.pal.Accent) + " " + head + "\n")
	}

	sep := " " + u.c(u.pal.Muted, u.g.Dot) + " "

	// Заголовки колонок — приглушённые, разделитель между ними — точка.
	var hb strings.Builder
	hb.WriteString("  ")
	for i := range cols {
		hb.WriteString(u.c(u.pal.Muted, padRight(StripANSI(cols[i].Title), sizes[i])))
		if i < len(cols)-1 {
			hb.WriteString(sep)
		}
	}
	out.WriteString(hb.String() + "\n")

	for _, r := range rows {
		var b strings.Builder
		b.WriteString("  ")
		for i := range cols {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			// Выравнивание учитывает ANSI: padRight/padLeft считают
			// видимую ширину без экранных последовательностей. Раньше
			// цветные ячейки не добивались пробелами, и колонки
			// разъезжались (каждая цветная ячейка сдвигала таблицу).
			if cols[i].Right {
				b.WriteString(padLeft(cell, sizes[i]))
			} else {
				b.WriteString(padRight(cell, sizes[i]))
			}
			if i < len(cols)-1 {
				b.WriteString(sep)
			}
		}
		out.WriteString(b.String() + "\n")
	}
	if o.Footer != "" {
		out.WriteString("  " + u.c(u.pal.Muted, o.Footer) + "\n")
	}
	out.WriteString("\n")
	u.emit(out.String())
}
