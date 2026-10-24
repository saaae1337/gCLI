// Package tui — собственный интерфейс gcli: полноэкранный терминал с
// прокруткой, полем ввода, панелями и пиксельным маскотом.
//
// Пакет намеренно не зависит от остального проекта: он получает готовые
// события и рисует кадры в io.Writer. Всё оформление — свои глифы и свои
// цвета, без внешних эмодзи: один и тот же вид на любой машине, где есть
// монопространственный шрифт.
package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// RuneWidth — ширина руны в терминальных клетках.
//
// 0 — комбинирующие символы и управляющие; 2 — широкие (CJK, эмодзи);
// 1 — всё остальное. Таблиц из x/term нет: пакет должен собираться без
// внешних зависимостей, а диапазоны ниже покрывают то, что реально
// встречается в ответах модели и файлах проекта.
func RuneWidth(r rune) int {
	if r == 0 || r == '\n' || r == '\r' || r == '\t' {
		return 0
	}
	if r < 32 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	// Управляющие форматы (ZWJ, variation selectors, directional marks).
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

// isWideRune — диапазоны двойной ширины.
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115f, // хангыль-джонсон
		r >= 0x2e80 && r <= 0x303e, // CJK-радикалы, пунктуация
		r >= 0x3041 && r <= 0x33ff, // кана, CJK-совместимость
		r >= 0x3400 && r <= 0x4dbf, // CJK расширение A
		r >= 0x4e00 && r <= 0x9fff, // CJK
		r >= 0xa000 && r <= 0xa4cf, // и-цзин
		r >= 0xa960 && r <= 0xa97f,
		r >= 0xac00 && r <= 0xd7a3, // хангыль
		r >= 0xf900 && r <= 0xfaff, // CJK-совместимые идеограммы
		r >= 0xfe10 && r <= 0xfe19,
		r >= 0xfe30 && r <= 0xfe6f,
		r >= 0xff00 && r <= 0xff60, // полноширинные формы
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f000 && r <= 0x1f02f, // маджонг, домино
		r >= 0x1f0a0 && r <= 0x1f0ff,
		r >= 0x1f100 && r <= 0x1f1ff,
		r >= 0x1f200 && r <= 0x1f2ff, // смайлы в рамке
		r >= 0x1f300 && r <= 0x1f64f, // эмодзи
		r >= 0x1f680 && r <= 0x1f6ff,
		r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x1fa70 && r <= 0x1faff,
		r >= 0x20000 && r <= 0x3fffd:
		return true
	}
	// Отдельные эмодзи-символы вне основных блоков.
	switch r {
	case 0x231a, 0x231b, 0x23e9, 0x23ea, 0x23eb, 0x23ec, 0x23f0, 0x23f3,
		0x25fd, 0x25fe, 0x2614, 0x2615, 0x2648, 0x2649, 0x264a, 0x264b,
		0x264c, 0x264d, 0x264e, 0x264f, 0x2650, 0x2651, 0x2652, 0x2653,
		0x267f, 0x2693, 0x26a1, 0x26aa, 0x26ab, 0x26bd, 0x26be, 0x26c4,
		0x26c5, 0x26ce, 0x26d4, 0x26ea, 0x26f2, 0x26f3, 0x26f5, 0x26fa,
		0x26fd, 0x2705, 0x270a, 0x270b, 0x2728, 0x274c, 0x274e, 0x2753,
		0x2754, 0x2755, 0x2757, 0x2795, 0x2796, 0x2797, 0x27b0, 0x27bf,
		0x2b1b, 0x2b1c, 0x2b50, 0x2b55:
		return true
	}
	return false
}

// StringWidth — ширина строки в клетках.
func StringWidth(s string) int {
	w := 0
	for _, r := range s {
		w += RuneWidth(r)
	}
	return w
}

// Truncate — обрезать строку до ширины с многоточием.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if StringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := RuneWidth(r)
		if cur+rw > w-1 {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	b.WriteString("…")
	return b.String()
}

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
// Перенос по словам, длинные слова режутся по границе руны. Переводы
// строки сохраняются. Пустая строка остаётся пустой строкой.
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
				// Слово длиннее строки: режем по клеткам.
				for _, r := range word {
					rw := RuneWidth(r)
					if lineW+rw > w {
						flush()
					}
					line.WriteRune(r)
					lineW += rw
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
