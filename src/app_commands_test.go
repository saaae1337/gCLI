package main

// Тесты чистых функций слэш-команд из app_commands.go.
//
// Почему они, а не cmdHelp или cmdPerms. Файл app_commands.go — 1379 строк
// и 46 функций, и до этого не имел НИ ОДНОГО теста. Большинство функций
// печатают в UI и требуют живого приложения с провайдерами, репозиторием
// и сессией — такой тест проверял бы не логику, а способность собрать
// фикстуру. А чистые функции разбирают пользовательский ввод: именно там
// опечатка в разборе аргументов превращает команду в тихо ничего не делающую,
// и заметить это можно только на живом терминале.
//
// Здесь разбирается то, что человек набирает руками.

import (
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
)

// TestSplitArgs — разбор аргументов команды с кавычками.
//
// Кавычки тут не украшение: /permissions test bash "git commit -m 'x'" и
// /init "src/**" без их учёта распадаются на лишние аргументы, и команда
// применяет правило не к тому имени файла.
func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`bash "git status"`, []string{"bash", "git status"}},
		{`bash 'git status'`, []string{"bash", "git status"}},
		// Пробел внутри кавычек не разделяет.
		{`write_file "src/my file.go"`, []string{"write_file", "src/my file.go"}},
		// Кавычки разные внутри одной строки.
		{`bash "a'b c" 'd"e f'`, []string{"bash", "a'b c", `d"e f`}},
		// Одинарная кавычка внутри двойных — обычный символ.
		{`bash "it's"`, []string{"bash", "it's"}},
		// Лишние пробелы схлопываются.
		{`  bash   git   status  `, []string{"bash", "git", "status"}},
		{`bash` + "\t" + `tab` + "\t" + `sep`, []string{"bash", "tab", "sep"}},
		// Пустые кавычки дают ПУСТОЙ аргумент, а не исчезают: иначе
		// /init "" молча превратился бы в /init со всем проектом.
		{`init ""`, []string{"init", ""}},
		{``, nil},
		{`   `, nil},
	}
	for _, c := range cases {
		got := splitArgs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitArgs(%q) = %q, ждали %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitArgs(%q)[%d] = %q, ждали %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestSplitArgsUnclosedQuote — незакрытая кавычка не теряет аргумент.
func TestSplitArgsUnclosedQuote(t *testing.T) {
	// Человек забыл кавычку: /init "src/**
	got := splitArgs(`init "src/**`)
	if len(got) != 2 || got[1] != "src/**" {
		t.Fatalf("незакрытая кавычка: %q", got)
	}
}

// TestSplitFirstWord — первое слово команды отделяется, хвост сохраняется.
func TestSplitFirstWord(t *testing.T) {
	cases := []struct {
		in, word, rest string
	}{
		{`bash "git commit -m 'x'"`, "bash", `"git commit -m 'x'"`},
		{"bash   git   status", "bash", "git   status"},
		{"/help", "/help", ""},
		{"", "", ""},
		{"   ", "", ""},
		// Регистр первой команды не важен — приводим к нижнему.
		{"BASH ls", "bash", "ls"},
		// Хвост обрезается по краям, но внутри не трогается.
		{"model   claude-sonnet  ", "model", "claude-sonnet"},
	}
	for _, c := range cases {
		w, rest := splitFirstWord(c.in)
		if w != c.word || rest != c.rest {
			t.Errorf("splitFirstWord(%q) = (%q, %q), ждали (%q, %q)", c.in, w, rest, c.word, c.rest)
		}
	}
}

// TestIsPathLike — файловые инструменты отличаются от прочих.
//
// От этого решает permsTest и cmdPerms: для файловых инструментов путь
// приводится к виду от проекта, иначе правило write_file(src/**) не
// совпало бы с абсолютным путём и право на запись тихо спрашивалось
// каждый раз.
func TestIsPathLike(t *testing.T) {
	pathTools := []string{"edit", "edit_file", "write_file", "multi_edit", "apply_patch", "patch"}
	for _, name := range pathTools {
		if !isPathLike(name) {
			t.Errorf("%q должен считаться файловым инструментом", name)
		}
	}
	otherTools := []string{"bash", "read_file", "grep", "web_fetch", "multi_read", "spawn_agent", ""}
	for _, name := range otherTools {
		if isPathLike(name) {
			t.Errorf("%q не должен считаться файловым инструментом", name)
		}
	}
}

// TestOrDash — пустое значение печатается прочерком, а не пустотой.
func TestOrDash(t *testing.T) {
	for _, empty := range []string{"", "   ", "\t"} {
		if got := orDash(empty); got != "-" {
			t.Errorf("orDash(%q) = %q, ждали прочерк", empty, got)
		}
	}
	if got := orDash("claude-sonnet"); got != "claude-sonnet" {
		t.Errorf("orDash не должен трогать непустое: %q", got)
	}
}

// TestYesNo — однозначный «да/нет» для переключателей.
func TestYesNo(t *testing.T) {
	if yesNo(true) != "да" {
		t.Error("true должен печататься как \"да\"")
	}
	if yesNo(false) != "нет" {
		t.Error("false должен печататься как \"нет\"")
	}
}

// TestModeStatus — переключатель показывается словом, а не 1/0.
func TestModeStatus(t *testing.T) {
	if modeStatus(true) != "вкл" {
		t.Error("включено должно быть \"вкл\"")
	}
	if modeStatus(false) != "выкл" {
		t.Error("выключено должно быть \"выкл\"")
	}
}

// TestKeyStatus — ключ никогда не печатается целиком.
//
// Это проверка на утечку секрета в вывод /status. Ключ должен быть
// замаскирован, а отсутствие ключа — сказано прямо, чтобы человек
// понял, что пора ввести его, а не молчало.
func TestKeyStatus(t *testing.T) {
	full := "sk-ant-api03-SECRETVALUE-1234567890"
	for _, p := range []*providers.Provider{
		{Key: full},
		{Key: ""},
		{NoKey: true, Key: full}, // локальный: ключ не должен выводиться даже если есть
	} {
		got := keyStatus(p)
		if strings.Contains(got, "SECRETVALUE") {
			t.Errorf("ключ утёк в вывод: %q", got)
		}
	}
	// Нет ключа — прямое требование /setup.
	if got := keyStatus(&providers.Provider{}); !strings.Contains(got, "/setup") {
		t.Errorf("без ключа ждали подсказку /setup, получили %q", got)
	}
	// Локальный провайдер без ключа — норма, а не ошибка.
	if got := keyStatus(&providers.Provider{NoKey: true}); !strings.Contains(got, "локальный") {
		t.Errorf("локальный провайдер: %q", got)
	}
	// Длинный ключ маскируется, но остаётся узнаваемым по краям.
	if got := keyStatus(&providers.Provider{Key: full}); !strings.HasPrefix(got, "sk-an") {
		t.Errorf("маска должна сохранять начало ключа: %q", got)
	}
}

// TestSandboxStatus — состояние песочницы показывается честно.
//
// Формулировка важна: выключенная песочница означает, что файлы видны где
// угодно. Молчаливое «выключена» читалось бы как «всё в порядке».
func TestSandboxStatus(t *testing.T) {
	// Песочница без корней — выключенная (Enabled требует непустой roots).
	off := sandboxStatus(tools.NewSandbox())
	if !strings.Contains(off, "выключена") || !strings.Contains(off, "где угодно") {
		t.Errorf("выключенная песочница должна говорить про границы: %q", off)
	}
	// Настоящую выключенность проверим отдельно: nil тоже выключен.
	if got := sandboxStatus(nil); !strings.Contains(got, "выключена") {
		t.Errorf("nil-песочница: %q", got)
	}
	// Корни показываются с обрезкой до 60 символов (core.Truncate), а
	// длинный путь t.TempDir() в неё не влезает. Поэтому корень делаем
	// коротким: NewSandbox всё равно разворачивает его в абсолютный
	// (filepath.Abs), и на Windows это добавит букву диска.
	const root = "/work/proj"
	sb := tools.NewSandbox(root)
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	on := sandboxStatus(sb)
	if !strings.Contains(on, "включена") {
		t.Errorf("включённая песочница: %q", on)
	}
	// Песочница хранит корень как filepath.Abs: на Windows короткий
	// «/work/proj» разворачивается в «C:\work\proj». Проверяем именно
	// реально настроенный корень, а не литерал из теста.
	if got := sb.Roots(); len(got) != 1 || got[0] != abs {
		t.Fatalf("корень песочницы: %q, ждали %q", got, abs)
	}
	shown := core.Truncate(strings.Join(sb.Roots(), ", "), 60)
	if !strings.Contains(on, shown) {
		t.Errorf("включённая песочница должна называть корень %q: %q", abs, on)
	}
}

// ---------- Итерация 2: каталог команд и подсказки при опечатке ----------

// TestCommandCatalogIntegrity — у каждой команды каталога должны быть
// обработчик, описание и группа, а имена не должны пересекаться между
// собой. Каталог питает роутер, /help и палитру TUI: дыра в нём — дыра
// во всех трёх сразу.
func TestCommandCatalogIntegrity(t *testing.T) {
	seen := map[string]bool{}
	groups := 0
	for i := range commandDefs {
		d := &commandDefs[i]
		if len(d.names) == 0 {
			t.Fatalf("команда #%d без имени", i)
		}
		if d.run == nil {
			t.Errorf("%s: нет обработчика", d.names[0])
		}
		if d.desc == "" {
			t.Errorf("%s: нет описания - в палитре и /help будет пустая строка", d.names[0])
		}
		if d.group != "" {
			groups++
		}
		for _, n := range d.names {
			if seen[n] {
				t.Errorf("имя %s повторяется в каталоге", n)
			}
			seen[n] = true
		}
	}
	if groups == 0 {
		t.Error("ни у одной команды нет группы - /help станет пустым")
	}
	// Индекс обязан покрывать все имена каталога.
	for name := range seen {
		if lookupCommand(name) == nil {
			t.Errorf("lookupCommand(%s) = nil, а имя есть в каталоге", name)
		}
	}
}

// TestCatalogCoversRouter — каждый обработчик каталога реально
// вызывается: выборочная проверка пары команд через handleCommand.
func TestCatalogCoversRouter(t *testing.T) {
	a, buf := cmdApp(t)
	for _, line := range []string{"/todos", "/cls"} {
		if !a.handleCommand(line) {
			t.Errorf("%q прервал сеанс", line)
		}
	}
	_ = buf
}

// TestLevenshtein — расстояние редактирования.
func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"модель", "модели", 1},
		{"models", "model", 1},
		{"", "abc", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, ждали %d", c.a, c.b, got, c.want)
		}
	}
}

// TestClosestCommands — опечатка получает подсказку, точное имя не
// советует само себя.
func TestClosestCommands(t *testing.T) {
	a, _ := cmdApp(t)
	sugg := a.closestCommands("/modles")
	if len(sugg) == 0 {
		t.Fatal("опечатка /modles не получила подсказок")
	}
	found := false
	for _, s := range sugg {
		if s == "/models" || s == "/model" {
			found = true
		}
	}
	if !found {
		t.Errorf("среди подсказок нет /models: %v", sugg)
	}
	if sugg := a.closestCommands("/help"); len(sugg) != 0 {
		t.Errorf("точное имя советует других: %v", sugg)
	}
}

// TestContainsModel — сравнение имён моделей без учета регистра.
func TestContainsModel(t *testing.T) {
	if !containsModel([]string{"GLM-5.3"}, "glm-5.3") {
		t.Error("регистр не должен иметь значения")
	}
	if containsModel([]string{"glm-5.2"}, "glm-5.3") {
		t.Error("разные модели совпали")
	}
}

// TestClosestNames — общий помощник подсказок.
func TestClosestNames(t *testing.T) {
	known := []string{"zai", "openai", "openrouter", "deepseek"}
	got := closestNames("openaii", known)
	if len(got) == 0 || got[0] != "openai" {
		t.Errorf("openaii -> %v, ждали openai первым", got)
	}
	// префиксные совпадения идут раньше левенштейна
	got = closestNames("open", known)
	if len(got) != 2 || got[0] != "openai" || got[1] != "openrouter" {
		t.Errorf("open -> %v", got)
	}
}
