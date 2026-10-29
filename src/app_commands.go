package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
	"gcli/ui"
)

// handleCommand — обработать slash-команду. false = выход.
func (a *app) handleCommand(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return true
	}
	cmd := strings.ToLower(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	arg := ""
	if len(fields) > 1 {
		arg = fields[1]
	}

	switch cmd {
	case "/quit", "/exit", "/q":
		return false
	case "/help", "/?", "/h":
		a.cmdHelp()
	case "/status":
		a.cmdStatus()
	case "/iters":
		a.cmdIters()
	case "/model":
		a.cmdModel(rest)
	case "/provider":
		a.cmdProvider(rest)
	case "/setup":
		a.runSetup()
	case "/models":
		a.cmdModels(rest)
	case "/think":
		a.cmdThink(arg)
	case "/skills", "/skill":
		a.cmdSkills(rest)
	case "/ext":
		a.cmdExt(rest)
	case "/agents":
		a.cmdAgents(rest)
	case "/agent":
		a.cmdAgent(arg)
	case "/yolo":
		a.cmdYolo()
	case "/autopilot":
		a.cmdAutopilot(rest)
	case "/mission":
		a.cmdMission(rest)
	case "/todos":
		a.ui.RenderTodos(a.sessionTodos())
		a.ui.Println("")
	case "/compact":
		a.cmdCompact()
	case "/sessions":
		a.cmdSessions()
	case "/fork":
		a.cmdFork(rest)
	case "/resume":
		a.cmdResume(arg)
	case "/clear":
		a.cmdClear()
	case "/export":
		a.cmdExport()
	case "/permissions":
		a.cmdPerms(rest)
	case "/sandbox":
		a.cmdSandbox(rest)
	case "/cron":
		a.cmdCron(rest)
	case "/usage":
		a.cmdUsage()
	case "/stats":
		a.cmdStats(rest)
	case "/tools":
		a.cmdTools()
	case "/doctor":
		a.cmdDoctor(rest)
	case "/init":
		a.cmdInit(rest)
	case "/memory":
		a.cmdMemory()
	case "/mascot":
		a.cmdMascot(rest)
	case "/mcp":
		a.cmdMcp(rest)
	case "/context":
		a.cmdContext()
	case "/plan":
		a.cmdPlan(rest)
	case "/ab":
		a.cmdAb(rest)
	case "/undo":
		a.cmdUndo()
	case "/snapshots":
		a.cmdSnapshots()
	case "/revert":
		a.cmdRevert(rest)
	case "/copy":
		a.cmdCopy()
	case "/cls", "/clear-screen":
		a.ui.ClearScreen()
	default:
		// Кастомные slash-команды из .gcli/commands и ~/.gcli/commands:
		// файл commit.md превращается в /commit.
		if cmd, ok := a.findCustomCommand(cmd); ok {
			a.runCustomCommand(cmd, rest)
			return true
		}
		a.ui.Warn("неизвестная команда: " + cmd + " — /help для списка")
	}
	return true
}

// ---------- Справка и статус ----------

func (a *app) cmdHelp() {
	groups := []struct {
		title string
		rows  [][2]string
	}{
		{"Настройка", [][2]string{
			{"/setup", "мастер: провайдер → ключ → модель"},
			{"/provider", "провайдеры; add — свой endpoint, key, model, rm"},
			{"/models [фильтр]", "модели с endpoint-а провайдера"},
			{"/model [имя]", "сменить модель"},
			{"/think on|off|auto", "размышления модели"},
			{"/mascot", "кот Искра: on|off, demo, say <текст>, wave"},
		}},
		{"Агент", [][2]string{
			{"/agents", "субагенты: список, запуск, настройки"},
			{"/agents run <тип> <задача>", "запустить: explorer, planner, tester, frontend…"},
			{"/agents types", "все типы + свои агенты"},
			{"/agents new <имя>", "свой агент (.gcli/agents/*.md)"},
			{"/agent on|off", "агентный режим (инструменты)"},
			{"/plan on|off", "режим планирования: сначала план — потом код"},
			{"/ab <модель> <промпт>", "один вопрос двум моделям: сравнить ответ, время, цену"},
			{"/autopilot on|off|all", "автопилот: одобрять безопасные действия самому"},
			{"/sandbox on|off", "песочница файлов: пути вне рабочего каталога запрещены"},
			{"/cron on|off|status", "автономные итерации: сам ищет и чинит баги, уязвимости, проблемы"},
			{"/yolo", "без подтверждений (кроме опасных команд)"},
			{"/tools", "список инструментов агента"},
			{"/mission", "автономный прогон: start, stop, status, save, report"},
			{"/cron on|off|status", "автономные итерации: поиск и исправление багов, уязвимости, проблемы при тишине 15–20 мин"},
			{"/iters", "лимит итераций и продления хода; при прогоне — его расход"},
			{"/todos", "план текущей задачи"},
			{"/compact", "сжать историю диалога"},
			{"/snapshots, /revert N", "снимки проекта по ходам; откат к снимку N"},
		}},
		{"Расширение", [][2]string{
			{"/skills, /skill", "навыки; new, on|off, показать"},
			{"/ext", "расширения; new, trust, reload"},
			{"/mcp", "MCP-серверы; trust, reload, new, path"},
			{"/init", "разобрать проект: GCLI.md + gcli.json; show, force"},
			{"/memory", "файлы памяти проекта"},
		}},
		{"Сессия", [][2]string{
			{"/status, /usage", "состояние, токены, контекст"},
			{"/stats [дней]", "дашборд расходов: по дням и моделям"},
			{"/context", "разбивка контекста по частям"},
			{"/sessions, /resume", "сохранённые сессии"},
			{"/fork [N]", "новая сессия из истории текущей до сообщения N"},
			{"/clear", "новая сессия"},
			{"/export", "экспорт диалога в Markdown"},
			{"/undo", "отменить последнюю правку файла"},
			{"/permissions [reset]", "разрешения сессии, доверие коду, сброс"},
		}}, {"Сервис", [][2]string{
			{"/doctor [key]", "диагностика окружения и ключа"},
			{"/copy", "скопировать последний ответ (OSC52)"},
			{"/cls", "очистить экран"},
			{"/quit", "выход (также Ctrl+D)"},
		}},
	}
	a.ui.Println("")
	for _, g := range groups {
		a.ui.Println("  " + a.ui.Head(g.title))
		for _, r := range g.rows {
			a.ui.Println("    " + a.ui.Accent(ui.PadRight(r[0], 28)) + a.ui.Gray(r[1]))
		}
		a.ui.Println("")
	}
	// Кастомные команды из каталогов .gcli/commands.
	if rows := a.customHelpRows(); len(rows) > 0 {
		a.ui.Println("  " + a.ui.Head("Свои команды"))
		for _, r := range rows {
			a.ui.Println("    " + a.ui.Accent(ui.PadRight(r[0], 28)) + a.ui.Gray(r[1]))
		}
		a.ui.Println("")
	}
	a.ui.Println("  " + a.ui.Gray("Подсказки: @файл — приложить файл · \\ в конце строки — перенос"))
	a.ui.Println("  " + a.ui.Gray("#текст — записать факт в память проекта (GCLI.md) · Ctrl+C — прервать генерацию"))
	a.ui.Println("")
}

func (a *app) cmdStatus() {
	// Снимки под sessMu: панель собирается в очереди ввода, а счётчики
	// (usage, чекпоинты, история) пишут ход и горутины субагентов
	// параллельно, в том числе из cron-хода.
	msgs := a.Messages()
	id, _, turnsN := a.sessionCounters()
	u := a.sessionUsage()
	a.ui.Println("")
	a.ui.KVPairs([][2]string{
		{"Провайдер", a.prov.Label + "  (" + a.prov.ID + ")"},
		{"Модель", a.model},
		{"Ключ", keyStatus(a.prov)},
		{"Режим", modeStatus(a.agentMode()) + " · инструментов: " + strconv.Itoa(a.tools.Count()) +
			" · расширений: " + strconv.Itoa(a.tools.ExtCount()) +
			" · MCP: " + strconv.Itoa(a.tools.MCPToolCount())},
		{"Субагенты", agentsStatus(a)},
		{"Автопилот", autopilotStatus(a) + " · " + autopilotHint(a)},
		{"Размышления", a.repo.Cfg.Think},
		{"Сессия", id + " · сообщений: " + strconv.Itoa(len(msgs)) + " · ходов: " + strconv.Itoa(turnsN)},
		{"Контекст", "~" + core.Kfmt(core.EstimateContext(msgs)) + " ток. / порог " + core.Kfmt(a.autoCompactLimit())},
		{"Токены", "↑" + core.Kfmt(u.PromptTokens) + " ↓" + core.Kfmt(u.CompletionTokens)},
		{"Скиллы", strconv.Itoa(len(a.tools.LoadSkills()))},
		{"Цвет", a.ui.ColorLevel().String() +
			" · оболочка: " + tools.ShellName()},
		{"Каталог", a.workDir},
	})
	a.ui.Println("")
}

func keyStatus(p *providers.Provider) string {
	if p.NoKey {
		return "не требуется (локальный)"
	}
	if p.Key == "" {
		return "НЕ ЗАДАН — /setup"
	}
	return providers.MaskKey(p.Key)
}

func modeStatus(on bool) string {
	if on {
		return "вкл"
	}
	return "выкл"
}

func agentsStatus(a *app) string {
	if !a.pool.Enabled() {
		return "выключены (/agents on)"
	}
	return fmt.Sprintf("включены · параллельно: %d · глубина: %d · запусков: %d",
		a.repo.Cfg.SubMaxPar, a.repo.Cfg.SubMaxDepth, a.subagentRunCount())
}

// autopilotStatus — состояние автопилота одной строкой.
func autopilotStatus(a *app) string {
	switch {
	case a.sess.Perms.AutopilotAll:
		return "all — одобряет всё, включая опасные команды (рискованно)"
	case a.sess.Perms.Autopilot:
		return "on — безопасные действия одобряются без вопроса"
	}
	return "off — каждое действие спрашивает"
}

// autopilotHint — короткая подсказка по автопилоту.
func autopilotHint(a *app) string {
	if a.sess.Perms.Autopilot {
		return "/autopilot off"
	}
	return "/autopilot on"
}

// ---------- Провайдеры и модели ----------

func (a *app) cmdProvider(rest string) {
	parts := strings.Fields(rest)
	if len(parts) == 0 || parts[0] == "list" || parts[0] == "список" {
		a.listProviders()
		return
	}
	switch parts[0] {
	case "add", "import":
		if len(parts) < 3 {
			a.ui.Err("формат: /provider add <id> <base_url> [openai|anthropic]")
			a.ui.Hint("пример: /provider add myapi https://api.myservice.ru/v1")
			return
		}
		id := strings.ToLower(parts[1])
		base := parts[2]
		proto := providers.ProtoOpenAI
		if len(parts) > 3 {
			proto = strings.ToLower(parts[3])
		}
		if !providers.ValidID(id) {
			a.ui.Err("id: латиница/цифры/_/-, 2–25 символов")
			return
		}
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			a.ui.Err("base_url должен начинаться с http:// или https://")
			return
		}
		if proto != providers.ProtoOpenAI && proto != providers.ProtoAnthropic {
			a.ui.Err("протокол: openai или anthropic")
			return
		}
		pc := a.repo.EnsureProviderCfg(id)
		pc.BaseURL = strings.TrimRight(base, "/")
		pc.Protocol = proto
		pc.Custom = true
		a.saveConfig()
		a.registry = a.registry.Rebuild(a.repo.Cfg)
		a.ui.Ok(fmt.Sprintf("провайдер «%s» → %s (%s)", id, base, proto))
		a.ui.Hint("далее: /provider key " + id + " <ключ> · /provider " + id)
	case "key", "ключ", "keys", "ключи":
		a.cmdProviderKey(parts)
	case "model", "модель":
		if len(parts) < 3 {
			a.ui.Err("формат: /provider model <id> <имя модели>")
			return
		}
		id := strings.ToLower(parts[1])
		a.repo.EnsureProviderCfg(id).Model = parts[2]
		a.saveConfig()
		a.registry = a.registry.Rebuild(a.repo.Cfg)
		a.ui.Ok(fmt.Sprintf("модель «%s» для «%s» сохранена", parts[2], id))
	case "rm", "remove", "удалить":
		if len(parts) < 2 {
			a.ui.Err("формат: /provider rm <id>")
			return
		}
		id := strings.ToLower(parts[1])
		pc, ok := a.repo.Cfg.Providers[id]
		if !ok || !pc.Custom {
			a.ui.Err("«" + id + "» не найден среди своих провайдеров (пресеты удалить нельзя)")
			return
		}
		delete(a.repo.Cfg.Providers, id)
		a.saveConfig()
		a.registry = a.registry.Rebuild(a.repo.Cfg)
		if a.prov.ID == id {
			np := a.registry.Pick("")
			a.setProviderModel(np, providers.ResolveModel(np, "", a.repo.Cfg.Model))
		}
		a.ui.Ok("провайдер «" + id + "» удалён")
	default:
		if p := a.registry.Find(strings.ToLower(parts[0])); p != nil {
			a.switchProvider(p)
		} else {
			a.ui.Warn("нет провайдера «" + parts[0] + "» — /provider list")
		}
	}
}

// cmdProviderKey — работа с ключами провайдера.
//
// Ключей у провайдера может быть много: основной и запасные. Когда
// основной упирается в лимит, запрос продолжается со следующего, и
// сессия не встаёт.
//
//	/provider key add <id> <ключ> [ключ…]   добавить ключи
//	/provider key rm <id> <N>                убрать ключ номер N
//	/provider key use <id> <N>               сделать ключ N основным
//	/provider key reset <id>                 снять паузы с ключей
//	/provider keys <id>                      показать пул
func (a *app) cmdProviderKey(parts []string) {
	// /provider keys <id> — показать пул.
	if len(parts) >= 2 && isKeysList(parts[0]) {
		a.showProviderKeys(parts[1])
		return
	}
	// Форма «/provider key <id> <ключ>» (старая) остаётся рабочей.
	if len(parts) >= 3 && !isKeySub(parts[1]) {
		a.addProviderKeys(parts[1], parts[2:])
		return
	}
	if len(parts) < 3 {
		a.ui.Err("формат: /provider key add <id> <ключ> · /provider keys <id>")
		return
	}
	sub := strings.ToLower(parts[1])
	id := strings.ToLower(parts[2])
	switch sub {
	case "add", "добавить":
		a.addProviderKeys(id, parts[3:])
	case "rm", "remove", "del", "удалить":
		if len(parts) < 4 {
			a.ui.Err("формат: /provider key rm <id> <номер>")
			return
		}
		a.removeProviderKey(id, parts[3:])
	case "use", "включить", "primary":
		if len(parts) < 4 {
			a.ui.Err("формат: /provider key use <id> <номер>")
			return
		}
		a.useProviderKey(id, parts[3:])
	case "reset", "сброс":
		a.resetProviderKeys(id)
	default:
		a.ui.Err("подкоманда: add · rm · use · reset · /provider keys <id>")
	}
}

// isKeysList — команда показа пула ключей.
func isKeysList(s string) bool {
	switch strings.ToLower(s) {
	case "keys", "ключи", "list", "список":
		return true
	}
	return false
}

// isKeySub — подкоманда управления ключами (а не id провайдера).
func isKeySub(s string) bool {
	switch strings.ToLower(s) {
	case "add", "rm", "remove", "del", "use", "reset", "добавить", "удалить", "включить", "сброс":
		return true
	}
	return false
}

// addProviderKeys — добавить ключи в пул провайдера.
func (a *app) addProviderKeys(id string, raw []string) {
	keys := providers.SplitKeys(strings.Join(raw, " "))
	if len(keys) == 0 {
		a.ui.Err("ключ пустой после нормализации")
		return
	}
	pc := a.repo.EnsureProviderCfg(id)
	added := 0
	for _, k := range keys {
		if containsKey(pc.Keys, k) {
			continue
		}
		pc.Keys = append(pc.Keys, k)
		added++
	}
	if added == 0 {
		a.ui.Warn("такие ключи уже есть в пуле")
		return
	}
	// Основной ключ — первый в пуле: старые конфиги читают api_key.
	if pc.APIKey == "" {
		pc.APIKey = pc.Keys[0]
	}
	a.saveConfig()
	a.registry = a.registry.Rebuild(a.repo.Cfg)
	a.syncProvider()
	// Основной ключ в конфиге — тот, что активен в пуле: иначе после
	// перезапуска пул начнёт с другого.
	if p := a.registry.Find(id); p != nil && p.Pool != nil {
		if cur := p.Pool.Current(); cur != "" && cur != pc.APIKey {
			pc.APIKey = cur
			a.saveConfig()
		}
	}
	a.ui.Ok(fmt.Sprintf("ключей у «%s» добавлено: %d (всего %d)", id, added, len(pc.Keys)))
	a.ui.Hint("при лимите gcli переключится на следующий ключ сам")
}

// removeProviderKey — убрать ключ номер N из пула.
func (a *app) removeProviderKey(id string, args []string) {
	if len(args) < 1 {
		a.ui.Err("формат: /provider key rm <id> <номер>")
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || n < 1 {
		a.ui.Err("номер ключа: целое число от 1")
		return
	}
	pc := a.repo.EnsureProviderCfg(id)
	if n > len(pc.Keys) {
		a.ui.Err(fmt.Sprintf("у «%s» всего %d ключей", id, len(pc.Keys)))
		return
	}
	removed := pc.Keys[n-1]
	pc.Keys = append(pc.Keys[:n-1], pc.Keys[n:]...)
	if pc.APIKey == removed {
		pc.APIKey = ""
		if len(pc.Keys) > 0 {
			pc.APIKey = pc.Keys[0]
		}
	}
	a.saveConfig()
	a.registry = a.registry.Rebuild(a.repo.Cfg)
	a.syncProvider()
	a.ui.Ok(fmt.Sprintf("ключ #%d у «%s» убран: %s", n, id, providers.MaskKey(removed)))
}

// useProviderKey — сделать ключ номер N основным.
func (a *app) useProviderKey(id string, args []string) {
	if len(args) < 1 {
		a.ui.Err("формат: /provider key use <id> <номер>")
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || n < 1 {
		a.ui.Err("номер ключа: целое число от 1")
		return
	}
	p := a.registry.Find(id)
	if p == nil || p.Pool == nil {
		a.ui.Err(fmt.Sprintf("у «%s» нет пула ключей", id))
		return
	}
	statuses := p.KeyStatuses()
	if n > len(statuses) {
		a.ui.Err(fmt.Sprintf("у «%s» всего %d ключей", id, len(statuses)))
		return
	}
	key := p.Pool.Keys()[n-1]
	p.Pool.Use(key)
	// Основной ключ провайдера в конфиге должен совпадать: иначе
	// следующий запуск вернёт старый.
	pc := a.repo.EnsureProviderCfg(id)
	pc.APIKey = key
	a.saveConfig()
	a.syncProvider()
	a.ui.Ok(fmt.Sprintf("ключ #%d у «%s» стал основным: %s", n, id, providers.MaskKey(key)))
}

// resetProviderKeys — снять паузы с ключей провайдера.
func (a *app) resetProviderKeys(id string) {
	p := a.registry.Find(id)
	if p == nil || p.Pool == nil {
		a.ui.Err(fmt.Sprintf("у «%s» нет пула ключей", id))
		return
	}
	p.Pool.Reset()
	a.ui.Ok(fmt.Sprintf("паузы ключей «%s» сняты", id))
}

// showProviderKeys — показать пул ключей провайдера.
func (a *app) showProviderKeys(id string) {
	p := a.registry.Find(id)
	if p == nil {
		a.ui.Err(fmt.Sprintf("провайдер «%s» не найден — /provider list", id))
		return
	}
	statuses := p.KeyStatuses()
	if len(statuses) == 0 {
		a.ui.Warn("у провайдера «" + id + "» нет ключей")
		return
	}
	rows := make([][2]string, 0, len(statuses))
	for _, st := range statuses {
		mark := " "
		switch {
		case st.Current:
			mark = "→"
		case st.Invalid:
			mark = "×"
		case st.Paused:
			mark = "⏸"
		}
		state := ""
		switch {
		case st.Invalid:
			state = a.ui.Red("отвергнут: " + st.Reason)
		case st.Paused:
			state = a.ui.Yellow("пауза до " + st.Until.Format("15:04:05") + " · " + st.Reason)
		}
		rows = append(rows, [2]string{
			mark + " " + strconv.Itoa(st.Index) + ".",
			a.ui.Accent(st.Masked) + "  " + state,
		})
	}
	a.ui.KVPanel("Ключи · "+id, rows)
	a.ui.Hint("добавить: /provider key add " + id + " <ключ> · основной: /provider key use " + id + " <N>")
}

// syncProvider — обновить текущего провайдера после пересборки реестра.
func (a *app) syncProvider() {
	if a.prov == nil {
		return
	}
	if p := a.registry.Find(a.prov.ID); p != nil {
		a.setProvider(p)
		a.bindKeyRotate(p)
	}
}

// bindKeyRotate — подписаться на смену ключа у провайдера.
func (a *app) bindKeyRotate(p *providers.Provider) {
	if p == nil {
		return
	}
	p.OnRotate = a.onKeyRotate
}

// onKeyRotate — сообщить, что ключ сменился.
//
// Молча переключаться нельзя: человек смотрит на счёт и видит расход по
// одному ключу, а потом вдруг получает отказ по лимиту. Смена видна —
// значит, поверить можно.
func (a *app) onKeyRotate(from, to int, reason string) {
	if a.quiet {
		return
	}
	a.ui.Warn(fmt.Sprintf("ключ #%d %s — переключаюсь на #%d", from, reason, to))
}

// containsKey — есть ли ключ в списке.
func containsKey(keys []string, k string) bool {
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}

func (a *app) listProviders() {
	rows := make([][]string, 0, len(a.registry.All))
	for i, p := range a.registry.All {
		mark := " "
		if p.ID == a.prov.ID {
			mark = "→"
		}
		key := a.ui.Red("нет ключа")
		switch {
		case p.NoKey:
			key = a.ui.Green("без ключа")
		default:
			if n := p.KeyCount(); n > 0 {
				key = a.ui.Green(providers.MaskKey(p.CurrentKey()))
				if n > 1 {
					// Запасные ключи видны сразу: человек должен знать,
					// что лимит одного ключа его не остановит.
					key += a.ui.Gray(fmt.Sprintf("  +%d", n-1))
				}
			}
		}
		label := p.Label
		if p.Custom {
			label += " " + a.ui.Magenta("[свой]")
		}
		rows = append(rows, []string{
			mark + " " + strconv.Itoa(i+1) + ". " + p.ID,
			label,
			p.BaseURL,
			key,
		})
	}
	a.ui.Table([]ui.Column{
		{Title: "id", Width: 16},
		{Title: "сервис", Width: 30},
		{Title: "base URL", Width: 42},
		{Title: "ключ", Width: 20},
	}, rows, ui.BlockOpts{Title: "Провайдеры"})
	a.ui.Println("  " + a.ui.Gray("свой endpoint: /provider add <id> <url> [openai|anthropic] · ключ: /provider key <id> <ключ>"))
	a.ui.Println("  " + a.ui.Gray("пул ключей: /provider keys <id> · добавить: /provider key add <id> <ключ> · при лимите gcli переключается сам"))
	a.ui.Println("  " + a.ui.Gray("переключиться: /provider <id> · удалить свой: /provider rm <id>"))
	a.ui.Println("")
}

func (a *app) switchProvider(p *providers.Provider) {
	// Пара читается и пишется одним снимком под provMu: ход из чужой
	// горутины не должен увидеть провайдера от одной подмены, а модель —
	// от другой.
	_, cur := a.providerModel()
	found := false
	for _, m := range p.Models {
		if m == cur {
			found = true
			break
		}
	}
	next := cur
	if !found {
		next = p.DefaultModel
	}
	a.setProviderModel(p, next)
	a.bindKeyRotate(p)
	cfgMu.Lock()
	a.repo.Cfg.Provider = p.ID
	cfgMu.Unlock()
	a.sess.Provider = p.ID
	a.sess.Model = next
	a.saveConfig()
	a.saveSession()
	a.syncMetrics()
	a.ui.Ok(fmt.Sprintf("провайдер: %s · модель: %s", p.Label, next))
}

// syncMetrics — обновить нижнюю панель и шапку TUI после смены модели.
//
// В полноэкранном режиме шапка показывает модель и провайдера из снимка
// метрик: без этого /model менял бы ответы, а подпись оставалась старой.
func (a *app) syncMetrics() {
	if a.ui == nil {
		return
	}
	a.ui.SetMetrics(a.metrics())
}

func (a *app) cmdModel(rest string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		rows := make([][]string, 0, len(a.registry.All))
		for _, p := range a.registry.All {
			mark := " "
			if p.ID == a.prov.ID {
				mark = "→"
			}
			rows = append(rows, []string{mark + " " + p.ID, p.Label, strings.Join(p.Models, ", ")})
		}
		a.ui.Table([]ui.Column{
			{Title: "id", Width: 14},
			{Title: "сервис", Width: 26},
			{Title: "модели", Width: 50},
		}, rows, ui.BlockOpts{Title: "Провайдеры и модели"})
		a.ui.Println("  " + a.ui.Gray("текущая: ") + a.ui.Bold(a.model))
		a.ui.Println("  " + a.ui.Gray("сменить: /model glm-4.6 · или /provider openai, затем /model gpt-4o"))
		a.ui.Println("")
		return
	}
	for _, p := range a.registry.All {
		for _, m := range p.Models {
			if m == rest {
				a.switchProvider(p)
				a.setModel(rest)
				return
			}
		}
	}
	a.setModel(rest)
}

func (a *app) setModel(m string) {
	provMu.Lock()
	a.model = m
	provMu.Unlock()
	cfgMu.Lock()
	a.repo.Cfg.Model = m
	cfgMu.Unlock()
	a.sess.Model = m
	a.saveConfig()
	a.saveSession()
	a.syncMetrics()
	a.ui.Ok("модель: " + m)
}

func (a *app) cmdModels(filter string) {
	if !a.prov.HasKey() {
		a.ui.Warn("нет ключа для «" + a.prov.ID + "» — /provider key " + a.prov.ID + " <ключ>")
		return
	}
	a.ui.SpinnerStart("Получаю список моделей с " + a.prov.ID)
	models, err := providers.FetchModels(context.Background(), a.client, a.prov)
	a.ui.SpinnerStop()
	if err != nil {
		a.ui.Err("список моделей недоступен: " + core.Truncate(core.OneLine(err.Error()), 110))
		a.ui.Hint("задай модель вручную: /model <имя>")
		return
	}
	if filter != "" {
		var f []string
		for _, m := range models {
			if strings.Contains(strings.ToLower(m), strings.ToLower(filter)) {
				f = append(f, m)
			}
		}
		models = f
	}
	if len(models) == 0 {
		a.ui.Warn("по фильтру «" + filter + "» ничего не найдено")
		return
	}
	shown := core.Min(len(models), 40)
	rows := make([][]string, 0, shown)
	for i := 0; i < shown; i++ {
		mark := " "
		if models[i] == a.model {
			mark = "→"
		}
		rows = append(rows, []string{mark + " " + strconv.Itoa(i+1), models[i]})
	}
	a.ui.Table([]ui.Column{
		{Title: "", Width: 5},
		{Title: "модель", Width: 50},
	}, rows, ui.BlockOpts{
		Title:  "Модели " + a.prov.ID,
		Footer: fmt.Sprintf("всего: %d — установить: /model <имя>", len(models)),
	})
	a.ui.Println("")
}

func (a *app) cmdThink(arg string) {
	var v string
	switch strings.ToLower(arg) {
	case "on", "вкл":
		v = "on"
	case "off", "выкл":
		v = "off"
	case "auto", "авто", "":
		v = "auto"
	default:
		a.ui.Warn("формат: /think on|off|auto (сейчас: " + providers.ThinkState(a.repo.Cfg) + ")")
		return
	}
	cfgMu.Lock()
	a.repo.Cfg.Think = v
	cfgMu.Unlock()
	a.saveConfig()
	switch providers.ThinkState(a.repo.Cfg) {
	case "on":
		a.ui.Ok("размышления включены — модель получает запрос на thinking")
	case "off":
		a.ui.Ok("размышления скрыты")
	default:
		a.ui.Ok("авто: показываем всё, что присылает модель")
	}
}

// ---------- Сессия ----------

func (a *app) cmdSessions() {
	ss := a.repo.ListSessionInfos()
	if len(ss) == 0 {
		a.ui.Info("сохранённых сессий нет")
		return
	}
	rows := make([][]string, 0, 15)
	for i, s := range ss {
		if i >= 15 {
			break
		}
		cur := ""
		if s.ID == a.sess.ID {
			cur = " ← текущая"
		}
		rows = append(rows, []string{
			strconv.Itoa(i + 1),
			s.Updated.Format("02.01 15:04"),
			core.Truncate(core.OneLine(s.Title), 46),
			strconv.Itoa(s.Messages),
			cur,
		})
	}
	a.ui.Table([]ui.Column{
		{Title: "#", Width: 3, Right: true},
		{Title: "изменена", Width: 12},
		{Title: "тема", Width: 48},
		{Title: "сообщ.", Width: 8, Right: true},
		{Title: "", Width: 12},
	}, rows, ui.BlockOpts{Title: "Сессии"})
	a.ui.Println("  " + a.ui.Gray("продолжить: /resume <номер>"))
	a.ui.Println("")
}

func (a *app) cmdResume(arg string) {
	if a.turnActive() {
		a.ui.Warn("ход ещё идёт — прерви (Esc) или подожди: /resume подменяет сессию под ногами хода")
		return
	}
	ss := a.repo.ListSessionInfos()
	if len(ss) == 0 {
		a.ui.Info("сохранённых сессий нет")
		return
	}
	idx := -1
	if n, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && n >= 1 && n <= len(ss) {
		idx = n - 1
	} else {
		a.cmdSessions()
		s, ok := a.askLine("Номер сессии (Enter — отмена)")
		if !ok {
			return
		}
		if s == "" {
			return
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > len(ss) {
			a.ui.Warn("неверный номер")
			return
		}
		idx = n - 1
	}
	// Барьер подмены: номер могли спрашивать долго — за это время мог
	// начаться ход (cron, watch). Lock дождётся его конца.
	a.sessSwap.Lock()
	defer a.sessSwap.Unlock()
	loaded, err := a.repo.LoadSession(ss[idx].ID)
	if err != nil {
		a.ui.Err("не удалось загрузить: " + err.Error())
		return
	}
	a.sess = loaded
	a.sess.Perms.BashExact = loaded.Perms.BashExact
	if a.sess.Perms.BashExact == nil {
		a.sess.Perms.BashExact = map[string]bool{}
	}
	np := a.registry.Pick(a.sess.Provider)
	nm := a.sess.Model
	if nm == "" {
		nm = providers.ResolveModel(np, "", a.repo.Cfg.Model)
	}
	a.setProviderModel(np, nm)
	cfgMu.Lock()
	a.repo.Cfg.Provider = np.ID
	cfgMu.Unlock()
	a.saveConfig()
	a.saveSession()
	a.syncMetrics()
	// Прежний диалог больше не относится к загруженной сессии.
	a.ui.ClearScreen()
	a.ui.Ok(fmt.Sprintf("сессия %s · сообщений: %d", a.sess.ID, len(a.sess.Messages)))
}

func (a *app) cmdClear() {
	if a.turnActive() {
		a.ui.Warn("ход ещё идёт — прерви (Esc) или подожди: /clear подменяет сессию под ногами хода")
		return
	}
	// Экран чистим до сообщения о новой сессии: иначе в ленте остался бы
	// старый диалог, к которому эта сессия уже не относится.
	// Барьер подмены берём до сохранения старой сессии: между проверкой
	// выше и этой точкой мог начаться ход (cron, watch).
	a.sessSwap.Lock()
	defer a.sessSwap.Unlock()
	a.ui.ClearScreen()
	a.saveSession()
	a.sess = a.repo.NewSession(a.prov.ID, a.model, a.workDir)
	a.saveSession()
	a.syncMetrics()
	a.ui.Ok("новая сессия: " + a.sess.ID)
}

func (a *app) cmdUsage() {
	// Снимки под sessMu: usage и чекпоинты пишут ход и горутины субагентов
	// параллельно с этой панелью (в том числе из cron-хода).
	msgs := a.Messages()
	est := core.EstimateContext(msgs)
	_, ckptN, _ := a.sessionCounters()
	u := a.sessionUsage()
	limit := a.autoCompactLimit()
	bar := a.ui.Bar(est, limit, 20)
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Токены (по данным API)"))
	a.ui.KVPanel("Расход", [][2]string{
		{"вход", a.ui.Accent("↑ " + core.Kfmt(u.PromptTokens))},
		{"выход", a.ui.Accent("↓ " + core.Kfmt(u.CompletionTokens))},
		{"всего", a.ui.Bold(core.Kfmt(u.Total()))},
		{"контекст", bar + "  " + core.Kfmt(est) + " / " + core.Kfmt(limit)},
		{"модель", a.model + " · лимит " + core.Kfmt(a.prov.CtxLimit())},
		{"сообщений", fmt.Sprintf("%d · чекпоинтов: %d", len(msgs), ckptN)},
	})
	a.printEconomy()
	if est >= limit {
		a.ui.Warn("контекст заполнен — история сожмётся после следующего хода (/compact — сейчас)")
	}
	a.ui.Println("")
}

// printEconomy — блок экономии: сколько стоили запросы и что сэкономлено.
//
// Числа важны не сами по себе, а вместе: «12 запросов, 340 токенов в
// среднем, 18 тысяч срезано подрезкой» — это уже картина расхода, по
// которой видно, что именно стоит поправить.
func (a *app) printEconomy() {
	// Глубокий снимок под sessMu: TopTool ниже идёт по карте ToolTokens,
	// а AddCall пишет в неё из горутин субагентов — живая карта здесь
	// падала бы фатально на итерации против записи.
	e := a.sessionEconomy()
	if e.Requests == 0 && e.PrunedTokens == 0 && e.CacheHits == 0 {
		return
	}
	u := a.sessionUsage()
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Экономия"))
	rows := [][2]string{
		{"запросов к модели", core.Kfmt(e.Requests)},
	}
	if e.Requests > 0 && u.Total() > 0 {
		rows = append(rows, [2]string{
			"в среднем на запрос",
			core.Kfmt(u.Total()/e.Requests) + " ток.",
		})
	}
	if e.PrunedTokens > 0 {
		rows = append(rows, [2]string{
			"срезано подрезкой",
			a.ui.Green("~"+core.Kfmt(e.PrunedTokens)+" ток.") +
				fmt.Sprintf("  (%d сообщений)", e.PrunedCount),
		})
	}
	if e.CacheHits > 0 {
		rows = append(rows, [2]string{
			"повторов из кеша", a.ui.Green(core.Kfmt(e.CacheHits)),
		})
	}
	if e.SavedCalls > 0 {
		rows = append(rows, [2]string{
			"повторов вызовов", a.ui.Green(core.Kfmt(e.SavedCalls)),
		})
	}
	if name, tok := e.TopTool(); name != "" {
		rows = append(rows, [2]string{
			"дороже всего в контексте", name + " · " + core.Kfmt(tok) + " ток.",
		})
	}
	a.ui.KVPanel("Экономия", rows)
}

// showPermissionRules — вывести правила разрешений в порядке применения.
//
// Порядок в списке значим: последнее совпавшее правило решает, поэтому
// список показан именно в том порядке, в котором правила применяются.
// Иначе человек читал бы правила и не мог предсказать итог.
func (a *app) showPermissionRules() {
	if a.rules.Len() == 0 {
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Gray("правил в конфигах нет — всё спрашивается"))
		a.ui.Println("  " + a.ui.Gray("завести правила: /permissions init"))
		return
	}
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Gray("правила разрешений (последнее совпавшее выигрывает):"))
	for _, r := range a.rules.Items() {
		mode := string(r.Mode)
		switch r.Mode {
		case core.PermDeny:
			mode = a.ui.Red(mode)
		case core.PermAllow:
			mode = a.ui.Green(mode)
		}
		src := ""
		if r.Source != "" {
			src = a.ui.Gray("  (" + r.Source + ")")
		}
		a.ui.Println("    " + r.String() + "  → " + mode + src)
	}
}

// permsSub — подкоманды /permissions: init | rules | where | test.
//
// arg — подкоманда в нижнем регистре, rest — аргументы после неё.
// Разделение нужно для test: имя команды и путь приходят как есть, и
// приводить их к нижнему регистру нельзя.
func (a *app) permsSub(arg, rest string) {
	switch arg {
	case "init":
		a.permsInit()
	case "rules", "правила":
		a.showPermissionRules()
	case "where", "где":
		a.ui.Println("")
		for _, p := range core.PermissionSources(a.workDir) {
			a.ui.Println("  " + a.ui.Gray("· "+core.RelToWD(a.workDir, p)))
		}
	case "test", "проверить":
		a.permsTest(rest)
	default:
		a.ui.Warn("не понял: " + arg + " — /permissions init | rules | where | test | reset")
	}
}

// splitFirstWord — первое слово в нижнем регистре и остаток строки.
//
// Возвращает пустое слово, если строки нет: у вызывающего «нет подкоманды»
// и «подкоманда с аргументами» должны быть разными случаями.
func splitFirstWord(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	// Первый разрез по пробелу, дальше строка идёт как есть.
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return strings.ToLower(s[:i]), strings.TrimSpace(s[i:])
	}
	return strings.ToLower(s), ""
}

// permsTest — спросить у движка, что он решит по конкретному действию.
//
// /permissions test bash "git status"
//
// Команда отвечает на главный вопрос человека: «почему моё правило не
// сработало?». Проверять это вслепую невозможно — цепочка от правила до
// подтверждения проходит через сопоставление имени инструмента, шаблона и
// приведение пути к относительному виду, и ошибка в любом из трёх мест
// выглядит одинаково: действие просто спросит вместо запрета.
func (a *app) permsTest(rest string) {
	parts := splitArgs(rest)
	if len(parts) == 0 {
		a.ui.Println("")
		a.ui.Warn("нужен инструмент и аргумент: /permissions test bash \"git status\"")
		return
	}
	name := parts[0]
	subject := strings.Join(parts[1:], " ")
	if name == "exec" || name == "write" || name == "net" {
		// Род операции: /permissions test write src/main.go
		group := core.ToolsForPermission(name)
		d, hit, ok := a.rules.DecideAny(group, subject)
		a.showPermDecision(name, subject, d, hit, ok)
		return
	}
	// Конкретный инструмент: /permissions test edit_file src/main.go
	if subject == "" {
		d, hit, ok := a.rules.Match(name, "")
		a.showPermDecision(name, "", d, hit, ok)
		return
	}
	if isPathLike(name) && filepath.IsAbs(subject) {
		subject = core.PatternForPath(a.workDir, subject)
	}
	// Решение принимает группа инструментов, а не одно имя, поэтому и
	// проверка должна идти по группе — иначе /permissions test врал бы
	// на правиле "edit(src/**)".
	if g, ok := core.GroupForTool(name); ok {
		d, hit, ok := a.rules.DecideAny(g, subject)
		a.showPermDecision(name, subject, d, hit, ok)
		return
	}
	d, hit, ok := a.rules.Match(name, subject)
	a.showPermDecision(name, subject, d, hit, ok)
}

// showPermDecision — напечатать решение движка и объяснить, чем оно вызвано.
func (a *app) showPermDecision(name, subject string, d core.Permission, hit core.Rule, ok bool) {
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Bold(name) + " " + a.ui.Gray(subject))
	switch {
	case !ok:
		a.ui.Println("  " + a.ui.Gray("решение: спросить пользователя — правил нет"))
		a.ui.Hint("завести правило: /permissions init")
	case hit.Tool == "":
		// Решение из блока mode: правила, которое совпало, не существует,
		// и показывать нечего — показываем сам дефолт.
		a.ui.Println("  " + a.ui.Gray("решение: "+string(d)+" — из блока \"mode\" конфига"))
	default:
		verb := map[core.Permission]string{
			core.PermAllow: "разрешить",
			core.PermAsk:   "спросить",
			core.PermDeny:  "запретить",
		}[d]
		line := "  решение: " + verb
		if src := hit.Label(); src != "" {
			line += "  " + a.ui.Gray("по правилу "+hit.String())
		}
		a.ui.Println(line)
		if hit.Source != "" {
			a.ui.Hint("откуда: " + hit.Source)
		}
	}
}

// splitArgs — разбить аргумент команды на слова с учётом кавычек.
//
// Без кавычек не разобрать "/permissions test bash "git status --short"" —
// а именно такую команду человек и напишет: путь с пробелом или строка
// команды целиком.
func splitArgs(s string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote byte
		open  bool
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote, open = c, true
		case c == ' ' || c == '\t':
			if cur.Len() > 0 || open {
				out = append(out, cur.String())
				cur.Reset()
				open = false
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 || open {
		out = append(out, cur.String())
	}
	return out
}

// isPathLike — похож ли инструмент на операцию с файлом.
//
// Имя файла и путь — единственное, что приходит в правило записи вместо
// команды, поэтому для них путь приводится к виду от проекта. Отличать надо
// именно по этому признаку, а не по наличию «write» в имени: правило
// "write_file(src/**)" само по себе шаблон уже содержит путь.
func isPathLike(tool string) bool {
	switch tool {
	case "edit", "edit_file", "write_file", "multi_edit", "apply_patch", "patch":
		return true
	}
	return false
}

// permsInit — создать gcli.json с заготовкой правил, если его нет.
//
// Существующий файл не трогаем: правила проекта написаны руками, и
// перезаписать их заготовкой — значит потерять работу человека без
// предупреждения. Лучше сказать, что файл уже есть, и показать путь.
func (a *app) permsInit() {
	path := filepath.Join(a.workDir, "gcli.json")
	if _, err := os.Stat(path); err == nil {
		a.ui.Ok("gcli.json уже есть: " + core.RelToWD(a.workDir, path))
		a.ui.Hint("правь руками: последнее совпавшее правило выигрывает")
		return
	}
	if err := os.WriteFile(path, []byte(core.ProjectConfigTemplate), 0o600); err != nil {
		a.ui.Warn("не создал gcli.json: " + err.Error())
		return
	}
	a.rules = a.setupRules()
	a.ui.Ok("создал gcli.json с заготовкой правил: " + core.RelToWD(a.workDir, path))
	a.ui.Hint("правила применятся в следующем запуске — сейчас перезапусти gcli")
}

// cmdPerms — /permissions [init|rules|where|test|reset] [аргументы].
//
// Подкоманда — только первое слово. Раньше она бралась из всей строки
// целиком, поэтому "/permissions test bash git status" уходил в ветку
// «не понял», а "/permissions init" и "where" случайно работали — просто
// потому, что состоят из одного слова.
func (a *app) cmdPerms(rest string) {
	arg, tail := splitFirstWord(rest)
	if arg != "" && arg != "reset" {
		a.permsSub(arg, tail)
		return
	}
	// Снимок под sessMu: панель идёт по списку «всегда разрешённых»
	// команд из очереди ввода, а grantBashExact пишет в карту из хода —
	// итерация живой карты против записи падала бы фатально.
	exact := a.permBashExactList()
	a.ui.Println("")
	a.ui.KVPairs([][2]string{
		{"автопилот", autopilotStatus(a)},
		{"песочница файлов", sandboxStatus(a.sandbox)},
		{"правила из конфигов", strconv.Itoa(a.rules.Len())},
		{"запись файлов без подтверждения", yesNo(a.permFileWrite())},
		{"любые bash-команды без подтверждения", yesNo(a.permBashAll())},
		{"сетевые запросы без подтверждения", yesNo(a.permWebFetch())},
		{"разрешённых команд", strconv.Itoa(len(exact))},
	})
	a.showPermissionRules()
	if len(exact) > 0 {
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Gray("разрешённые команды:"))
		for _, c := range exact {
			a.ui.Println("    " + a.ui.Gray("· "+c))
		}
	}
	if sb := a.sandbox; sb.Enabled() {
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Gray("песочница разрешает:"))
		for _, p := range sb.Roots() {
			a.ui.Println("    " + a.ui.Gray("· "+p))
		}
		if d := sb.Deny(); len(d) > 0 {
			a.ui.Println("")
			a.ui.Println("  " + a.ui.Gray("песочница запрещает (даже внутри корня):"))
			for _, p := range d {
				a.ui.Println("    " + a.ui.Gray("· "+p))
			}
		}
	}
	// Код проекта, которому пользователь разрешил выполняться. Показываем тем
	// же списком, что и /ext trust: одно место, где видно, что именно будет
	// запущено и кто это разрешил.
	if trusted := a.tools.TrustedList(); len(trusted) > 0 {
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Gray("доверенный код проекта:"))
		keys := make([]string, 0, len(trusted))
		for k := range trusted {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			a.ui.Println("    " + a.ui.Gray("· "+k+"  отпечаток "+core.Truncate(trusted[k], 12)))
		}
	}
	if pending := a.tools.ScanProjectCode(); len(pending) > 0 {
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Gray("ждёт подтверждения:"))
		for _, w := range pending {
			a.ui.Println("    " + a.ui.Gray("· "+w.Label()+" — "+w.Path))
		}
	}
	if strings.TrimSpace(rest) == "reset" {
		a.sess.Perms = core.Perms{BashExact: map[string]bool{}}
		cfgMu.Lock()
		a.repo.Cfg.Autopilot = false
		a.repo.Cfg.AutopilotAll = false
		cfgMu.Unlock()
		a.saveConfig()
		a.saveSession()
		// Согласия на код — тоже разрешение: сброс должен их убрать. Иначе
		// «сбросить» оставило бы расширения и MCP-серверы включёнными.
		forgotten := a.tools.UntrustCode()
		// Реестр пересобираем сразу: пока инструменты проекта висят в старом
		// Registry, они продолжают работать, и «сброс» ничего не отключал
		// бы. MCP-процессы гасим ДО сборки, иначе они бы остались
		// запущенными сиротами (реестр их уже не держит).
		a.tools.MCPShutdown()
		extBefore, mcpBefore := a.tools.ExtCount(), a.tools.MCPToolCount()
		a.buildTools()

		a.ui.Ok("разрешения сброшены")
		if forgotten > 0 {
			a.ui.Hint(fmt.Sprintf("забыто подтверждений кода: %d", forgotten))
		}
		if n := extBefore + mcpBefore; n > 0 {
			a.ui.Hint(fmt.Sprintf("отключено инструментов проекта: %d (расширения: %d, MCP: %d)",
				n, extBefore, mcpBefore))
		}
		if pending := a.tools.ScanProjectCode(); len(pending) > 0 {
			a.ui.Hint("код из проекта снова ждёт подтверждения: /ext trust <имя> · /mcp trust <имя>")
		}
	} else {
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Gray("автопилот: /autopilot on|off|all · сброс: /permissions reset · песочница: /sandbox on|off"))
		a.ui.Println("  " + a.ui.Gray("код проекта: /ext trust <имя> · /mcp trust <имя>"))
	}
	a.ui.Println("")
}

// sandboxStatus — строка состояния песочницы для /permissions и /status.
func sandboxStatus(sb *tools.Sandbox) string {
	if !sb.Enabled() {
		return "выключена (файлы видны где угодно)"
	}
	return "включена: " + core.Truncate(strings.Join(sb.Roots(), ", "), 60)
}

// showSandbox — напечатать состояние песочницы (/sandbox без аргументов).
func (a *app) showSandbox() {
	if !a.sandbox.Enabled() {
		a.ui.Println("песочница выключена — инструменты видят всю файловую систему")
		return
	}
	a.ui.Ok("песочница включена: пути ограничены рабочим каталогом, секреты закрыты")
	for _, p := range a.sandbox.Roots() {
		a.ui.Println("    " + a.ui.Gray("· "+p))
	}
}

// cmdSandbox — переключить песочницу файловой системы: on | off | status.
//
// Переключение пересобирает реестр инструментов: песочница живёт в Env, а не
// внутри инструментов, поэтому без пересборки команда «включила» бы
// песочницу, а инструменты продолжили бы работать по-старому.
func (a *app) cmdSandbox(rest string) {
	arg := strings.ToLower(strings.TrimSpace(rest))
	if arg == "status" || arg == "показать" {
		a.showSandbox()
		return
	}
	mode, ok := core.ParseSandboxMode(arg)
	if !ok {
		a.ui.Warn("не понял: " + rest + " — /sandbox on | off | status")
		return
	}
	if mode == core.SandboxUnset {
		a.showSandbox()
		return
	}
	if mode == core.SandboxOff {
		a.sandbox = nil
		cfgMu.Lock()
		a.repo.Cfg.Sandbox = core.SandboxOff
		cfgMu.Unlock()
	} else {
		a.sandbox = tools.NewSandbox(a.workDir).WithDeny(tools.DefaultDeny()...)
		if home := a.store.Root; home != "" {
			a.sandbox.WithDeny(home)
		}
		cfgMu.Lock()
		a.repo.Cfg.Sandbox = core.SandboxOn
		cfgMu.Unlock()
	}
	a.saveConfig()
	a.buildTools()
	if a.sandbox.Enabled() {
		a.ui.Ok("песочница включена — пути вне рабочего каталога запрещены")
	} else {
		a.ui.Ok("песочница выключена")
	}
}

// showCron — напечатать состояние автономных итераций (/cron без аргументов).
func (a *app) showCron() {
	a.cronMu.Lock()
	enabled := a.cronEnabled
	interval := a.cronInterval
	lastActive := a.cronLastActive
	a.cronMu.Unlock()

	state := "выключены"
	if enabled {
		state = "включены"
	}
	a.ui.Ok("автономные итерации: " + state)
	if enabled {
		a.ui.Println("  интервал: " + interval.String() + " (15–20 мин, случайный сдвиг)")
		d := time.Since(lastActive)
		if d < 0 {
			d = 0
		}
		a.ui.Println("  последняя активность: " + core.FormatDur(d) + " назад")
		a.ui.Println("  следующая итерация: после " + core.FormatDur(interval-d) + " тишины")
	}
	a.ui.Println("  подробнее: /cron on | /cron off | /cron status")
}

// cmdCron — переключить автономные итерации: on | off | status.
//
// Итерации работают только в интерактивном режиме и не прерывают активный
// ход. Когда пользователь молчит 15–20 минут после завершения хода, агент
// сам берёт задачу «продолжай работу над проектом, ищи баги, проблемы и
// уязвимости, улучшай проект дальше».
func (a *app) cmdCron(rest string) {
	arg := strings.ToLower(strings.TrimSpace(rest))
	switch arg {
	case "", "status", "показать":
		a.showCron()
		return
	case "on", "вкл", "1", "true":
		a.cronMu.Lock()
		if a.cronEnabled {
			a.cronMu.Unlock()
			a.ui.Ok("автономные итерации уже включены")
			return
		}
		a.cronEnabled = true
		if a.cronDone == nil {
			a.cronDone = make(chan struct{})
			go a.cronLoop()
		}
		a.cronInterval = cronNextInterval()
		a.cronLastActive = time.Now()
		a.cronMu.Unlock()
		// Cfg.Cron — вне cronMu: cfgMu не вкладывается в cronMu.
		cfgMu.Lock()
		a.repo.Cfg.Cron = true
		cfgMu.Unlock()
		a.saveConfig()
		if t := a.tuiApp(); t != nil {
			t.SetCronEnabled(true)
		}
		a.ui.Ok("автономные итерации включены — через 15–20 мин тишины агент сам найдёт и починит баги, проблемы, уязвимости")
	case "off", "выкл", "0", "false":
		a.cronMu.Lock()
		if !a.cronEnabled {
			a.cronMu.Unlock()
			a.ui.Ok("автономные итерации уже выключены")
			return
		}
		a.cronEnabled = false
		if a.cronDone != nil {
			close(a.cronDone)
			a.cronDone = nil
		}
		a.cronMu.Unlock()
		cfgMu.Lock()
		a.repo.Cfg.Cron = false
		cfgMu.Unlock()
		a.saveConfig()
		if t := a.tuiApp(); t != nil {
			t.SetCronEnabled(false)
		}
		a.ui.Ok("автономные итерации выключены")
	default:
		a.ui.Warn("не понял: " + rest + " — /cron on | off | status")
	}
}

func yesNo(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

func (a *app) cmdTools() {
	rows := make([][]string, 0, a.tools.Count())
	for _, t := range a.tools.All() {
		rows = append(rows, []string{t.Def.Name, t.Category, core.Truncate(core.OneLine(t.Def.Description), 60)})
	}
	a.ui.Table([]ui.Column{
		{Title: "инструмент", Width: 16},
		{Title: "категория", Width: 10},
		{Title: "описание", Width: 56},
	}, rows, ui.BlockOpts{Title: "Инструменты агента"})
	a.ui.Println("")
}

func (a *app) cmdAgent(arg string) {
	var on bool
	switch strings.ToLower(arg) {
	case "on", "вкл":
		on = true
		a.setAgentMode(true)
	case "off", "выкл":
		on = false
		a.setAgentMode(false)
	default:
		on = !a.agentMode()
		a.setAgentMode(on)
	}
	// Cfg.Agent — отдельно от sessMu: сессия и конфиг лочатся по очереди,
	// не вкладываясь друг в друга.
	cfgMu.Lock()
	a.repo.Cfg.Agent = on
	cfgMu.Unlock()
	a.saveConfig()
	a.saveSession()
	if a.agentMode() {
		a.ui.Ok(fmt.Sprintf("агентный режим включён · инструментов: %d", a.tools.Count()))
	} else {
		a.ui.Ok("агентный режим выключен — обычный чат без инструментов")
	}
}

func (a *app) cmdYolo() {
	// Переключение под sessMu: confirm в ходе читает эти флаги на каждом
	// подтверждении и выдаёт гранты параллельно с командой (cron-ход).
	on := a.toggleBashAll()
	a.saveSession()
	if on {
		a.ui.Warn("YOLO: подтверждения отключены (кроме опасных команд). Действуй осторожно!")
	} else {
		a.ui.Ok("подтверждения снова включены")
	}
}

// cmdAutopilot — режим автопилота: on | off | all | status.
func (a *app) cmdAutopilot(rest string) {
	parts := strings.Fields(strings.ToLower(rest))
	if len(parts) == 0 {
		parts = []string{"toggle"}
	}
	switch parts[0] {
	case "toggle", "переключить", "":
		// Без аргумента — переключение on/off (all всегда требует явного ввода).
		if a.sess.Perms.Autopilot {
			a.setAutopilot(false)
			a.ui.Ok("автопилот выключен — опасные действия снова спрашивают")
		} else {
			a.setAutopilot(true)
			a.ui.Ok("автопилот включён: безопасные действия одобряются без вопроса")
		}
		return
	case "on", "вкл", "1", "true":
		a.setAutopilot(true)
		a.ui.Ok("автопилот включён: безопасные действия одобряются без вопроса")
		a.ui.Hint("потенциально опасные команды (rm -rf, sudo, форматирование) всё ещё спрашивают")
		return
	case "all", "риск", "risk", "yolo", "полный":
		a.setAutopilotAll(true)
		a.ui.Warn("автопилот all: одобряется ВСЁ, включая разрушительные команды")
		a.ui.Hint("отключить: /autopilot off")
		return
	case "off", "выкл", "0", "false":
		a.setAutopilot(false)
		a.ui.Ok("автопилот выключен — опасные действия снова спрашивают")
		return
	case "status", "статус", "?":
		// Ниже — вывод состояния.
	default:
		a.ui.Warn("формат: /autopilot on|off|all|status (сейчас: " + a.autopilotWord() + ")")
		return
	}

	a.ui.Println("")
	switch {
	case a.sess.Perms.AutopilotAll:
		a.ui.Println("  " + a.ui.Red("● автопилот: all") +
			a.ui.Gray("  — одобряется всё, включая опасные команды. Рискованно."))
		a.ui.Println("  " + a.ui.Gray("безопасные действия, сеть, запись файлов, опасные команды — без вопроса"))
	case a.sess.Perms.Autopilot:
		a.ui.Println("  " + a.ui.Accent("● автопилот: on") +
			a.ui.Gray("  — безопасные действия одобряются самим агентом"))
		a.ui.Println("  " + a.ui.Gray("потенциально опасные команды по-прежнему требуют подтверждения"))
	default:
		a.ui.Println("  " + a.ui.Gray("● автопилот: off — каждое действие спрашивает подтверждение"))
	}
	a.ui.Println("  " + a.ui.Gray("переключить: /autopilot on · /autopilot all · /autopilot off"))
	a.ui.Println("")
}

func (a *app) cmdCopy() {
	var last string
	msgs := a.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == core.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			last = msgs[i].Content
			break
		}
	}
	if last == "" {
		a.ui.Info("нечего копировать — ответов ещё нет")
		return
	}
	enc := base64.StdEncoding.EncodeToString([]byte(last))
	a.ui.RawWrite("\033]52;c;" + enc + "\a")
	a.ui.Ok("последний ответ скопирован (OSC52)")
}

// cmdInit — разобраться в проекте и записать это в GCLI.md и gcli.json.
//
// Команда делает две вещи, и обе нужны сразу: пишет память проекта для
// агента и заводит правила разрешений под этот стек. Только память —
// половина работы (агент всё равно будет спрашивать разрешение на каждый
// запуск go test); только правила — тоже половина (агент не знает, где
// корень и чем собирать).
//
// Существующие файлы не трогаются без force: человек писал их руками,
// и перезапись заготовкой — потеря работы без предупреждения.
func (a *app) cmdInit(arg string) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	// show — ничего не пишет: показать, что /init узнал о проекте. Нужно
	// перед решением «перезаписывать ли», иначе человек сравнивает
	// содержимое своего файла с пустым воображением о том, что сгенерирует
	// агент.
	if arg == "show" || strings.Contains(arg, "показать") {
		a.initShow()
		return
	}
	force := strings.Contains(arg, "force") || strings.Contains(arg, "заново")
	prof := a.tools.InitProfileFor()

	md := filepath.Join(a.workDir, "GCLI.md")
	_, mdErr := os.Stat(md)
	cfg := filepath.Join(a.workDir, "gcli.json")
	_, cfgErr := os.Stat(cfg)

	// Файлы создаются независимо друг от друга. Раньше здесь стоял
	// безусловный выход, если есть GCLI.md: человек, у которого уже была
	// память проекта, не мог получить правила вообще — пришлось бы
	// звать /init force и рисковать перезаписью своего файла.
	if mdErr == nil && cfgErr == nil && !force {
		a.ui.Warn("GCLI.md и gcli.json уже есть — перезаписать оба: /init force")
		a.ui.Hint("посмотреть, что найдено о проекте, ничего не записывая: /init show")
		return
	}

	if mdErr != nil || force {
		if err := os.WriteFile(md, []byte(tools.MemoryDoc(prof)), 0o644); err != nil {
			a.ui.Err("не удалось создать GCLI.md: " + err.Error())
			return
		}
		a.ui.Ok("создан GCLI.md — память проекта для агента")
	}
	if cfgErr != nil || force {
		rules := core.ProjectConfigDoc(tools.PermissionRules(prof))
		if err := os.WriteFile(cfg, []byte(rules), 0o600); err != nil {
			a.ui.Warn("GCLI.md создан, но gcli.json не записался: " + err.Error())
		} else {
			a.ui.Ok("создан gcli.json — правила разрешений под этот проект")
			a.rules = a.setupRules()
		}
	}

	a.initReport(prof)
}

// initShow — что /init узнал бы о проекте, ничего не записывая.
func (a *app) initShow() {
	prof := a.tools.InitProfileFor()
	a.initReport(prof)

	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Команды, которые станут разрешены"))
	rules := tools.PermissionRules(prof)
	allowed := 0
	for _, r := range rules {
		if strings.Contains(r, ": allow") {
			a.ui.Println("    " + a.ui.Green("· "+r))
			allowed++
		}
	}
	// Базовые правила (чтение, grep, edit) есть всегда, поэтому «список
	// не пуст» ещё не значит, что проект понят. Без этой оговорки
	// человек увидит четыре правила и решит, что /init разобрался.
	if prof.Stack() == "" {
		a.ui.Println("    " + a.ui.Yellow("стек не найден — выше только базовые правила"))
	}
	if allowed == 0 {
		a.ui.Println("    " + a.ui.Gray("разрешать нечего"))
	}

	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Останется вопросом"))
	for _, r := range rules {
		if strings.Contains(r, ": ask") {
			a.ui.Println("    " + a.ui.Yellow("· "+r))
		}
	}

	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Будет запрещено"))
	for _, r := range rules {
		if strings.Contains(r, ": deny") {
			a.ui.Println("    " + a.ui.Red("· "+r))
		}
	}
}

// initReport — что машина узнала о проекте.
//
// Показывается всегда, даже когда файлы созданы: человек должен видеть,
// откуда взялись строки в GCLI.md, иначе он будет считать их выдуманными
// и править не то.
func (a *app) initReport(p tools.InitProfile) {
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("О проекте"))
	a.ui.KVPairs([][2]string{
		{"корень", orDash(p.RootRel())},
		{"стек", orDash(p.Stack())},
		{"точек входа", orDash(strconv.Itoa(len(p.EntryPoints)))},
		{"команд сборки", orDash(strconv.Itoa(len(p.Build)))},
		{"команд тестов", orDash(strconv.Itoa(len(p.Test)))},
	})
	if len(p.Test) > 0 {
		a.ui.Hint("правило allow на проверку: /permissions test bash \"" + p.Test[0] + "\"")
	}
	a.ui.Hint("заполни строки вида <…> в GCLI.md — остальное агент уже знает")
}

// orDash — значение или прочерк, чтобы в таблице не было пустот.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func (a *app) cmdMemory() {
	files := a.memory.Files()
	a.ui.Println("")
	if len(files) == 0 {
		a.ui.Info("файлов памяти нет — создай: /init")
		a.ui.Println("")
		return
	}
	rows := make([][]string, 0, len(files))
	for _, p := range files {
		sz := ""
		if st, err := os.Stat(p); err == nil {
			sz = core.HumanSize(int(st.Size()))
		}
		rows = append(rows, []string{core.RelToWD(a.workDir, p), sz})
	}
	a.ui.Table([]ui.Column{
		{Title: "файл", Width: 50},
		{Title: "размер", Width: 10, Right: true},
	}, rows, ui.BlockOpts{Title: "Память проекта"})
	a.ui.Println("  " + a.ui.Gray("глобальная память: ~/.gcli/GCLI.md"))
	a.ui.Println("")
}

func (a *app) cmdUndo() {
	// Под sessMu: /undo режет слайс чекпоинтов из горутины команд, пока ход
	// (multi_edit, субагенты, cron) дописывает чекпоинты через recordCheckpoint.
	sessMu.Lock()
	cp, ok, err := a.repo.UndoCheckpoint(a.sess)
	sessMu.Unlock()
	a.saveSession()
	if !ok {
		if err != nil {
			a.ui.Err("откат не удался: " + err.Error())
		} else {
			a.ui.Info("нечего отменять — чекпоинтов в сессии нет")
		}
		return
	}
	if !cp.Existed {
		a.ui.Ok("undo: удалён созданный файл " + core.RelToWD(a.workDir, cp.Path))
		return
	}
	a.ui.Ok("undo: восстановлен " + core.RelToWD(a.workDir, cp.Path))
}

// cmdSnapshots — список теневых снимков проекта (старые сверху, как ходы).
func (a *app) cmdSnapshots() {
	if !core.SnapshotsAvailable() {
		a.ui.Warn("git не найден в PATH — снимки недоступны")
		return
	}
	if err := a.lastSnapErr(); err != nil {
		a.ui.Warn("последняя ошибка снимков: " + err.Error())
	}
	snaps, err := core.ListSnapshots(a.workDir)
	if err != nil {
		a.ui.Err("снимки: " + err.Error())
		return
	}
	if len(snaps) == 0 {
		a.ui.Info("снимков пока нет — они появляются после первого хода")
		return
	}
	rows := make([][]string, 0, len(snaps))
	for i, s := range snaps {
		rows = append(rows, []string{
			strconv.Itoa(i + 1), s.Time.Format("02.01 15:04"),
			core.Truncate(core.OneLine(s.Subject), 52), core.Truncate(s.Hash, 8),
		})
	}
	a.ui.Table([]ui.Column{
		{Title: "#", Width: 4, Right: true}, {Title: "время", Width: 11},
		{Title: "ход", Width: 54}, {Title: "снимок", Width: 8},
	}, rows, ui.BlockOpts{Title: "Снимки проекта"})
	a.ui.Println("  " + a.ui.Gray("откатить: /revert <номер> — проект вернётся к состоянию хода"))
}

// cmdRevert — откатить проект к снимку N из /snapshots.
// Откат сам откатываем: текущее состояние фиксируется снимком,
// а точка ухода остаётся на ветке keep/pre-revert (тоже видна в списке).
func (a *app) cmdRevert(rest string) {
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || n < 1 {
		a.ui.Warn("укажи номер снимка из /snapshots: /revert 3")
		return
	}
	snaps, err := core.ListSnapshots(a.workDir)
	if err != nil {
		a.ui.Err("снимки: " + err.Error())
		return
	}
	if n > len(snaps) {
		a.ui.Warn(fmt.Sprintf("снимка №%d нет — всего их %d (/snapshots)", n, len(snaps)))
		return
	}
	target := snaps[n-1]
	snap, err := core.RestoreSnapshot(a.workDir, target.Hash)
	if err != nil {
		a.ui.Err("откат не удался: " + err.Error())
		return
	}
	a.ui.Ok(fmt.Sprintf("проект откачен к снимку №%d «%s» (%s)",
		n, core.OneLine(snap.Subject), snap.Time.Format("02.01 15:04")))
	a.ui.Info("состояние на момент отката сохранено: /snapshots → ветка keep/pre-revert")
}

func (a *app) saveConfig() { _ = a.repo.SaveConfig() }
