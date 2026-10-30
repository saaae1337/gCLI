// Package wcwidth — единый движок ширины текста в терминальных колонках.
//
// До 0.7.2.0 в проекте жили два независимых вычислителя ширины: пакет ui
// считал по графическим кластерам, пакет tui — по отдельным рунам. На
// составных символах они расходились с тем, что реально рисует терминал:
//
//	🇷🇺       два региональных индикатора  -> 2 колонки, а tui насчитал 4;
//	👨‍👩‍👧‍👦      четыре эмодзи на ZWJ         -> 2 колонки, а tui насчитал 8;
//	👍🏽       эмодзи плюс тон кожи         -> 2 колонки, а tui насчитал 4;
//	1️⃣       цифра плюс keycap            -> 2 колонки, а tui насчитал 1.
//
// Из-за этого лента TUI разъезжалась: сетка клеток отводила под символ
// одно число колонок, а терминал рисовал другой. Теперь и ui, и tui
// считают через этот пакет — источник истины один.
//
// Пакет намеренно не зависит ни от чего остального в проекте: только
// стандартная библиотека. Его импортируют и ui, и tui, а принципиальная
// независимость tui от бизнес-слоёв сохраняется.
package wcwidth

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// zwj — соединитель эмодзи-последовательностей.
const zwj = 0x200D

// ZWJ — соединитель эмодзи-последовательностей (U+200D).
const ZWJ = zwj

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

// IsWideSymbol — символ шириной в две ячейки из блоков до U+1F000.
func IsWideSymbol(r rune) bool { return isWideSymbol(r) }

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

// RuneWidth — ширина одиночного руна в колонках без учёта кластера.
//
// Для составных символов (флаги, ZWJ-последовательности) суммировать
// RuneWidth по рунам нельзя — используйте StringWidth или ClusterAt:
// продолжения кластера присоединяются к базовому символу и места не
// добавляют. RuneWidth нужен там, где руна заведомо одиночная:
// псевдографика, рамки, ASCII.
func RuneWidth(r rune) int { return baseWidth(r) }

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

// IsEmoji — символ рисуется как картинка (две колонки).
func IsEmoji(r rune) bool { return isEmoji(r) }

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

// IsWideRange — блоки CJK и прочие двухколоночные диапазоны.
func IsWideRange(r rune) bool { return isWideRange(r) }

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

// IsEmojiVariation — variation selector: FE0F — эмодзи, FE0E — текст.
func IsEmojiVariation(r rune) (emoji, text bool) { return isEmojiVariation(r) }

// isRegionalIndicator — составная часть флага.
func isRegionalIndicator(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// IsRegionalIndicator — составная часть флага (U+1F1E6…U+1F1FF).
func IsRegionalIndicator(r rune) bool { return isRegionalIndicator(r) }

// isSkinTone — модификатор тона кожи (U+1F3FB…U+1F3FF).
func isSkinTone(r rune) bool { return r >= 0x1F3FB && r <= 0x1F3FF }

// IsSkinTone — модификатор тона кожи (U+1F3FB…U+1F3FF).
func IsSkinTone(r rune) bool { return isSkinTone(r) }

// isKeycap — комбинирующий знак keycap («1️⃣»).
func isKeycap(r rune) bool { return r == 0x20E3 }

// IsKeycap — комбинирующий знак keycap (U+20E3).
func IsKeycap(r rune) bool { return isKeycap(r) }

// isContinuation — рун, который присоединяется к предыдущему символу, не
// занимая места: комбинирующие диакритики, ZWJ, модификаторы, ключи.
func isContinuation(r rune) bool {
	return r == zwj || isKeycap(r) || isSkinTone(r) || isRegionalIndicator(r) ||
		unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r)
}

// IsContinuation — рун присоединяется к предыдущему кластеру, места не
// занимает.
func IsContinuation(r rune) bool { return isContinuation(r) }

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
			// ESC разрывает кластер. Разбор escape-последовательностей
			// (skipANSI, stripANSI) начинается с этого байта; уйди ESC
			// внутрь кластера — и ширина разошлась бы с очищенным
			// текстом на символ. Нашлось фаззингом: «00000 ZWJ ESC 0».
			if n == 0x1B {
				break
			}
			// После ZWJ базовый символ продолжает кластер; повторный ZWJ
			// просто останется соединением.
			if n != zwj {
				if isEmoji(n) {
					pic = true
				}
				joined = false
				i += nsz
				// База после ZWJ — часть рисунка, но её ширина входит
				// в кластер: иначе текст после ZWJ исчезал бы из
				// подсчёта и вёрстка разъезжалась с очищенным текстом.
				// У эмодзи итог всё равно переопределяется флагом pic.
				w += baseWidth(n)
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

// ClusterAt — ширина графического кластера в s[i:] и его длина в байтах.
//
// Первый возврат — сколько колонок терминал отрисует под кластер, второй —
// сколько байтов он занимает. size всегда больше нуля, поэтому цикл
// «i += size» гарантированно сдвигается.
func ClusterAt(s string, i int) (w, size int) { return nextCluster(s, i) }

// NextClusterRunes — то же, что ClusterAt, но над срезом рун: ширина
// кластера в text[i:] и его длина в рунах.
//
// Нужен редакторам, которые хранят текст как []rune (поле ввода tui):
// курсор и перенос там меряются рунами, а ширина — кластерами.
func NextClusterRunes(text []rune, i int) (w, size int) {
	if i < 0 || i >= len(text) {
		return 0, 0
	}
	start := i
	r := text[i]
	i++
	w = baseWidth(r)

	pic := isEmoji(r) || isRegionalIndicator(r)
	textPres := false
	ri := 0
	if isRegionalIndicator(r) {
		ri = 1
	}
	joined := false

	for i < len(text) {
		n := text[i]
		if joined {
			// ESC разрывает кластер — как в nextCluster: уйди ESC
			// внутрь кластера, и ширина разошлась бы с очищенным
			// текстом (нашлось фаззингом в строковом движке).
			if n == 0x1B {
				break
			}
			if n != zwj {
				if isEmoji(n) {
					pic = true
				}
				joined = false
				i++
				// База после ZWJ — часть рисунка, но её ширина
				// входит в кластер, как в nextCluster: без неё
				// текст после ZWJ исчезал бы из подсчёта, и
				// курсор/перенос редактора разъезжались бы с
				// тем, что рисует терминал.
				w += baseWidth(n)
				if isSkinTone(n) || isRegionalIndicator(n) {
					pic = true
				}
				continue
			}
		}
		if !isContinuation(n) {
			break
		}
		if !joined && isRegionalIndicator(n) && !isRegionalIndicator(r) {
			break
		}
		if isRegionalIndicator(n) {
			if isRegionalIndicator(r) {
				if ri >= 2 {
					break
				}
				ri++
			}
		}
		if em, tx := isEmojiVariation(n); em || tx {
			pic = pic || em
			textPres = textPres || tx
		}
		if isKeycap(n) || isSkinTone(n) || isRegionalIndicator(n) {
			pic = true
		}
		if n == zwj {
			joined = true
		}
		i++
	}

	if textPres {
		w = 1
	} else if pic {
		w = 2
	}
	if w < 0 {
		w = 0
	}
	return w, i - start
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

// SkipANSI — длина управляющей последовательности ANSI, начинающейся в
// s[i]. Если s[i] не ESC, возвращается 0.
func SkipANSI(s string, i int) int { return skipANSI(s, i) }

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

// SeqEnd — конец escape-последовательности в s[i] == ESC.
// ok == false означает, что последовательность оборвана концом строки.
func SeqEnd(s string, i int) (end int, ok bool) { return ansiSeqEnd(s, i) }

// stripANSI — убрать управляющие последовательности, оставив текст.
//
// Приводит строку через validUTF8 — по той же причине, что и StringWidth:
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

// StripANSI — убрать управляющие последовательности ANSI, оставив текст.
func StripANSI(s string) string { return stripANSI(s) }

func hasESC(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1B {
			return true
		}
	}
	return false
}

// HasESC — есть ли в строке escape-байт.
func HasESC(s string) bool { return hasESC(s) }

// validUTF8 — привести строку к тому, что реально увидит терминал.
//
// Невалидные байты в выводе заменяются на знак замены, и
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

// ValidUTF8 — заменить невалидные байты на знак замены U+FFFD, как это
// сделает терминал при печати.
func ValidUTF8(s string) string { return validUTF8(s) }

// StringWidth — ширина строки в колонках терминала.
//
// Считает по графическим кластерам, а не по рунам: 👨‍👩‍👧‍👦 — 2 колонки,
// а не 8; 🇷🇺 — 2, а не 4. Escape-последовательности места не занимают.
// Невалидные байты предварительно заменяются на U+FFFD — ровно то, что
// сделает терминал, поэтому мерило и картинка не расходятся.
func StringWidth(s string) int {
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

// truncateCellsRaw — обрезать строку до n колонок без многоточия.
//
// Режем по кластерам и по накопленной ширине: символ, который не помещается
// целиком, отбрасывается вместе со своими продолжениями, поэтому обрезка не
// оставляет половину эмодзи и не выдаёт строку шире заданной.
//
// Сброс цвета при обрезке цветной строки здесь не добавляется: у этого
// пакета нет стилей. Обёртку со сбросом делает вызывающий пакет, у
// которого есть свой cReset (см. ui).
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

// TruncateRaw — обрезать строку до n колонок, не разрывая кластер и не
// добавляя многоточие.
func TruncateRaw(s string, n int) string { return truncateCellsRaw(s, n) }

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

// TrimTrailingEscape — убрать недописанную ANSI-последовательность в конце
// строки.
func TrimTrailingEscape(s string) string { return trimTrailingEscape(s) }

// Truncate — обрезать строку до n колонок, не разрывая кластер и срезая
// недописанную escape-последовательность на хвосте. Сброс цвета вызывающий
// пакет дописывает сам, если ему нужен цветной контекст.
func Truncate(s string, n int) string {
	return trimTrailingEscape(truncateCellsRaw(s, n))
}

// firstCluster — первый графический кластер строки целиком, с ZWJ,
// модификаторами и variation selectors.
//
// Нужна там, где символ шире строки и обрезка по колонкам не даёт ничего:
// брать «первую руну» нельзя, отрыв от ZWJ оставил бы на следующей строке
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

// FirstCluster — первый графический кластер строки целиком.
func FirstCluster(s string) string { return firstCluster(s) }

// ellipsis — многоточие с учётом ширины: одна колонка.
const ellipsis = "…"

// Ellipsis — многоточие шириной в одну колонку.
const Ellipsis = ellipsis

// TruncateEllipsis — обрезать до n колонок, поставив в конце «…».
func TruncateEllipsis(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if StringWidth(s) <= n {
		return s
	}
	// Многоточие должно поместиться: режем до n-1.
	cut := Truncate(s, n-1)
	return cut + ellipsis
}
