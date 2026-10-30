package wcwidth

import "testing"

// Тесты инвариантов, ради которых пакет существует: ширина составных
// символов должна совпадать с тем, что рисует терминал. Именно на них
// расходились старые движки ui (кластеры) и tui (руны).

func TestStringWidthComposite(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want int
	}{
		{"ascii", "abc", 3},
		{"кириллица", "привет", 6},
		{"CJK", "日本", 4},
		{"флаг", "🇷🇺", 2},
		{"два флага", "🇺🇸🇬🇧", 4},
		{"флаг с соседями", "a🇷🇺b", 4},
		{"семья на ZWJ", "👨‍👩‍👧‍👦", 2},
		{"пара на ZWJ", "👨‍👩", 2},
		{"тон кожи", "👍🏽", 2},
		{"keycap", "1️⃣", 2},
		{"флажок-эмодзи", "✅", 2},
		{"галка текстом", "✔", 1},
		{"галка с VS16", "✔️", 2},
		{"ножницы с FE0E", "✂︎", 1},
		{"звезда", "⭐", 2},
		{"часы", "⌚", 2},
		{"e с диакритикой", "é", 1},
		{"разъединение флагов", "🇷🇺🇩🇪", 4},
		{"emoji+CJK", "🎉日", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StringWidth(c.s); got != c.want {
				t.Errorf("StringWidth(%q) = %d, хочу %d", c.s, got, c.want)
			}
		})
	}
}

func TestStringWidthANSI(t *testing.T) {
	// Escape-последовательности места не занимают.
	if got := StringWidth("\x1b[31mкрасный\x1b[0m"); got != 7 {
		t.Errorf("StringWidth с ANSI = %d, хочу 7", got)
	}
	// Оборванный CSI не съедает текст за собой: управляющий байт внутри
	// последовательности её завершает, хвост печатается текстом.
	if got := StringWidth("\x1b[38;2;\nabc"); got != 3 {
		t.Errorf("оборванный CSI: %d, хочу 3", got)
	}
}

func TestStringWidthInvalidUTF8(t *testing.T) {
	// Оборванная последовательность -> один знак замены -> одна колонка,
	// как реально напечатает терминал.
	if got := StringWidth("日\xe6\x97"); got != 3 {
		t.Errorf("битый UTF-8: %d, хочу 3", got)
	}
}

func TestTruncateNotSplittingClusters(t *testing.T) {
	// Обрезка не оставляет половину эмодзи и не выдаёт лишнюю ширину.
	s := "a👍🏽b"
	if got := Truncate(s, 2); StringWidth(got) > 2 {
		t.Errorf("Truncate выдал шире лимита: %q (%d)", got, StringWidth(got))
	}
	// Хвост кластера не должен остаться сиротой.
	got := Truncate("ab🇷🇺", 3)
	if got[0] != 'a' || got[1] != 'b' {
		t.Fatalf("Truncate испортил начало: %q", got)
	}
	if StringWidth(got) > 3 {
		t.Errorf("шире лимита: %q", got)
	}
}

func TestTruncateEllipsis(t *testing.T) {
	if got := TruncateEllipsis("привет мир", 7); got != "привет…" {
		t.Errorf("TruncateEllipsis = %q", got)
	}
	if got := TruncateEllipsis("коротко", 20); got != "коротко" {
		t.Errorf("короткая строка изменилась: %q", got)
	}
	if got := TruncateEllipsis("длинно", 0); got != "" {
		t.Errorf("нулевой лимит: %q", got)
	}
}

func TestTruncateEllipsisWideBoundary(t *testing.T) {
	// Широкий символ, не помещающийся целиком, отбрасывается весь.
	got := TruncateEllipsis("日本語", 5)
	if StringWidth(got) > 5 {
		t.Errorf("шире лимита: %q (%d)", got, StringWidth(got))
	}
	if got != "日本…" {
		t.Errorf("TruncateEllipsis = %q, хочу «日本…»", got)
	}
}

func TestNextClusterRunes(t *testing.T) {
	// []rune-вариант обязан соглашаться со строковым.
	s := []rune("a🇷🇺b")
	w, size := NextClusterRunes(s, 1)
	if w != 2 || size != 2 {
		t.Errorf("флаг в рунах: w=%d size=%d, хочу 2 и 2", w, size)
	}
	fam := []rune("👨‍👩‍👧‍👦x")
	w, size = NextClusterRunes(fam, 0)
	if w != 2 {
		t.Errorf("семья в рунах: w=%d, хочу 2", w)
	}
	if size != 7 {
		t.Errorf("семья в рунах: size=%d, хочу 7 (4 эмодзи + 3 ZWJ)", size)
	}
	if w, size := NextClusterRunes(fam, len(fam)); w != 0 || size != 0 {
		t.Errorf("за концом: w=%d size=%d, хочу 0 и 0", w, size)
	}
}

func TestRuneWidthBasics(t *testing.T) {
	if RuneWidth('a') != 1 || RuneWidth('日') != 2 || RuneWidth('✔') != 1 {
		t.Error("базовые ширины руны неверны")
	}
	// Продолжения в одиночку места не добавляют (ZWJ — Cf, keycap — Me).
	if RuneWidth(0x200D) != 0 || RuneWidth(0x20E3) != 0 {
		t.Error("продолжения кластера должны быть нулевой ширины")
	}
	// Тон кожи как база кластера — из диапазона isEmoji (2); в кластере
	// он присоединяется к базе и места не добавляет — см. StringWidth.
	if RuneWidth(0x1F3FB) != 2 {
		t.Error("тон кожи как одиночная руна: ожидался вес 2")
	}
}

func TestFirstCluster(t *testing.T) {
	if got := FirstCluster("👍🏽ab"); got != "👍🏽" {
		t.Errorf("FirstCluster = %q", got)
	}
	if got := FirstCluster(""); got != "" {
		t.Errorf("пустая строка: %q", got)
	}
}

// Фаззинг-инвариант: обрезка никогда не выдаёт строку шире лимита и
// всегда продвигается по байтам (нет вечного цикла).
func FuzzTruncate(f *testing.F) {
	f.Add("a👍🏽b🇷🇺日", 3)
	f.Add("\x1b[31mцвет\x1b[0m", 5)
	f.Add("\xe6\x97", 2)
	f.Fuzz(func(t *testing.T, s string, n int) {
		if n < 0 || n > 100 {
			return
		}
		got := Truncate(s, n)
		if StringWidth(got) > n {
			t.Errorf("Truncate(%q, %d) шире лимита: %q", s, n, got)
		}
	})
}

func FuzzStringWidth(f *testing.F) {
	f.Add("🇺🇸🇬🇧👨‍👩‍👧‍👦\xe6")
	f.Add("\x1b]0;title\x07abc")
	f.Fuzz(func(t *testing.T, s string) {
		_ = StringWidth(s) // главное — не зависнуть и не паниковать
	})
}

// TestNextClusterRunesMatchesStringEngine — руновый движок обязан давать те
// же ширины, что строковый: редактор меряет курсор рунами, а терминал
// рисует кластерами. Раньше база после ZWJ теряла ширину в NextClusterRunes
// (в nextCluster её прибавляли), и текст вида «a‍日» съедал две колонки.
func TestNextClusterRunesMatchesStringEngine(t *testing.T) {
	inputs := []string{
		"a‍日",
		"👨‍👩‍👧 один вывод",
		"👍🏽‍日",
		"x‍y‍z конец",
		"🇷🇺‍🇺🇸 флаги",
	}
	for _, s := range inputs {
		rs := []rune(s)
		total, size := 0, 0
		for i := 0; i < len(rs); {
			w, n := NextClusterRunes(rs, i)
			if n == 0 {
				t.Fatalf("%q: кластер на %d нулевой", s, i)
			}
			total += w
			i += n
			size += n
		}
		if want := StringWidth(s); total != want {
			t.Errorf("%q: сумма ширин кластеров %d, StringWidth %d", s, total, want)
		}
		if size != len(rs) {
			t.Errorf("%q: кластеры покрыли %d рун из %d", s, size, len(rs))
		}
	}
}
