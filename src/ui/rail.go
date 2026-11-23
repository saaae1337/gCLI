package ui

import (
	"fmt"
	"strings"
	"time"

	"gcli/core"
)

// ---------- Вызовы инструментов ----------
//
// Разметка Claude Code: вызов печатается маркером «⏺», результат - строкой
// ниже с зубцом «⎿»:
//
//	⏺ read_file(src/ui/ui.go)
//	  ⎿ 481 строка · 3ms
//
// Пока инструмент работает, живую строку ожидания ведёт общий спиннер.

// RailStart - заголовок вызова инструмента: «⏺ Name(args)».
func (u *UI) RailStart(tc ToolCall) {
	// Имя инструмента - обычным цветом терминала, аргументы - приглушённым:
	// глаз цепляется за маркер события и путь, а не за оформление.
	name := tc.Name
	if name == "" {
		name = toolKind(tc.Name)
	}
	args := core.OneLine(tc.Args)
	indent := u.railIndent()
	used := visibleWidth(indent) + 1 + cellWidth(name) + 2 + 1
	if maxArgs := u.Width() - used - 2; args != "" && maxArgs > 14 {
		args = "(" + truncate(args, maxArgs) + ")"
	} else {
		args = ""
	}
	u.SpinnerStop()
	u.Println(indent + u.emberMark(u.pal.Accent) + " " + name + u.Gray(args))
}

// RailEnd - строка результата под вызовом: «⎿ 481 строка · 3ms».
func (u *UI) RailEnd(tc ToolCall) {
	text := core.OneLine(tc.Detail)
	if text == "" {
		text = "готово"
	}
	// «Зубец» прижат к тому же левому полю, что и маркер, и всегда
	// приглушён: результат - фон события, а не второе событие. Цвет текста
	// говорит о статусе: успех - приглушённый, ошибка - красный, отказ -
	// янтарный.
	color := u.pal.Muted
	switch tc.Status {
	case "fail", "error":
		color = u.pal.Err
	case "denied":
		color = u.pal.Warn
	}
	meta := []string{}
	if tc.Lines > 0 {
		meta = append(meta, fmt.Sprintf("%d стр.", tc.Lines))
	}
	if tc.Elapsed > 0 {
		meta = append(meta, core.HumanDuration(tc.Elapsed))
	}
	indent := u.railIndent()
	used := visibleWidth(indent) + visibleWidth(u.g.Tick) + 2
	tail := ""
	if len(meta) > 0 {
		tail = u.c(u.pal.Muted, " · "+strings.Join(meta, " · "))
	}
	avail := u.Width() - used - visibleWidth(tail)
	if avail < 8 {
		tail = ""
		avail = u.Width() - used
	}
	if avail < 8 {
		avail = 8
	}
	u.RevealLine(indent + u.emberTail() + " " + u.c(color, truncate(text, avail)) + tail)
}

// RailEndChain - закрыть серию вызовов (последний зубец).
func (u *UI) RailEndChain(note string) {
	if note == "" {
		note = "готово"
	}
	u.SpinnerStop()
	u.Println("  " + u.emberTail() + " " + u.Gray(core.OneLine(note)))
}

// railOn - вертикаль вложенного рельса, railGap - шаг отступа.
const (
	railOn  = "|"
	railGap = " "
)

// railIndent - отступ под текущий уровень рельса.
func (u *UI) railIndent() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.railIndentLocked()
}

// railIndentLocked - railIndent для вызова под уже взятым u.mu.
func (u *UI) railIndentLocked() string {
	// Отступ базовый - две колонки, чтобы рельс не лип к краю.
	base := railGap + railGap
	extra := ""
	for i := 0; i < u.railDepth; i++ {
		extra += u.c(u.pal.Muted, railOn) + railGap
	}
	return base + extra
}

// RailPush - войти на уровень глубже (для вложенных вызовов, например субагентов).
func (u *UI) RailPush() {
	u.mu.Lock()
	u.railDepth++
	u.mu.Unlock()
}

// RailPop - выйти на уровень выше.
func (u *UI) RailPop() {
	u.mu.Lock()
	if u.railDepth > 0 {
		u.railDepth--
	}
	u.mu.Unlock()
}

// ThinkNote - блок приватных размышлений агента одним куском.
func (u *UI) ThinkNote(text string) {
	u.SpinnerStop()
	for i, l := range wrap(core.OneLine(text), u.Width()-6) {
		mark := u.emberTail()
		if i == 0 {
			mark = u.emberMark(u.pal.Muted)
		}
		u.Println("  " + mark + " " + u.Italic(u.c(u.pal.Muted, l)))
	}
}

// ElapsedColor - цвет для времени выполнения (быстрые - зелёные, долгие - жёлтые).
func (u *UI) ElapsedColor(d time.Duration) string {
	switch {
	case d < 500*time.Millisecond:
		return u.pal.OK
	case d < 3*time.Second:
		return u.pal.Warn
	default:
		return u.pal.Err
	}
}
