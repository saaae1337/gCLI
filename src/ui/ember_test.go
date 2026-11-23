package ui

import (
	"strings"
	"testing"
)

// emberUI - UI в теме «УГОЛЬ» без цвета, пишущий в буфер.
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

// emberASCIIUI - то же, но в ASCII-режиме (терминалы без юникода).
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
			t.Errorf("normalizeTheme(%q) = %q, ожидалось %q - стиль один, старые имена сводятся к канону", th, got, themeEmber)
		}
	}
}

// Глифы: набор Claude Code (маркер «●», зубец «└»), ASCII-фолбэк.
func TestEmberGlyphs(t *testing.T) {
	g := emberGlyphs()
	// Брендового маскота нет; маркер вызова и зубец результата — как в CC.
	if g.Mark != "" {
		t.Errorf("брендовый знак должен быть пуст: mark=%q", g.Mark)
	}
	if g.Dot != "●" {
		t.Errorf("маркер события = %q, ожидался '●'", g.Dot)
	}
	if g.Tick != "└" {
		t.Errorf("зубец результата = %q, ожидался '└'", g.Tick)
	}
	a := emberASCIIGlyphs()
	if !isASCIIString(a.Mark + a.Tick + a.Dot) {
		t.Errorf("ASCII-набор не должен содержать юникод-глифы: mark=%q tick=%q", a.Mark, a.Tick)
	}
}

// В ASCII-режиме «зубец» пуст: результат смещается отступом.
func TestEmberTickASCII(t *testing.T) {
	u, _ := emberASCIIUI()
	if tick := u.emberTickOf(); tick != "" {
		t.Errorf("ASCII-зубец должен быть пуст, получено %q", tick)
	}
	u2, _ := emberUI()
	if tick := u2.emberTickOf(); tick != "" {
		t.Errorf("юникод-зубец должен быть пуст, получено %q", tick)
	}
}

// GlyphsFor/SpinnerFor обязаны возвращать спокойный набор по теме.
func TestEmberGlyphsFor(t *testing.T) {
	g := GlyphsFor("ember", true)
	if g.Mark != "" {
		t.Errorf("GlyphsFor(ember) маркер = %q, ожидалась пустота (без маскота)", g.Mark)
	}
	if GlyphsFor("coal", false).Unicode {
		t.Error("алиас «coal» без юникода должен вернуть ASCII-набор")
	}
	sp := SpinnerFor("ember", true)
	// Спиннер — фирменные кадры Claude Code; ASCII — палочка.
	if len(sp) == 0 || sp[0] != "·" {
		t.Errorf("спиннер = %v, ожидались кадры Claude Code", sp)
	}
	if sp2 := SpinnerFor("ember", false); sp2[0] != "|" {
		t.Errorf("ASCII-спиннер = %v, ожидалась палочка", sp2)
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

// Блок: жирная шапка, тело со сдвигом, без маркеров и рамок.
func TestEmberBlock(t *testing.T) {
	u, buf := emberUI()
	u.Block("строка один\nстрока два", BlockOpts{Title: "Заголовок"})
	out := StripANSI(buf.String())
	if !strings.Contains(out, "Заголовок") || !strings.Contains(out, "строка один") {
		t.Errorf("блок потерял содержимое:\n%s", out)
	}
	if strings.Contains(out, "*") {
		t.Errorf("шапке блока маркер не нужен:\n%s", out)
	}
	if strings.Contains(out, "+") {
		t.Errorf("плоской теме рамки не нужны:\n%s", out)
	}
}

// Таблица: колонки выровнены пробелами, без декоративных разделителей.
func TestEmberTable(t *testing.T) {
	u, buf := emberUI()
	u.Table([]Column{{Title: "имя", Width: 10}, {Title: "год", Width: 6, Right: true}},
		[][]string{{"uno", "1"}, {"dos", "2"}}, BlockOpts{Title: "Т"})
	out := StripANSI(buf.String())
	if strings.Contains(out, "⏺") || strings.Contains(out, "·") {
		t.Errorf("декоративных разделителей быть не должно:\n%s", out)
	}
	if !strings.Contains(out, "uno") || !strings.Contains(out, "dos") {
		t.Errorf("таблица потеряла строки:\n%s", out)
	}
}

// Размышления: поток открывается маркером, дальше идёт текст.
func TestEmberThinking(t *testing.T) {
	u, buf := emberUI()
	u.ThinkChunk("мысль")
	u.ThinkEnd()
	out := StripANSI(buf.String())
	if !strings.Contains(out, "thinking") {
		t.Errorf("нет подписи размышлений:\n%s", out)
	}
	if !strings.Contains(out, "мысль") {
		t.Errorf("кусочек размышлений потерян:\n%s", out)
	}
}

// ThinkChunk открывает поток сам, если маркера не было: без заголовка
// куски размышлений выглядели бы как обычный ответ модели.
func TestThinkChunkOpensMarker(t *testing.T) {
	u, buf := emberUI()
	u.ThinkChunk("первая мысль\n")
	u.ThinkChunk("вторая\n")
	out := StripANSI(buf.String())
	if n := strings.Count(out, "thinking"); n != 1 {
		t.Errorf("маркер потока должен быть один, напечатано %d:\n%s", n, out)
	}
	if !strings.Contains(out, "первая мысль") || !strings.Contains(out, "вторая") {
		t.Errorf("куски размышлений потеряны:\n%s", out)
	}
}

// Показ разовым блоком: маркер печатается один раз, даже если блок уже был
// открыт раньше - иначе на экране появлялось бы второе «размышления».
func TestThinkHiddenNoSecondMarker(t *testing.T) {
	u, buf := emberUI()
	u.ThinkChunk("старое\n")
	u.ThinkHidden("новое")
	out := StripANSI(buf.String())
	if n := strings.Count(out, "thinking"); n != 1 {
		t.Errorf("маркер потока должен быть один, напечатано %d:\n%s", n, out)
	}
	if !strings.Contains(out, "новое") {
		t.Errorf("разовый блок не показан:\n%s", out)
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
	if !strings.Contains(out, "готово") {
		t.Errorf("нет строки результата под вызовом:\n%s", out)
	}
}

// Строки печатаются как есть: анимации появления удалены.
func TestEmberRevealVsAnimate(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{Theme: "ember", Unicode: true, Color: true, Grade: Color16, Animations: true, Width: 60, Out: buf})
	u.RevealLine("строка")
	out := buf.String()
	if strings.Contains(out, "\r\033[K") {
		t.Errorf("перерисовок быть не должно:\n%q", out)
	}
	if !strings.Contains(StripANSI(out), "строка") {
		t.Errorf("строка потеряна:\n%q", out)
	}
}

// isASCIIString — нет ли в строке декоративных не-ASCII символов.
// Кириллица разрешена: это текст интерфейса, а не оформление.
func isASCIIString(s string) bool {
	for _, r := range s {
		if r > 127 && !(r >= 0x400 && r <= 0x4FF) {
			return false
		}
	}
	return true
}
