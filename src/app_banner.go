package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"gcli/providers"
	"gcli/ui"
)

func osGOOS() string { return runtime.GOOS + "/" + runtime.GOARCH }

func osArch() string { return runtime.Version() }

// banner - приветственный экран в стиле Claude Code: жирная строка
// приветствия, подсказка, каталог и модель. Без бокса и без брендовых
// значков: приветствие - обычный текст, который можно выделить и скопировать.
func (a *app) banner() {
	if a.quiet {
		return
	}

	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Welcome to gCLI v"+version()))
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Gray("/help for help, /status for your current setup"))
	a.ui.Println("")
	a.ui.Println(a.ui.Gray("cwd: ") + a.workDir)
	prov, _ := a.providerModel()
	label := ""
	if prov != nil {
		label = prov.Label
	}
	if label != "" {
		a.ui.Println(a.ui.Gray("model: ") + a.model + a.ui.Gray(" · "+label))
	} else {
		a.ui.Println(a.ui.Gray("model: ") + a.model)
	}
	// Режимы - последним рядом: агент, план, автопилот, песочница.
	if chips := a.modeChips(); chips != "" {
		a.ui.Println(a.ui.Gray("mode: ") + chips)
	}
	// Нет ключа - предупреждаем сразу: это первое, что надо исправить.
	if !a.prov.HasKey() {
		a.ui.Println(a.ui.Gray("key:  ") + a.ui.Paint(a.ui.Pal().Warn, "не задан - /setup"))
	}

	// Замечания старта - приглушённым списком, без дублирования: заметка
	// может прийти и через setupNotes, и через startupNotes, а печатать
	// её дважды - прямой путь к «вырвиглазному» старту.
	seen := map[string]bool{}
	for _, n := range a.setupNotes {
		key := strings.TrimSpace(n)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		a.ui.Info(n)
	}
	for _, n := range a.startupNotes {
		key := strings.TrimSpace(n)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		a.ui.Info(n)
	}
	if a.pool.Enabled() {
		a.ui.Info(a.ui.Gray("субагенты подключены - /agents"))
	}
	a.ui.Println("")
}

// modeChips - краткие бейджи режимов для бокса и строки подсказок.
func (a *app) modeChips() string {
	var chips []string
	sessMu.Lock()
	agentOn, auto, autoAll, bashAll := false, false, false, false
	if a.sess != nil {
		agentOn = a.sess.AgentMode
		auto = a.sess.Perms.Autopilot
		autoAll = a.sess.Perms.AutopilotAll
		bashAll = a.sess.Perms.BashAll
	}
	sessMu.Unlock()
	if agentOn {
		chips = append(chips, a.ui.Accent("agent"))
	}
	if a.cfgPlanMode() {
		chips = append(chips, a.ui.Paint(a.ui.Pal().Warn, "plan"))
	}
	switch {
	case autoAll:
		chips = append(chips, a.ui.Paint(a.ui.Pal().Err, "autopilot: all"))
	case auto:
		chips = append(chips, a.ui.Accent("autopilot"))
	case bashAll:
		chips = append(chips, a.ui.Paint(a.ui.Pal().Warn, "yolo"))
	}
	return strings.Join(chips, " · ")
}

// footerRight - правая часть строки подсказок под полем ввода:
// режимы, модель и занятый контекст.
func (a *app) footerRight() string {
	var chips []string
	if c := a.modeChips(); c != "" {
		chips = append(chips, c)
	}
	_, mdl := a.providerModel()
	if mdl != "" {
		chips = append(chips, mdl)
	}
	m := a.metrics()
	if m.Context > 0 {
		chips = append(chips, m.ContextText())
	}
	return strings.Join(chips, " · ")
}

// menuItems - пункты pop-up меню команд по «/».
//
// Собирается из каталога commandDefs - того же, что питает роутер и
// /help: три списка раньше переписывались отдельно и расползались.
// Свои команды из .gcli/commands идут после встроенных: имя, совпавшее
// со встроенным, не показывается второй раз - роутер и меню отдают
// приоритет встроенным одинаково.
func (a *app) menuItems() []ui.MenuItem {
	out := make([]ui.MenuItem, 0, len(commandDefs)+4)
	for i := range commandDefs {
		d := &commandDefs[i]
		if d.group == "" {
			continue
		}
		out = append(out, ui.MenuItem{Name: d.names[0], Desc: d.desc})
	}
	for _, r := range a.customHelpRows() {
		name := strings.TrimPrefix(r[0], "/")
		if lookupCommand("/"+name) != nil {
			continue
		}
		out = append(out, ui.MenuItem{Name: "/" + name, Desc: r[1]})
	}
	return out
}

// PrintNoKeyHint - подсказка, если у провайдера нет ключа.
func (a *app) PrintNoKeyHint() {
	if a.prov.HasKey() {
		return
	}
	a.ui.Info("Нет ключа для " + a.prov.Label + " - настройка: " + a.ui.Accent("/setup") +
		a.ui.Gray("  |  или свой endpoint: /provider add <id> <base_url>"))
}

func (a *app) autopilotChip() (string, string) {
	switch {
	case a.sess.Perms.AutopilotAll:
		return "всё без спроса (рискованно)", a.ui.Pal().Err
	case a.sess.Perms.Autopilot:
		return "вкл | безопасные действия", a.ui.Pal().Accent
	}
	return "", ""
}

// usage - справка по флагам.
func usage() {
	fmt.Println("gcli v" + version() + " - ИИ-агент в терминале (в духе Claude Code)")
	fmt.Println()
	fmt.Println("Использование: gcli [флаги] [команда]")
	fmt.Println()
	fmt.Println("Основное:")
	fmt.Println("  -p <запрос>       один запрос и выход (для скриптов и CI)")
	fmt.Println("  -watch <команда>  слежение за файлами; проверка упала - агент чинит")
	fmt.Println("  -mcp-serve        gcli как MCP-сервер по stdio (для других агентов)")
	fmt.Println("  -stats            дашборд расходов по сессиям и выход")
	fmt.Println("  -c                продолжить последнюю сессию")
	fmt.Println("  -r <номер>        возобновить сессию NN (см. /sessions)")
	fmt.Println("  -agent on|off     агентный режим (по умолчанию on)")
	fmt.Println("  -autopilot on|off  автопилот: сам одобряет безопасные действия")
	fmt.Println("  -autopilot-all on|off  автопилот повышенного риска: одобряет всё")
	fmt.Println("  -yolo             не спрашивать подтверждений (кроме опасных команд)")
	fmt.Println("  -snapshots on|off теневые снимки проекта на каждый ход (откат - /revert)")
	fmt.Println()
	fmt.Println("Провайдер и модель:")
	fmt.Println("  -provider <id>    zai | openrouter | openai | anthropic | ollama | свой id")
	fmt.Println("  -model <имя>      модель, напр. glm-5.3 или openai/gpt-6-luna")
	fmt.Println("  -setup            мастер настройки: провайдер -> ключ -> модель")
	fmt.Println("  -subagent on|off  разрешить субагентов")
	fmt.Println()
	fmt.Println("Автономный прогон (без новых промптов):")
	fmt.Println("  -mission <режим> long-time | extra-long-time | overnight, либо путь к mission.json")
	fmt.Println("  -deadline <срок>  4h, 90m или число минут")
	fmt.Println("  -budget <предел>  500k (токены), 25 (доллары), 500k,25 (оба)")
	fmt.Println("  -objective <цель> цель одной строкой")
	fmt.Println("  -cron on|off      автономные итерации: агент сам чинит баги, проблемы, уязвимости при тишине 15-20 мин")
	fmt.Println("  -cron-interval <длительность>  интервал итераций (по умолчанию 15-20 мин)")
	fmt.Println("  Задание живёт в .gcli/mission.json; флаги переопределяют его на этот запуск.")
	fmt.Println("  То же самое из разговора: /mission start long-time 4h 1m \"цель\"")
	fmt.Println()
	fmt.Println("Интерфейс:")
	fmt.Println("  -compact          компактный вывод (меньше пустых строк)")
	fmt.Println("  -no-color         без цвета")
	fmt.Println("  -color <уровень>  auto | 16 | 256 | truecolor | none")
	fmt.Println("  -ascii            без юникод-глифов (рамки и маркеры из ASCII)")
	fmt.Println("  -json             машинный вывод: только ответ, без UI")
	fmt.Println("  -v                версия и платформа")
	fmt.Println()
	fmt.Println("Переменные окружения:")
	fmt.Println("  ZAI_API_KEY         Z.ai GLM (ZAI_BASE_URL)")
	fmt.Println("  OPENROUTER_API_KEY  OpenRouter - 300+ моделей одним ключом")
	fmt.Println("  OPENAI_API_KEY      OpenAI/совместимые (OPENAI_BASE_URL, OPENAI_MODEL)")
	fmt.Println("  ANTHROPIC_API_KEY   Anthropic (ANTHROPIC_BASE_URL, ANTHROPIC_MODEL)")
	fmt.Println("  DEEPSEEK_API_KEY | GROQ_API_KEY | MISTRAL_API_KEY | XAI_API_KEY | TOGETHER_API_KEY")
	fmt.Println("  GCLI_HOME           каталог данных (по умолчанию ~/.gcli)")
	fmt.Println("  GCLI_SHELL          оболочка для bash (bash, powershell, cmd)")
	fmt.Println("  GCLI_COLOR          уровень цвета: 16 | 256 | truecolor | none")
	fmt.Println("  NO_COLOR | GCLI_ASCII | GCLI_NO_ANIM")
	fmt.Println()
	fmt.Println("Свой endpoint любого сервиса:")
	fmt.Println("  /provider add <id> <base_url> [openai|anthropic]")
	fmt.Println("  /provider key <id> <ключ>        ключ в ~/.gcli/config.json")
	fmt.Println()
	fmt.Println("Расширяемость:")
	fmt.Println("  навыки      ~/.gcli/skills/*.md и ./.gcli/skills/*.md (+ встроенные)")
	fmt.Println("  агенты      ~/.gcli/agents/*.md и ./.gcli/agents/*.md - свои субагенты")
	fmt.Println("  расширения  ~/.gcli/extensions/*.json и ./.gcli/extensions/*.json")
	fmt.Println("  MCP         ~/.gcli/mcp.json и ./.gcli/mcp.json - Model Context Protocol")
	fmt.Println("  данные      ~/.gcli (конфиг, сессии, экспорты, чекпоинты)")
	fmt.Println()
	fmt.Println("Зрение агента:")
	fmt.Println("  screenshot / read_image - снимок страницы или картинка в контекст.")
	fmt.Println("  Нужен headless-браузер (Edge/Chrome/Chromium); путь: GCLI_BROWSER.")
}

// pingProvider - проверить доступность endpoint-а.
func pingProvider(a *app) (time.Duration, int, error) {
	return providers.Ping(a.client, a.prov)
}

// testKey - проверить ключ активного провайдера.
func testKey(a *app) (bool, string) { return providers.TestKey(a.client, a.prov) }

// testKeyFor - проверить ключ произвольного провайдера.
func testKeyFor(a *app, p *providers.Provider) (bool, string) {
	return providers.TestKey(a.client, p)
}

// mustNotExist - проверка наличия файла.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
