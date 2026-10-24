package tui

import (
        "strconv"
        "strings"

        "gcli/wcwidth"
)

// ---------- ANSI в стилизованные строки ----------
//
// Старые панели (справка, таблицы, диффы) печатают текст с escape-кодами.
// В новом интерфейсе их не должно быть видно: строка разбирается на куски
// со стилями и рисуется клетками, как обычный текст.

// stripANSI — убрать escape-последовательности.
func stripANSI(s string) string {
        var b strings.Builder
        for i := 0; i < len(s); {
                if s[i] == 0x1b {
                        i = skipEscape(s, i)
                        continue
                }
                b.WriteByte(s[i])
                i++
        }
        return b.String()
}

// skipEscape — индекс за концом escape-последовательности с позиции i
// (i стоит на ESC). Исторический контракт этого пакета — абсолютный
// индекс, в отличие от wcwidth.SkipANSI, возвращающего длину.
//
// Логика живёт в gcli/wcwidth — общей с ui реализации, куда доехали
// все починки с фаззинга: не-ASCII после ESC не съедается, финальный
// байт CSI ищется не только среди букв, оборванный OSC не тащит хвост
// строки внутрь последовательности.
func skipEscape(s string, i int) int {
        return i + wcwidth.SkipANSI(s, i)
}

// parseStyled — разобрать строку с ANSI в куски.
func parseStyled(s string, t Theme, base Style) Line {
        var out Line
        var buf strings.Builder
        st := base
        flush := func() {
                if buf.Len() == 0 {
                        return
                }
                out = append(out, Seg{Text: buf.String(), S: st})
                buf.Reset()
        }
        for i := 0; i < len(s); {
                if s[i] != 0x1b {
                        if s[i] == '\t' {
                                buf.WriteString("    ")
                                i++
                                continue
                        }
                        if s[i] == '\r' {
                                i++
                                continue
                        }
                        buf.WriteByte(s[i])
                        i++
                        continue
                }
                // Escape: SGR меняет стиль, остальное (курсор, очистка) — мимо.
                j := i + 1
                if j < len(s) && s[j] == '[' {
                        k := j + 1
                        for k < len(s) && !(s[k] >= 0x40 && s[k] <= 0x7e) {
                                k++
                        }
                        if k < len(s) {
                                if s[k] == 'm' {
                                        flush()
                                        st = applySGR(st, s[j+1:k], t, base)
                                }
                                i = k + 1
                                continue
                        }
                }
                i = skipEscape(s, i)
        }
        flush()
        return out
}

// applySGR — применить параметры SGR к стилю.
func applySGR(st Style, params string, t Theme, base Style) Style {
        if params == "" {
                return base
        }
        parts := strings.Split(params, ";")
        for i := 0; i < len(parts); i++ {
                p := parts[i]
                if p == "" {
                        p = "0"
                }
                n, err := strconv.Atoi(p)
                if err != nil {
                        continue
                }
                switch {
                case n == 0:
                        st = base
                case n == 1:
                        st.Bold = true
                case n == 2:
                        st.Dim = true
                case n == 3:
                        st.Italic = true
                case n == 4:
                        st.Underline = true
                case n == 7:
                        st.Reverse = true
                case n == 9:
                        st.Strike = true
                case n == 22:
                        st.Bold, st.Dim = false, false
                case n == 23:
                        st.Italic = false
                case n == 24:
                        st.Underline = false
                case n == 27:
                        st.Reverse = false
                case n == 29:
                        st.Strike = false
                case n >= 30 && n <= 37:
                        st.FG = ansiColor(n-30, false, t)
                case n == 39:
                        st.FG = base.FG
                case n >= 90 && n <= 97:
                        st.FG = ansiColor(n-90, true, t)
                case n >= 40 && n <= 47:
                        st.BG = ansiColor(n-40, false, t)
                case n == 49:
                        st.BG = base.BG
                case n >= 100 && n <= 107:
                        st.BG = ansiColor(n-100, true, t)
                case n == 38 || n == 48:
                        col, used := parseExtColor(parts, i)
                        if n == 38 {
                                st.FG = col
                        } else {
                                st.BG = col
                        }
                        i += used
                }
        }
        return st
}

// parseExtColor — 256-цветный или truecolor из продолжения параметров.
func parseExtColor(parts []string, i int) (Color, int) {
        if i+1 >= len(parts) {
                return Color{}, 0
        }
        mode, _ := strconv.Atoi(parts[i+1])
        switch mode {
        case 5:
                if i+2 < len(parts) {
                        n, _ := strconv.Atoi(parts[i+2])
                        return xtermColor(n), 2
                }
                return Color{}, 1
        case 2:
                if i+4 < len(parts) {
                        r, _ := strconv.Atoi(parts[i+2])
                        g, _ := strconv.Atoi(parts[i+3])
                        b, _ := strconv.Atoi(parts[i+4])
                        return RGB(uint8(clamp8(r)), uint8(clamp8(g)), uint8(clamp8(b))), 4
                }
                return Color{}, 1
        }
        return Color{}, 1
}

func clamp8(v int) int {
        if v < 0 {
                return 0
        }
        if v > 255 {
                return 255
        }
        return v
}

// ansiColor — базовые 16 цветов в палитре темы.
func ansiColor(idx int, bright bool, t Theme) Color {
        var c Color
        switch idx {
        case 0:
                c = t.Dim
        case 1:
                c = t.Err
        case 2:
                c = t.OK
        case 3:
                c = t.Warn
        case 4:
                c = t.Info
        case 5:
                c = t.Accent2
        case 6:
                c = t.Info
        case 7:
                c = t.Text
        default:
                c = t.Muted
        }
        if bright {
                c = Lighten(c, 0.22)
        }
        return c
}

// xtermColor — цвет xterm-256.
func xtermColor(n int) Color {
        if n < 0 {
                n = 0
        }
        if n < 16 {
                base := [16]Color{
                        RGB(0, 0, 0), RGB(128, 0, 0), RGB(0, 128, 0), RGB(128, 128, 0),
                        RGB(0, 0, 128), RGB(128, 0, 128), RGB(0, 128, 128), RGB(192, 192, 192),
                        RGB(128, 128, 128), RGB(255, 0, 0), RGB(0, 255, 0), RGB(255, 255, 0),
                        RGB(0, 0, 255), RGB(255, 0, 255), RGB(0, 255, 255), RGB(255, 255, 255),
                }
                return base[n]
        }
        if n < 232 {
                n -= 16
                r := n / 36
                g := (n % 36) / 6
                b := n % 6
                lvl := func(v int) uint8 {
                        if v == 0 {
                                return 0
                        }
                        return uint8(55 + v*40)
                }
                return RGB(lvl(r), lvl(g), lvl(b))
        }
        g := uint8(8 + (n-232)*10)
        return RGB(g, g, g)
}
