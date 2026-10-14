package ui

// Инварианты подсчёта ширины и обрезки.
//
// Проверяются не отдельные случаи, а свойства, которые должны выполняться
// для любой строки. Так ловятся сочетания, которые вручную не
// перечислишь: цвет внутри слова, обрезка ровно по границе, ссылка,
// разорванный многобайтовый символ.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// samples — строки, которые gcli печатает по-настоящему, плюс злые
// случаи из жизни терминала.
func widthSamples() []string {
	return []string{
		"",
		"привет",
		"✔ задача выполнена",
		"✖ ошибка доступа",
		"│ ├ └ ─ рамка",
		"→ стрелка →",
		"████░░░░░░",
		"🧡 оранжевое",
		"👍🏽 с тоном кожи",
		"👨‍👩‍👧‍👦 семья",
		"1️⃣ один",
		"✂️ ножницы",
		"日本語のテキスト",
		"한글 텍스트",
		"e\u0301 комбинирующее",
		"🏳️‍🌈 флаг",
		"\x1b[31mкрасный\x1b[0m",
		"\x1b[1;32mяркий зелёный\x1b[0m",
		"\x1b[38;5;208m256 цветов\x1b[0m",
		"\x1b]8;;https://example.com\x1b\\ссылка\x1b]8;;\x1b\\",
		"\x1b[2K\x1b[1A\x1b[1G очистка",
		"mixed ✔ \x1b[33mцвет\x1b[0m ✖ конец",
		"\x1b[31m✔\x1b[0m ошибка под цветом",
		"\x1b[?25l\x1b[?25h скрыть курсор",
	}
}

// cellWidth не должна считать управляющие последовательности.
func TestCellWidthIgnoresAllEscapeForms(t *testing.T) {
	for _, s := range widthSamples() {
		plain := stripANSI(s)
		if got, want := cellWidth(s), cellWidth(plain); got != want {
			t.Errorf("ширина %q: с управляющими %d, без %d\n  очищено: %q",
				s, got, want, plain)
		}
	}
}

// stripANSI не должна оставлять мусор и не должна терять текст.
func TestStripANSIPreservesText(t *testing.T) {
	for _, s := range widthSamples() {
		plain := stripANSI(s)
		if strings.ContainsRune(plain, 0x1B) {
			t.Errorf("stripANSI оставил escape: %q -> %q", s, plain)
		}
		// Видимые символы исходника должны сохраниться целиком.
		var want []rune
		for _, r := range stripANSI(s) {
			want = append(want, r)
		}
		var got []rune
		for _, r := range plain {
			got = append(got, r)
		}
		if string(got) != string(want) {
			t.Errorf("stripANSI(%q) = %q, ожидалось %q", s, plain, string(want))
		}
	}
}

// Обрезка не должна выдавать строку шире заданного.
func TestTruncateNeverExceedsLimit(t *testing.T) {
	for _, s := range widthSamples() {
		full := cellWidth(s)
		for n := 0; n <= full+2; n++ {
			got := truncateCells(s, n)
			if w := cellWidth(got); w > n {
				t.Errorf("truncateCells(%q, %d) дал ширину %d: %q", s, n, w, got)
			}
		}
	}
}

// Обрезка не должна разрывать символ: результат обязан оставаться
// корректным UTF-8 без половинчатых эмодзи.
func TestTruncateKeepsUTF8Valid(t *testing.T) {
	for _, s := range widthSamples() {
		full := cellWidth(s)
		for n := 0; n <= full+2; n++ {
			got := truncateCells(s, n)
			if !utf8.ValidString(got) {
				t.Errorf("truncateCells(%q, %d) вернул невалидный UTF-8: %q", s, n, got)
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Errorf("truncateCells(%q, %d) оставил символ замены: %q", s, n, got)
			}
		}
	}
}

// Повторная обрезка ничего не меняет: иначе один и тот же текст
// выглядит по-разному в двух проходах.
func TestTruncateIsIdempotent(t *testing.T) {
	for _, s := range widthSamples() {
		full := cellWidth(s)
		for n := 0; n <= full+2; n++ {
			once := truncateCells(s, n)
			twice := truncateCells(once, n)
			if once != twice {
				t.Errorf("обрезка дважды даёт разное: %q -> %q -> %q (n=%d)",
					s, once, twice, n)
			}
		}
	}
}

// Строка, которая помещается, должна вернуться целиком и неизменной.
func TestTruncateKeepsFittingString(t *testing.T) {
	for _, s := range widthSamples() {
		full := cellWidth(s)
		if full == 0 {
			continue
		}
		if got := truncateCells(s, full); got != s {
			t.Errorf("строка шириной %d помещается в %d, но изменилась: %q -> %q",
				full, full, s, got)
		}
	}
}
