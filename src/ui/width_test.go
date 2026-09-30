package ui

import (
	"strings"
	"testing"
	"time"
)

// ---------- Ширина CJK и эмодзи ----------
//
// Дефект был неочевидным: подсчёт в рунах даёт правильный результат для
// кириллицы (1 колонка) и неправильный для CJK и эмодзи (2 колонки). Из-за
// этого padRight дописывал на два пробела меньше, чем нужно, таблицы
// разъезжались, а wrap резал слова посередине.

func TestCellWidthCyrillicIsOne(t *testing.T) {
	if got := cellWidth("привет"); got != 6 {
		t.Errorf("кириллица: %d колонок вместо 6", got)
	}
}

func TestCellWidthCJKIsTwo(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"иероглифы", "日本", 4},
		{"хирагана", "あい", 4},
		{"хангль", "한글", 4},
		{"полноширинная скобка", "（", 2},
	}
	for _, c := range cases {
		if got := cellWidth(c.in); got != c.want {
			t.Errorf("%s: %d колонок вместо %d (%q)", c.name, got, c.want, c.in)
		}
	}
}

func TestCellWidthEmojiIsTwo(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"👍", 2},
		{"🚀", 2},
		{"✅", 2},
		{"😀", 2},
		{"🧡", 2},
	}
	for _, c := range cases {
		if got := cellWidth(c.in); got != 2 {
			t.Errorf("%q: %d колонок вместо 2", c.in, got)
		}
	}
}

func TestCellWidthIgnoresANSI(t *testing.T) {
	if got := cellWidth("\033[31mпривет\033[0m"); got != 6 {
		t.Errorf("цветной текст: %d колонок вместо 6", got)
	}
}

func TestCellWidthCombiningMarks(t *testing.T) {
	// "e" + U+0301 (комбинирующее ударение) занимает одну колонку.
	if got := cellWidth("é"); got != 1 {
		t.Errorf("комбинирующий диакритик: %d колонок вместо 1", got)
	}
}

// TestPadRightFillsWideChars — главный симптом дефекта: дополнение должно
// считать колонки, а не руны, иначе строка с CJK короче, чем задано.
func TestPadRightFillsWideChars(t *testing.T) {
	for _, s := range []string{"日本", "привет", "👍", "日本 👍"} {
		padded := padRight(s, 10)
		if got := cellWidth(padded); got != 10 {
			t.Errorf("padRight(%q, 10) = %d колонок вместо 10: %q", s, got, padded)
		}
	}
}

func TestPadLeftFillsWideChars(t *testing.T) {
	for _, s := range []string{"日本", "👍", "привет"} {
		padded := padLeft(s, 10)
		if got := cellWidth(padded); got != 10 {
			t.Errorf("padLeft(%q, 10) = %d колонок вместо 10", s, got)
		}
	}
}

func TestCenterFillsWideChars(t *testing.T) {
	for _, s := range []string{"日本", "👍", "привет", ""} {
		got := cellWidth(center(s, 12))
		if got != 12 {
			t.Errorf("center(%q, 12) = %d колонок вместо 12", s, got)
		}
	}
}

// TestTruncateCellsNeverExceedsWidth — обрезка не должна выдавать строку
// шире заданной: разрез по рунам оставлял «日本» шире 3 колонок.
func TestTruncateCellsNeverExceedsWidth(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"日本語テキスト", 5},
		{"👍👍👍👍", 3},
		{"привет", 4},
		{"mixed 日本 text", 8},
	}
	for _, c := range cases {
		got := truncateCells(c.s, c.want)
		if w := cellWidth(got); w > c.want {
			t.Errorf("truncateCells(%q, %d) дал %d колонок", c.s, c.want, w)
		}
	}
}

func TestTruncateCellsEllFitsWithMarker(t *testing.T) {
	got := truncateCellsEll("日本語テキスト", 5)
	if w := cellWidth(got); w > 5 {
		t.Errorf("строка с многоточием шире лимита: %d колонок (%q)", w, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("нет метки обрезки: %q", got)
	}
}

// TestWrapCJKRespectsWidth — перенос по ширине обязан считать колонки:
// 20 японских иероглифов — это 40 колонок, а не 20.
func TestWrapCJKRespectsWidth(t *testing.T) {
	const width = 20
	lines := wrap(strings.Repeat("日本語", 20), width)
	if len(lines) == 0 {
		t.Fatal("wrap вернул пустой результат")
	}
	for i, l := range lines {
		if w := cellWidth(l); w > width {
			t.Errorf("строка %d шире %d колонок: %d (%q)", i, width, w, l)
		}
	}
	// Текст не должен теряться: склеиваем обратно.
	if joined := strings.Join(lines, ""); joined != strings.Repeat("日本語", 20) {
		t.Errorf("перенос потерял символы: %d рун вместо %d",
			len([]rune(joined)), len([]rune(strings.Repeat("日本語", 20))))
	}
}

// TestWrapLongWordWithWideChars — слово длиннее строки режется по колонкам,
// а не по рунам, и не зацикливается на символе шире самой строки.
func TestWrapLongWordWithWideChars(t *testing.T) {
	// Ширина 11: символ в 2 колонки помещается, но один «👍» — нет.
	lines := wrap("👍👍👍👍👍👍👍", 11)
	if len(lines) == 0 {
		t.Fatal("wrap вернул пустой результат")
	}
	for i, l := range lines {
		if w := cellWidth(l); w > 11 {
			t.Errorf("строка %d шире 11 колонок: %d (%q)", i, w, l)
		}
	}
	if joined := strings.Join(lines, ""); len([]rune(joined)) != 7 {
		t.Errorf("потеряны эмодзи: осталось %d из 7", len([]rune(joined)))
	}
}

func TestWrapTerminatesOnWideCharNarrowWidth(t *testing.T) {
	// Защита от бесконечного цикла: ширина меньше символа.
	done := make(chan []string, 1)
	go func() { done <- wrap("👍👍", 1) }()
	select {
	case lines := <-done:
		if len(lines) == 0 {
			t.Error("ожидался хотя бы один фрагмент")
		}
	case <-time.After(time.Second):
		t.Fatal("wrap зациклился на символе шире строки")
	}
}

// TestCellWidthGraphemeClusters — эмодзи, собранные из нескольких рун, рисуются
// терминалом как одна картинка, а значит занимают одну ширину. Считать руны
// здесь нельзя: иначе «🇷🇺» растягивал колонку на 8 символов и ломал рамки
// таблиц. Каждый кластер обязан весить ровно 2 колонки.
func TestCellWidthGraphemeClusters(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"флаг из двух индикаторов", "🇷🇺", 2},
		{"два флага подряд", "🇺🇸🇬🇧", 4},
		{"семья через ZWJ", "👨‍👩‍👧‍👦", 2},
		{"девушка с ноутбуком", "👩‍💻", 2},
		{"эмодзи с тоном кожи", "👍🏽", 2},
		{"keycap", "1️⃣", 2},
		{"флаг внутри текста", "a🇷🇺b", 4},
	}
	for _, c := range cases {
		if got := cellWidth(c.in); got != c.want {
			t.Errorf("%s: %d колонок вместо %d (%q)", c.name, got, c.want, c.in)
		}
	}
}

// TestCellWidthVariationSelector — FE0F просит нарисовать символ как эмодзи,
// FE0E — как обычный текст. Один и тот же «✂» поэтому весит то 2, то 1.
func TestCellWidthVariationSelector(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"текстовый вариант FE0E", "✂︎", 1},
		{"эмодзи-вариант FE0F", "✂️", 2},
	}
	for _, c := range cases {
		if got := cellWidth(c.in); got != c.want {
			t.Errorf("%s: %d колонок вместо %d (%q)", c.name, got, c.want, c.in)
		}
	}
}

// TestCellWidthANSIConsecutive — подряд идущие управляющие последовательности
// (сброс цвета плюс следующий SGR) не должны сбивать считыватель с толку:
// возвращаемая skipANSI длина считается от позиции входа, а не от начала строки.
func TestCellWidthANSIConsecutive(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"один SGR", "\033[36mabc\033[0m", 3},
		{"два подряд", "\033[0m\033[1mabc", 3},
		{"сброс перед буквой", "a\033[0mb\033[0mc", 3},
		{"OSC с BEL", "\033]0;title\007abc", 3},
	}
	for _, c := range cases {
		if got := cellWidth(c.in); got != c.want {
			t.Errorf("%s: %d колонок вместо %d (%q)", c.name, got, c.want, c.in)
		}
	}
}

// TestStripANSIKeepsText — вырезание последовательностей не должно терять текст
// и не должно зависать на неполной последовательности в конце строки.
func TestStripANSIKeepsText(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"\033[31mпривет\033[0m", "привет"},
		{"\033]0;title\007abc", "abc"},
		{"no escapes", "no escapes"},
		{"dangling \033[", "dangling "},
	}
	for _, c := range cases {
		if got := stripANSI(c.in); got != c.want {
			t.Errorf("stripANSI(%q) = %q вместо %q", c.in, got, c.want)
		}
	}
}

// TestTruncateCellsKeepsClustersWhole — обрезка не должна оставлять половину
// кластера: символ, который не помещается, уходит целиком вместе с ZWJ,
// модификаторами и variation selectors.
func TestTruncateCellsKeepsClustersWhole(t *testing.T) {
	got := truncateCells("🇺🇸🇬🇧", 3)
	if w := cellWidth(got); w > 3 {
		t.Errorf("обрезанная строка шире лимита: %d колонок (%q)", w, got)
	}
	if w := cellWidth(got); w != 2 {
		t.Errorf("обрезано %d колонок вместо 2: первый флаг должен уместиться целиком", w)
	}
	if strings.ContainsRune(got, '🇬') {
		t.Errorf("второй флаг попал в результат по кускам: %q", got)
	}
}

// TestWrapEmojiClusters — перенос по ширине не должен разрывать ZWJ-семью и
// терять её части: слово шире строки режется по границам кластеров.
func TestWrapEmojiClusters(t *testing.T) {
	const src = "👨‍👩‍👧‍👦"
	const width = 10 // wrap поднимает меньшую ширину до 10
	lines := wrap(strings.Repeat(src, 6), width)
	if len(lines) == 0 {
		t.Fatal("wrap вернул пустой результат")
	}
	for i, l := range lines {
		if w := cellWidth(l); w > width {
			t.Errorf("строка %d шире %d колонок: %d (%q)", i, width, w, l)
		}
	}
	if joined := strings.Join(lines, ""); joined != strings.Repeat(src, 6) {
		t.Errorf("перенос потерял части эмодзи: %q", joined)
	}
}

// TestTableWidthsUsesColumns — автоширина таблицы обязана считать колонки,
// иначе колонка с CJK вдвое шире остальных.
func TestTableWidthsUsesColumns(t *testing.T) {
	// Заголовок короче японской ячейки: ширину колонки обязано задавать
	// содержимое, посчитанное в колонках терминала.
	cols := []Column{{Title: "x"}}
	rows := [][]string{{"日本"}, {"ok"}}
	w := tableWidths(cols, rows)
	if w[0] != 4 {
		t.Errorf("ширина колонки %d вместо 4 (иероглифы занимают по 2 колонки)", w[0])
	}

	// И обратный случай: подсчёт в рунах дал бы 10 вместо 4, если бы в
	// таблице смешались кириллица и CJK.
	cols = []Column{{Title: "инструмент"}}
	rows = [][]string{{"日本"}, {"write_file"}}
	if w := tableWidths(cols, rows); w[0] != 10 {
		t.Errorf("ширина %d вместо 10 (заголовок длиннее ячеек)", w[0])
	}
}
