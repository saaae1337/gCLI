package ui

import (
	"strings"
	"testing"
)

// emberUI — UI в теме «УГОЛЬ» без цвета, пишущий в буфер.
func emberUI() (*UI, *strings.Builder) {
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

// emberASCIIUI — то же, но в ASCII-режиме (терминалы без юникода).
func emberASCIIUI() (*UI, *strings.Builder) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme:   "ember",
		Unicode: false,
		Color:   false,
		Width:   60,
		Out:     buf,
	})
	return u, buf
}

func TestNormalizeThemeEmber(t *testing.T) {
	for _, th := range []string{"ember", "coal", "clay", "flat", "EMBER", " Ember ",
		"orbit", "claude", "classic", "nord", "matrix", "mono", "", "unknown"} {
		if got := normalizeTheme(th); got != themeEmber {
			t.Errorf("normalizeTheme(%q) = %q, ожидалось %q — стиль один, старые имена сводятся к канону", th, got, themeEmber)
		}
	}
}

// Глифы «УГЛА»: ромб-маркер и «зубец» результата, ASCII-фолбэк на всё.
func TestEmberGlyphs(t *testing.T) {
	g := emberGlyphs()
	if g.Mark != "◆" {
		t.Errorf("маркер юникода = %q, ожидался ромб", g.Mark)
	}
	if g.Tick != "⎿" {
		t.Errorf("зубец юникода = %q, ожидался ⎿", g.Tick)
	}
	if g.Star != "◆" {
		t.Errorf("событийный маркер = %q, ожидался ромб", g.Star)
	}
	a := emberASCIIGlyphs()
	if strings.ContainsAny(a.Mark+a.Tick, "◆⎿") {
		t.Errorf("ASCII-набор не должен содержать юникод-ромбы: mark=%q tick=%q", a.Mark, a.Tick)
	}
	if a.Mark != "*" {
		t.Errorf("ASCII-маркер = %q, ожидался '*'", a.Mark)
	}
}

// В ASCII-режиме «зубец» не должен оставлять юникод-кракозябру.
func TestEmberTickASCII(t *testing.T) {
	u, _ := emberASCIIUI()
	if tick := u.emberTickOf(); strings.ContainsRune(tick, '⎿') {
		t.Errorf("ASCII-зубец содержит юникод: %q", tick)
	}
	u2, _ := emberUI()
	if tick := u2.emberTickOf(); tick != "⎿" {
		t.Errorf("юникод-зубец = %q, ожидался ⎿", tick)
	}
}

// GlyphsFor/SpinnerFor обязаны возвращать набор «УГЛА» по теме.
func TestEmberGlyphsFor(t *testing.T) {
	g := GlyphsFor("ember", true)
	if g.Mark != "◆" {
		t.Errorf("GlyphsFor(ember) маркер = %q", g.Mark)
	}
	if GlyphsFor("coal", false).Unicode {
		t.Error("алиас «coal» без юникода должен вернуть ASCII-набор")
	}
	sp := SpinnerFor("ember", true)
	if len(sp) == 0 || (sp[0] != "↻" && sp[0] != "↺") {
		t.Errorf("спиннер «УГЛА» = %v, ожидались ↻/↺", sp)
	}
}

// Палитра на 16 цветах не вырождается.
func TestEmberPaletteAt16(t *testing.T) {
	pal := upgradePalette(PaletteFor("ember"), "ember", color16)
	if pal.Accent == "" {
		t.Error("на 16 цветах акцент должен сохраниться")
	}
	if pal.Muted == "" {
		t.Error("на 16 цветах приглушённый цвет должен сохраниться")
	}
}

// Блок «УГЛА»: ромб в шапке, тело со сдвигом, без рамок.
func TestEmberBlock(t *testing.T) {
	u, buf := emberUI()
	u.Block("строка один\nстрока два", BlockOpts{Title: "Заголовок"})
	out := StripANSI(buf.String())
	if !strings.Contains(out, "◆") {
		t.Errorf("шапка блока должна начинаться с ромба:\n%s", out)
	}
	if !strings.Contains(out, "Заголовок") || !strings.Contains(out, "строка один") {
		t.Errorf("блок потерял содержимое:\n%s", out)
	}
	if strings.Contains(out, "╭") || strings.Contains(out, "┌") {
		t.Errorf("плоской теме рамки не нужны:\n%s", out)
	}
}

// Таблица «УГЛА»: колонки разделяет точка.
func TestEmberTable(t *testing.T) {
	u, buf := emberUI()
	u.Table([]Column{{Title: "имя", Width: 10}, {Title: "год", Width: 6, Right: true}},
		[][]string{{"uno", "1"}, {"dos", "2"}}, BlockOpts{Title: "Т"})
	out := StripANSI(buf.String())
	if !strings.Contains(out, "·") {
		t.Errorf("колонки должны разделяться точкой:\n%s", out)
	}
	if !strings.Contains(out, "uno") || !strings.Contains(out, "dos") {
		t.Errorf("таблица потеряла строки:\n%s", out)
	}
}

// Размышления: спиннер до первого куска, затем маркер события.
func TestEmberThinking(t *testing.T) {
	u, buf := emberUI()
	u.ThinkStart()
	u.ThinkChunk("мысль")
	u.ThinkEnd()
	out := StripANSI(buf.String())
	if !strings.Contains(out, "размышления") {
		t.Errorf("нет подписи размышлений:\n%s", out)
	}
	if !strings.Contains(out, "мысль") {
		t.Errorf("кусочек размышлений потерян:\n%s", out)
	}
}

// Рельс и «зубец» результата в теме «УГОЛЬ».
func TestEmberRail(t *testing.T) {
	u, buf := emberUI()
	u.RailStart(ToolCall{Name: "read_file", Args: "x.go", Kind: "read"})
	u.RailEnd(ToolCall{Name: "read_file", Args: "x.go", Kind: "read"})
	out := StripANSI(buf.String())
	if !strings.Contains(out, "read_file") {
		t.Errorf("нет вызова инструмента:\n%s", out)
	}
	if !strings.Contains(out, "⎿") {
		t.Errorf("результат должен идти с «зубцом»:\n%s", out)
	}
}

// Приглашение: ромб акцентом в агентном режиме.
func TestEmberPrompt(t *testing.T) {
	u, buf := emberUI()
	u.Prompt(true)
	out := StripANSI(buf.String())
	if !strings.Contains(out, "◆") {
		t.Errorf("приглашение должно начинаться с ромба:\n%s", out)
	}
}

// Появление строк работает в «УГЛЕ» — но требует цвета.
func TestEmberRevealVsAnimate(t *testing.T) {
	// Появление строк требует цвета (затуханию нечему гаситься без него).
	buf := &strings.Builder{}
	u := New(Options{Theme: "ember", Unicode: true, Color: true, Animations: true, Width: 60, Out: buf})
	if !u.canReveal() {
		t.Error("появление строк должно работать и в «УГЛЕ»")
	}
	// А без цвета затухания нет — строки печатаются сразу.
	u2, _ := emberUI()
	if u2.canReveal() {
		t.Error("без цвета появление строк отключается")
	}
}
