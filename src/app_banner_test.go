package main

import (
	"strings"
	"testing"

	"gcli/core"
	"gcli/providers"
	"gcli/subagents"
	"gcli/ui"
)

// bannerApp — приложение с подменённым хранилищем, готовое к печати баннера.
// Рабочий каталог подменён на t.TempDir(), чтобы баннер не читал ~/.gcli.
func bannerApp(t *testing.T, buf *strings.Builder, anim bool) *app {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GCLI_HOME", dir)

	a := &app{
		repo:    &core.Repo{Store: core.NewStore(), Cfg: core.DefaultConfig()},
		workDir: dir,
		model:   "glm-4.6",
		prov:    &providers.Provider{ID: "zai", Label: "Z.ai", NoKey: true},
		sess:    &core.Session{ID: "s1", AgentMode: true},
		pool:    &subagents.Pool{},
	}
	a.repo.Cfg.Animations = &anim
	a.ui = ui.New(ui.Options{
		Theme: "ember", Unicode: true, Color: true, Grade: ui.ColorRGB,
		Animations: anim, Width: 80, Out: buf,
	})
	return a
}

// TestBannerShowsSession — баннер печатает параметры сессии: имя
// проекта, версия, модель, каталог, ключ.
func TestBannerShowsSession(t *testing.T) {
	buf := &strings.Builder{}
	a := bannerApp(t, buf, true)
	a.banner()

	out := buf.String()
	clean := ui.StripANSI(out)
	for _, want := range []string{"gCLI", "Welcome", "/help for help", "cwd:", "model:"} {
		if !strings.Contains(clean, want) {
			t.Errorf("в баннере нет %q:\n%s", want, clean)
		}
	}
	// Бокс со скруглёнными углами и брендовый «✻».
	if !strings.Contains(clean, "╭") || !strings.Contains(clean, "╰") || !strings.Contains(clean, "✻") {
		t.Errorf("баннер должен быть боксом Claude Code с «✻»:\n%s", clean)
	}
	if strings.Contains(clean, "/\\_/\\") || strings.Contains(clean, "> ^ <") {
		t.Errorf("баннер не должен рисовать маскота:\n%s", clean)
	}
}

// TestBannerPlainWithoutAnimations — с выключенными анимациями баннер
// печатается обычным текстом: без управления курсором, иначе вывод в
// пайп или лог испорчен.
func TestBannerPlainWithoutAnimations(t *testing.T) {
	buf := &strings.Builder{}
	a := bannerApp(t, buf, false)
	a.banner()

	out := buf.String()
	if strings.Contains(out, "\r") || strings.Contains(out, "\033[K") {
		t.Errorf("без анимаций в баннере не должно быть управления курсором:\n%q", firstRunes(out))
	}
	clean := ui.StripANSI(out)
	for _, want := range []string{"gCLI", "model:"} {
		if !strings.Contains(clean, want) {
			t.Errorf("в баннере нет %q:\n%s", want, clean)
		}
	}
}

// firstRunes — первые символы строки для компактного сообщения об ошибке.
func firstRunes(s string) string {
	r := []rune(s)
	if len(r) > 400 {
		return string(r[:400]) + "..."
	}
	return s
}

// TestSetupUIAnimationsFollowColor — интеграция запуска: анимации решаются
// ПОСЛЕ обработки -color и видят итоговый цвет.
//
// Раньше порядок был обратный, и при `-color truecolor` без tty
// (например, в Windows Terminal, где isTerminal() иногда врёт) цвет
// включался, а анимации оставались выключенными — эффект пропадал молча.
func TestSetupUIAnimationsFollowColor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GCLI_HOME", dir)
	t.Setenv("GCLI_NO_ANIM", "")
	t.Setenv("NO_COLOR", "")

	a := &app{
		repo:    &core.Repo{Store: core.NewStore(), Cfg: core.DefaultConfig()},
		workDir: dir,
		model:   "glm-4.6",
	}
	anim := true
	a.repo.Cfg.Animations = &anim

	a.setupUI(false, false, false, false, "truecolor")
	if !a.ui.AnimationsOn() {
		t.Error("-color truecolor должен включать анимации: решения по цвету и анимациям разошлись")
	}

	// GCLI_NO_ANIM остаётся главным даже при явном цвете.
	t.Setenv("GCLI_NO_ANIM", "1")
	a.setupUI(false, false, false, false, "truecolor")
	if a.ui.AnimationsOn() {
		t.Error("GCLI_NO_ANIM должен выключать анимации даже при -color")
	}
}
