package main

import "testing"

func boolPtr(b bool) *bool { return &b }

// animBase — обычные условия: цветной терминал, юникод, не машинный режим.
func animBase() animEnv {
	return animEnv{Color: true, UnicodeOK: true}
}

func TestAnimsAutoOn(t *testing.T) {
	if !animsEnabled(animBase()) {
		t.Error("в цветном терминале с юникодом анимации должны быть включены")
	}
}

func TestAnimsOffByNoAnimEnv(t *testing.T) {
	e := animBase()
	e.NoAnimEnv = true
	if animsEnabled(e) {
		t.Error("GCLI_NO_ANIM должен выключать анимации")
	}
}

// TestAnimsNoAnimEnvBeatsConfig — переменная окружения сильнее config.json.
//
// Это и был дефект: ключ "animations": true в конфиге затирал
// GCLI_NO_ANIM, и переменная не делала ничего.
func TestAnimsNoAnimEnvBeatsConfig(t *testing.T) {
	e := animBase()
	e.NoAnimEnv = true
	e.Cfg = boolPtr(true)
	if animsEnabled(e) {
		t.Error("GCLI_NO_ANIM должен иметь приоритет над config.json")
	}
}

func TestAnimsConfigWinsOverAuto(t *testing.T) {
	e := animBase()
	e.Cfg = boolPtr(false)
	if animsEnabled(e) {
		t.Error("\"animations\": false должен выключать анимации")
	}
	e.Cfg = boolPtr(true)
	if !animsEnabled(e) {
		t.Error("\"animations\": true должен включать анимации")
	}
}

func TestAnimsOffInJSON(t *testing.T) {
	e := animBase()
	e.Machine = true
	e.Cfg = boolPtr(true)
	if animsEnabled(e) {
		t.Error("в -json анимаций быть не должно даже при \"animations\": true")
	}
}

func TestAnimsOffInASCII(t *testing.T) {
	e := animBase()
	e.ASCII = true
	if animsEnabled(e) {
		t.Error("в -ascii анимаций быть не должно")
	}

	// Терминал без юникода (GCLI_ASCII или локаль не в UTF-8) — то же самое.
	e = animBase()
	e.UnicodeOK = false
	if animsEnabled(e) {
		t.Error("без юникода анимаций быть не должно")
	}
}

func TestAnimsOffWithoutColor(t *testing.T) {
	e := animBase()
	e.Color = false
	if animsEnabled(e) {
		t.Error("без цвета анимаций быть не должно")
	}
}

// colorBase — обычные условия: настоящий терминал с виртуальным режимом.
func colorBase() colorEnv {
	return colorEnv{TTY: true, VT: true}
}

func TestColorAutoOnInTerminal(t *testing.T) {
	color, _ := resolveColor(colorBase())
	if !color {
		t.Error("в терминале цвет должен включаться автоматически")
	}
}

func TestColorOffByFlags(t *testing.T) {
	for name, mod := range map[string]func(*colorEnv){
		"-no-color":   func(e *colorEnv) { e.NoColor = true },
		"NO_COLOR":    func(e *colorEnv) { e.NoColorEnv = true },
		"-json":       func(e *colorEnv) { e.Machine = true },
		"не терминал": func(e *colorEnv) { e.TTY = false },
		"нет VT":      func(e *colorEnv) { e.VT = false },
	} {
		e := colorBase()
		mod(&e)
		if color, _ := resolveColor(e); color {
			t.Errorf("%s должен выключать цвет", name)
		}
	}
}

// TestExplicitColorForcesColorWithoutTTY — Windows Terminal не всегда
// определяется как tty, поэтому явный -color включает окраску принудительно.
func TestExplicitColorForcesColorWithoutTTY(t *testing.T) {
	for _, mode := range []string{"16", "256", "truecolor"} {
		e := colorBase()
		e.TTY = false
		e.Mode = mode
		color, grade := resolveColor(e)
		if !color {
			t.Errorf("-color %s должен включать цвет даже без tty", mode)
		}
		if grade == 0 {
			t.Errorf("-color %s должен задавать уровень цвета", mode)
		}
	}
}

// TestAnimsSeeExplicitColor — дефект порядка в setupUI: анимации считались
// до обработки -color, поэтому при явном цвете без tty они молча выключались,
// хотя цвет и его требования выполнялись.
func TestAnimsSeeExplicitColor(t *testing.T) {
	color, _ := resolveColor(colorEnv{TTY: false, VT: false, Mode: "truecolor"})
	if !animsEnabled(animEnv{Color: color, UnicodeOK: true}) {
		t.Error("явный -color должен включать и анимации, а не только цвет")
	}

	// -no-color остаётся главным: никакой -color его не перебивает.
	color, _ = resolveColor(colorEnv{TTY: true, VT: true, NoColor: true, Mode: "truecolor"})
	if color {
		t.Error("-no-color должен выигрывать у -color")
	}
	if animsEnabled(animEnv{Color: color, UnicodeOK: true}) {
		t.Error("анимации без цвета быть не должно")
	}
}
