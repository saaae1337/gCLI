package ui

import (
	"strings"
	"testing"

	"gcli/core"
)

// testUI — UI без цвета, пишущий в буфер.
func testUI() (*UI, *strings.Builder) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme:   "ember",
		Unicode: true,
		Color:   false,
		Width:   60,
		Out:     buf,
	})
	return u, buf
}

func TestGlyphsFor(t *testing.T) {
	uni := GlyphsFor("ember", true)
	if !uni.Unicode || uni.TL != "│" {
		t.Errorf("ожидались юникод-глифы «УГЛЯ», получено %q", uni.TL)
	}
	asc := GlyphsFor("ember", false)
	if asc.Unicode || asc.TL != "|" {
		t.Errorf("ожидались ASCII-глифы, получено %q", asc.TL)
	}
	if old := GlyphsFor("orbit", true); old.Mark != "◆" {
		t.Errorf("старые имена тем сводятся к «УГЛЮ», получено %q", old.Mark)
	}
}

func TestBlockRendersFlat(t *testing.T) {
	u, buf := testUI()
	u.Block("строка один\nстрока два", BlockOpts{Title: "Тест", Accent: true})
	out := buf.String()

	for _, want := range []string{"◆", "Тест", "строка один", "строка два"} {
		if !strings.Contains(out, want) {
			t.Errorf("в выводе нет %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(out, bad) {
			t.Errorf("плоский блок не должен рисовать рамку (%q):\n%s", bad, out)
		}
	}
}

func TestBlockWrapsLongLines(t *testing.T) {
	u, buf := testUI()
	long := strings.Repeat("я", 200)
	u.Block(long, BlockOpts{Title: "Длинный"})
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("длинный текст не перенёсся: %d строк", len(lines))
	}
	for i, l := range lines {
		if w := runeLen(StripANSI(l)); w > 60 {
			t.Errorf("строка %d шире терминала (%d): %q", i, w, StripANSI(l))
		}
	}
}

func TestTableAligns(t *testing.T) {
	u, buf := testUI()
	rows := [][]string{
		{"read_file", "read", "Прочитать файл"},
		{"bash", "exec", "Выполнить команду"},
	}
	u.Table([]Column{
		{Title: "инструмент", Width: 12},
		{Title: "кат.", Width: 6},
		{Title: "описание", Width: 20},
	}, rows, BlockOpts{Title: "Инструменты"})

	out := buf.String()
	for _, want := range []string{"read_file", "bash", "read", "exec", "Инструменты"} {
		if !strings.Contains(out, want) {
			t.Errorf("в таблице нет %q", want)
		}
	}
	// Колонки разделяет точка — стиль «УГОЛЬ».
	if !strings.Contains(out, "·") {
		t.Error("в таблице нет разделителей колонок")
	}
}

func TestTableRightAlign(t *testing.T) {
	u, buf := testUI()
	u.Table([]Column{
		{Title: "n", Width: 5, Right: true},
	}, [][]string{{"1"}, {"22"}, {"333"}}, BlockOpts{})
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Данные в строках 3..5 — все одной ширины.
	for i := 3; i < len(lines) && i < 6; i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if runeLen(lines[i]) != runeLen(lines[3]) {
			t.Errorf("строка %d: ширина %d, ожидалось %d: %q", i, runeLen(lines[i]), runeLen(lines[3]), lines[i])
		}
	}
}

func TestDiffBasic(t *testing.T) {
	// Полное совпадение — все операции контекстные.
	ops := Diff("одна\nдве\nтри", "одна\nдве\nтри")
	if len(ops) != 3 {
		t.Fatalf("ожидалось 3 операции, получено %d: %+v", len(ops), ops)
	}
	for _, op := range ops {
		if op.Kind != ' ' {
			t.Errorf("неожиданное изменение: %+v", op)
		}
	}
}

func TestDiffAddRemove(t *testing.T) {
	// «две» → «два»: одно удаление и одно добавление плюс контекст.
	ops := Diff("а\nдве\nв", "а\nдва\nв")
	added, removed := 0, 0
	for _, op := range ops {
		switch op.Kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	if added != 1 || removed != 1 {
		t.Errorf("ожидалось +1/-1, получено +%d/-%d", added, removed)
	}
}

func TestDiffLineNumbers(t *testing.T) {
	ops := Diff("a\nb\nc", "a\nB\nc")
	found := false
	for _, op := range ops {
		if op.Kind == '+' && op.Text == "B" {
			found = true
			if op.New != 2 {
				t.Errorf("номер новой строки = %d, ожидалось 2", op.New)
			}
			if op.Old != 0 {
				t.Errorf("у добавленной строки не должно быть старого номера, получено %d", op.Old)
			}
		}
	}
	if !found {
		t.Error("добавленная строка B не найдена")
	}
}

func TestDiffEmptySides(t *testing.T) {
	if ops := Diff("", "a\nb"); len(ops) != 2 {
		t.Errorf("Diff(\"\", \"a\\nb\") дал %d операций", len(ops))
	}
	if ops := Diff("a\nb", ""); len(ops) != 2 {
		t.Errorf("Diff(\"a\\nb\", \"\") дал %d операций", len(ops))
	}
	if ops := Diff("", ""); len(ops) != 0 {
		t.Errorf("Diff пустых дал %d операций", len(ops))
	}
}

func TestPrintDiffOutput(t *testing.T) {
	u, buf := testUI()
	u.PrintDiff(Diff("a\nb\nc", "a\nB\nc"), DiffOpts{Path: "f.txt"})
	out := buf.String()
	if !strings.Contains(out, "f.txt") {
		t.Errorf("нет пути в выводе:\n%s", out)
	}
	if !strings.Contains(out, "+1") || !strings.Contains(out, "−1") {
		t.Errorf("нет статистики:\n%s", out)
	}
	if !strings.Contains(out, "B") {
		t.Errorf("нет изменённой строки:\n%s", out)
	}
}

func TestDiffNoChanges(t *testing.T) {
	u, buf := testUI()
	u.PrintDiff(Diff("same\ntext", "same\ntext"), DiffOpts{Path: "f.txt"})
	if !strings.Contains(buf.String(), "без изменений") {
		t.Errorf("ожидалось сообщение об отсутствии изменений:\n%s", buf.String())
	}
}

func TestInlineCode(t *testing.T) {
	u, _ := testUI()
	got := u.inline("вызови `read_file` сейчас")
	if !strings.Contains(got, "read_file") {
		t.Errorf("код потерян: %q", got)
	}
	// Внутренние кавычки не должны ломаться.
	got2 := u.inline("`a * b` и `c_d`")
	if !strings.Contains(got2, "a * b") {
		t.Errorf("код с символами искажён: %q", got2)
	}
}

func TestInlineBold(t *testing.T) {
	u, _ := testUI()
	got := u.inline("это **важно** тут")
	if !strings.Contains(got, "важно") {
		t.Errorf("жирный текст потерян: %q", got)
	}
}

func TestInlineNestedCode(t *testing.T) {
	u, _ := testUI()
	// Код не должен интерпретироваться: ``*a*`` остаётся как есть.
	got := u.inline("``**not bold**``")
	if !strings.Contains(got, "**not bold**") {
		t.Errorf("разметка внутри кода обработана неверно: %q", got)
	}
}

func TestRenderHeading(t *testing.T) {
	u, _ := testUI()
	got := u.RenderLine("## Заголовок")
	if !strings.Contains(got, "Заголовок") {
		t.Errorf("заголовок потерян: %q", got)
	}
	if !strings.Contains(got, "─") {
		t.Errorf("подчёркивание заголовка отсутствует: %q", got)
	}
}

func TestRenderList(t *testing.T) {
	u, _ := testUI()
	if got := u.RenderLine("- пункт"); !strings.Contains(got, "пункт") || !strings.Contains(got, "•") {
		t.Errorf("маркированный список: %q", got)
	}
	if got := u.RenderLine("1. пункт"); !strings.Contains(got, "1.") {
		t.Errorf("нумерованный список: %q", got)
	}
}

func TestRenderTask(t *testing.T) {
	u, _ := testUI()
	done := u.RenderLine("- [x] сделано")
	if !strings.Contains(done, "сделано") {
		t.Errorf("чекбокс потерян: %q", done)
	}
}

func TestRenderFence(t *testing.T) {
	u, _ := testUI()
	open := u.RenderLine("```go")
	if !strings.Contains(open, "go") {
		t.Errorf("язык не показан: %q", open)
	}
}

func TestStreamWritesLines(t *testing.T) {
	u, buf := testUI()
	s := u.NewStream()
	s.Write("первая ")
	s.Write("часть\nвторая\nтретья")
	s.End()

	out := buf.String()
	if !strings.Contains(out, "первая часть") {
		t.Errorf("текст не склеен: %q", out)
	}
	if !strings.Contains(out, "вторая") || !strings.Contains(out, "третья") {
		t.Errorf("строки потеряны: %q", out)
	}
}

func TestVisibleWidthIgnoresANSI(t *testing.T) {
	plain := "abc"
	colored := "\033[36mabc\033[0m"
	if visibleWidth(plain) != visibleWidth(colored) {
		t.Errorf("ширина с ANSI: %d, без: %d", visibleWidth(colored), visibleWidth(plain))
	}
}

func TestPadRightWithANSI(t *testing.T) {
	colored := "\033[36mabc\033[0m"
	got := padRight(colored, 10)
	if visibleWidth(got) != 10 {
		t.Errorf("PadRight дал ширину %d, ожидалось 10: %q", visibleWidth(got), got)
	}
}

func TestBar(t *testing.T) {
	u, _ := testUI()
	half := u.Bar(5, 10, 10)
	if !strings.Contains(half, "▬") {
		t.Errorf("нет заполненной части: %q", half)
	}
	full := u.Bar(10, 10, 10)
	if strings.Contains(full, "·") {
		t.Errorf("полная полоса содержит пустую часть: %q", full)
	}
	if u.Bar(0, 0, 10) != "" {
		t.Error("при total=0 полоса должна быть пустой")
	}
}

func TestToolStartEnd(t *testing.T) {
	u, buf := testUI()
	u.ToolStart(ToolCall{Name: "read_file", Args: "main.go", Kind: "read"})
	u.ToolEnd(ToolCall{Name: "read_file", Kind: "read", Detail: "прочитано 100 строк", Status: "ok"})
	out := buf.String()
	if !strings.Contains(out, "read_file") {
		t.Errorf("нет имени инструмента: %q", out)
	}
	if !strings.Contains(out, "main.go") {
		t.Errorf("нет аргумента: %q", out)
	}
	if !strings.Contains(out, "прочитано") {
		t.Errorf("нет результата: %q", out)
	}
}

func TestToolEndFailure(t *testing.T) {
	u, buf := testUI()
	u.ToolEnd(ToolCall{Name: "bash", Kind: "exec", Detail: "команда не найдена", Status: "fail"})
	if !strings.Contains(buf.String(), "команда не найдена") {
		t.Errorf("результат ошибки не показан: %q", buf.String())
	}
}

func TestSpinNoopWithoutColor(t *testing.T) {
	u, buf := testUI()
	u.SpinnerStart("работа")
	u.SpinnerStop()
	if buf.String() != "" {
		t.Errorf("без цвета спиннер не должен ничего писать: %q", buf.String())
	}
}

func TestThemePalette(t *testing.T) {
	// Тема одна: что бы ни запросили — возвращается палитра «УГЛЯ».
	for _, name := range []string{"ember", "mono", "classic", "orbit"} {
		pal := PaletteFor(name)
		if pal.Accent == "" || pal.Muted == "" {
			t.Errorf("палитра %q вырождена", name)
		}
	}
}

func TestStripANSI(t *testing.T) {
	s := "\033[36mтекст\033[0m"
	if got := StripANSI(s); got != "текст" {
		t.Errorf("StripANSI = %q, ожидалось %q", got, "текст")
	}
}

func TestWrap(t *testing.T) {
	lines := wrap("слово "+strings.Repeat("оченьдлинное ", 20), 40)
	for i, l := range lines {
		if runeLen(l) > 40 {
			t.Errorf("строка %d длиннее 40: %q", i, l)
		}
	}
}

func TestRenderTodos(t *testing.T) {
	u, buf := testUI()
	u.RenderTodos(nil)
	if !strings.Contains(buf.String(), "пуст") {
		t.Errorf("пустой список должен сообщать об этом: %q", buf.String())
	}

	buf.Reset()
	u.RenderTodos([]core.Todo{
		{Content: "изучить код", Status: core.TodoCompleted},
		{Content: "написать тест", Status: core.TodoInProgress},
		{Content: "собрать", Status: core.TodoPending},
	})
	// Зачёркивание добавляет combining-символы, поэтому сравниваем по
	// очищенной строке (видимые символы без зачёркивания).
	out := strings.ReplaceAll(buf.String(), "\u0336", "")
	for _, want := range []string{"изучить код", "написать тест", "собрать"} {
		if !strings.Contains(out, want) {
			t.Errorf("в плане нет %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "1/3") {
		t.Errorf("нет счётчика выполненных: %q", out)
	}
}

func TestQuietSuppressesColor(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{Color: true, Unicode: true, Width: 40, Out: buf})
	u.SetQuiet(true)
	got := u.Red("текст")
	if strings.Contains(got, "\033") {
		t.Errorf("в тихом режиме цвета не должны добавляться: %q", got)
	}
}

func TestAutoHelpers(t *testing.T) {
	u, buf := testUI()
	u.AutoOK("src/main.go")
	out := StripANSI(buf.String())
	if !strings.Contains(out, "src/main.go") || !strings.Contains(out, "автопилот") {
		t.Errorf("AutoOK должен показать путь и метку:\n%s", out)
	}
	buf.Reset()
	if tag := StripANSI(u.AutoTag()); tag != "автопилот" {
		t.Errorf("AutoTag = %q, ожидалось %q", tag, "автопилот")
	}
}

func TestStatusLine(t *testing.T) {
	u, buf := testUI()
	u.StatusLine([]StatusItem{
		{Text: "модель", Kind: StatusMuted},
		{Text: "autopilot: all", Kind: StatusErr},
		{Text: "", Kind: StatusMuted},
	})
	out := StripANSI(buf.String())
	if !strings.Contains(out, "модель") || !strings.Contains(out, "autopilot: all") {
		t.Errorf("строка статуса неполна:\n%s", out)
	}
	// Пустые элементы пропускаются, разделитель не двоится.
	if strings.Contains(out, "·  ·") {
		t.Errorf("пустой элемент оставил лишний разделитель:\n%s", out)
	}
}
