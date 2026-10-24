package ui

import (
	"strings"
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

// isWideSymbol — символы шириной в две ячейки из блоков до U+1F000.
//
// Нужны потому, что блоки U+2300–U+27BF и U+2B00–U+2BFF смешанные:
// внутри них лежат и настоящие широкие эмодзи (✅ ☕ ⚓), и обычные знаки
// с той же псевдографикой, что рисуется в одну ячейку (✔ ✖ ✎ ☑ ⌕).
// Разница — ровно в свойстве East Asian Width, а не в «похоже на эмодзи».
//
// Диапазоны взяты из EastAsianWidth.txt: только W. Всё остальное в этих
// блоках — A (неоднозначно) или N, и в обычном терминале занимает одну
// ячейку.
//
// Список закрытый и небольшой: эмодзи старше U+1F000 в интерфейсе
// встречаются редко, а ошибиться в ширине заметно — колонки в таблице
// разъезжаются на ячейку за строку.
// Каждому условию отдан свой return true. Одним общим «return true» в
// конце не обойтись: в Go case не проваливается в следующий, поэтому
// условие без своего return молча уходит к return false ниже. Список
// выглядит рабочим, а таблица не работает целиком — так и вышло.
func isWideSymbol(r rune) bool {
	switch {
	case r >= 0x231A && r <= 0x231B: // ⌚ ⌛
		return true
	case r >= 0x2329 && r <= 0x232A: // 〈 〉
		return true
	case r >= 0x23E9 && r <= 0x23EC: // ⏩ ⏪ ⏫ ⏬
		return true
	case r == 0x23F0: // ⏰
		return true
	case r == 0x23F3: // ⏳
		return true
	case r >= 0x25FD && r <= 0x25FE: // ◽ ◾
		return true
	case r >= 0x2614 && r <= 0x2615: // ☔ ☕
		return true
	case r >= 0x2648 && r <= 0x2653: // ♈ … ♓
		return true
	case r == 0x267F: // ♿
		return true
	case r == 0x2693: // ⚓
		return true
	case r == 0x26A1: // ⚡
		return true
	case r >= 0x26AA && r <= 0x26AB: // ⚪ ⚫
		return true
	case r >= 0x26BD && r <= 0x26BE: // ⚽ ⚾
		return true
	case r >= 0x26C4 && r <= 0x26C5: // ⛄ ⛅
		return true
	case r == 0x26CE: // ⛎
		return true
	case r == 0x26D4: // ⛔
		return true
	case r == 0x26EA: // ⛪
		return true
	case r >= 0x26F2 && r <= 0x26F3: // ⛲ ⛳
		return true
	case r == 0x26F5: // ⛵
		return true
	case r == 0x26FA: // ⛺
		return true
	case r == 0x26FD: // ⛽
		return true
	case r == 0x2705: // ✅
		return true
	case r >= 0x270A && r <= 0x270B: // ✊ ✋
		return true
	case r == 0x2728: // ✨
		return true
	case r == 0x274C: // ❌
		return true
	case r == 0x274E: // ❎
		return true
	case r >= 0x2753 && r <= 0x2755: // ❓ ❔ ❕
		return true
	case r == 0x2757: // ❗
		return true
	case r >= 0x2795 && r <= 0x2797: // ➕ ➖ ➗
		return true
	case r == 0x27B0: // ➰
		return true
	case r == 0x27BF: // ➿
		return true
	case r >= 0x2B1B && r <= 0x2B1C: // ⬛ ⬜
		return true
	case r == 0x2B50: // ⭐
		return true
	case r == 0x2B55: // ⭕
		return true
	}
	return false
}

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
	// Широкие символы старше U+1F000 (✅ ☕ ⚓). Отдельно от isEmoji:
	// блоки смешанные, и без этого списка ✔ ✖ ✎ считались бы широкими
	// вместе с ними, хотя терминал рисует их в одну ячейку.
	if isWideSymbol(r) {
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
//
// Про U+2600–U+27BF отдельно: блок смешанный, и для терминала важнее не
// «настоящий это эмодзи или нет», а какая у него ширина. Почти все символы
// блока — East Asian Ambiguous, то есть в обычном терминале занимают одну
// ячейку. Считать их широкими нельзя: gcli печатает здесь ✔ ✖ ☑ ✎, самые
// частые знаки вывода, и лишняя ячейка в каждой строке разъезжала колонки.
// Ошибка была не гипотетическая: в одном списке маркер ▲ мерился как 1,
// а ✔ — как 2.
//
// Кто действительно рисуется цветным и широким, помечает себя вариатором
// FE0F — его обрабатывает nextCluster, ширина становится 2 там и только там.
func isEmoji(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF, // пиктограммы, животные, предметы
		r >= 0x1F200 && r <= 0x1F2FF, // идеографические эмодзи
		r == 0x1F004 || r == 0x1F0CF, // масть, джокер
		r >= 0x1F1E6 && r <= 0x1F1FF: // региональные индикаторы (флаги)
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
	// После ESC не-ASCII не бывает частью escape-последовательности:
	// все они состоят из ASCII-байтов. Такой байт — обычный символ, и
	// съедать его вместе с ESC нельзя: на битом UTF-8 отсчёт байтов
	// разъезжался, и из «ESC» + U+FFFD выпадала половина знака
	// замены, а с ней и вся дальнейшая вёрстка. Нашлось фаззингом.
	if i >= len(s) || s[i] >= 0x80 {
		return i - start
	}
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
	// CSI: ESC [ … финальный байт 0x40–0x7E. Финалом может быть не
	// только буква: «ESC[3~» (Delete) заканчивается тильдой, и прежняя
	// проверка «только буква» съедала следующий за ней текст.
	if i < len(s) && s[i] == '[' {
		i++
		for i < len(s) {
			c := s[i]
			if c >= 0x40 && c <= 0x7E {
				return i + 1 - start
			}
			// Управляющий или не-ASCII байт не может быть частью CSI:
			// иначе недописанная последовательность съест соседний текст.
			if c < 0x20 || c >= 0x80 {
				return i - start
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
//
// Приводит строку через validUTF8 — по той же причине, что и cellWidth:
// обе обязаны видеть один и тот же текст, иначе на битом UTF-8 рядом с
// escape расхождение в один символ. Символ замены не является частью
// escape-последовательности и остаётся в тексте.
func stripANSI(s string) string {
	s = validUTF8(s)
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
// validUTF8 — привести строку к тому, что реально увидит терминал.
//
// Невалидные байты в выводе заменяются на знак замены (UI.put), и
// strings.ToValidUTF8 схлопывает подряд идущие битые байты в ОДИН знак.
// Если считать ширину по исходным байтам, вёрстка и картинка разойдутся:
// на «\xe6\x97» (оборванная трёхбайтовая последовательность) получается
// 2 ячейки против 1 на экране, и колонка поедет ровно там, где текст
// битый. Нашлось фаззингом.
//
// Поэтому сначала приводим, потом меряем. На валидной строке
// utf8.ValidString — один быстрый проход, а валидны почти все строки.
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, string(utf8.RuneError))
}

func cellWidth(s string) int {
	s = validUTF8(s)
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

// trimTrailingEscape — убрать недописанную escape-последовательность
// в конце строки. Обрезка по кластерам может остановиться сразу после
// ESC или посреди CSI; дописанный к такому хвосту сброс цвета слился бы
// с ним в одну последовательность и напечатал бы её хвост как текст.
func trimTrailingEscape(s string) string {
	for i := 0; i < len(s); {
		if s[i] == 0x1B {
			end, ok := ansiSeqEnd(s, i)
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

// ansiSeqEnd — конец escape-последовательности в s[i] == ESC.
// ok == false означает, что последовательность оборвана концом строки.
func ansiSeqEnd(s string, i int) (int, bool) {
	if i+1 >= len(s) {
		return 0, false
	}
	switch s[i+1] {
	case '[':
		for j := i + 2; j < len(s); j++ {
			c := s[j]
			if c >= 0x40 && c <= 0x7E {
				return j + 1, true
			}
			if c < 0x20 || c >= 0x80 {
				// Последовательность оборвана посторонним байтом:
				// skipANSI съедает только ESC[, а байт оставляет
				// текстом — его нельзя терять при чистке хвоста.
				return j, true
			}
		}
		return 0, false
	case ']':
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1, true
			}
			if s[j] == 0x1B && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2, true
			}
		}
		return 0, false
	default:
		c := s[i+1]
		if c < 0x20 || c >= 0x80 {
			// ESC не бывает началом такой пары: skipANSI считает
			// его одиночным управляющим символом.
			return i + 1, true
		}
		return i + 2, true
	}
}

// truncateCellsRaw — обрезка без добавления сброса цвета. Нужна там, где
// результат сравнивается с префиксом исходника (перенос по словам).
func truncateCellsRaw(s string, n int) string {
	if n <= 0 {
		return ""
	}
	// Обрезаем по починенной строке: иначе индексы байтов сойдутся с
	// тем, что действительно печатается, и хвост обрежется не там.
	s = validUTF8(s)
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
