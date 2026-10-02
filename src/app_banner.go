package main

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"gcli/core"
	"gcli/providers"
)

func osGOOS() string { return runtime.GOOS + "/" + runtime.GOARCH }

func osArch() string { return runtime.Version() }

// banner — приветственный экран.
//
// Плоская вёрстка в духе Claude Code: три строки параметров сессии —
// и всё. Кот в баннере не нужен: он сидит на своём единственном посту —
// над строкой состояния, прямо под этим экраном. Второй кот рядом
// выглядел бы как баг, а не как встреча.
func (a *app) banner() {
	// Правая колонка: не больше трёх строк — по числу строк кота.
	key := a.ui.Red("не задан — /setup")
	switch {
	case a.prov.NoKey:
		key = a.ui.Green("локальный провайдер")
	case a.prov.Key != "":
		key = a.ui.Green("задан")
	}
	agent := a.ui.Gray("агент выкл")
	if a.sess.AgentMode {
		agent = a.ui.Accent("агент")
	}
	line3 := agent
	if a.pool.Enabled() {
		line3 += a.ui.Gray("  ·  ") + a.ui.Paint(a.ui.Pal().Accent2, "субагенты")
	}
	if a.repo.Cfg.PlanMode {
		line3 += a.ui.Gray("  ·  ") + a.ui.Paint(a.ui.Pal().Warn, "план")
	}
	right := []string{
		a.ui.Accent(a.ui.Glyphs().Star) + " " + a.ui.Bold("gcli") +
			a.ui.Gray(" v"+version()) + a.ui.Gray("  ·  ИИ-агент в терминале"),
		a.ui.Gray("модель ") + a.ui.Accent(a.model) + a.ui.Gray("  ·  каталог ") +
			a.ui.Gray(core.RelToWD(a.workDir, a.workDir)),
		a.ui.Gray("ключ ") + key + a.ui.Gray("  ·  ") + line3,
	}
	// Автопилот — предупреждаем сразу, чтобы режим не был сюрпризом.
	if ap, risk := a.autopilotChip(); ap != "" {
		right[2] += a.ui.Gray("  ·  ") + a.ui.Paint(a.ui.Pal().Warn, "автопилот ") + a.ui.Paint(risk, ap)
	}

	a.ui.Println("")
	for _, line := range right {
		a.ui.Println("  " + line)
	}
	a.PrintNoKeyHint()
	for _, n := range a.setupNotes {
		a.ui.Info(n)
	}
	a.ui.Println("")
}

// PrintNoKeyHint — подсказка, если у провайдера нет ключа.
func (a *app) PrintNoKeyHint() {
	if a.prov.HasKey() {
		return
	}
	// В «УГЛЕ» подсказка — ещё одна строка потока: ромб-маркер акцентом,
	// зубец продолжения — как у результатов инструментов.
	a.ui.Println("  " + a.ui.Yellow("▲ ") +
		"Нет ключа для " + a.prov.Label + " — настройка: " + a.ui.Accent("/setup") +
		a.ui.Gray("  ·  или свой endpoint: /provider add <id> <base_url>"))
}

func (a *app) autopilotChip() (string, string) {
	switch {
	case a.sess.Perms.AutopilotAll:
		return "всё без спроса (рискованно)", a.ui.Pal().Err
	case a.sess.Perms.Autopilot:
		return "вкл · безопасные действия", a.ui.Pal().Accent
	}
	return "", ""
}

// usage — справка по флагам.
func usage() {
	fmt.Println("gcli v" + version() + " — ИИ-агент в терминале (в духе Claude Code)")
	fmt.Println()
	fmt.Println("Использование: gcli [флаги] [команда]")
	fmt.Println()
	fmt.Println("Основное:")
	fmt.Println("  -p <запрос>       один запрос и выход (для скриптов и CI)")
	fmt.Println("  -watch <команда>  слежение за файлами; проверка упала — агент чинит")
	fmt.Println("  -serve <адрес>    HTTP-сервер: API /v1/status, /v1/message, /v1/events")
	fmt.Println("  -mcp-serve        gcli как MCP-сервер по stdio (для других агентов)")
	fmt.Println("  -stats            дашборд расходов по сессиям и выход")
	fmt.Println("  -c                продолжить последнюю сессию")
	fmt.Println("  -r <номер>        возобновить сессию №N (см. /sessions)")
	fmt.Println("  -agent on|off     агентный режим (по умолчанию on)")
	fmt.Println("  -autopilot on|off  автопилот: сам одобряет безопасные действия")
	fmt.Println("  -autopilot-all on|off  автопилот повышенного риска: одобряет всё")
	fmt.Println("  -yolo             не спрашивать подтверждений (кроме опасных команд)")
	fmt.Println("  -snapshots on|off теневые снимки проекта на каждый ход (откат — /revert)")
	fmt.Println()
	fmt.Println("Провайдер и модель:")
	fmt.Println("  -provider <id>    zai | openrouter | openai | anthropic | ollama | свой id")
	fmt.Println("  -model <имя>      модель, напр. glm-4.6 или openai/gpt-4o")
	fmt.Println("  -setup            мастер настройки: провайдер → ключ → модель")
	fmt.Println("  -subagent on|off  разрешить субагентов")
	fmt.Println()
	fmt.Println("Автономный прогон (без новых промптов):")
	fmt.Println("  -mission <режим> long-time | extra-long-time | overnight, либо путь к mission.json")
	fmt.Println("  -deadline <срок>  4h, 90m или число минут")
	fmt.Println("  -budget <предел>  500k (токены), 25 (доллары), 500k,25 (оба)")
	fmt.Println("  -objective <цель> цель одной строкой")
	fmt.Println("  Задание живёт в .gcli/mission.json; флаги переопределяют его на этот запуск.")
	fmt.Println("  То же самое из разговора: /mission start long-time 4h 1m «цель»")
	fmt.Println()
	fmt.Println("Интерфейс:")
	fmt.Println("  -compact          компактный вывод (меньше пустых строк)")
	fmt.Println("  -no-color         без цвета")
	fmt.Println("  -color <уровень>  auto | 16 | 256 | truecolor | none")
	fmt.Println("  -ascii            только ASCII-псевдографика")
	fmt.Println("  -json             машинный вывод: только ответ, без UI")
	fmt.Println("  -v                версия и платформа")
	fmt.Println()
	fmt.Println("Маскот:")
	fmt.Println("  Искра — кот gcli. Живёт над строкой состояния: моргает на простое,")
	fmt.Println("  скучает и засыпает, если долго не писать, думает («thinking...»")
	fmt.Println("  переливается цветом) и работает вместе с ИИ, мурчит от результата")
	fmt.Println("  и фыркает на ошибках.")
	fmt.Println("  /mascot on|off|demo|say|wave · GCLI_MASCOT=0 — выключить")
	fmt.Println()
	fmt.Println("Переменные окружения:")
	fmt.Println("  ZAI_API_KEY         Z.ai GLM (ZAI_BASE_URL)")
	fmt.Println("  OPENROUTER_API_KEY  OpenRouter — 300+ моделей одним ключом")
	fmt.Println("  OPENAI_API_KEY      OpenAI/совместимые (OPENAI_BASE_URL, OPENAI_MODEL)")
	fmt.Println("  ANTHROPIC_API_KEY   Anthropic (ANTHROPIC_BASE_URL, ANTHROPIC_MODEL)")
	fmt.Println("  DEEPSEEK_API_KEY · GROQ_API_KEY · MISTRAL_API_KEY · XAI_API_KEY · TOGETHER_API_KEY")
	fmt.Println("  GCLI_HOME           каталог данных (по умолчанию ~/.gcli)")
	fmt.Println("  GCLI_SHELL          оболочка для bash (bash, powershell, cmd)")
	fmt.Println("  GCLI_COLOR          уровень цвета: 16 | 256 | truecolor | none")
	fmt.Println("  NO_COLOR · GCLI_ASCII · GCLI_NO_ANIM · GCLI_MASCOT")
	fmt.Println()
	fmt.Println("Свой endpoint любого сервиса:")
	fmt.Println("  /provider add <id> <base_url> [openai|anthropic]")
	fmt.Println("  /provider key <id> <ключ>        ключ в ~/.gcli/config.json")
	fmt.Println()
	fmt.Println("Расширяемость:")
	fmt.Println("  навыки      ~/.gcli/skills/*.md и ./.gcli/skills/*.md (+ встроенные)")
	fmt.Println("  агенты      ~/.gcli/agents/*.md и ./.gcli/agents/*.md — свои субагенты")
	fmt.Println("  расширения  ~/.gcli/extensions/*.json и ./.gcli/extensions/*.json")
	fmt.Println("  MCP         ~/.gcli/mcp.json и ./.gcli/mcp.json — Model Context Protocol")
	fmt.Println("  данные      ~/.gcli (конфиг, сессии, экспорты, чекпоинты)")
	fmt.Println()
	fmt.Println("Зрение агента:")
	fmt.Println("  screenshot / read_image — снимок страницы или картинка в контекст.")
	fmt.Println("  Нужен headless-браузер (Edge/Chrome/Chromium); путь: GCLI_BROWSER.")
}

// pingProvider — проверить доступность endpoint-а.
func pingProvider(a *app) (time.Duration, int, error) {
	return providers.Ping(a.client, a.prov)
}

// testKey — проверить ключ активного провайдера.
func testKey(a *app) (bool, string) { return providers.TestKey(a.client, a.prov) }

// testKeyFor — проверить ключ произвольного провайдера.
func testKeyFor(a *app, p *providers.Provider) (bool, string) {
	return providers.TestKey(a.client, p)
}

// mustNotExist — проверка наличия файла.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
