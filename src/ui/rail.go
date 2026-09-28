package ui

import (
	"fmt"
	"strings"
	"time"

	"gcli/core"
)

// ---------- Рельс вызовов инструментов ----------
//
// Идея: каждый вызов инструмента печатается как событие с ромбом, а
// результат — строкой ниже, с «зубцом». Глаз держит структуру дерева
// вызовов и не путает, к какому вызову относится результат:
//
//      ◆ read_file  src/ui/ui.go
//        ⎿ прочитано 481 строка · 3ms
//
// Пока инструмент работает, под карточкой висит строка кота
// «( >.< ) working...» — живой статус вместо мёртвой тишины.

// RailStart — заголовок карточки инструмента с рельсом.
func (u *UI) RailStart(tc ToolCall) {
	kind := tc.Kind
	if kind == "" {
		kind = toolKind(tc.Name)
	}
	_, color := u.kindGlyph(kind)
	name := u.Head(tc.Name)
	if kind == "agent" {
		name = u.c(color, tc.Name)
	}
	//  ◆ read_file  src/ui/ui.go
	//    ⎿ 481 строка · 3ms
	//
	// Имя и аргументы разведены пробелами, а не скобками: так строка
	// остаётся плоской, и длинный путь читается как отдельное поле.
	// Имя всегда жирное — вызов это главное событие хода. Вложенные
	// вызовы (субагенты) получают вертикаль рельса в отступе.
	args := core.OneLine(tc.Args)
	indent := u.railIndent()
	used := visibleWidth(indent) + 1 + 1 + runeLen(tc.Name) + 2
	if maxArgs := u.Width() - used; args != "" && maxArgs > 14 {
		args = truncate(args, maxArgs)
	} else {
		args = ""
	}
	u.SpinnerStop()
	u.Println(indent + u.emberMark(color) + " " + name + u.Gray("  "+args))

	// Кот работает: пока идёт инструмент, под карточкой переливается
	// «( >.< ) working...». Результат (RailEnd) погасит строку сам.
	if u.mascot {
		u.ShimmerStart("working...", MascotWork)
	}
}

// RailEnd — строка результата под вызовом инструмента.
func (u *UI) RailEnd(tc ToolCall) {
	text := core.OneLine(tc.Detail)
	if text == "" {
		text = "готово"
	}
	//  ◆ read_file  src/ui/ui.go
	//    ⎿ 481 строка · 3ms
	//
	// «Зубец» прижат к тому же левому полю, что и маркер, и всегда приглушён:
	// результат — фон события, а не второе событие. Иконка статуса есть только
	// когда есть что сказать: у успешного чтения она не нужна, лишний знак
	// перед текстом только шумит.
	indent := u.railIndent()
	meta := []string{}
	if tc.Lines > 0 {
		meta = append(meta, fmt.Sprintf("%d стр.", tc.Lines))
	}
	if tc.Elapsed > 0 {
		meta = append(meta, core.HumanDuration(tc.Elapsed))
	}
	// Иконку статуса показываем только для неуспеха.
	mark := ""
	if tc.Status == "fail" || tc.Status == "error" || tc.Status == "denied" {
		g, c := u.emberStatusGlyph(tc.Status)
		mark = u.c(c, g) + " "
	}
	used := visibleWidth(indent) + runeLen(u.g.Tick) + 2 + visibleWidth(mark)
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
	u.RevealLine(indent + u.emberTail() + " " + mark +
		u.Gray(truncate(text, avail)) + tail)
}

// RailEndChain — закрыть рельс после серии вызовов (последний зубец).
func (u *UI) RailEndChain(note string) {
	if note == "" {
		note = "готово"
	}
	u.SpinnerStop()
	u.Println(u.railIndent() + u.emberTail() + " " + u.Gray(core.OneLine(note)))
}

// railOn — вертикаль вложенного рельса, railGap — шаг отступа.
const (
	railOn  = "│"
	railGap = " "
)

// railIndent — отступ под текущий уровень рельса.
func (u *UI) railIndent() string {
	// Отступ базовый — две колонки, чтобы рельс не лип к краю.
	base := railGap + railGap
	extra := ""
	for i := 0; i < u.railDepth; i++ {
		extra += u.c(u.pal.Muted, railOn) + railGap
	}
	return base + extra
}

// RailPush — войти на уровень глубже (для вложенных вызовов, например субагентов).
func (u *UI) RailPush() { u.railDepth++ }

// RailPop — выйти на уровень выше.
func (u *UI) RailPop() {
	if u.railDepth > 0 {
		u.railDepth--
	}
}

// statusGlyph — иконка и цвет статуса результата.
func (u *UI) statusGlyph(status string) (string, string) {
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

// ThinkNote — блок приватных размышлений агента одним куском:
//
//	◆ размышления
//	  текст
//
// Ромб вместо «── размышления ──»: такая черта выглядит заголовком
// блока кода, а размышления — это событие, а не раздел.
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

// ElapsedColor — цвет для времени выполнения (быстрые — зелёные, долгие — жёлтые).
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
