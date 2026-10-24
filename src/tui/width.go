// Package tui — собственный интерфейс gcli: полноэкранный терминал с
// прокруткой, полем ввода, панелями и пиксельным маскотом.
//
// Пакет намеренно не зависит от бизнес-слоёв проекта: он получает готовые
// события и рисует кадры в io.Writer. Всё оформление — свои глифы и свои
// цвета, без внешних эмодзи: один и тот же вид на любой машине, где есть
// монопространственный шрифт.
package tui

import (
        "strings"
        "unicode/utf8"

        "gcli/wcwidth"
)

// Ширина считается в пакете gcli/wcwidth — едином движке проекта (через
// него же считает и ui). Раньше этот пакет мерял по отдельным рунам, и на
// составных символах расходился с терминалом: 🇷🇺 насчитывал 4 колонки
// вместо 2, 👨‍👩‍👧‍👦 — 8 вместо 2, 👍🏽 — 4 вместо 2. Сетка клеток
// отводила лишние клетки, а терминал рисовал одну картинку — лента
// разъезжалась со сдвигом, растущим к концу строки.

// RuneWidth — ширина одиночной руны в терминальных клетках.
//
// 0 — комбинирующие символы и управляющие; 2 — широкие (CJK, эмодзи);
// 1 — всё остальное. Для составных символов (флаги, ZWJ-последовательности)
// суммировать по рунам нельзя: продолжения кластера не добавляют ширины,
// поэтому для текста используйте StringWidth. RuneWidth остался для сетки
// клеток, куда одиночные руны ставит Set.
func RuneWidth(r rune) int { return wcwidth.RuneWidth(r) }

// StringWidth — ширина строки в клетках.
//
// Считает по графическим кластерам, как рисует терминал: 👨‍👩‍👧‍👦 — 2,
// а не 8. Escape-последовательности места не занимают.
func StringWidth(s string) int { return wcwidth.StringWidth(s) }

// Truncate — обрезать строку до ширины с многоточием.
//
// Режет по кластерам: половина эмодзи или флага остаться не может.
func Truncate(s string, w int) string { return wcwidth.TruncateEllipsis(s, w) }

// PadRight — дополнить строку пробелами справа до ширины.
func PadRight(s string, w int) string {
        n := w - StringWidth(s)
        if n <= 0 {
                return s
        }
        return s + strings.Repeat(" ", n)
}

// wrapPlain — разбить простой текст (без ANSI) на строки по ширине.
//
// Перенос по словам, длинные слова режутся по клеткам (по кластерам).
// Переводы строки сохраняются. Пустая строка остаётся пустой строкой.
func wrapPlain(text string, w int) []string {
        if w <= 0 {
                return []string{""}
        }
        var out []string
        for _, raw := range strings.Split(text, "\n") {
                if raw == "" {
                        out = append(out, "")
                        continue
                }
                var line strings.Builder
                lineW := 0
                flush := func() {
                        out = append(out, strings.TrimRight(line.String(), " "))
                        line.Reset()
                        lineW = 0
                }
                for _, word := range strings.Fields(raw) {
                        wordW := StringWidth(word)
                        if lineW > 0 && lineW+1+wordW > w {
                                flush()
                        }
                        if wordW > w {
                                // Слово длиннее строки: режем по клеткам. Кластер, не
                                // помещающийся целиком, уходит на следующую строку весь —
                                // половина эмодзи посреди строки не появляется.
                                for i := 0; i < len(word); {
                                        cw, size := wcwidth.ClusterAt(word, i)
                                        if size <= 0 {
                                                break
                                        }
                                        if lineW+cw > w {
                                                flush()
                                        }
                                        line.WriteString(word[i : i+size])
                                        lineW += cw
                                        i += size
                                }
                                continue
                        }
                        if lineW > 0 {
                                line.WriteByte(' ')
                                lineW++
                        }
                        line.WriteString(word)
                        lineW += wordW
                }
                flush()
        }
        if len(out) == 0 {
                out = []string{""}
        }
        return out
}

// cleanControl — убрать управляющие символы, сохранив ANSI-разметку.
//
// Старые панели присылают цветной текст escape-кодами; его разбирает
// parseStyled. Если вырезать ESC, как делает trimControl, на экране
// остались бы хвосты вида «[38;2;…m». Строка потока ответа тоже приходит
// с разметкой markdown.
func cleanControl(s string) string {
        var b strings.Builder
        b.Grow(len(s))
        for i := 0; i < len(s); {
                if s[i] == 0x1b {
                        j := skipEscape(s, i)
                        b.WriteString(s[i:j])
                        i = j
                        continue
                }
                r, size := utf8.DecodeRuneInString(s[i:])
                i += size
                switch {
                case r == '\n' || r == '\t':
                        b.WriteRune(r)
                case r == '\r':
                        // CR обработан в Doc.Text: здесь он уже не несёт смысла.
                case r < 32 || r == 0x7f:
                        // прочие управляющие — мусор
                default:
                        b.WriteRune(r)
                }
        }
        return b.String()
}

// trimControl — убрать управляющие символы и escape-последовательности.
// Для однострочных подписей, которые рисуются без разбора ANSI.
func trimControl(s string) string {
        var b strings.Builder
        b.Grow(len(s))
        for i := 0; i < len(s); {
                if s[i] == 0x1b {
                        i = skipEscape(s, i)
                        continue
                }
                r, size := utf8.DecodeRuneInString(s[i:])
                i += size
                switch {
                case r == '\n' || r == '\t':
                        b.WriteRune(r)
                case r < 32 || r == 0x7f:
                        // прочие управляющие — мусор
                default:
                        b.WriteRune(r)
                }
        }
        return b.String()
}

// sanitizeOneLine — одна строка без управляющих символов.
func sanitizeOneLine(s string) string {
        s = strings.ReplaceAll(s, "\r", "")
        s = strings.ReplaceAll(s, "\n", " ")
        return trimControl(s)
}
