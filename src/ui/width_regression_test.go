package ui

// Регрессии, найденные фаззингом подсчёта ширины.
//
// Каждый случай — реальный вход, на котором вёрстка и то, что увидит
// терминал, сходились по-разному. Фаззинг их нашёл, но объяснить, что
// именно сломалось, может только человек: поэтому здесь каждый вход
// подписан словами, а не лежит файлом в testdata.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Оборванная трёхбайтовая последовательность: 0xE6 ждёт два
// продолжения, а дальше конец строки.
//
// Мерялось 2 ячейки (два байта по одной), а печатался один знак
// замены — одна ячейка. Колонка съезжала ровно там, где текст битый.
func TestWidthMatchesRepairedTextOnTruncatedSequence(t *testing.T) {
	broken := "\xe6\x97"
	repaired := strings.ToValidUTF8(broken, string(utf8.RuneError))

	if got, want := cellWidth(broken), cellWidth(repaired); got != want {
		t.Fatalf("ширина битой строки %d, а напечатанной %d: %q -> %q",
			got, want, broken, repaired)
	}
	if cellWidth(broken) != 1 {
		t.Errorf("хотели 1 ячейку на месте обломка, получили %d", cellWidth(broken))
	}
}

// Одинокий байт-продолжение 0x96 — следы windows-1251.
func TestWidthMatchesRepairedTextOnLoneContinuationByte(t *testing.T) {
	broken := "\x96"
	repaired := strings.ToValidUTF8(broken, string(utf8.RuneError))
	if got, want := cellWidth(broken), cellWidth(repaired); got != want {
		t.Fatalf("ширина битой строки %d, напечатанной %d", got, want)
	}
}

// Escape подряд с битым байтом: 0xED ждёт два продолжения, но вместо
// них получает ESC, а тот — ещё один 0xD7.
//
// skipANSI считал байты и, увидев после ESC старший байт U+FFFD,
// съедал только первую половину знака замены. Дальше отсчёт ехал, и
// вёрстка разъезжалась.
func TestWidthStableWithEscapeNextToBrokenBytes(t *testing.T) {
	broken := "\xed\x1b\xd70"
	repaired := strings.ToValidUTF8(broken, string(utf8.RuneError))
	if got, want := cellWidth(broken), cellWidth(repaired); got != want {
		t.Fatalf("ширина битой строки %d, напечатанной %d: %q -> %q",
			got, want, broken, repaired)
	}
	if got, want := cellWidth(broken), cellWidth(stripANSI(broken)); got != want {
		t.Fatalf("цветные последовательности повлияли на ширину: %d против %d", got, want)
	}
}

// После ESC не-ASCII не бывает частью escape: все последовательности
// состоят из ASCII-байтов. ESC перед многобайтовым знаком — это ESC
// плюс обычный символ, и буква обязана остаться на месте.
//
// Раньше skipANSI съедал ESC вместе с первым байтом буквы, и она
// теряла половину: счёт ехал, буква на экране становилась другой.
func TestEscapeBeforeMultibyteRuneKeepsRune(t *testing.T) {
	s := "\x1b" + "ы"
	// Escape места не занимает, буква — одна ячейка: итого одна.
	if got := cellWidth(s); got != 1 {
		t.Errorf("«ESC» плюс кириллица: ширина %d вместо 1", got)
	}
	if plain := stripANSI(s); plain != "ы" {
		t.Errorf("stripANSI съел часть буквы: %q вместо %q", plain, "ы")
	}
}
