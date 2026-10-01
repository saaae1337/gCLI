package ui

import (
	"unicode"
	"unicode/utf8"
)

// ---------- Ширина текста в терминальных колонках ----------
//
// Ширина считается по графическим кластерам, а не по рунам. Разница видна на
// символах, которые терминал рисует как одну картинку из нескольких рун:
//
//	🇷🇺   два региональных индикатора -> 2 колонки, а не 4;
//	👨‍👩‍👧‍👦 четыре эмодзи на ZWJ -> 2 колонки, а не 8;
//	👍🏽  эмодзи плюс модификатор тона кожи -> 2 колонки, а не 4;
//	1️⃣    цифра плюс keycap -> 2 колонки, а не 1+0+0.
//
// Раньше ширина считалась в рунах: len([]rune(s)). Для кириллицы это верно,
// но для CJK и эмодзи нет — «日本» это 4 колонки, а не 2. Из-за этого таблицы
// разъезжались, строки с emoji наезжали друг на друга, а перенос по ширине
// ломал слова посередине.
//
// Правило здесь одно: классу достаётся ширина первого значащего символа,
// а продолжения (комбинирующие диакритики, ZWJ, модификаторы, variation
// selectors, keycap) не добавляют ничего. Исключение одно — сам кластер помечается
// эмодзи, если в нём есть признак эмодзи-презентации.

// baseWidth — ширина одиночного руна без учёта продолжений.
func baseWidth(r rune) int {
	// Управляющие и непечатаемые: места не занимают.
	if r == 0 {
		return 0
	}
	if r < 32 || (r >= 0x7F && r < 0xA0) {
		return 0
	}
	// Комбинирующие: «e» + U+0301 должен занимать одну колонку, а не две.
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	// Эмодзи и пиктограммы: почти все несут вес 2.
	if isEmoji(r) {
		return 2
	}
	// Восточноазиатские широкие и полноширинные формы.
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
		return 2
	}
	if isWideRange(r) {
		return 2
	}
	return 1
}

// zwj — соединитель эмодзи-последовательностей.
const zwj = 0x200D

// isEmojiVariation — variation selector: FE0F просит нарисовать символ как
// эмодзи, FE0E — как обычный текст.
func isEmojiVariation(r rune) (emoji, text bool) {
	switch r {
	case 0xFE0F:
		return true, false
	case 0xFE0E:
		return false, true
	}
	return false, false
}

// isRegionalIndicator — составная часть флага.
func isRegionalIndicator(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// isSkinTone — модификатор тона кожи (U+1F3FB…U+1F3FF).
func isSkinTone(r rune) bool { return r >= 0x1F3FB && r <= 0x1F3FF }

// isKeycap — комбинирующий знак keycap («1️⃣»).
func isKeycap(r rune) bool { return r == 0x20E3 }

// isContinuation — рун, который присоединяется к предыдущему символу, не
// занимая места: комбинирующие диакритики, ZWJ, модификаторы, ключи.
func isContinuation(r rune) bool {
	return r == zwj || isKeycap(r) || isSkinTone(r) || isRegionalIndicator(r) ||
		unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r)
}

// nextCluster — ширина очередного графического кластера и его длина в байтах.
//
// Кластер начинается с базового символа и включает всё, что к нему
// присоединяется. Возвращаемая длина всегда больше нуля, даже для
// невидимых символов, — иначе вызывающий цикл не сдвинулся бы и завис.
func nextCluster(s string, i int) (w, size int) {
	start := i
	r, sz := utf8.DecodeRuneInString(s[i:])
	i += sz
	w = baseWidth(r)

	pic := isEmoji(r) || isRegionalIndicator(r)
	text := false
	// ri — сколько региональных индикаторов уже учтено в кластере.
	ri := 0
	if isRegionalIndicator(r) {
		ri = 1
	}
	// joined — после ZWJ идёт следующий базовый символ, и он принадлежит
	// тому же рисунку: 👨‍👩 — одна картинка, а не два лица рядом.
	joined := false

	for i < len(s) {
		n, nsz := utf8.DecodeRuneInString(s[i:])
		if joined {
			// После ZWJ базовый символ продолжает кластер; повторный ZWJ
			// просто останется соединением.
			if n != zwj {
				if isEmoji(n) {
					pic = true
				}
				joined = false
				i += nsz
				// Модификатор может стоять сразу после базы («👍🏽»).
				if isSkinTone(n) || isRegionalIndicator(n) {
					pic = true
				}
				continue
			}
		}
		if !isContinuation(n) {
			break
		}
		// Региональный индикатор не присоединяется к чужому основанию:
		// «a🇷🇺b» — это три символа (1 + флаг + 1), а не один кластер.
		if !joined && isRegionalIndicator(n) && !isRegionalIndicator(r) {
			break
		}
		if isRegionalIndicator(n) {
			// Флаг — это ровно два индикатора. Третий подряд терминал
			// рисует отдельным символом, поэтому он не должен втягиваться
			// в кластер: иначе «🇺🇸🇬🇧» молча склеивалось бы в один символ
			// и «съедало» 2 колонки.
			if isRegionalIndicator(r) {
				if ri >= 2 {
					break
				}
				ri++
			}
		}
		if em, tx := isEmojiVariation(n); em || tx {
			pic = pic || em
			text = text || tx
		}
		if isKeycap(n) || isSkinTone(n) || isRegionalIndicator(n) {
			pic = true
		}
		if n == zwj {
			joined = true
		}
		i += nsz
	}

	// «1️⃣» — цифра с keycap рисуется как эмодзи: 2 колонки, хотя сама
	// цифра узкая. «✂︎» с FE0E наоборот — текст, одна колонка.
	if text {
		w = 1
	} else if pic {
		w = 2
	}
	if w < 0 {
		w = 0
	}
	return w, i - start
}

// isEmoji — символ рисуется как картинка (две колонки).
//
// Отдельная функция, а не только диапазоны Unicode: основные эмодзи лежат в
// U+1F300–U+1FAFF, но раскрываются в терминале и более старые символы
// (U+2700–U+27BF, U+2600–U+26FF), которые терминалы часто рисуют цветными.
func isEmoji(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF, // пиктограммы, животные, предметы
		r >= 0x1F000 && r <= 0x1F2FF, // игральные кости, масти, флаги
		r >= 0x1F900 && r <= 0x1F9FF, // люди, профессии
		r >= 0x1F1E6 && r <= 0x1F1FF, // региональные индикаторы (флаги)
		r >= 0x2600 && r <= 0x27BF,   // символы и Dingbats
		r >= 0x2B00 && r <= 0x2BFF:   // стрелки и математические знаки
		return true
	}
	return false
}

// isWideRange — блоки CJK и прочие двухколоночные диапазоны.
func isWideRange(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Хангль джеамо
		r >= 0x2E80 && r <= 0x303E, // иероглифы, пунктуация CJK
		r >= 0x3041 && r <= 0x33FF, // хирагана, кана, совместимость
		r >= 0x3400 && r <= 0x4DBF, // иероглифы, расширение A
		r >= 0x4E00 && r <= 0x9FFF, // иероглифы, основной блок
		r >= 0xA000 && r <= 0xA4CF, // юнагури
		r >= 0xAC00 && r <= 0xD7A3, // хангльские слоги
		r >= 0xF900 && r <= 0xFAFF, // иероглифы, совместимость
		r >= 0xFF00 && r <= 0xFF60, // полноширинные формы ASCII
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F004 || r == 0x1F0CF, // масть, джокер
		r >= 0x20000 && r <= 0x3FFFD: // иероглифы, расширения B+
		return true
	}
	return false
}

// skipANSI — длина управляющей последовательности, начинающейся в i.
//
// Возвращается именно ДЛИНА, а не позиция за ней: вызывающий код делает
// i += skipANSI(s, i), и возврат абсолютного индекса уводил бы считыватель
// за конец строки. По симметрии с nextCluster ошибиться тут легко, поэтому
// результат отсчитывается от позиции входа.
//
// Escape-последовательности места не занимают, но вырезать их из строки
// перед обрезкой нельзя: срезка сдвинула бы байтовые индексы.
func skipANSI(s string, i int) int {
	if i >= len(s) || s[i] != 0x1B {
		return 0
	}
	start := i
	i++
	// OSC: ESC ] … BEL или ESC ] … ESC \
	if i < len(s) && s[i] == ']' {
		i++
		for i < len(s) {
			if s[i] == 0x07 {
				return i + 1 - start
			}
			if s[i] == 0x1B && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2 - start
			}
			i++
		}
		return i - start
	}
	// Двухбайтовный CSI: ESC [ … финальная буква
	if i < len(s) && s[i] == '[' {
		i++
		for i < len(s) {
			c := s[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				return i + 1 - start
			}
			i++
		}
		return i - start
	}
	// Одиночная escape-последовательность из двух символов.
	if i < len(s) {
		return i + 1 - start
	}
	return i - start
}

// stripANSI — убрать управляющие последовательности, оставив текст.
func stripANSI(s string) string {
	if !hasESC(s) {
		return s
	}
	var b []byte
	for i := 0; i < len(s); {
		if s[i] == 0x1B {
			i += skipANSI(s, i)
			continue
		}
		b = append(b, s[i])
		i++
	}
	return string(b)
}

func hasESC(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1B {
			return true
		}
	}
	return false
}

// cellWidth — ширина строки в колонках терминала, без ANSI.
func cellWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1B {
			i += skipANSI(s, i)
			continue
		}
		cw, sz := nextCluster(s, i)
		if sz <= 0 {
			break
		}
		w += cw
		i += sz
	}
	return w
}

// truncateCells — обрезать строку до n колонок, не разрывая символ.
//
// Режем по кластерам и по накопленной ширине: символ, который не помещается
// целиком, отбрасывается вместе со своими продолжениями, поэтому обрезка не
// оставляет половину эмодзи и не выдаёт строку шире заданной.
func truncateCells(s string, n int) string {
	if n <= 0 {
		return ""
	}
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1B {
			i += skipANSI(s, i)
			continue
		}
		cw, sz := nextCluster(s, i)
		if sz <= 0 {
			break
		}
		if w+cw > n {
			// Обрезать прямо здесь: кластер не помещается целиком.
			return s[:i]
		}
		w += cw
		i += sz
	}
	return s
}

// firstCluster — первый графический кластер строки целиком, с ZWJ,
// модификаторами и variation selectors.
//
// Нужна там, где символ шире строки и обрезка по колонкам не даёт ничего:
// брать «первый рун» нельзя, отрыв от ZWJ оставил бы на следующей строке
// половину эмодзи.
func firstCluster(s string) string {
	if s == "" {
		return ""
	}
	_, sz := nextCluster(s, 0)
	if sz <= 0 || sz > len(s) {
		sz = utf8.RuneLen(rune(s[0]))
		if sz <= 0 {
			sz = 1
		}
	}
	return s[:sz]
}

// ellipsis — многоточие с учётом ширины: одна колонка.
const ellipsis = "…"

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
