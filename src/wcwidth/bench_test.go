package wcwidth

import "testing"

// Бенчмарки ширины — базовая линия для работ по аллокациям. Задача
// итерации 0.9.0.2: держать измеримым то, что раньше мерили на глаз.
//
// Наборы строк покрывают все ветки вычислителя: чистый ASCII (быстрый
// путь), кириллица (одинарная ширина вне ASCII), CJK (двойная),
// составные эмодзи (кластеры ZWJ/флаги/тон кожи), строка с ANSI-кодами
// (путь skipANSI, самый частый в ленте TUI).

var benchCases = []struct {
	name string
	s    string
}{
	{"ascii", "The quick brown fox jumps over the lazy dog 0123456789"},
	{"кириллица", "Съешь же ещё этих мягких французских булок, да выпей чаю"},
	{"CJK", "日本語のテキストは全角で二列を占める"},
	{"эмодзи", "👨‍👩‍👧‍👦 👍🏽 1️⃣ 🇷🇺 ✅ — составные и одиночные"},
	{"ANSI", "\x1b[1m\x1b[38;2;255;180;80mЗаголовок\x1b[0m с \x1b[3mкурсом\x1b[0m и без"},
	{"смешанная", "Ответ: 42 (\x1b[32mок\x1b[0m), привет мир — 日本 test"},
}

func BenchmarkStringWidth(b *testing.B) {
	for _, tc := range benchCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = StringWidth(tc.s)
			}
		})
	}
}

func BenchmarkStripANSI(b *testing.B) {
	s := benchCases[4].s
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = StripANSI(s)
	}
}

func BenchmarkTruncate(b *testing.B) {
	s := benchCases[5].s
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Truncate(s, 20)
	}
}

func BenchmarkStringWidthLongLine(b *testing.B) {
	// Реалистичный худший случай: абзац ответа модели с примесями —
	// примерно 400 рун, из них ~10% широких и ~5% управляющих.
	s := benchCases[5].s
	long := ""
	for i := 0; i < 20; i++ {
		long += s + " "
	}
	b.SetBytes(int64(len(long)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = StringWidth(long)
	}
}
