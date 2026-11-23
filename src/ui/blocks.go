package ui

import (
	"strings"
	"time"
)

// ---------- Блоки ----------

// BlockOpts - параметры блока.
type BlockOpts struct {
	Title    string
	Subtitle string
	Accent   bool // выделить заголовок акцентным цветом
	Footer   string
	NoPad    bool
	// Framed - нарисовать блок в скруглённой рамке (стиль OpenCode).
	// По умолчанию рамки нет: рамка вокруг каждой таблицы превращается
	// в лес рамок, и взгляд перестаёт цепляться за главное.
	Framed bool
}

// Block - блок в стиле Claude Code: жирный заголовок, тело с отступом,
// без маркеров, линеек и рамок.
//
//	Заголовок
//	  тело
//	  ...
func (u *UI) Block(content string, o BlockOpts) {
	// Рамка вокруг блока: данные отделены от потока ответа.
	if u.opts.Panels || o.Framed {
		body := content
		if o.Subtitle != "" {
			// Подзаголовок - первой строкой содержимого: в рамке нет
			// отдельной строки под заголовком, а потерять его нельзя.
			body = u.c(u.pal.Muted, o.Subtitle) + "\n" + content
		}
		u.Panel(body, PanelOpts{
			Title:  o.Title,
			Footer: o.Footer,
			Accent: o.Accent,
		})
		return
	}
	var b strings.Builder

	title := o.Title
	plain := StripANSI(title)

	// Тело смещено на четыре знакоместа: заголовок - на два.
	inner := u.Width() - 4
	if inner < 8 {
		inner = 8
	}
	lines := u.contentLines(content, inner)

	if plain != "" {
		b.WriteString("  " + u.Head(plain) + "\n")
		if o.Subtitle != "" {
			b.WriteString("    " + u.c(u.pal.Muted, o.Subtitle) + "\n")
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

// contentLines - разбить содержимое на строки нужной ширины.
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
		l = validUTF8(l)
		if cellWidth(l) <= width {
			out = append(out, l)
			continue
		}
		// Длинная строка (например, вывод команды) - режем по колонкам
		// терминала, а не по рунам: иероглиф занимает две колонки, и
		// «ширина в рунах» выводила строку за правый край блока.
		rest := l
		for cellWidth(rest) > width {
			cut := truncateCellsRaw(rest, width)
			if cut == "" {
				// Широкий кластер не помещается: берём его целиком,
				// иначе цикл не сдвинется.
				_, sz := nextCluster(rest, 0)
				if sz <= 0 || sz > len(rest) {
					sz = 1
				}
				cut = rest[:sz]
			}
			// Стараемся резать по пробелу, но не отдаём под это
			// больше половины строки. В цветной строке пробел может
			// оказаться внутри escape-последовательности - такую
			// строку режем строго по границе кластера.
			if !hasESC(cut) {
				if idx := strings.LastIndexByte(cut, ' '); idx > 0 &&
					cellWidth(cut[:idx]) >= width/2 {
					cut = cut[:idx]
				}
			}
			out = append(out, cut)
			rest = rest[len(cut):]
			for len(rest) > 0 && rest[0] == ' ' {
				rest = rest[1:]
			}
		}
		out = append(out, rest)
	}
	return out
}

// emit - запись в терминал. Обязана идти через Write, а не напрямую в
// u.out: пока активна строка ожидания, Write гасит её перед печатью.
// Прямая запись в u.out оставила бы «thinking...» поверх блока.
//
// Возврат каретки - как и в Println: блок должен начинаться с нулевой
// колонки, где бы курсор ни остался.
func (u *UI) emit(s string) { u.Write(u.lineStart() + s) }

// ---------- Таблицы ----------

// Column - столбец таблицы.
type Column struct {
	Title string
	Width int  // 0 -auto
	Right bool // выравнивание вправо
}

// Table - таблица в стиле Claude Code: приглушённые заголовки колонок,
// колонки разделяют два пробела. Никаких маркеров над таблицей и рамок:
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
	// В рамке заголовок живёт в верхней границе, а не отдельной строкой.
	framed := o.Framed || u.opts.Panels
	title := o.Title
	plain := StripANSI(title)
	if plain != "" && !framed {
		out.WriteString("  " + u.Head(plain) + "\n")
	}

	sep := "  "

	// Заголовки колонок - приглушённые.
	var hb strings.Builder
	if !framed {
		hb.WriteString("  ")
	}
	for i := range cols {
		title := truncate(StripANSI(cols[i].Title), sizes[i])
		hb.WriteString(u.c(u.pal.Muted, padRight(title, sizes[i])))
		if i < len(cols)-1 {
			hb.WriteString(sep)
		}
	}
	out.WriteString(hb.String() + "\n")

	for _, r := range rows {
		var b strings.Builder
		if !framed {
			b.WriteString("  ")
		}
		for i := range cols {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			// Явно заданная Width - это предел колонки. Без обрезки
			// длинная ячейка сдвигала разделитель и ломала таблицу.
			cell = truncate(cell, sizes[i])
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
		if framed {
			// Подпись в рамке - последней строкой перед нижней границей.
			u.Panel(strings.TrimRight(out.String(), "\n"), PanelOpts{
				Title:  o.Title,
				Footer: o.Footer,
				Accent: o.Accent,
			})
			return
		}
		out.WriteString("  " + u.c(u.pal.Muted, o.Footer) + "\n")
	}
	if framed {
		u.Panel(strings.TrimRight(out.String(), "\n"), PanelOpts{
			Title:  o.Title,
			Accent: o.Accent,
		})
		return
	}
	out.WriteString("\n")
	u.emit(out.String())
}

// tableWidths - автоширина столбцов: максимум из заголовка и ячеек.
//
// Ширина считается в колонках терминала: иначе колонка с японским текстом или
// эмодзи выходила вдвое шире отведённой и таблица разъезжалась.
func tableWidths(cols []Column, rows [][]string) []int {
	out := make([]int, len(cols))
	for i := range cols {
		if cols[i].Width > 0 {
			out[i] = cols[i].Width
			continue
		}
		w := cellWidth(cols[i].Title)
		for _, r := range rows {
			if i < len(r) && cellWidth(r[i]) > w {
				w = cellWidth(r[i])
			}
		}
		out[i] = w
	}
	return out
}

// ---------- Карточки инструментов ----------

// ToolCall - описание вызова инструмента для отрисовки.
type ToolCall struct {
	// ID - идентификатор вызова от модели (для параллельных вызовов).
	ID      string
	Name    string
	Args    string
	Kind    string // read | write | exec | net | plan | think | agent | ext
	Detail  string
	Status  string // running | ok | fail | denied
	Elapsed time.Duration
	Lines   int
}

// toolKind - определить категорию инструмента по имени.
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

// ToolStart - заголовок вызова инструмента (печатается до выполнения).
func (u *UI) ToolStart(tc ToolCall) {
	if sink := u.sink(); sink != nil {
		sink.ToolStart(tc)
		return
	}
	u.RailStart(tc)
}

// ToolEnd - результат инструмента под карточкой.
func (u *UI) ToolEnd(tc ToolCall) {
	if sink := u.sink(); sink != nil {
		sink.ToolEnd(tc)
		return
	}
	u.RailEnd(tc)
}

// ---------- Дифф ----------

// ShowDiff - цветной дифф перед подтверждением записи.
func (u *UI) ShowDiff(oldS, newS string, o DiffOpts) {
	u.PrintDiff(Diff(oldS, newS), o)
}
