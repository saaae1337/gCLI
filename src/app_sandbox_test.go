package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
	"gcli/tools"
)

// sandboxApp — приложение, готовое к /sandbox и setupSandbox.
//
// Отдельный GCLI_HOME обязателен: песочница закрывает каталог данных gcli
// (там config.json с ключами), и без изоляции тесты читали бы настоящие
// настройки пользователя.
func sandboxApp(t *testing.T) (*app, *strings.Builder) {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	t.Setenv("GCLI_SANDBOX", "")

	buf := &strings.Builder{}
	store := core.NewStore()
	store.Ensure()
	return &app{
		repo:    &core.Repo{Store: store, Cfg: core.DefaultConfig()},
		store:   store,
		ui:      testUI(buf),
		workDir: work,
	}, buf
}

// TestSandboxOnByDefault — главное изменение P0.1: песочница включена без
// просьбы пользователя. Раньше дефолт был «выключено», и это делало
// уязвимым обычный запуск, а защищённым — специальный.
func TestSandboxOnByDefault(t *testing.T) {
	a, _ := sandboxApp(t)
	if !a.setupSandbox("").Enabled() {
		t.Error("без флага и переменной окружения песочница обязана быть включена")
	}
}

// TestSandboxFlagOffWins — флаг -sandbox off перекрывает всё остальное.
func TestSandboxFlagOffWins(t *testing.T) {
	a, _ := sandboxApp(t)
	a.repo.Cfg.Sandbox = core.SandboxOn
	t.Setenv("GCLI_SANDBOX", "on")
	if a.setupSandbox("off").Enabled() {
		t.Error("-sandbox off должен выключать песочницу при любых других настройках")
	}
}

// TestSandboxEnvOffBeatsConfig — переменная окружения сильнее конфига.
func TestSandboxEnvOffBeatsConfig(t *testing.T) {
	a, _ := sandboxApp(t)
	a.repo.Cfg.Sandbox = core.SandboxOn
	t.Setenv("GCLI_SANDBOX", "off")
	if a.setupSandbox("").Enabled() {
		t.Error("GCLI_SANDBOX=off должен выключать песочницу поверх конфига")
	}
}

// TestSandboxEnvBeatsFlagEmpty — пустой флаг не должен перекрывать переменную.
func TestSandboxEnvBeatsFlagEmpty(t *testing.T) {
	a, _ := sandboxApp(t)
	t.Setenv("GCLI_SANDBOX", "off")
	if a.setupSandbox("").Enabled() {
		t.Error("пустой -sandbox обязан уступать GCLI_SANDBOX")
	}
}

// TestSandboxConfigOff — режим из конфига тоже выключает.
func TestSandboxConfigOff(t *testing.T) {
	a, _ := sandboxApp(t)
	a.repo.Cfg.Sandbox = core.SandboxOff
	if a.setupSandbox("").Enabled() {
		t.Error("sandbox=off в конфиге должен выключать песочницу")
	}
}

// TestSandboxConfigOnWinsOverNothing — явное on в конфиге оставляет защиту.
func TestSandboxConfigOnWinsOverNothing(t *testing.T) {
	a, _ := sandboxApp(t)
	a.repo.Cfg.Sandbox = core.SandboxOn
	if !a.setupSandbox("").Enabled() {
		t.Error("sandbox=on в конфиге должен оставлять песочницу включённой")
	}
}

// TestSandboxGarbageFallsBackToSafe — опечатка в значении не должна
// молча отключать защиту: раз непонятно, действует включённая.
func TestSandboxGarbageFallsBackToSafe(t *testing.T) {
	a, _ := sandboxApp(t)
	if !a.setupSandbox("выклклено").Enabled() {
		t.Error("на опечатке песочница обязана остаться включённой")
	}
	if !strings.Contains(strings.Join(a.startupNotes, " "), "не понял флаг -sandbox") {
		t.Errorf("об опечатке надо сказать пользователю, заметки: %v", a.startupNotes)
	}
}

// TestSandboxGarbageFlagDoesNotFallThroughToConfigOff — опечатка в флаге не
// должна откатываться на конфиг. Иначе `-sandbox выклклено` при
// sandbox=off в конфиге тихо оставил бы защиту выключенной именно там, где
// человек её просил не трогать.
func TestSandboxGarbageFlagDoesNotFallThroughToConfigOff(t *testing.T) {
	a, _ := sandboxApp(t)
	a.repo.Cfg.Sandbox = core.SandboxOff
	if !a.setupSandbox("выклклено").Enabled() {
		t.Error("на опечатке в флаге песочница обязана остаться включённой, даже если в конфиге off")
	}
}

// TestSandboxGarbageEnvDoesNotFallThroughToConfigOff — то же для окружения.
func TestSandboxGarbageEnvDoesNotFallThroughToConfigOff(t *testing.T) {
	a, _ := sandboxApp(t)
	a.repo.Cfg.Sandbox = core.SandboxOff
	t.Setenv("GCLI_SANDBOX", "maybe")
	if !a.setupSandbox("").Enabled() {
		t.Error("на опечатке в GCLI_SANDBOX песочница обязана остаться включённой, даже если в конфиге off")
	}
}

// TestSandboxGarbageEnvAlsoReported — то же для переменной окружения.
func TestSandboxGarbageEnvAlsoReported(t *testing.T) {
	a, _ := sandboxApp(t)
	t.Setenv("GCLI_SANDBOX", "maybe")
	if !a.setupSandbox("").Enabled() {
		t.Error("на опечатке в GCLI_SANDBOX песочница обязана остаться включённой")
	}
	if !strings.Contains(strings.Join(a.startupNotes, " "), "GCLI_SANDBOX") {
		t.Errorf("об опечатке в GCLI_SANDBOX надо сказать, заметки: %v", a.startupNotes)
	}
}

// TestSandboxSecretsDeniedEvenWhenHomeInsideProject — каталог данных gcli
// закрыт всегда, даже если он физически внутри проекта: там ключи API.
func TestSandboxSecretsDeniedEvenWhenHomeInsideProject(t *testing.T) {
	a, _ := sandboxApp(t)
	sb := a.setupSandbox("on")
	if err := sb.Check(a.store.ConfigPath()); err == nil {
		t.Fatalf("песочница должна закрывать %s (там ключи API)", a.store.ConfigPath())
	}
}

// TestSandboxCoversWorkDir — ради чего песочница и нужна: рабочий каталог
// остаётся доступным.
func TestSandboxCoversWorkDir(t *testing.T) {
	a, _ := sandboxApp(t)
	sb := a.setupSandbox("on")
	if err := sb.Check(filepath.Join(a.workDir, "main.go")); err != nil {
		t.Errorf("рабочий каталог должен оставаться доступным: %v", err)
	}
}

// TestSandboxCommandOffWritesConfig — /sandbox off обязан пережить перезапуск.
func TestSandboxCommandOffWritesConfig(t *testing.T) {
	a, buf := sandboxApp(t)
	a.sandbox = a.setupSandbox("on")
	a.cmdSandbox("off")

	if a.sandbox.Enabled() {
		t.Error("после /sandbox off песочница должна быть выключена в этой сессии")
	}
	if got := savedConfig(t, a); got.Sandbox != core.SandboxOff {
		t.Errorf("в config.json ожидалось sandbox=off, записали %q", got.Sandbox)
	}
	if !strings.Contains(out(buf), "песочница выключена") {
		t.Errorf("нет подтверждения:\n%s", out(buf))
	}
}

// TestSandboxCommandOnWritesConfig — и обратное переключение тоже на диск.
func TestSandboxCommandOnWritesConfig(t *testing.T) {
	a, buf := sandboxApp(t)
	a.cmdSandbox("off")
	buf.Reset()
	a.cmdSandbox("on")

	if !a.sandbox.Enabled() {
		t.Error("после /sandbox on песочница должна быть включена")
	}
	if got := savedConfig(t, a); got.Sandbox != core.SandboxOn {
		t.Errorf("в config.json ожидалось sandbox=on, записали %q", got.Sandbox)
	}
	if err := a.sandbox.Check(filepath.Join(os.TempDir(), "..", "etc")); err == nil {
		t.Error("после /sandbox on пути вне проекта обязаны быть закрыты")
	}
	if !strings.Contains(out(buf), "песочница включена") {
		t.Errorf("нет подтверждения:\n%s", out(buf))
	}
}

// TestSandboxCommandSurvivesRestart — конфиг реально читается при старте.
func TestSandboxCommandSurvivesRestart(t *testing.T) {
	a, _ := sandboxApp(t)
	a.cmdSandbox("off")

	r := core.Open()
	if r.Cfg.SandboxEnabled() {
		t.Errorf("после перезапуска песочница должна остаться выключенной, в конфиге %q", r.Cfg.Sandbox)
	}
}

// TestSandboxCommandNoArgShowsState — пустой аргумент показывает состояние,
// а не переключает его наугад: иначе /sandbox (человек нажал Enter) тихо
// менял бы защиту на противоположную.
func TestSandboxCommandNoArgShowsState(t *testing.T) {
	a, buf := sandboxApp(t)
	a.sandbox = a.setupSandbox("on")
	buf.Reset()
	a.cmdSandbox("")

	got := out(buf)
	if !strings.Contains(got, "песочница включена") {
		t.Errorf("ожидался показ состояния:\n%s", got)
	}
	if !a.sandbox.Enabled() {
		t.Error("пустой аргумент не должен менять состояние песочницы")
	}
	if _, err := os.Stat(a.store.ConfigPath()); err == nil {
		// Конфиг мог быть создан ранее store.Ensure; главное — значение.
		if got := savedConfig(t, a); got.Sandbox != "" {
			t.Errorf("пустой аргумент не должен писать режим в конфиг, записали %q", got.Sandbox)
		}
	}
}

// TestSandboxCommandBadArgKeepsState — опечатка не переключает защиту.
func TestSandboxCommandBadArgKeepsState(t *testing.T) {
	a, buf := sandboxApp(t)
	a.sandbox = a.setupSandbox("on")
	buf.Reset()
	a.cmdSandbox("выклклено")

	if !a.sandbox.Enabled() {
		t.Error("на опечатке песочница должна остаться включённой")
	}
	if !strings.Contains(out(buf), "/sandbox on | off | status") {
		t.Errorf("надо подсказать допустимые значения:\n%s", out(buf))
	}
}

// TestSandboxStatusWordWorks — слово status осталось рабочим.
func TestSandboxStatusWordWorks(t *testing.T) {
	a, buf := sandboxApp(t)
	a.sandbox = a.setupSandbox("on")
	buf.Reset()
	a.cmdSandbox("status")
	if !strings.Contains(out(buf), "песочница включена") {
		t.Errorf("/sandbox status должен показывать состояние:\n%s", out(buf))
	}
}

// TestParseSandboxModeAliases — значения, которыми писали раньше, принимаются.
func TestParseSandboxModeAliases(t *testing.T) {
	cases := []struct {
		in   string
		want core.SandboxMode
		ok   bool
	}{
		{"", core.SandboxUnset, true},
		{"  ", core.SandboxUnset, true},
		{"on", core.SandboxOn, true},
		{"ON", core.SandboxOn, true},
		{"1", core.SandboxOn, true},
		{"true", core.SandboxOn, true},
		{"вкл", core.SandboxOn, true},
		{"включить", core.SandboxOn, true},
		{"off", core.SandboxOff, true},
		{"OFF", core.SandboxOff, true},
		{"0", core.SandboxOff, true},
		{"false", core.SandboxOff, true},
		{"выкл", core.SandboxOff, true},
		{"отключить", core.SandboxOff, true},
		{"maybe", core.SandboxUnset, false},
		{"да", core.SandboxUnset, false},
	}
	for _, c := range cases {
		got, ok := core.ParseSandboxMode(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseSandboxMode(%q) = %q,%v — ждали %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestSandboxModeAcceptsLegacyJSONBool — конфиги, написанные до 5.5.1, содержат
// "sandbox": true/false. Разбор молча ронял бы поле, и песочница выключалась
// у того, кто её просил.
func TestSandboxModeAcceptsLegacyJSONBool(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want core.SandboxMode
	}{
		{`true`, core.SandboxOn},
		{`false`, core.SandboxOff},
		{`true `, core.SandboxOn},
	} {
		var m core.SandboxMode
		if err := m.UnmarshalJSON([]byte(c.raw)); err != nil {
			t.Fatalf("UnmarshalJSON(%q): %v", c.raw, err)
		}
		if m != c.want {
			t.Errorf("UnmarshalJSON(%q) = %q, ждали %q", c.raw, m, c.want)
		}
	}
}

func TestSandboxModeAcceptsJSONStrings(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want core.SandboxMode
	}{
		{`"on"`, core.SandboxOn},
		{`"off"`, core.SandboxOff},
		{`""`, core.SandboxUnset},
		{`null`, core.SandboxUnset},
		{`"  Off "`, core.SandboxOff},
		{`"ON"`, core.SandboxOn},
	} {
		var m core.SandboxMode
		if err := m.UnmarshalJSON([]byte(c.raw)); err != nil {
			t.Fatalf("UnmarshalJSON(%q): %v", c.raw, err)
		}
		if m != c.want {
			t.Errorf("UnmarshalJSON(%q) = %q, ждали %q", c.raw, m, c.want)
		}
	}
}

func TestSandboxModeRejectsGarbage(t *testing.T) {
	for _, raw := range []string{`"maybe"`, `42`, `{"a":1}`, `[]`} {
		var m core.SandboxMode
		if err := m.UnmarshalJSON([]byte(raw)); err == nil {
			t.Errorf("UnmarshalJSON(%q) должен вернуть ошибку, а не молча проглотить", raw)
		}
	}
}

// TestSandboxLegacyBoolInRealConfig — сквозная проверка: старый config.json
// целиком, со значением true, читается и даёт включённую песочницу.
func TestSandboxLegacyBoolInRealConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GCLI_HOME", home)

	cfg := `{"agent":true,"model":"glm-4.6","sandbox":true}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("запись конфига: %v", err)
	}
	r := core.Open()
	if !r.Cfg.SandboxEnabled() {
		t.Error(`старый конфиг с "sandbox": true должен давать включённую песочницу`)
	}

	// И выключение в старом виде — тоже уважается.
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"sandbox":false}`), 0o600); err != nil {
		t.Fatalf("запись конфига: %v", err)
	}
	if core.Open().Cfg.SandboxEnabled() {
		t.Error(`старый конфиг с "sandbox": false должен выключать песочницу`)
	}
}

// TestSandboxModeMarshalsAsString — конфиг, который мы пишем сами, должен
// оставаться читаемым и человеком, и следующими версиями.
func TestSandboxModeMarshalsAsString(t *testing.T) {
	data, err := json.Marshal(core.Config{Sandbox: core.SandboxOn})
	if err != nil {
		t.Fatalf("маршалинг: %v", err)
	}
	if !strings.Contains(string(data), `"sandbox":"on"`) {
		t.Errorf("ожидалось \"sandbox\":\"on\", получили %s", data)
	}
	data, err = json.Marshal(core.Config{Sandbox: core.SandboxOff})
	if err != nil {
		t.Fatalf("маршалинг: %v", err)
	}
	if !strings.Contains(string(data), `"sandbox":"off"`) {
		t.Errorf("ожидалось \"sandbox\":\"off\", получили %s", data)
	}
	// Пустой режим не должен попадать в файл: «не задано» и «включено»
	// различаются только в коде, а в конфиге достаточно явного выбора.
	data, err = json.Marshal(core.Config{})
	if err != nil {
		t.Fatalf("маршалинг: %v", err)
	}
	if strings.Contains(string(data), "sandbox") {
		t.Errorf("пустой режим не должен попадать в конфиг, получили %s", data)
	}
}

// TestSandboxDefaultIsOn — сам дефолт, без всяких запусков.
func TestSandboxDefaultIsOn(t *testing.T) {
	if !core.DefaultConfig().SandboxEnabled() {
		t.Error("песочница по умолчанию обязана быть включена")
	}
	if (core.Config{Sandbox: core.SandboxOff}).SandboxEnabled() {
		t.Error("явное off должно выключать")
	}
	if !(core.Config{Sandbox: core.SandboxOn}).SandboxEnabled() {
		t.Error("явное on должно включать")
	}
}

// TestSandboxStatusStringForPermissions — /permissions показывает честное
// состояние, а не «выключена» по умолчанию.
func TestSandboxStatusStringForPermissions(t *testing.T) {
	if s := sandboxStatus(nil); !strings.Contains(s, "выключена") {
		t.Errorf("nil-песочница = выключена, получили %q", s)
	}
	sb := tools.NewSandbox(t.TempDir())
	s := sandboxStatus(sb)
	if !strings.Contains(s, "включена") {
		t.Errorf("включённая песочница = %q", s)
	}
	// sandboxStatus уходит в таблицу /permissions, где ANSI только шумит.
	if strings.Contains(s, "\x1b[") {
		t.Errorf("sandboxStatus не должен содержать ANSI: %q", s)
	}
}
