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

// Раньше isEmoji считал эмодзи весь блок U+1F000–U+1F2FF и весь блок
// стрелок U+2B00–U+2BFF. В них много узких символов, и каждая такая
// строка получала лишнюю колонку. Широкими остаются только реальные
// эмодзи, в том числе старые символы вроде ⬛ и 🀄.
func TestCellWidthMixedBlocks(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		want int
	}{
		{"стрелка U+2B04", 0x2B04, 1},
		{"цифра U+1F100", 0x1F100, 1},
		{"символ U+1FB00", 0x1FB00, 1},
		{"квадрат U+2B1B", 0x2B1B, 2},
		{"звезда U+2B50", 0x2B50, 2},
		{"круг U+2B55", 0x2B55, 2},
		{"масть U+1F004", 0x1F004, 2},
		{"джокер U+1F0CF", 0x1F0CF, 2},
	}
	for _, c := range cases {
		if got := cellWidth(string(c.r)); got != c.want {
			t.Errorf("%s: ширина %d вместо %d", c.name, got, c.want)
		}
	}
}

// CSI заканчивается не только буквой: у «ESC[3~» (Delete) финал — тильда.
// Прежний разбор ждал букву и съедал текст после последовательности.
func TestCSITildeDoesNotEatText(t *testing.T) {
	s := "\033[3~abc"
	if got := cellWidth(s); got != 3 {
		t.Errorf("ширина %d вместо 3: %q", got, s)
	}
	if got := stripANSI(s); got != "abc" {
		t.Errorf("stripANSI = %q вместо %q", got, "abc")
	}
}

// Сброс цвета, отрезанный вместе с хвостом строки, обязан быть дописан:
// иначе терминал остаётся в красном и красит приглашение и следующий вывод.
func TestTruncateCellsClosesColor(t *testing.T) {
	s := "\033[31m" + strings.Repeat("я", 10) + "\033[0m"
	got := truncateCells(s, 4)
	if !strings.HasSuffix(got, cReset) {
		t.Errorf("после обрезки нет сброса цвета: %q", got)
	}
	if w := cellWidth(got); w != 4 {
		t.Errorf("ширина %d вместо 4: %q", w, got)
	}
	if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("обрезка испортила UTF-8: %q", got)
	}
}

// Недописанная escape-последовательность в хвосте не должна попасть
// в вывод: сброс цвета, приклеенный к «ESC[3», превращал остаток
// последовательности в обычный текст и разъезжалась ширина.
func TestTruncateCellsDropsIncompleteEscape(t *testing.T) {
	got := truncateCells("ab\033[3", 2)
	if strings.ContainsRune(got, 0x1B) {
		t.Errorf("в обрезанной строке остался escape: %q", got)
	}
	if w := cellWidth(got); w != 2 {
		t.Errorf("ширина %d вместо 2: %q", w, got)
	}
}

// Оборванная CSI перед обычным текстом не должна уносить этот текст:
// разборщик считает ESC[ съеденным, а следующий байт — уже содержимым.
func TestTruncateCellsKeepsTextAfterBrokenCSI(t *testing.T) {
	got := truncateCells("x\033[\uFFFDabc", 100)
	if !strings.Contains(got, "abc") {
		t.Errorf("текст после оборванной CSI потерян: %q", got)
	}
	if w := cellWidth(got); w != 5 {
		t.Errorf("ширина %d вместо 5: %q", w, got)
	}
}

// StripANSI обязан понимать все виды CSI и OSC, а не только SGR с «m»:
// «ESC[2K» очищает строку и заканчивается буквой K, OSC — BEL или ESC \.
func TestStripANSINonSGR(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"\033[2Kabc", "abc"},
		{"\033[3~abc", "abc"},
		{"\033]0;title\033\\abc", "abc"},
		{"\033]8;;https://example.com\007ссылка", "ссылка"},
	}
	for _, c := range cases {
		if got := stripANSI(c.in); got != c.want {
			t.Errorf("stripANSI(%q) = %q вместо %q", c.in, got, c.want)
		}
	}
}

// ZWJ перед ESC не должен втягивать escape внутрь кластера: иначе ширина
// строки расходится с очищенным текстом ровно на один символ («00000» +
// ZWJ + ESC + «0» считалась как 6 против 5 после чистки). Нашлось
// фаззингом FuzzWidthInvariants на CI.
func TestZWJBeforeESCBreaksCluster(t *testing.T) {
	for _, s := range []string{
		"00000\u200d\x1b0",
		"\u200d\x1b[3;2uхвост",
		"привет\u200d\x1b]8;;https://example.com\x1b\\ссылка",
	} {
		if got, want := cellWidth(s), cellWidth(stripANSI(s)); got != want {
			t.Errorf("ширина %q: с escape %d, без %d", s, got, want)
		}
	}
}
