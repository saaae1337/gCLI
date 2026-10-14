package ui

// Фаззинг подсчёта ширины и обрезки.
//
// Эти функции решают, где ляжет каждый символ на экране. Ошибка в любой
// из них не падает, а тихо разъезжает колонки или обрезает строку не там:
// заметить можно только глазами в терминале. Фаззинг находит сочетания
// вроде «цвет ровно на границе обрезки» или «разорванный кластер»
// раньше, чем их найдёт пользователь на CJK-тексте.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzWidthInvariants — инварианты, которые должны держаться на любом
// входе из произвольных байт.
func FuzzWidthInvariants(f *testing.F) {
	seeds := []string{
		"", "привет", "✔ готово", "✖ слом", "日本語", "🧡", "👍🏽",
		"👨‍👩‍👧‍👦", "1️⃣", "e\u0301", "\x1b[31mкрасный\x1b[0m",
		"\x1b]8;;https://example.com\x1b\\ссылка\x1b]8;;\x1b\\",
		"█░░░", "\x1b[38;5;208m256\x1b[0m",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		// 1. Разбор управляющих последовательностей не может оставить
		//    escape в очищенной строке.
		plain := stripANSI(s)
		if strings.ContainsRune(plain, 0x1B) {
			t.Fatalf("stripANSI оставил escape: %q -> %q", s, plain)
		}

		// 2. Цветные последовательности места не занимают.
		if got, want := cellWidth(s), cellWidth(plain); got != want {
			t.Fatalf("ширина %q: с escape %d, без %d", s, got, want)
		}

		// 3. Ширина неотрицательна и не превосходит числа рун
		//    в худшем случае (каждый символ максимум две ячейки).
		w := cellWidth(s)
		if w < 0 {
			t.Fatalf("отрицательная ширина %d для %q", w, s)
		}
		if n := utf8.RuneCountInString(plain); w > 2*n {
			t.Fatalf("ширина %d больше двух ячеек на символ (%d рун) для %q", w, n, s)
		}

		// 4. Починка байтов не должна менять ширину. Иначе колонки
		//    разъедутся именно там, где текст битый. Валидность UTF-8
		//    тут не требуется: ею занимается вывод (UI.put), и требование
		//    «truncateCells вернёт валидный UTF-8» проверяло бы не тот
		//    слой.
		if fixed := cellWidth(strings.ToValidUTF8(s, string(utf8.RuneError))); fixed != w {
			t.Fatalf("починка байтов изменила ширину %d -> %d для %q", w, fixed, s)
		}

		// 5. Обрезка не выходит за лимит.
		for _, n := range []int{0, 1, 2, 5, 10, w} {
			got := truncateCells(s, n)
			if gw := cellWidth(got); gw > n {
				t.Fatalf("truncateCells(%q, %d) дал ширину %d", s, n, gw)
			}
			// Повторная обрезка обязана быть тождеством.
			if again := truncateCells(got, n); again != got {
				t.Fatalf("обрезка не идемпотентна: %q -> %q -> %q (n=%d)", s, got, again, n)
			}
		}

		// 6. Пустая строка и нулевой лимит не падают.
		if got := truncateCells(s, 0); got != "" {
			t.Fatalf("truncateCells(%q, 0) = %q", s, got)
		}
	})
}
