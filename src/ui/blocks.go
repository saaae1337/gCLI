package ui

import (
	"strings"
	"time"
)

// ---------- Блоки ----------

// BlockOpts — параметры блока.
type BlockOpts struct {
	Title    string
	Subtitle string
	Accent   bool // выделить заголовок акцентным цветом
	Footer   string
	NoPad    bool
}

// Block — блок в стиле «УГОЛЬ»: ромб-маркер и линия вправо, без рамки.
//
//	◆ Заголовок ─────────────────
//	  тело
//	  ...
//
// Форма заимствована у Claude Code, но с поправкой на собственный примитив:
// линия не рамка (тело не «заперто» в прямоугольник) и не стержень (нет
// вертикали, которая превращалась бы в рельс вызовов). Зато по заголовку
// сразу видно, где начинается самостоятельный блок.
func (u *UI) Block(content string, o BlockOpts) {
	var b strings.Builder

	// Маркер у заголовка один — ромб темы. Отдельная иконка рядом с ним
	// удваивала символ («◆ ◆ Навыки») и читалась как баг.
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

// contentLines — разбить содержимое на строки нужной ширины.
func (u *UI) contentLines(content string, width int) []string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, l := range strings.Split(content, "\n") {
		if l == "" {
			out = append(out, "")
			continue
		}
		if runeLen(l) <= width {
			out = append(out, l)
			continue
		}
		// Длинная строка (например, вывод команды) — режем аккуратно.
		r := []rune(l)
		for len(r) > width {
			cut := width
			// Стараемся резать по пробелу.
			for cut > width/2 && r[cut-1] != ' ' {
				cut--
			}
			if cut <= width/2 {
				cut = width
			}
			out = append(out, string(r[:cut]))
			r = r[cut:]
			for len(r) > 0 && r[0] == ' ' {
				r = r[1:]
			}
		}
		out = append(out, string(r))
	}
	return out
}

// emit — запись в терминал. Обязана идти через Write, а не напрямую в
// u.out: пока активна строка ожидания, Write гасит её перед печатью.
// Прямая запись в u.out оставила бы «thinking...» поверх блока.
func (u *UI) emit(s string) { u.Write(s) }

// ---------- Таблицы ----------

// Column — столбец таблицы.
type Column struct {
	Title string
	Width int  // 0 —auto
	Right bool // выравнивание вправо
}

// Table — таблица в стиле «УГОЛЬ»: колонки разделяет точка,
// как в Claude Code. Сплошной вертикальный разделитель здесь только шумит:
// данные и так читаются по ширине столбцов.
func (u *UI) Table(cols []Column, rows [][]string, o BlockOpts) {
	if len(cols) == 0 {
		return
	}
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
	// Один маркер на заголовок — как в Block: без дополнительной иконки.
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

// tableWidths — автоширина столбцов: максимум из заголовка и ячеек.
func tableWidths(cols []Column, rows [][]string) []int {
	out := make([]int, len(cols))
	for i := range cols {
		if cols[i].Width > 0 {
			out[i] = cols[i].Width
			continue
		}
		w := runeLen(cols[i].Title)
		for _, r := range rows {
			if i < len(r) && runeLen(r[i]) > w {
				w = runeLen(r[i])
			}
		}
		out[i] = w
	}
	return out
}

// ---------- Карточки инструментов ----------

// ToolCall — описание вызова инструмента для отрисовки.
type ToolCall struct {
	Name    string
	Args    string
	Kind    string // read | write | exec | net | plan | think | agent | ext
	Detail  string
	Status  string // running | ok | fail | denied
	Elapsed time.Duration
	Lines   int
}

// toolKind — определить категорию инструмента по имени.
func toolKind(name string) string {
	switch name {
	case "read_file", "list_dir", "glob", "grep":
		return "read"
	case "write_file", "edit_file":
		return "write"
	case "bash", "task_note":
		return "exec"
	case "web_search", "web_fetch":
		return "net"
	case "todo_write":
		return "plan"
	case "think":
		return "think"
	case "spawn_agent", "agent_status":
		return "agent"
	case "screenshot", "read_image":
		return "read"
	}
	if strings.HasPrefix(name, "mcp__") {
		return "mcp"
	}
	if strings.HasPrefix(name, "x_") {
		return "ext"
	}
	return "exec"
}

// kindGlyph — иконка по категории.
func (u *UI) kindGlyph(kind string) (glyph, color string) {
	switch kind {
	case "read":
		return "◧", u.pal.Info
	case "write":
		return "✎", u.pal.Warn
	case "exec":
		return "⚡", u.pal.Accent
	case "net":
		return "⇩", u.pal.Accent2
	case "plan":
		return "☑", u.pal.OK
	case "think":
		return "◈", u.pal.Muted
	case "agent":
		return "◆", u.pal.Accent2
	case "mcp":
		return "⬡", u.pal.Info
	default:
		return "◆", u.pal.Accent
	}
}

// ToolStart — заголовок карточки инструмента (печатается до выполнения).
func (u *UI) ToolStart(tc ToolCall) {
	u.RailStart(tc)
}

// ToolEnd — результат инструмента под карточкой.
func (u *UI) ToolEnd(tc ToolCall) {
	u.RailEnd(tc)
}

// ---------- Дифф ----------

// ShowDiff — цветной дифф перед подтверждением записи.
func (u *UI) ShowDiff(oldS, newS string, o DiffOpts) {
	u.PrintDiff(Diff(oldS, newS), o)
}
