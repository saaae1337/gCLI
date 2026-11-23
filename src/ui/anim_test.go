package ui

import (
	"strings"
	"testing"
)

// Анимации появления удалены: строки печатаются как есть. Тесты следят,
// чтобы «проявляющие» методы не оставляли ни перерисовок, ни мусора,
// и печатали ровно тот текст, что им передан.

func revealUI() (*UI, *strings.Builder) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme: themeEmber, Unicode: true, Color: true, Grade: ColorRGB,
		Animations: true, Width: 80, Out: buf,
	})
	return u, buf
}

func TestRevealLinesPrintsAllLinesPlainly(t *testing.T) {
	u, buf := revealUI()
	lines := []string{"первая строка", "вторая строка", "третья строка"}
	u.RevealLines(lines)
	out := buf.String()

	for _, l := range lines {
		if !strings.Contains(StripANSI(out), l) {
			t.Errorf("строка %q не появилась в выводе", l)
		}
	}
	// Никаких перерисовок: строка печатается один раз.
	if strings.Contains(out, "\r\033[K") {
		t.Errorf("перерисовки быть не должно:\n%q", out)
	}
	if strings.Count(strings.TrimRight(out, "\n"), "\n")+1 != len(lines) {
		t.Errorf("напечатано не ровно %d строк:\n%q", len(lines), out)
	}
}

func TestRevealLinesIgnoresAnimationFlag(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme: themeEmber, Unicode: true, Color: true, Grade: ColorRGB,
		Animations: false, Width: 80, Out: buf,
	})
	u.RevealLines([]string{"одна", "две"})
	out := buf.String()
	if strings.Contains(out, "\033[") {
		t.Errorf("управляющих кодов быть не должно:\n%q", out)
	}
}

func TestTypeLinePrintsWholeString(t *testing.T) {
	u, buf := revealUI()
	u.TypeLine("проверка печати")
	out := buf.String()
	if got := StripANSI(strings.TrimRight(out, "\n")); got != "проверка печати" {
		t.Errorf("напечатано %q, ожидалась вся строка", got)
	}
}

func TestRevealLinePrintsColoredStringAsIs(t *testing.T) {
	u, buf := revealUI()
	line := u.Accent("готово")
	u.RevealLine(line)
	out := buf.String()
	if !strings.Contains(out, "готово") {
		t.Errorf("строка потеряна:\n%q", out)
	}
}
