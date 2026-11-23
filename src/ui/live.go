package ui

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"gcli/core"
)

// ---------- Нижняя живая область ----------
//
// Интерфейс Claude Code живёт в обычной прокрутке терминала: ответы и
// результаты уходят в скроллбэк, а внизу экрана постоянно нарисована
// «живая область» — поле ввода в скруглённой рамке, под ним строка
// подсказок, над ним (во время работы агента) строка ожидания и pop-up
// меню команд. Регион перерисовывается целиком при каждой печати: это
// дёшево (несколько строк) и не зависит от умения терминала что-то
// помнить.
//
// Курсор к началу региона возвращается счётом строк: каждая печать в
// прокрутку идёт через put(), который сначала стирает регион, потом
// печатает контент (по одной строке на строку терминала), потом рисует
// регион заново. Курсор всегда остаётся на последней строке региона.

// MenuItem - элемент pop-up меню команд.
type MenuItem struct {
	Name string // "/help"
	Desc string // подпись справа
}

// CCVerbs - глаголы строки ожидания, как в Claude Code: осмысленное
// «процессное» слово вместо безликого «думаю...».
var ccVerbs = []string{
	"Thinking", "Pondering", "Musing", "Brewing", "Simmering", "Infusing",
	"Conjuring", "Forging", "Baking", "Mulling", "Scheming", "Percolating",
	"Crystallizing", "Reticulating", "Meandering", "Noodling",
}

// ccVerb - случайный глагол ожидания.
func ccVerb() string { return ccVerbs[rand.Intn(len(ccVerbs))] }

// liveMenuVisible - сколько пунктов меню видно одновременно.
const liveMenuVisible = 6

// ---------- Публичное API ----------

// LiveStart - включить живую область и нарисовать пустой регион.
func (u *UI) LiveStart(placeholder string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.liveOn = true
	u.livePlaceholder = placeholder
	u.liveMenu = nil
	u.liveMenuSel = 0
	u.flushLiveLocked()
}

// LiveStop - выключить живую область и стереть её с экрана.
func (u *UI) LiveStop() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.eraseLiveLocked()
	u.liveOn = false
	u.liveRows = 0
}

// LiveActive - включена ли живая область.
func (u *UI) LiveActive() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.liveOn
}

// LiveInput - новый текст поля ввода и позиция курсора (в рунах).
func (u *UI) LiveInput(text string, cur int) {
	u.mu.Lock()
	u.liveInp = text
	u.liveCur = cur
	u.flushLiveLocked()
	u.mu.Unlock()
}

// LivePlaceholder - подсказка в пустом поле ввода.
func (u *UI) LivePlaceholder(text string) {
	u.mu.Lock()
	u.livePlaceholder = text
	u.flushLiveLocked()
	u.mu.Unlock()
}

// LiveMenu - пункты pop-up меню (nil/пусто = закрыть меню).
func (u *UI) LiveMenu(items []MenuItem, sel int) {
	u.mu.Lock()
	u.liveMenu = items
	u.liveMenuSel = sel
	u.flushLiveLocked()
	u.mu.Unlock()
}

// LiveFooter - строка подсказок под полем: слева и справа.
func (u *UI) LiveFooter(left, right string) {
	u.mu.Lock()
	u.liveFooterL = left
	u.liveFooterR = right
	u.flushLiveLocked()
	u.mu.Unlock()
}

// ---------- Служебное ----------

// liveWidth - рабочая ширина региона.
func (u *UI) liveWidth() int {
	w := u.Width()
	if w < 24 {
		w = 24
	}
	if w > 200 {
		w = 200
	}
	return w
}

// eraseLiveLocked - стереть регион с экрана. Курсор обязан стоять на
// последней строке региона (так его оставляет drawLiveLocked).
func (u *UI) eraseLiveLocked() {
	if u.liveRows <= 0 {
		return
	}
	up := ""
	if u.liveRows > 1 {
		up = fmt.Sprintf("\x1b[%dA", u.liveRows-1)
	}
	_, _ = u.out.Write([]byte(up + "\r\x1b[J"))
	u.liveRows = 0
}

// flushLiveLocked - перерисовать регион: стереть старый, нарисовать новый.
func (u *UI) flushLiveLocked() {
	if !u.liveOn {
		return
	}
	u.eraseLiveLocked()
	u.drawLiveLocked()
}

// drawLiveLocked - нарисовать регион и посчитать его высоту.
func (u *UI) drawLiveLocked() {
	if !u.liveOn {
		return
	}
	w := u.liveWidth()
	var rows []string

	// 1. Pop-up меню команд - над полем ввода.
	rows = append(rows, u.liveMenuRows(w)...)

	// 2. Строка ожидания - над полем, когда агент занят.
	if u.liveBusy != "" {
		rows = append(rows, u.liveBusyLine())
	}

	// 3. Поле ввода в скруглённой рамке.
	rows = append(rows, u.liveBoxRows(w)...)

	// 4. Подсказки под полем.
	rows = append(rows, u.liveFooterLine(w))

	// Каждая строка - ровно одна строка терминала: с возвратом каретки,
	// очисткой хвоста и переводом строки. Последняя строка - без перевода:
	// курсор остаётся в конце региона, откуда его возьмёт eraseLiveLocked.
	var b strings.Builder
	for i, row := range rows {
		b.WriteString("\r\x1b[K")
		b.WriteString(row)
		if i < len(rows)-1 {
			b.WriteString("\n")
		}
	}
	_, _ = u.out.Write([]byte(b.String()))
	u.liveRows = len(rows)
}

// liveMenuRows - pop-up меню в скруглённой рамке: выбор стрелками,
// Enter или Tab завершают ввод команды.
func (u *UI) liveMenuRows(w int) []string {
	if len(u.liveMenu) == 0 {
		return nil
	}
	// Окно видимости: скользит за выбором, если пунктов больше шести.
	items := u.liveMenu
	sel := u.liveMenuSel
	if sel < 0 {
		sel = 0
	}
	if sel >= len(items) {
		sel = len(items) - 1
	}
	lo := sel - liveMenuVisible + 1
	if lo < 0 {
		lo = 0
	}
	if len(items)-lo < liveMenuVisible {
		lo = len(items) - liveMenuVisible
	}
	if lo < 0 {
		lo = 0
	}
	win := items[lo:]
	if len(win) > liveMenuVisible {
		win = win[:liveMenuVisible]
	}

	// Ширина меню: имя + подпись + воздух, но не шире экрана.
	inner := 0
	for _, it := range win {
		n := cellWidth(it.Name) + 2 + cellWidth(it.Desc)
		if n > inner {
			inner = n
		}
	}
	inner += 4 // "❯ " слева и запас справа
	if max := w - 6; inner > max {
		inner = max
	}
	if inner < 16 {
		inner = 16
	}

	bar := strings.Repeat(u.g.H, inner)
	var rows []string
	rows = append(rows, " "+u.c(u.pal.Muted, u.g.TL+bar+u.g.TR))
	for i, it := range win {
		global := lo + i
		cursor := u.c(u.pal.Accent, "❯ ")
		if global != sel {
			cursor = "  "
		}
		name := it.Name
		if global == sel {
			name = u.c(u.pal.Accent, name)
		} else {
			name = u.c(u.pal.Muted, name)
		}
		desc := u.c(u.pal.Muted, it.Desc)
		row := " " + u.c(u.pal.Muted, u.g.V) + " " + cursor + name
		if it.Desc != "" {
			row += "  " + desc
		}
		rows = append(rows, u.truncateRow(row, inner+4))
	}
	rows = append(rows, " "+u.c(u.pal.Muted, u.g.BL+bar+u.g.BR))
	return rows
}

// liveBusyLine - строка ожидания: «✻ Infusing… (esc to interrupt · 4s · ↓ 1.2k tok)».
func (u *UI) liveBusyLine() string {
	el := time.Since(u.spinT0).Truncate(time.Second)
	// Клавиша прерывания - как в Claude Code: «(esc to interrupt · 4s · ↓ 1.2k tok)».
	parts := []string{"esc to interrupt"}
	if u.spinHint != "" {
		parts = append(parts, u.spinHint)
	}
	parts = append(parts, el.String())
	// Пока модель пишет, видно, сколько токенов уже сошло с её пера.
	if m := u.metrics; m.Completion > 0 {
		parts = append(parts, "↓ "+core.Kfmt(m.Completion)+" tok")
	}
	if m := u.metrics; m.Context > 0 {
		parts = append(parts, "ctx "+m.ContextText())
	}
	line := u.c(u.pal.Accent, u.liveMark+" "+u.liveBusy+"…") +
		u.c(u.pal.Muted, " ("+strings.Join(parts, " · ")+")")
	return "  " + u.truncateRow(line, u.liveWidth()-2)
}

// liveBoxRows - поле ввода в скруглённой рамке: первая строка с «> »,
// курсор - контрастным блоком, длинный текст переносится внутри рамки.
func (u *UI) liveBoxRows(w int) []string {
	inner := w - 4 // между «│ » и « │»
	if inner < 8 {
		inner = 8
	}
	// Ширина строки рамки ровно w: пробел + угол + линия + угол.
	bar := strings.Repeat(u.g.H, w-3)
	rows := []string{" " + u.c(u.pal.Muted, u.g.TL+bar+u.g.TR)}

	text := u.liveInp
	if text == "" && u.livePlaceholder != "" {
		// Пустое поле: подсказка приглушённым, без курсора.
		body := "  " + u.c(u.pal.Muted, u.livePlaceholder)
		rows = append(rows, u.boxRow(body, inner))
		rows = append(rows, " "+u.c(u.pal.Muted, u.g.BL+bar+u.g.BR))
		return rows
	}

	// Промпт и курсор. Курсор - обратный блок на следующем символе,
	// как в Claude Code; в конце текста - блок на пробеле.
	cur := u.liveCur
	runes := []rune(text)
	if cur < 0 {
		cur = 0
	}
	if cur > len(runes) {
		cur = len(runes)
	}
	before := string(runes[:cur])
	atCur := " "
	after := ""
	if cur < len(runes) {
		atCur = string(runes[cur])
		after = string(runes[cur+1:])
	}
	caret := u.c(cReverse, atCur)
	if atCur == " " {
		caret = u.c(cReverse, " ")
	}

	// Первая строка - с промптом «> », продолжения - с отступом.
	first := u.c(u.pal.Accent, "> ")
	head := before
	rest := caret + after
	// Строки ввода могут содержать перевод строки (многострочный ввод).
	headLines := strings.Split(head, "\n")
	for i, hl := range headLines {
		prefix := ""
		if i == 0 {
			prefix = first
		} else {
			prefix = "  "
		}
		rows = append(rows, u.boxRow(prefix+hl, inner))
	}
	// Хвост с курсора переносится по словам на ширину строки ввода.
	// Цветная строка: перенос по колонкам с учётом ANSI. Хвоста нет -
	// дополнительной строки не рисуем (пустой contentLines даёт пустую
	// строку, которая раздувала бы бокс).
	if strings.TrimSpace(StripANSI(rest)) != "" || after != "" {
		restLines := u.contentLines(rest, inner-2)
		for _, rl := range restLines {
			rows = append(rows, u.boxRow("  "+rl, inner))
		}
	}
	rows = append(rows, " "+u.c(u.pal.Muted, u.g.BL+bar+u.g.BR))
	return rows
}

// boxRow - строка внутри рамки: «│ контент │», контент добивается
// пробелами до ширины рамки (с учётом ANSI в контенте).
func (u *UI) boxRow(content string, inner int) string {
	// Ширина строки ввода - inner минус два на внутренние отступы.
	pad := inner - 2 - visibleWidth(content)
	if pad < 0 {
		pad = 0
	}
	line := " " + u.c(u.pal.Muted, u.g.V) + "  " + content + strings.Repeat(" ", pad) +
		" " + u.c(u.pal.Muted, u.g.V)
	return line
}

// liveFooterLine - строка подсказок под полем: слева «? for shortcuts»,
// справа - модель, контекст и режимы.
func (u *UI) liveFooterLine(w int) string {
	left := u.liveFooterL
	right := u.liveFooterR
	if left != "" {
		left = "  " + u.c(u.pal.Muted, left)
	}
	if right != "" {
		right = u.c(u.pal.Muted, right)
	}
	return fitRight(left, right, w)
}

// truncateRow - обрезать строку до n колонок (учитывая ANSI).
func (u *UI) truncateRow(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if visibleWidth(s) <= n {
		return s
	}
	return truncateCells(s, n)
}

// putLiveLocked - печать контента при включённой живой области: стереть
// регион, напечатать строки (по одной на строку терминала), нарисовать
// регион заново.
func (u *UI) putLiveLocked(s string) {
	if !strings.HasSuffix(s, "\n") {
		// Весь контент печатается построчно: незавершённая строка
		// дописывается переводом, иначе курсор остался бы внутри региона.
		s += "\n"
	}
	w := u.liveWidth()
	u.eraseLiveLocked()
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	var b strings.Builder
	for _, l := range lines {
		for _, row := range u.contentLines(l, w) {
			b.WriteString("\r\x1b[K")
			b.WriteString(row)
			b.WriteString("\n")
		}
	}
	_, _ = u.out.Write([]byte(b.String()))
	u.drawLiveLocked()
}
