package ui

import (
	"strings"
	"testing"
	"time"
)

// uiAt — UI с заданным уровнем цвета, пишущий в буфер.
func uiAt(grade ColorLevel, width int) (*UI, *strings.Builder) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme:   "ember",
		Unicode: true,
		Color:   grade != ColorNone,
		Grade:   grade,
		Width:   width,
		Out:     buf,
	})
	return u, buf
}

func TestTitleFlatStyle(t *testing.T) {
	u, buf := uiAt(ColorNone, 60)
	u.Title("Заголовок")
	out := StripANSI(buf.String())
	if !strings.Contains(out, "◆") || !strings.Contains(out, "Заголовок") {
		t.Errorf("заголовок должен быть помечен ромбом:\n%s", out)
	}
	if strings.Contains(out, "╭") || strings.Contains(out, "┌") {
		t.Errorf("рамка в заголовке не нужна:\n%s", out)
	}
}

func TestRailKeepsResultAligned(t *testing.T) {
	u, buf := uiAt(ColorNone, 70)
	u.ToolStart(ToolCall{Name: "bash", Args: "go test ./...", Kind: "exec"})
	u.ToolEnd(ToolCall{
		Name: "bash", Kind: "exec",
		Detail:  "очень длинный текст результата, который должен быть обрезан по ширине терминала",
		Status:  "fail",
		Elapsed: 12 * time.Second,
		Lines:   42,
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	for i, l := range lines {
		if w := visibleWidth(l); w > 70 {
			t.Errorf("строка %d шире терминала (%d > 70): %q", i, w, l)
		}
	}
	// Вызов и результат связаны «зубцом» на том же поле.
	if !strings.Contains(StripANSI(lines[1]), u.emberTickOf()) {
		t.Errorf("в строке результата нет зубца: %q", lines[1])
	}
}

// Имя инструмента и аргументы с CJK меряются в колонках: расчёт по
// рунам оставлял аргументам лишнее место и строка уезжала за край.
func TestRailCJKWidth(t *testing.T) {
	u, buf := uiAt(ColorNone, 40)
	name := strings.Repeat("日", 5)
	u.ToolStart(ToolCall{
		Name: name,
		Args: strings.Repeat("本", 30),
		Kind: "read",
	})
	u.ToolEnd(ToolCall{
		Name:   name,
		Kind:   "read",
		Detail: strings.Repeat("結", 30),
		Status: "fail",
	})
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if w := cellWidth(line); w > 40 {
			t.Errorf("строка рельса шире 40 колонок (%d): %q", w, line)
		}
	}
}

// NO_COLOR сильнее явного Color: true: вызывающий может включить цвет по
// умолчанию, но среда уже попросила его не печатать.
func TestNoColorDisablesForcedColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	buf := &strings.Builder{}
	u := New(Options{Unicode: true, Color: true, Width: 40, Out: buf})

	if got := u.Red("текст"); strings.ContainsRune(got, 0x1B) {
		t.Errorf("NO_COLOR не отключил цвет: %q", got)
	}
	u.Ok("готово")
	if strings.ContainsRune(buf.String(), 0x1B) {
		t.Errorf("в выводе остались escape-коды: %q", buf.String())
	}
	if got := u.ColorLevel(); got != ColorNone {
		t.Errorf("уровень цвета %v вместо none", got)
	}
}

// TERM=dumb — терминал без управления: цвета и escape-коды в него писать
// нельзя, даже если Windows-ветка по умолчанию считает иначе.
func TestTermDumbDisablesColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM", "dumb")
	t.Setenv("WT_SESSION", "")
	t.Setenv("ConEmuANSI", "")
	t.Setenv("TERM_PROGRAM", "")
	if got := detectColorLevel(); got != colorNone {
		t.Errorf("TERM=dumb дал уровень %v вместо none", got)
	}
}

func TestRailPushPopIndents(t *testing.T) {
	u, buf := uiAt(ColorNone, 60)
	u.ToolStart(ToolCall{Name: "read_file", Args: "a.go", Kind: "read"})
	u.RailPush()
	u.ToolStart(ToolCall{Name: "bash", Args: "go build", Kind: "exec"})
	u.RailPop()
	u.ToolStart(ToolCall{Name: "edit_file", Args: "b.go", Kind: "write"})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// Во вложенном вызове появляется дополнительный отступ с рельсом.
	if !strings.HasPrefix(StripANSI(lines[1]), "  │ ") {
		t.Errorf("во вложенном вызове нет дополнительного рельса:\n%s", buf.String())
	}
	if !strings.HasPrefix(StripANSI(lines[0]), "  ◆") {
		t.Errorf("базовый вызов должен начинаться с отступа 2:\n%s", buf.String())
	}
	// После выхода из вложенности отступ возвращается.
	if strings.HasPrefix(StripANSI(lines[2]), "  │ ") {
		t.Errorf("после RailPop отступ не восстановился:\n%s", buf.String())
	}
}

func TestRailEndChainCloses(t *testing.T) {
	u, buf := uiAt(ColorNone, 60)
	u.ToolStart(ToolCall{Name: "grep", Args: "TODO", Kind: "read"})
	u.RailEndChain("готово")
	out := buf.String()
	if !strings.Contains(StripANSI(out), "⎿") {
		t.Errorf("серия вызовов должна закрываться зубцом:\n%s", out)
	}
}

func TestStatusGlyphs(t *testing.T) {
	u, _ := uiAt(ColorNone, 60)
	cases := map[string]string{
		"ok":     "✔",
		"fail":   "✖",
		"denied": "⊘",
		"skip":   "⊘",
	}
	for status, want := range cases {
		got, color := u.emberStatusGlyph(status)
		if got != want {
			t.Errorf("статус %q: глиф %q, ожидался %q", status, got, want)
		}
		if color == "" {
			t.Errorf("статус %q: пустой цвет", status)
		}
	}
}

func TestColorLevelDetection(t *testing.T) {
	cases := []struct {
		in   string
		want ColorLevel
	}{
		{"16", Color16},
		{"ansi", Color16},
		{"256", Color256},
		{"xterm-256color", Color256},
		{"truecolor", ColorRGB},
		{"24bit", ColorRGB},
		{"none", ColorNone},
		{"off", ColorNone},
	}
	for _, c := range cases {
		got, ok := ParseColorLevel(c.in)
		if !ok {
			t.Errorf("ParseColorLevel(%q) не распознал значение", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("ParseColorLevel(%q) = %v, ожидалось %v", c.in, got, c.want)
		}
	}
	// auto и пустое значение означают «определить автоматически».
	for _, s := range []string{"", "auto"} {
		if _, ok := ParseColorLevel(s); ok {
			t.Errorf("ParseColorLevel(%q) должен возвращать ok=false", s)
		}
	}
}

func TestColorLevelApplied(t *testing.T) {
	for _, want := range []ColorLevel{Color16, Color256, ColorRGB} {
		u, _ := uiAt(want, 60)
		if u.ColorLevel() != want {
			t.Errorf("уровень цвета = %v, ожидалось %v", u.ColorLevel(), want)
		}
	}
}

func TestGradientOnlyWithRichColor(t *testing.T) {
	// Без цвета градиент — обычный текст.
	u, _ := uiAt(ColorNone, 60)
	if got := u.Gradient("текст", [3]int{1, 2, 3}, [3]int{4, 5, 6}); got != "текст" {
		t.Errorf("без цвета градиент должен вернуть исходный текст, получено %q", got)
	}
	// В truecolor — escape-последовательности с настоящим градиентом.
	u, _ = uiAt(ColorRGB, 60)
	got := u.Gradient("abc", [3]int{255, 0, 0}, [3]int{0, 0, 255})
	if !strings.Contains(got, "\033[38;2;") {
		t.Errorf("ожидался truecolor-код, получено %q", got)
	}
	if StripANSI(got) != "abc" {
		t.Errorf("градиент изменил текст: %q", StripANSI(got))
	}
}

func TestCodeFenceShowsLanguage(t *testing.T) {
	u, _ := uiAt(ColorRGB, 70)
	open := u.RenderLine("```go")
	if !strings.Contains(StripANSI(open), "go") {
		t.Errorf("язык не показан: %q", open)
	}
	if !strings.Contains(StripANSI(open), "◆") {
		t.Errorf("блок кода должен быть помечен ромбом: %q", open)
	}
}

func TestThemeColorsOverride(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme: "classic", Unicode: true, Color: true, Grade: ColorRGB,
		Width: 60, Out: buf,
		Colors: ThemeColors{Accent: "#ff00aa"},
	})
	if !strings.Contains(u.Pal().Accent, "255;0;170") {
		t.Errorf("пользовательский цвет акцента не применён: %q", u.Pal().Accent)
	}
	// Некорректный hex игнорируем, палитра остаётся рабочей.
	u2 := New(Options{
		Theme: "classic", Unicode: true, Color: true, Grade: ColorRGB,
		Width: 60, Out: buf, Colors: ThemeColors{Accent: "не цвет"},
	})
	if u2.Pal().Accent == "" {
		t.Error("некорректный hex не должен обнулять акцент")
	}
}

func TestRGBTo256Mapping(t *testing.T) {
	// Чистые цвета должны попадать в куб xterm-256.
	if got := rgbTo256([3]int{255, 0, 0}); got != 196 {
		t.Errorf("rgbTo256(255,0,0) = %d, ожидалось 196", got)
	}
	if got := rgbTo256([3]int{0, 0, 0}); got != 16 {
		t.Errorf("rgbTo256(0,0,0) = %d, ожидалось 16", got)
	}
	// Белый — либо куб, либо верх серой шкалы.
	if got := rgbTo256([3]int{255, 255, 255}); got != 231 {
		t.Errorf("rgbTo256(255,255,255) = %d, ожидалось 231", got)
	}
}

func TestNearestBasicAlwaysColored(t *testing.T) {
	// Даже при запросе 16-цветного уровня пользовательский цвет
	// должен превратиться в один из базовых ANSI-кодов.
	c := nearestBasic([3]int{255, 0, 170})
	if c == "" {
		t.Fatal("ближайший базовый цвет не найден")
	}
	if !strings.HasPrefix(c, "\033[") {
		t.Errorf("ожидался ANSI-код, получено %q", c)
	}
}

func firstRunes(s string) string {
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120])
	}
	return s
}
