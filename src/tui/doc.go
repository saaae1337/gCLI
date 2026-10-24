package tui

import (
        "fmt"
        "strings"
        "time"
        "unicode/utf8"

        "gcli/wcwidth"
)

// ---------- Лента диалога ----------
//
// В отличие от потока строк в обычном терминале, здесь у каждого события
// есть вид: запрос, ответ, размышления, инструмент. Лента перерисовывается
// целиком (только видимая часть), поэтому ширина меняется без потерь, а
// карточки инструментов обновляются на месте.

type blockKind int

const (
        blockText blockKind = iota
        blockUser
        blockAssistant
        blockThink
        blockTool
        blockRule
)

// ToolInfo — состояние вызова инструмента.
type ToolInfo struct {
        // ID — идентификатор вызова от модели. Нужен, чтобы параллельные
        // вызовы одного инструмента не перепутали карточки при завершении.
        ID      string
        Name    string
        Args    string
        Kind    string
        Detail  string
        Status  string // running | ok | fail | denied
        Elapsed time.Duration
        Lines   int
}

type block struct {
        kind blockKind
        // lines — строки исходного текста (с ANSI) для текстовых блоков.
        lines []string
        // tool — данные карточки.
        tool ToolInfo
        // meta — подпись под ответом (время, токены).
        meta string
        // collapsed — размышления свёрнуты в одну строку.
        collapsed bool
        // done — блок закрыт (ответ кончился, инструмент отработал).
        done bool
}

// Doc — лента блоков.
type Doc struct {
        blocks  []*block
        pending string
        cur     *block // куда дописывается обычный текст
        stream  *block // открытый ответ модели
        think   *block // открытый поток размышлений
        // armed — ответ начался, но текста ещё не было. Блок создаётся при
        // первой строке: иначе ход из одних инструментов оставлял бы пустую
        // карточку ответа, а размышления вставали бы после неё.
        armed   bool
        version int
}

// Version — номер версии ленты (для кэша раскладки).
func (d *Doc) Version() int { return d.version }

func (d *Doc) touch() { d.version++ }

// Clear — очистить ленту.
func (d *Doc) Clear() {
        d.blocks = nil
        d.pending = ""
        d.cur = nil
        d.stream = nil
        d.think = nil
        d.armed = false
        d.touch()
}

// Empty — лента пуста.
func (d *Doc) Empty() bool { return len(d.blocks) == 0 && d.pending == "" }

// LastAssistant — последний ответ модели (для «скопировать»).
func (d *Doc) LastAssistant() string {
        for i := len(d.blocks) - 1; i >= 0; i-- {
                if d.blocks[i].kind == blockAssistant {
                        return strings.Join(d.blocks[i].lines, "\n")
                }
        }
        return ""
}

// AddUser — запрос пользователя.
func (d *Doc) AddUser(text string) {
        if d.stream != nil {
                d.stream.done = true
        }
        d.stream = nil
        d.armed = false
        b := &block{kind: blockUser, lines: []string{sanitizeBlock(text)}}
        d.blocks = append(d.blocks, b)
        d.cur = nil
        d.touch()
}

// AddNotice — системная строка (команда, предупреждение).
func (d *Doc) AddNotice(text string) {
        d.Text(text + "\n")
}

// Text — добавить обычный текст (вывод команд, сообщения).
func (d *Doc) Text(s string) {
        if s == "" {
                return
        }
        d.feed(s)
}

// feed — разобрать кусок текста на строки.
//
// Символ возврата каретки — не перевод строки: старые программы
// перерисовывают им прогресс-бар, поэтому всё, что было в текущей строке
// до CR, затирается (как на настоящем терминале). Иначе прогресс
// превратился бы в сотню строк мусора.
func (d *Doc) feed(s string) {
        s = strings.ReplaceAll(s, "\r\n", "\n")
        for len(s) > 0 {
                iNL := strings.IndexByte(s, '\n')
                iCR := strings.IndexByte(s, '\r')
                if iCR >= 0 && (iNL < 0 || iCR < iNL) {
                        d.pending = ""
                        s = s[iCR+1:]
                        continue
                }
                if iNL < 0 {
                        d.pending += cleanControl(s)
                        break
                }
                d.pending += cleanControl(s[:iNL])
                d.addLine(d.pending)
                d.pending = ""
                s = s[iNL+1:]
        }
        // Незавершённая строка удлиняет блок, но не фиксируется в истории:
        // её покажет Layout.
        d.touch()
}

// addLine — дописать готовую строку в текущий блок.
func (d *Doc) addLine(line string) {
        if d.think != nil {
                d.think.lines = append(d.think.lines, line)
                return
        }
        if d.armed && d.stream == nil {
                d.stream = &block{kind: blockAssistant}
                d.blocks = append(d.blocks, d.stream)
        }
        if d.stream != nil {
                d.stream.lines = append(d.stream.lines, line)
                d.cur = d.stream
                return
        }
        if d.cur == nil || d.cur.kind != blockText || d.cur.done {
                d.cur = &block{kind: blockText}
                d.blocks = append(d.blocks, d.cur)
        }
        d.cur.lines = append(d.cur.lines, line)
}

// BeginStream — начать ответ модели.
//
// Блок создаётся лениво: агент может ответить одними вызовами
// инструментов, и пустая карточка «ответ» между ними выглядела бы
// дырой. Границы нужны только для того, чтобы текст ответа не смешался
// с выводом команд.
func (d *Doc) BeginStream() {
        d.armed = true
        d.stream = nil
        d.cur = nil
        d.touch()
}

// EndStream — закончить ответ.
func (d *Doc) EndStream() {
        if d.stream != nil {
                // Хвост без перевода строки — последняя строка ответа.
                if d.pending != "" {
                        d.stream.lines = append(d.stream.lines, d.pending)
                        d.pending = ""
                }
                d.stream.done = true
        }
        d.stream = nil
        d.armed = false
        d.cur = nil
        d.touch()
}

// StreamText — кусок ответа (для подсветки «печатает»).
func (d *Doc) StreamText(s string) { d.Text(s) }

// SetMeta — подпись под последним ответом.
func (d *Doc) SetMeta(meta string) {
        for i := len(d.blocks) - 1; i >= 0; i-- {
                if d.blocks[i].kind == blockAssistant {
                        d.blocks[i].meta = meta
                        break
                }
        }
        d.touch()
}

// BeginThink — открыть размышления.
func (d *Doc) BeginThink() {
        if d.think != nil {
                return
        }
        // Недописанная строка предыдущего вывода не должна попасть в мысли.
        if d.pending != "" {
                d.addLine(d.pending)
                d.pending = ""
        }
        b := &block{kind: blockThink, collapsed: true}
        d.blocks = append(d.blocks, b)
        d.think = b
        d.touch()
}

// ThinkText — дописать размышления.
//
// Модель отдаёт рассуждения кусками произвольной длины: резать каждый
// кусок на отдельные строки нельзя — одна мысль превратилась бы в столбик
// обрывков. Копим до перевода строки, как обычный текст.
func (d *Doc) ThinkText(s string) {
        if s == "" {
                return
        }
        if d.think == nil {
                d.BeginThink()
        }
        d.feed(s)
}

// EndThink — закрыть размышления.
func (d *Doc) EndThink() {
        if d.think != nil {
                if d.pending != "" {
                        d.addLine(d.pending)
                        d.pending = ""
                }
                d.think.done = true
                d.think.collapsed = true
        }
        d.think = nil
        d.touch()
}

// ToggleThink — развернуть или свернуть размышления.
func (d *Doc) ToggleThink() {
        anyExpanded := false
        for _, b := range d.blocks {
                if b.kind == blockThink && !b.collapsed {
                        anyExpanded = true
                        break
                }
        }
        for _, b := range d.blocks {
                if b.kind == blockThink {
                        b.collapsed = anyExpanded
                }
        }
        d.touch()
}

// HasThink — есть ли что показывать по Ctrl+O.
func (d *Doc) HasThink() bool {
        for _, b := range d.blocks {
                if b.kind == blockThink && (len(b.lines) > 0 || (d.think == b && d.pending != "")) {
                        return true
                }
        }
        return false
}

// ToolStart — добавить карточку инструмента.
func (d *Doc) ToolStart(info ToolInfo) {
        info.Status = "running"
        b := &block{kind: blockTool, tool: info}
        d.blocks = append(d.blocks, b)
        d.cur = nil
        d.touch()
}

// ToolEnd — обновить карточку инструмента.
func (d *Doc) ToolEnd(info ToolInfo) {
        for i := len(d.blocks) - 1; i >= 0; i-- {
                b := d.blocks[i]
                if b.kind != blockTool || b.done {
                        continue
                }
                if info.ID != "" {
                        // Точное совпадение по id: параллельные вызовы одного
                        // инструмента завершаются в любом порядке.
                        if b.tool.ID != info.ID {
                                continue
                        }
                } else if b.tool.Name != info.Name {
                        continue
                }
                b.tool.Status = info.Status
                if info.Detail != "" {
                        b.tool.Detail = info.Detail
                }
                b.tool.Elapsed = info.Elapsed
                b.tool.Lines = info.Lines
                if b.tool.Kind == "" {
                        b.tool.Kind = info.Kind
                }
                b.done = true
                d.touch()
                return
        }
        // Карточка не найдена (например, инструмент был запущен до очистки
        // экрана) — показываем результат отдельной строкой.
        if info.Status == "" {
                info.Status = "ok"
        }
        d.blocks = append(d.blocks, &block{kind: blockTool, tool: info, done: true})
        d.touch()
}

// Rule — горизонтальная линия между ходами.
func (d *Doc) Rule() {
        if len(d.blocks) == 0 {
                return
        }
        d.blocks = append(d.blocks, &block{kind: blockRule})
        d.cur = nil
        d.touch()
}

// sanitizeBlock — многострочный текст без управляющих символов.
func sanitizeBlock(s string) string {
        s = strings.ReplaceAll(s, "\r\n", "\n")
        s = strings.ReplaceAll(s, "\r", "\n")
        lines := strings.Split(trimControl(s), "\n")
        for i := range lines {
                lines[i] = strings.TrimRight(lines[i], " ")
        }
        return strings.Join(lines, "\n")
}

// ---------- Раскладка ----------

// LayoutOpts — параметры раскладки.
type LayoutOpts struct {
        Width int
        Theme Theme
        // Spinner — текущий кадр ожидания для незакрытых карточек.
        Spinner string
}

// Layout — разложить ленту в строки экрана.
//
// Возвращает все строки ленты: видимую часть выбирает вызывающий. Так
// проще считать прокрутку и «прилипание» к низу.
func (d *Doc) Layout(o LayoutOpts) []Line {
        w := o.Width
        if w < 10 {
                w = 10
        }
        t := o.Theme
        var out []Line
        add := func(l Line) { out = append(out, l) }
        addAll := func(ls []Line) { out = append(out, ls...) }
        if d.Empty() {
                return nil
        }
        for i, b := range d.blocks {
                if i > 0 && b.kind != blockRule && d.blocks[i-1].kind != blockRule {
                        // Между соседними карточками инструментов пустой строки нет:
                        // серия вызовов должна читаться как один список.
                        if b.kind != blockTool || d.blocks[i-1].kind != blockTool {
                                add(Line{})
                        }
                }
                switch b.kind {
                case blockUser:
                        addAll(renderUser(b, w, t))
                case blockAssistant:
                        addAll(renderAssistant(b, w, t))
                case blockThink:
                        // Хвост размышлений без перевода строки — часть открытого
                        // блока: отдельная карточка ниже выглядела бы вторым
                        // заголовком «размышления».
                        if b == d.think && d.pending != "" {
                                cp := *b
                                cp.lines = append(append([]string{}, b.lines...), d.pending)
                                addAll(renderThink(&cp, w, t))
                        } else {
                                addAll(renderThink(b, w, t))
                        }
                case blockTool:
                        addAll(renderTool(b, w, t, o.Spinner))
                case blockRule:
                        add(Line{{Text: strings.Repeat("─", w), S: Style{FG: t.BorderDim, BG: t.Bg}}})
                default:
                        addAll(renderText(b, w, t))
                }
        }
        // Незавершённая строка — хвост текущей печати. Во время размышлений
        // она уже показана внутри их блока (см. выше).
        if d.pending != "" && d.think == nil {
                if d.armed {
                        addAll(renderAssistant(&block{kind: blockAssistant, lines: []string{d.pending}}, w, t))
                } else {
                        addAll(renderPlainText(d.pending, w, t, "  ", "  ", Style{FG: t.Text, BG: t.Bg}))
                }
        }
        // Убираем пустые строки в начале и в конце.
        for len(out) > 0 && out[0].Width() == 0 {
                out = out[1:]
        }
        for len(out) > 0 && out[len(out)-1].Width() == 0 {
                out = out[:len(out)-1]
        }
        return out
}

// renderUser — запрос пользователя: плашка с акцентной полосой.
func renderUser(b *block, w int, t Theme) []Line {
        text := strings.Join(b.lines, "\n")
        inner := w - 4
        if inner < 10 {
                inner = 10
        }
        st := Style{FG: t.Text, BG: t.User}
        lines := wrapPlain(text, inner)
        out := make([]Line, 0, len(lines))
        for _, ln := range lines {
                line := Line{
                        {Text: "▌ ", S: Style{FG: t.Accent, BG: t.User}},
                        {Text: PadRight(ln, inner), S: st},
                }
                out = append(out, line)
        }
        return out
}

// renderAssistant — ответ модели с искрой в начале.
func renderAssistant(b *block, w int, t Theme) []Line {
        var out []Line
        prefix := []Seg{{Text: "✦ ", S: Style{FG: t.Gold, BG: t.Bg}}}
        indent := []Seg{{Text: "  ", S: Style{FG: t.Muted, BG: t.Bg}}}
        first := true
        for _, raw := range b.lines {
                if strings.TrimSpace(stripANSI(raw)) == "" {
                        out = append(out, Line{})
                        continue
                }
                segs := parseStyled(raw, t, Style{FG: t.Text, BG: t.Bg})
                wrapped := wrapSegs(segs, w-2)
                for _, ln := range wrapped {
                        var line Line
                        if first {
                                line = append(line, prefix...)
                                first = false
                        } else {
                                line = append(line, indent...)
                        }
                        line = append(line, ln...)
                        out = append(out, line)
                }
        }
        if b.meta != "" {
                out = append(out, Line{{Text: "  " + b.meta, S: Style{FG: t.Dim, BG: t.Bg}}})
        }
        return out
}

// renderThink — размышления: свёрнуты в одну строку.
func renderThink(b *block, w int, t Theme) []Line {
        body := strings.Join(b.lines, "\n")
        body = strings.TrimSpace(stripANSI(body))
        if body == "" {
                return []Line{{{Text: "  ◇ размышления", S: Style{FG: t.Dim, BG: t.Bg, Italic: true}}}}
        }
        n := len(strings.Split(body, "\n"))
        if b.collapsed {
                hint := "ctrl+o — показать"
                line := fmt.Sprintf("  ◇ размышления · %d %s · %s", n, plural(n, "строка", "строки", "строк"), hint)
                return []Line{{{Text: Truncate(line, w), S: Style{FG: t.Dim, BG: t.Bg, Italic: true}}}}
        }
        // Развёрнутые мысли: приглушённый блок с вертикальной чертой.
        var out []Line
        out = append(out, Line{{Text: "  ◇ размышления · ctrl+o — скрыть", S: Style{FG: t.Muted, BG: t.Bg, Italic: true}}})
        max := 400
        for i, ln := range strings.Split(body, "\n") {
                if i >= max {
                        out = append(out, Line{{Text: "  │ … ещё " + fmt.Sprint(n-max) + " строк", S: Style{FG: t.Dim, BG: t.Bg}}})
                        break
                }
                for _, seg := range wrapSegs(parseStyled(ln, t, Style{FG: t.Muted, BG: t.Bg}), w-4) {
                        line := Line{{Text: "  │ ", S: Style{FG: t.Border, BG: t.Bg}}}
                        out = append(out, append(line, seg...).trimEnd())
                }
        }
        return out
}

// renderTool — карточка инструмента в одну-две строки.
func renderTool(b *block, w int, t Theme, spin string) []Line {
        ti := b.tool
        icon, color := toolIcon(ti.Kind, t)
        name := ti.Name
        args := sanitizeOneLine(ti.Args)
        nameW := StringWidth(name)
        if nameW > 22 {
                name = Truncate(name, 22)
                nameW = StringWidth(name)
        }
        // Правая часть: статус и время.
        var status string
        var scol Color
        switch ti.Status {
        case "", "running":
                status = spin
                scol = t.Accent
        case "ok":
                status = "✓"
                scol = t.OK
        case "denied":
                status = "⊘"
                scol = t.Warn
        default:
                status = "✕"
                scol = t.Err
        }
        right := status
        if ti.Status == "ok" && ti.Detail != "" {
                right = Truncate(sanitizeOneLine(ti.Detail), 28) + "  " + status
        }
        if ti.Elapsed > 0 {
                right = fmt.Sprintf("%s  %.1fс", right, ti.Elapsed.Seconds())
        }
        // На узком экране правая часть не должна съесть всё имя.
        if max := w / 2; StringWidth(right) > max && max > 6 {
                right = Truncate(right, max)
        }
        rightW := StringWidth(right)
        left := 2 + 1 + 1 + nameW // отступ + иконка + пробел + имя
        avail := w - left - rightW - 2
        if avail < 0 {
                avail = 0
        }
        if args != "" {
                args = " " + Truncate(args, avail)
        }
        st := Style{FG: t.Text, BG: t.Bg}
        line := Line{
                {Text: "  ", S: st},
                {Text: icon, S: Style{FG: color, BG: t.Bg}},
                {Text: " ", S: st},
                {Text: name, S: Style{FG: t.Text, BG: t.Bg, Bold: false}},
                {Text: args, S: Style{FG: t.Muted, BG: t.Bg}},
        }
        // Дополняем пробелами до правой части.
        pad := w - line.Width() - rightW - 1
        if pad < 1 {
                pad = 1
        }
        line = append(line, Seg{Text: strings.Repeat(" ", pad), S: Style{FG: t.Muted, BG: t.Bg}})
        line = append(line, Seg{Text: right, S: Style{FG: scol, BG: t.Bg}})
        out := []Line{line.trimEnd()}

        // Ошибка — отдельной строкой под карточкой: её видно, не раскрывая.
        if (ti.Status == "fail" || ti.Status == "denied") && ti.Detail != "" {
                msg := sanitizeOneLine(ti.Detail)
                for _, seg := range wrapSegs(Line{{Text: msg, S: Style{FG: t.Err, BG: t.Bg}}}, w-8) {
                        l := Line{{Text: "     └ ", S: Style{FG: t.Border, BG: t.Bg}}}
                        out = append(out, append(l, seg...).trimEnd())
                }
        }
        return out
}

// toolIcon — знак категории инструмента (не эмодзи: моноширинные символы).
func toolIcon(kind string, t Theme) (string, Color) {
        switch kind {
        case "read":
                return "◧", t.Info
        case "write":
                return "✎", t.Warn
        case "exec":
                return "❯", t.Accent
        case "net":
                return "⇩", t.Accent2
        case "plan":
                return "☑", t.OK
        case "think":
                return "◈", t.Muted
        case "agent":
                return "◆", t.Accent2
        case "mcp":
                return "⬡", t.Info
        default:
                return "◆", t.Accent
        }
}

// renderText — обычный текст. Отступ не добавляем: старые панели уже
// печатают его с полем, а двойной сдвиг съедал бы ширину.
func renderText(b *block, w int, t Theme) []Line {
        var out []Line
        for _, raw := range b.lines {
                segs := parseStyled(raw, t, Style{FG: t.Text, BG: t.Bg})
                if segs.Width() == 0 {
                        out = append(out, Line{})
                        continue
                }
                out = append(out, renderPlainSegs(segs, w, t, "", "", Style{FG: t.Text, BG: t.Bg})...)
        }
        return out
}

// renderPlainText — текст с отступами.
func renderPlainText(text string, w int, t Theme, first, next string, st Style) []Line {
        segs := parseStyled(text, t, st)
        return renderPlainSegs(segs, w, t, first, next, st)
}

// renderPlainSegs — разложить куски по строкам с отступом.
func renderPlainSegs(segs Line, w int, t Theme, first, next string, st Style) []Line {
        if w <= 0 {
                return nil
        }
        pad := first
        indentW := StringWidth(first)
        var out []Line
        cur := Line{}
        if pad != "" {
                cur = append(cur, Seg{Text: pad, S: Style{FG: st.FG, BG: t.Bg}})
        }
        for _, seg := range segs {
                text := seg.Text
                for len(text) > 0 {
                        avail := w - indentW - cur.Width()
                        if avail < 1 {
                                out = append(out, cur.trimEnd())
                                cur = Line{}
                                if next != "" {
                                        cur = append(cur, Seg{Text: next, S: Style{FG: st.FG, BG: t.Bg}})
                                }
                                continue
                        }
                        cut, rest := cutText(text, avail)
                        if cut == "" {
                                // Слово не влезло целиком — переносим.
                                out = append(out, cur.trimEnd())
                                cur = Line{}
                                if next != "" {
                                        cur = append(cur, Seg{Text: next, S: Style{FG: st.FG, BG: t.Bg}})
                                }
                                continue
                        }
                        cur = append(cur, Seg{Text: cut, S: seg.S})
                        text = rest
                        if text != "" {
                                out = append(out, cur.trimEnd())
                                cur = Line{}
                                if next != "" {
                                        cur = append(cur, Seg{Text: next, S: Style{FG: st.FG, BG: t.Bg}})
                                }
                        }
                }
        }
        if cur.Width() > 0 {
                out = append(out, cur.trimEnd())
        }
        if len(out) == 0 {
                out = append(out, Line{})
        }
        return out
}

// cutText — отрезать кусок не шире w. Старается резать по пробелу.
//
// Режет по графическим кластерам: составной символ (флаг, эмодзи с
// модификаторами) целиком уходит в одну из частей и не рвётся посередине —
// иначе на стыке строк оставалась бы половина эмодзи, а ширина кусков не
// сходилась с тем, что напечатает терминал.
func cutText(s string, w int) (cut, rest string) {
        s = wcwidth.ValidUTF8(s)
        cur := 0
        lastSpace := -1
        for i := 0; i < len(s); {
                cw, size := wcwidth.ClusterAt(s, i)
                if size <= 0 {
                        break
                }
                r, _ := utf8.DecodeRuneInString(s[i:])
                if cur+cw > w {
                        if lastSpace > 0 {
                                return strings.TrimRight(s[:lastSpace], " "), strings.TrimLeft(s[lastSpace:], " ")
                        }
                        if i == 0 {
                                // Даже один кластер шире строки: отдаём его, иначе цикл
                                // вызывающего кода не сдвинется с места.
                                return s[:size], s[size:]
                        }
                        return s[:i], s[i:]
                }
                cur += cw
                if r == ' ' {
                        lastSpace = i
                }
                i += size
        }
        return s, ""
}

// wrapSegs — разложить куски по ширине w, сохраняя стили.
func wrapSegs(segs Line, w int) []Line {
        if w < 2 {
                return []Line{segs}
        }
        if segs.Width() <= w {
                return []Line{segs}
        }
        var out []Line
        cur := Line{}
        curW := 0
        flush := func() {
                out = append(out, cur.trimEnd())
                cur = Line{}
                curW = 0
        }
        for _, seg := range segs {
                text := seg.Text
                for len(text) > 0 {
                        avail := w - curW
                        if avail < 1 {
                                flush()
                                continue
                        }
                        cut, rest := cutText(text, avail)
                        if cut == "" {
                                flush()
                                continue
                        }
                        cur = append(cur, Seg{Text: cut, S: seg.S})
                        curW += StringWidth(cut)
                        text = rest
                        if text != "" {
                                flush()
                        }
                }
        }
        if cur.Width() > 0 {
                out = append(out, cur)
        }
        if len(out) == 0 {
                out = append(out, Line{})
        }
        return out
}

// plural — форма слова по числу.
func plural(n int, one, few, many string) string {
        n = n % 100
        if n >= 11 && n <= 14 {
                return many
        }
        switch n % 10 {
        case 1:
                return one
        case 2, 3, 4:
                return few
        }
        return many
}
