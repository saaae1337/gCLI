package ui

// Ширина текста считается в пакете gcli/wcwidth — едином движке проекта.
// Здесь остаются тонкие обёртки со старыми именами: так весь код ui и его
// тесты (cellWidth, truncateCells, …) продолжают работать без правок, а
// источник истины один. Пакет tui считает ширину через тот же wcwidth —
// раньше у tui был собственный рунный движок, и на составных символах
// (флаги, ZWJ-последовательности, тона кожи) он расходился с этим.

import (
        "strings"

        "gcli/wcwidth"
)

// cellWidth — ширина строки в колонках терминала, без ANSI.
//
// validUTF8 внутри — по той причине, что мерило и печать обязаны видеть
// один и тот же текст: на битом UTF-8 рядом с escape расхождение в один
// символ съезжает вся колонка. Нашлось фаззингом.
func cellWidth(s string) int { return wcwidth.StringWidth(s) }

// nextCluster — ширина очередного графического кластера и его длина в байтах.
func nextCluster(s string, i int) (w, size int) { return wcwidth.ClusterAt(s, i) }

// validUTF8 — привести строку к тому, что реально увидит терминал.
func validUTF8(s string) string { return wcwidth.ValidUTF8(s) }

// skipANSI — длина управляющей последовательности, начинающейся в i.
// Возвращается именно ДЛИНА, а не позиция за ней: вызывающий код делает
// i += skipANSI(s, i).
func skipANSI(s string, i int) int { return wcwidth.SkipANSI(s, i) }

// stripANSI — убрать управляющие последовательности, оставив текст.
func stripANSI(s string) string { return wcwidth.StripANSI(s) }

// hasESC — есть ли в строке escape-байт.
func hasESC(s string) bool { return wcwidth.HasESC(s) }

// ellipsis — многоточие с учётом ширины: одна колонка.
const ellipsis = wcwidth.Ellipsis

// truncateCellsRaw — обрезка без добавления сброса цвета. Нужна там, где
// результат сравнивается с префиксом исходника (перенос по словам).
func truncateCellsRaw(s string, n int) string { return wcwidth.TruncateRaw(s, n) }

// trimTrailingEscape — убрать недописанную escape-последовательность
// в конце строки. Обрезка по кластерам может остановиться сразу после
// ESC или посреди CSI; дописанный к такому хвосту сброс цвета слился бы
// с ним в одну последовательность и напечатал бы её хвост как текст.
func trimTrailingEscape(s string) string {
        for i := 0; i < len(s); {
                if s[i] == 0x1B {
                        end, ok := wcwidth.SeqEnd(s, i)
                        if !ok {
                                return strings.TrimRight(s[:i], "\x1b")
                        }
                        i = end
                        continue
                }
                _, sz := nextCluster(s, i)
                if sz <= 0 {
                        sz = 1
                }
                i += sz
        }
        return s
}

// truncateCells — обрезать строку до n колонок, не разрывая символ.
//
// Режем по кластерам и по накопленной ширине: символ, который не помещается
// целиком, отбрасывается вместе со своими продолжениями, поэтому обрезка не
// оставляет половину эмодзи и не выдаёт строку шире заданной.
//
// Если строка была цветной и обрезка отрезала её сброс, цвет закрывается
// здесь же. Иначе терминал остаётся в последнем цвете, и весь следующий
// вывод (включая приглашение) красится чужим цветом.
func truncateCells(s string, n int) string {
        out := trimTrailingEscape(truncateCellsRaw(s, n))
        if out != s && out != "" && hasESC(out) {
                out += cReset
        }
        return out
}

// truncateCellsEll — обрезать до n колонок, поставив в конце «…».
func truncateCellsEll(s string, n int) string {
        if n <= 0 {
                return ""
        }
        if cellWidth(s) <= n {
                return s
        }
        // Многоточие должно поместиться: режем до n-1.
        cut := truncateCells(s, n-1)
        return cut + ellipsis
}

// firstCluster — первый графический кластер строки целиком, с ZWJ,
// модификаторами и variation selectors.
//
// Нужна там, где символ шире строки и обрезка по колонкам не даёт ничего:
// брать «первую руну» нельзя, отрыв от ZWJ оставил бы на следующей строке
// половину эмодзи.
func firstCluster(s string) string { return wcwidth.FirstCluster(s) }
