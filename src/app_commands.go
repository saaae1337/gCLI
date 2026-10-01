package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
	"gcli/ui"
)

// handleCommand — обработать slash-команду. false = выход.
func (a *app) handleCommand(line string) bool {
	fields := strings.Fields(line)
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
		a.ui.RenderTodos(a.sess.Todos)
		a.ui.Println("")
	case "/compact":
		a.cmdCompact()
	case "/sessions":
		a.cmdSessions()
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
	case "/usage":
		a.cmdUsage()
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
	case "/undo":
		a.cmdUndo()
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
			{"/autopilot on|off|all", "автопилот: одобрять безопасные действия самому"},
			{"/sandbox on|off", "песочница файлов: пути вне рабочего каталога запрещены"},
			{"/yolo", "без подтверждений (кроме опасных команд)"},
			{"/tools", "список инструментов агента"},
			{"/mission", "автономный прогон: start, stop, status, save, report"},
			{"/iters", "лимит итераций и продления хода; при прогоне — его расход"},
			{"/todos", "план текущей задачи"},
			{"/compact", "сжать историю диалога"},
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
			{"/context", "разбивка контекста по частям"},
			{"/sessions, /resume", "сохранённые сессии"},
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
	a.ui.Println("")
	a.ui.KVPairs([][2]string{
		{"Провайдер", a.prov.Label + "  (" + a.prov.ID + ")"},
		{"Модель", a.model},
		{"Ключ", keyStatus(a.prov)},
		{"Режим", modeStatus(a.sess.AgentMode) + " · инструментов: " + strconv.Itoa(a.tools.Count()) +
			" · расширений: " + strconv.Itoa(a.tools.ExtCount()) +
			" · MCP: " + strconv.Itoa(a.tools.MCPToolCount())},
		{"Субагенты", agentsStatus(a)},
		{"Автопилот", autopilotStatus(a) + " · " + autopilotHint(a)},
		{"Размышления", a.repo.Cfg.Think},
		{"Сессия", a.sess.ID + " · сообщений: " + strconv.Itoa(len(a.sess.Messages)) + " · ходов: " + strconv.Itoa(a.sess.Turns)},
		{"Контекст", "~" + core.Kfmt(core.EstimateContext(a.sess.Messages)) + " ток. / порог " + core.Kfmt(a.autoCompactLimit())},
		{"Токены", "↑" + core.Kfmt(a.sess.Usage.PromptTokens) + " ↓" + core.Kfmt(a.sess.Usage.CompletionTokens)},
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
		a.repo.Cfg.SubMaxPar, a.repo.Cfg.SubMaxDepth, len(a.sess.SubagentRuns))
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
		a.registry = providers.Build(a.repo.Cfg)
		a.ui.Ok(fmt.Sprintf("провайдер «%s» → %s (%s)", id, base, proto))
		a.ui.Hint("далее: /provider key " + id + " <ключ> · /provider " + id)
	case "key", "ключ":
		if len(parts) < 3 {
			a.ui.Err("формат: /provider key <id> <ключ>")
			return
		}
		id := strings.ToLower(parts[1])
		key := providers.CleanKey(strings.Join(parts[2:], " "))
		if key == "" {
			a.ui.Err("ключ пустой после нормализации")
			return
		}
		a.repo.EnsureProviderCfg(id).APIKey = key
		a.saveConfig()
		a.registry = providers.Build(a.repo.Cfg)
		if p := a.registry.Find(id); p != nil && a.prov.ID == id {
			a.prov = p
		}
		a.ui.Ok(fmt.Sprintf("ключ для «%s» сохранён: %s", id, providers.MaskKey(key)))
	case "model", "модель":
		if len(parts) < 3 {
			a.ui.Err("формат: /provider model <id> <имя модели>")
			return
		}
		id := strings.ToLower(parts[1])
		a.repo.EnsureProviderCfg(id).Model = parts[2]
		a.saveConfig()
		a.registry = providers.Build(a.repo.Cfg)
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
		a.registry = providers.Build(a.repo.Cfg)
		if a.prov.ID == id {
			a.prov = a.registry.Pick("")
			a.model = providers.ResolveModel(a.prov, "", a.repo.Cfg.Model)
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
		case p.Key != "":
			key = a.ui.Green(providers.MaskKey(p.Key))
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
	a.ui.Println("  " + a.ui.Gray("переключиться: /provider <id> · удалить свой: /provider rm <id>"))
	a.ui.Println("")
}

func (a *app) switchProvider(p *providers.Provider) {
	a.prov = p
	a.repo.Cfg.Provider = p.ID
	found := false
	for _, m := range p.Models {
		if m == a.model {
			found = true
			break
		}
	}
	if !found {
		a.model = p.DefaultModel
	}
	a.sess.Provider = p.ID
	a.sess.Model = a.model
	a.saveConfig()
	a.saveSession()
	a.ui.Ok(fmt.Sprintf("провайдер: %s · модель: %s", p.Label, a.model))
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
	a.model = m
	a.repo.Cfg.Model = m
	a.sess.Model = m
	a.saveConfig()
	a.saveSession()
	a.ui.Ok("модель: " + m)
}

func (a *app) cmdModels(filter string) {
	if !a.prov.HasKey() {
		a.ui.Warn("нет ключа для «" + a.prov.ID + "» — /provider key " + a.prov.ID + " <ключ>")
		return
	}
	a.ui.SpinnerStart("Получаю список моделей с " + a.prov.ID)
	models, err := providers.FetchModels(nil, a.client, a.prov)
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
	switch strings.ToLower(arg) {
	case "on", "вкл":
		a.repo.Cfg.Think = "on"
	case "off", "выкл":
		a.repo.Cfg.Think = "off"
	case "auto", "авто", "":
		a.repo.Cfg.Think = "auto"
	default:
		a.ui.Warn("формат: /think on|off|auto (сейчас: " + providers.ThinkState(a.repo.Cfg) + ")")
		return
	}
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
	ss := a.repo.ListSessions()
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
			strconv.Itoa(len(s.Messages)),
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
	ss := a.repo.ListSessions()
	if len(ss) == 0 {
		a.ui.Info("сохранённых сессий нет")
		return
	}
	idx := -1
	if n, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && n >= 1 && n <= len(ss) {
		idx = n - 1
	} else {
		a.cmdSessions()
		a.ui.Prompt2("Номер сессии (Enter — отмена)", "> ")
		if !a.stdin.Scan() {
			return
		}
		s := strings.TrimSpace(a.stdin.Text())
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
	a.prov = a.registry.Pick(a.sess.Provider)
	a.repo.Cfg.Provider = a.prov.ID
	a.model = a.sess.Model
	if a.model == "" {
		a.model = providers.ResolveModel(a.prov, "", a.repo.Cfg.Model)
	}
	a.saveConfig()
	a.saveSession()
	a.ui.Ok(fmt.Sprintf("сессия %s · сообщений: %d", a.sess.ID, len(a.sess.Messages)))
}

func (a *app) cmdClear() {
	a.saveSession()
	a.sess = a.repo.NewSession(a.prov.ID, a.model, a.workDir)
	a.saveSession()
	a.ui.Ok("новая сессия: " + a.sess.ID)
}

func (a *app) cmdUsage() {
	est := core.EstimateContext(a.sess.Messages)
	limit := a.autoCompactLimit()
	bar := a.ui.Bar(est, limit, 20)
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Токены (по данным API)"))
	a.ui.KVPairs([][2]string{
		{"вход", a.ui.Accent("↑ " + core.Kfmt(a.sess.Usage.PromptTokens))},
		{"выход", a.ui.Accent("↓ " + core.Kfmt(a.sess.Usage.CompletionTokens))},
		{"всего", a.ui.Bold(core.Kfmt(a.sess.Usage.Total()))},
		{"контекст", bar + "  " + core.Kfmt(est) + " / " + core.Kfmt(limit)},
		{"модель", a.model + " · лимит " + core.Kfmt(a.prov.CtxLimit())},
		{"сообщений", fmt.Sprintf("%d · чекпоинтов: %d", len(a.sess.Messages), len(a.sess.Checkpoints))},
	})
	if est >= limit {
		a.ui.Warn("контекст заполнен — история сожмётся после следующего хода (/compact — сейчас)")
	}
	a.ui.Println("")
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
	a.ui.Println("")
	a.ui.KVPairs([][2]string{
		{"автопилот", autopilotStatus(a)},
		{"песочница файлов", sandboxStatus(a.sandbox)},
		{"правила из конфигов", strconv.Itoa(a.rules.Len())},
		{"запись файлов без подтверждения", yesNo(a.sess.Perms.FileWrite)},
		{"любые bash-команды без подтверждения", yesNo(a.sess.Perms.BashAll)},
		{"сетевые запросы без подтверждения", yesNo(a.sess.Perms.WebFetch)},
		{"разрешённых команд", strconv.Itoa(len(a.sess.Perms.BashExact))},
	})
	a.showPermissionRules()
	if len(a.sess.Perms.BashExact) > 0 {
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Gray("разрешённые команды:"))
		for c := range a.sess.Perms.BashExact {
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
		a.repo.Cfg.Autopilot = false
		a.repo.Cfg.AutopilotAll = false
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
		a.repo.Cfg.Sandbox = core.SandboxOff
	} else {
		a.sandbox = tools.NewSandbox(a.workDir).WithDeny(tools.DefaultDeny()...)
		if home := a.store.Root; home != "" {
			a.sandbox.WithDeny(home)
		}
		a.repo.Cfg.Sandbox = core.SandboxOn
	}
	a.saveConfig()
	a.buildTools()
	if a.sandbox.Enabled() {
		a.ui.Ok("песочница включена — пути вне рабочего каталога запрещены")
	} else {
		a.ui.Ok("песочница выключена")
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
	switch strings.ToLower(arg) {
	case "on", "вкл":
		a.sess.AgentMode = true
		a.repo.Cfg.Agent = true
	case "off", "выкл":
		a.sess.AgentMode = false
		a.repo.Cfg.Agent = false
	default:
		a.sess.AgentMode = !a.sess.AgentMode
		a.repo.Cfg.Agent = a.sess.AgentMode
	}
	a.saveConfig()
	a.saveSession()
	if a.sess.AgentMode {
		a.ui.Ok(fmt.Sprintf("агентный режим включён · инструментов: %d", a.tools.Count()))
	} else {
		a.ui.Ok("агентный режим выключен — обычный чат без инструментов")
	}
}

func (a *app) cmdYolo() {
	a.sess.Perms.BashAll = !a.sess.Perms.BashAll
	if a.sess.Perms.BashAll {
		a.sess.Perms.FileWrite = true
		a.sess.Perms.WebFetch = true
		a.saveSession()
		a.ui.Warn("YOLO: подтверждения отключены (кроме опасных команд). Действуй осторожно!")
	} else {
		a.saveSession()
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
	for i := len(a.sess.Messages) - 1; i >= 0; i-- {
		if a.sess.Messages[i].Role == core.RoleAssistant && strings.TrimSpace(a.sess.Messages[i].Content) != "" {
			last = a.sess.Messages[i].Content
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
	cp, ok, err := a.repo.UndoCheckpoint(a.sess)
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

func (a *app) saveConfig() { _ = a.repo.SaveConfig() }
