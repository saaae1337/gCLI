package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gcli/core"
	"gcli/subagents"
	"gcli/tools"
	"gcli/ui"
)

// ---------- Субагенты ----------

func (a *app) cmdAgents(rest string) {
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		a.listAgents()
		return
	}
	switch strings.ToLower(parts[0]) {
	case "on", "вкл":
		a.setSubagents(true)
	case "off", "выкл":
		a.setSubagents(false)
	case "status":
		a.listAgents()
	case "list":
		a.listAgents()
	case "run", "запуск":
		a.runAgentCommand(parts[1:])
	case "cancel":
		n := a.pool.Cancel()
		a.ui.Ok(fmt.Sprintf("отменено субагентов: %d", n))
	case "par", "parallel":
		if len(parts) < 2 {
			a.ui.Info(fmt.Sprintf("параллельность: %d (изменить: /agents par 4)", a.repo.Cfg.SubMaxPar))
			return
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 || n > 8 {
			a.ui.Err("значение: 1..8")
			return
		}
		a.repo.Cfg.SubMaxPar = n
		a.saveConfig()
		a.pool = a.newPool()
		a.ui.Ok(fmt.Sprintf("параллельность субагентов: %d", n))
	case "depth", "глубина":
		if len(parts) < 2 {
			a.ui.Info(fmt.Sprintf("глубина: %d (изменить: /agents depth 2)", a.repo.Cfg.SubMaxDepth))
			return
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 || n > 3 {
			a.ui.Err("значение: 1..3")
			return
		}
		a.repo.Cfg.SubMaxDepth = n
		a.saveConfig()
		a.pool = a.newPool()
		a.ui.Ok(fmt.Sprintf("глубина вложенности: %d", n))
	case "model":
		if len(parts) < 2 {
			a.ui.Info("модель субагентов: " + firstNonEmpty(a.repo.Cfg.SubModel, "как у главного ("+a.model+")"))
			return
		}
		if strings.ToLower(parts[1]) == "same" || strings.ToLower(parts[1]) == "главная" {
			a.repo.Cfg.SubModel = ""
			a.ui.Ok("субагенты используют модель главного агента")
		} else {
			a.repo.Cfg.SubModel = parts[1]
			a.ui.Ok("модель субагентов: " + parts[1])
		}
		a.saveConfig()
	case "timeout", "таймаут":
		if len(parts) < 2 {
			a.ui.Info(fmt.Sprintf("таймаут субагента: %d мин (изменить: /agents timeout 20)", core.Clamp(a.repo.Cfg.SubTimeoutMin, 1, 60)))
			return
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 || n > 60 {
			a.ui.Err("значение: 1..60 (минут)")
			return
		}
		a.repo.Cfg.SubTimeoutMin = n
		a.saveConfig()
		a.ui.Ok(fmt.Sprintf("таймаут субагента: %d мин", n))
	case "types":
		a.listAgentTypes()
	case "new", "новый":
		name := "my-agent"
		if len(parts) > 1 {
			name = strings.ToLower(parts[1])
		}
		a.newCustomAgent(name)
	default:
		a.listAgents()
	}
}

func (a *app) newPool() *subagents.Pool {
	return subagents.NewPool(a.runSubagent, subagents.PoolOptions{
		MaxParallel: core.Clamp(a.repo.Cfg.SubMaxPar, 1, 8),
		MaxDepth:    core.Clamp(a.repo.Cfg.SubMaxDepth, 1, 3),
		Enabled:     a.repo.Cfg.Subagents,
		WorkDir:     a.workDir,
	})
}

func (a *app) setSubagents(on bool) {
	a.repo.Cfg.Subagents = on
	a.saveConfig()
	a.pool.SetEnabled(on)
	if on {
		a.tools.RegisterSubagentTools()
		a.ui.Ok("субагенты включены — модель может делегировать подзадачи")
	} else {
		a.ui.Ok("субагенты выключены")
	}
}

func (a *app) listAgentTypes() {
	rows := make([][]string, 0, len(subagents.Types))
	for _, t := range subagents.Types {
		toolsList := ""
		if allow, deny := subagents.ToolsFor(t); len(allow) > 0 {
			toolsList = strings.Join(allow, ", ")
		} else if len(deny) > 0 {
			toolsList = "все, кроме: " + strings.Join(deny, ", ")
		}
		ro := "чтение и запись"
		if t.ReadOnly() {
			ro = "только чтение"
		}
		rows = append(rows, []string{string(t), t.Label(), ro, core.Truncate(toolsList, 40)})
	}
	a.ui.Table([]ui.Column{
		{Title: "тип", Width: 12},
		{Title: "роль", Width: 20},
		{Title: "режим", Width: 18},
		{Title: "инструменты", Width: 40},
	}, rows, ui.BlockOpts{Title: "Типы субагентов"})

	// Пользовательские агенты — отдельной таблицей.
	if cas := a.customAgents(); len(cas) > 0 {
		rows := make([][]string, 0, len(cas))
		for _, ca := range cas {
			mode := "чтение и запись"
			if ca.ReadOnly {
				mode = "только чтение"
			}
			model := firstNonEmpty(ca.Model, "—")
			rows = append(rows, []string{ca.Name, ca.Scope, mode, model,
				core.Truncate(core.OneLine(ca.Desc), 40)})
		}
		a.ui.Table([]ui.Column{
			{Title: "агент", Width: 14},
			{Title: "область", Width: 10},
			{Title: "режим", Width: 16},
			{Title: "модель", Width: 14},
			{Title: "описание", Width: 36},
		}, rows, ui.BlockOpts{Title: "Свои агенты (.gcli/agents/*.md)"})
	}
	a.ui.Hint("создать своего: /agents new <имя> — запускается по имени через spawn_agent")
	a.ui.Println("")
}

// newCustomAgent — создать шаблон пользовательского агента.
func (a *app) newCustomAgent(name string) {
	if !tools.ValidSkillName(name) {
		a.ui.Err("имя: латиница/цифры/_/- (например: api-migrator)")
		return
	}
	dir := filepath.Join(a.workDir, ".gcli", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.ui.Err("не удалось создать каталог: " + err.Error())
		return
	}
	p := filepath.Join(dir, name+".md")
	if _, err := os.Stat(p); err == nil {
		a.ui.Warn("уже существует: " + p)
		return
	}
	if err := os.WriteFile(p, []byte(fmt.Sprintf(subagents.AgentTemplate, name)), 0o644); err != nil {
		a.ui.Err("не удалось создать: " + err.Error())
		return
	}
	a.ui.Ok("создан шаблон агента: " + p)
	a.ui.Hint("заполни front matter и тело, затем запускай: spawn_agent type=" + name + " (или /agents run " + name + " <задача>)")
}

func (a *app) listAgents() {
	rows := a.pool.Table()
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Субагенты") + " " + a.ui.Gray(fmt.Sprintf(
		"· состояние: %s · параллельно: %d · глубина: %d",
		enabledWord(a.pool.Enabled()), a.repo.Cfg.SubMaxPar, a.repo.Cfg.SubMaxDepth)))
	a.ui.Println("")
	if len(rows) == 0 {
		a.ui.Println("  " + a.ui.Gray("запусков ещё не было — модель может вызвать spawn_agent"))
	} else {
		a.ui.Table([]ui.Column{
			{Title: "имя", Width: 16},
			{Title: "тип", Width: 11},
			{Title: "статус", Width: 13},
			{Title: "время", Width: 8, Right: true},
			{Title: "инстр.", Width: 6, Right: true},
			{Title: "задача", Width: 40},
		}, rows, ui.BlockOpts{})
	}
	a.ui.Println("  " + a.ui.Gray("запустить вручную: /agents run explorer \"найди, где реализована авторизация\""))
	a.ui.Println("  " + a.ui.Gray("настройки: /agents on|off · /agents par N · /agents depth N · /agents model <имя> · /agents timeout N · /agents types"))
	a.ui.Println("")
}

func enabledWord(b bool) string {
	if b {
		return "включены"
	}
	return "выключены"
}

// customNames — имена пользовательских агентов для подсказок.
func customNames(cas []subagents.CustomAgent) []string {
	out := make([]string, 0, len(cas))
	for _, ca := range cas {
		out = append(out, ca.Name)
	}
	return out
}

// runAgentCommand — /agents run <тип> <задача>
func (a *app) runAgentCommand(parts []string) {
	if len(parts) < 2 {
		a.ui.Err("формат: /agents run <тип|имя-своего> <задача>")
		a.ui.Hint("типы: " + strings.Join(subagents.TypeNames(), " · "))
		if cas := a.customAgents(); len(cas) > 0 {
			a.ui.Hint("свои: " + strings.Join(customNames(cas), " · "))
		}
		return
	}
	typ := parts[0]
	task := strings.Join(parts[1:], " ")
	t, hint := subagents.ParseType(typ)
	if hint != "" {
		a.ui.Warn(hint)
		return
	}
	// Ручной запуск идёт через тот же пул: иначе он не занимает слот
	// параллельности, не попадает в журнал и не отменяется /agents cancel.
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	defer cancel()

	a.ui.Println("")
	a.ui.Println("  " + a.ui.Magenta("◆ субагент: ") + a.ui.Bold(string(t)) + a.ui.Gray(" · "+core.Truncate(task, 60)))
	a.ui.SpinnerStart("Субагент работает")

	t0 := core.RandID(4)
	name := fmt.Sprintf("%s-%s", t, t0)
	// В spawnAgent передаём исходное имя: для пользовательских агентов
	// это единственный ключ резолва (типа у файла нет).
	res, err := a.spawnAgent(ctx, tools.SpawnArgs{
		Type: typ, Task: task, Name: name, Depth: 1,
	})
	a.ui.SpinnerStop()

	if err != nil {
		a.ui.Err("субагент не завершился: " + err.Error())
		return
	}
	a.ui.Println("")
	a.ui.Block(res.Full, ui.BlockOpts{
		Title:  "Отчёт субагента " + res.Name,
		Accent: true,
		Footer: fmt.Sprintf("инструментов: %d · токены: ↑%s ↓%s",
			res.ToolCall, core.Kfmt(res.Usage.PromptTokens), core.Kfmt(res.Usage.CompletionTokens)),
	})
	a.ui.Println("")
}

// ---------- Скиллы ----------

// skillRowDesc — описание навыка для таблицы /skills.
//
// Триггеры when дописываются в конец: по одному только description нельзя
// понять, что навык не сработает, и пользователь его выключает, не разобравшись.
func skillRowDesc(s tools.Skill) string {
	d := s.Desc
	if s.When != "" {
		d += " · " + s.When
	}
	return core.Truncate(core.OneLine(d), 46)
}

func (a *app) cmdSkills(rest string) {
	parts := strings.Fields(rest)
	skills := a.tools.LoadSkills()
	switch {
	case len(parts) == 0:
		if len(skills) == 0 {
			a.ui.Info("навыков нет. Создай: /skill new <имя> или положи .md в ~/.gcli/skills")
			a.ui.Println("")
			return
		}
		rows := make([][]string, 0, len(skills))
		for _, s := range skills {
			state := "вкл"
			if a.tools.SkillOff(s.Name) {
				state = "выкл"
			}
			rows = append(rows, []string{s.Name, s.Scope, state, skillRowDesc(s)})
		}
		a.ui.Table([]ui.Column{
			{Title: "навык", Width: 20},
			{Title: "область", Width: 10},
			{Title: "состояние", Width: 10},
			{Title: "описание", Width: 48},
		}, rows, ui.BlockOpts{Title: "Навыки"})
		a.ui.Println("  " + a.ui.Gray("показать: /skill <имя> · подобрать: /skill <описание задачи> · выключить: /skill off <имя> · создать: /skill new <имя>"))
		a.ui.Println("")

	case parts[0] == "new" || parts[0] == "новый":
		name := "my-skill"
		if len(parts) > 1 {
			name = strings.ToLower(parts[1])
		}
		if !tools.ValidSkillName(name) {
			a.ui.Err("имя: латиница/цифры/_/- (например: deploy-helper)")
			return
		}
		dir := filepath.Join(a.workDir, ".gcli", "skills")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.ui.Err("не удалось создать каталог: " + err.Error())
			return
		}
		p := filepath.Join(dir, name+".md")
		if _, err := os.Stat(p); err == nil {
			a.ui.Warn("уже существует: " + p)
			return
		}
		if err := os.WriteFile(p, []byte(tools.SkillTemplate(name)), 0o644); err != nil {
			a.ui.Err("не удалось создать: " + err.Error())
			return
		}
		a.ui.Ok("создан шаблон: " + p)

	case parts[0] == "on" && len(parts) > 1:
		name := parts[1]
		var kept []string
		for _, n := range a.repo.Cfg.SkillsOff {
			if n != name {
				kept = append(kept, n)
			}
		}
		a.repo.Cfg.SkillsOff = kept
		a.tools.SetSkillsOff(kept)
		a.saveConfig()
		a.ui.Ok("навык «" + name + "» включён")

	case parts[0] == "off" && len(parts) > 1:
		name := parts[1]
		if !a.tools.SkillOff(name) {
			a.repo.Cfg.SkillsOff = append(a.repo.Cfg.SkillsOff, name)
			a.tools.SetSkillsOff(a.repo.Cfg.SkillsOff)
			a.saveConfig()
		}
		a.ui.Ok("навык «" + name + "» выключен")

	default:
		name := parts[0]
		for _, s := range skills {
			if s.Name == name {
				a.ui.Block(s.Body, ui.BlockOpts{
					Title:    s.Name,
					Subtitle: s.Scope + " · " + s.Path,
					Accent:   true,
				})
				return
			}
		}
		// Имя не найдено — пробуем понять, что имелось в виду: пользователь
		// пишет «/skill ревью» так же естественно, как модель зовёт навык по
		// описанию. Без подбора он получил бы только «не найден».
		if strings.TrimSpace(rest) != "" {
			if s, alts, err := a.tools.MatchSkillTop(rest); err == nil {
				a.ui.Block(s.Body, ui.BlockOpts{
					Title:    s.Name,
					Subtitle: s.Scope + " · " + s.Path,
					Accent:   true,
				})
				if len(alts) > 0 {
					names := make([]string, 0, len(alts))
					for _, al := range alts {
						names = append(names, al.Name)
					}
					a.ui.Println(a.ui.Gray("похожие: " + strings.Join(names, ", ")))
					a.ui.Println("")
				}
				return
			}
		}
		a.ui.Warn("навык «" + name + "» не найден — список: /skills")
	}
}

// ---------- Расширения ----------

func (a *app) cmdExt(rest string) {
	parts := strings.Fields(rest)
	switch {
	case len(parts) == 0 || parts[0] == "list":
		exts := a.tools.LoadExtensions()
		if len(exts) == 0 {
			a.ui.Info("расширений нет. Создай: /ext new <имя> или положи .json в ~/.gcli/extensions")
			a.ui.Println("")
			return
		}
		for _, ext := range exts {
			rows := make([][]string, 0, len(ext.Tools))
			for _, t := range ext.Tools {
				kind := "cmd"
				if t.Command == "" {
					kind = "http"
				}
				note := ""
				if t.NoConfirm {
					note = "без подтверждения"
				}
				rows = append(rows, []string{t.Name, kind, core.Truncate(core.OneLine(t.Desc), 40), note})
			}
			a.ui.Table([]ui.Column{
				{Title: "инструмент", Width: 20},
				{Title: "тип", Width: 6},
				{Title: "описание", Width: 44},
				{Title: "", Width: 18},
			}, rows, ui.BlockOpts{
				Title:    ext.Name,
				Subtitle: ext.Desc,
			})
		}
		a.ui.Println("  " + a.ui.Gray("подключено инструментов: "+strconv.Itoa(a.tools.ExtCount())+" · перечитать: /ext reload"))
		a.ui.Println("")

	case parts[0] == "new" || parts[0] == "новый":
		name := "my-ext"
		if len(parts) > 1 {
			name = strings.ToLower(parts[1])
		}
		if !tools.ValidSkillName(name) {
			a.ui.Err("имя: латиница/цифры/_/- (например: deploy-tools)")
			return
		}
		dir := filepath.Join(a.workDir, ".gcli", "extensions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.ui.Err("не удалось создать каталог: " + err.Error())
			return
		}
		p := filepath.Join(dir, name+".json")
		if _, err := os.Stat(p); err == nil {
			a.ui.Warn("уже существует: " + p)
			return
		}
		if err := os.WriteFile(p, []byte(tools.ExtTemplate(name)), 0o644); err != nil {
			a.ui.Err("не удалось создать: " + err.Error())
			return
		}
		a.ui.Ok("создан шаблон: " + p)
		a.ui.Hint("отредактируй tools и подключи: /ext reload")

	case parts[0] == "reload" || parts[0] == "перезагрузить":
		warns := a.tools.RegisterExtTools()
		for _, w := range warns {
			a.ui.Warn(w)
		}
		a.ui.Ok(fmt.Sprintf("инструменты перечитаны: всего %d (расширения: %d)",
			a.tools.Count(), a.tools.ExtCount()))
	default:
		a.ui.Warn("не понял: " + rest + " — доступно: /ext · /ext new <имя> · /ext reload")
	}
}

// ---------- Экспорт, сжатие, диагностика ----------

func (a *app) cmdExport() {
	dir := filepath.Join(a.store.Root, "exports")
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "gcli-"+a.sess.ID+".md")

	var b strings.Builder
	fmt.Fprintf(&b, "# Диалог gcli · %s\n\n", a.sess.Title)
	fmt.Fprintf(&b, "- Сессия: `%s`\n- Создана: %s\n- Провайдер: %s (%s)\n- Модель: %s\n",
		a.sess.ID, a.sess.Created.Format("02.01.2006 15:04"), a.prov.Label, a.prov.ID, a.model)

	// Снимок под sessMu: usage и журнал субагентов пишутся из горутин.
	sessMu.Lock()
	msgCount := len(a.sess.Messages)
	usage := a.sess.Usage
	sessMu.Unlock()

	fmt.Fprintf(&b, "- Сообщений: %d · токены: ↑%s ↓%s\n\n---\n",
		msgCount, core.Kfmt(usage.PromptTokens), core.Kfmt(usage.CompletionTokens))

	subReports := a.subagentReports()
	if len(subReports) > 0 {
		b.WriteString("\n## Субагенты\n\n")
		for _, r := range subReports {
			fmt.Fprintf(&b, "- **%s** (%s) — %s, инструментов: %d\n", r.Name, r.Type, r.Status, r.Tools)
			if r.Summary != "" {
				fmt.Fprintf(&b, "  > %s\n", core.Truncate(core.OneLine(r.Summary), 200))
			}
		}
	}

	for _, m := range a.sess.Messages {
		switch m.Role {
		case core.RoleUser:
			fmt.Fprintf(&b, "\n## Пользователь\n\n%s\n", m.Content)
		case core.RoleAssistant:
			who := "Ассистент"
			if m.Sub != "" {
				who = "Ассистент (" + m.Sub + ")"
			}
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", who, m.Content)
			if m.Reasoning != "" {
				fmt.Fprintf(&b, "\n<details><summary>размышления модели</summary>\n\n%s\n\n</details>\n", m.Reasoning)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "\n- `%s(%s)`\n", tc.Name, core.Truncate(core.OneLine(tc.Args), 120))
			}
		case core.RoleTool:
			fmt.Fprintf(&b, "\n<details><summary>результат %s</summary>\n\n```\n%s\n```\n\n</details>\n", m.Name, m.Content)
		}
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		a.ui.Err("экспорт не удался: " + err.Error())
		return
	}
	a.ui.Ok("экспортировано: " + p)
}

func (a *app) cmdCompact() {
	a.ui.SpinnerStart("Сжимаю историю")
	ag := a.newAgent(context.Background(), true)
	ag.WithAgentMode(false)
	ok := ag.Compact(false)
	a.ui.SpinnerStop()
	if ok {
		a.saveSession()
		a.ui.Ok("история сжата: осталось " + strconv.Itoa(len(a.sess.Messages)) + " сообщений")
	} else {
		a.ui.Warn("сжимать нечего или модель не ответила")
	}
}

func (a *app) cmdDoctor(rest string) {
	rest = strings.TrimSpace(rest)
	if rest == "key" || strings.HasPrefix(rest, "key ") {
		a.doctorKey(strings.TrimSpace(strings.TrimPrefix(rest, "key")))
		return
	}
	a.ui.Println("")
	rows := [][3]string{}
	add := func(ok bool, k, v string) {
		rows = append(rows, [3]string{fmt.Sprint(ok), k, v})
	}

	add(true, "версия", "gcli v"+version()+" · "+osGOOS()+" · "+osArch())
	add(true, "каталог данных", a.store.Root)
	if _, err := os.Stat(a.store.ConfigPath()); err == nil {
		add(true, "config.json", "найден")
	} else {
		add(true, "config.json", "нет — создастся при настройке")
	}
	add(true, "рабочий каталог", a.workDir)
	add(a.prov.HasKey(), "ключ "+a.prov.ID, keyStatus(a.prov)+" · "+a.prov.BaseURL)

	// Сеть.
	a.ui.SpinnerStart("Проверяю доступность " + a.prov.ID)
	el, code, err := pingProvider(a)
	a.ui.SpinnerStop()
	if err != nil {
		add(false, "сеть "+a.prov.ID, "недоступен: "+core.Truncate(core.OneLine(err.Error()), 50))
	} else {
		add(true, "сеть "+a.prov.ID, fmt.Sprintf("HTTP %d · %d мс", code, el.Milliseconds()))
	}

	// Ключ.
	if a.prov.NoKey {
		add(true, "тест ключа", "локальный провайдер — не нужен")
	} else if a.prov.Key == "" {
		add(false, "тест ключа", "не задан — /doctor key")
	} else {
		a.ui.SpinnerStart("Проверяю ключ " + a.prov.ID)
		ok, detail := testKey(a)
		a.ui.SpinnerStop()
		add(ok, "тест ключа", core.Truncate(detail, 60))
	}

	// Хранилище.
	ns := len(a.repo.ListSessions())
	add(true, "хранилище", fmt.Sprintf("сессий: %d · чекпоинтов: %d · навыков: %d · расширений: %d",
		ns, len(a.sess.Checkpoints), len(a.tools.LoadSkills()), len(a.tools.LoadExtensions())))

	// Память.
	if mems := a.memory.Files(); len(mems) > 0 {
		add(true, "память проекта", strings.Join(mems, ", "))
	} else {
		add(false, "память проекта", "нет — /init")
	}

	add(true, "инструменты", fmt.Sprintf("%d (расширений: %d)", a.tools.Count(), a.tools.ExtCount()))
	add(a.pool.Enabled(), "субагенты", enabledWord(a.pool.Enabled())+
		fmt.Sprintf(" · параллельно: %d · глубина: %d", a.repo.Cfg.SubMaxPar, a.repo.Cfg.SubMaxDepth))
	add(true, "размышления", a.repo.Cfg.Think)
	add(true, "оболочка", tools.ShellName())

	// Печать таблицей.
	trows := make([][]string, 0, len(rows))
	for _, r := range rows {
		mark := a.ui.Red("✗")
		if r[0] == "true" {
			mark = a.ui.Green("✓")
		}
		trows = append(trows, []string{mark, r[1], r[2]})
	}
	a.ui.Table([]ui.Column{
		{Title: "", Width: 2},
		{Title: "проверка", Width: 18},
		{Title: "результат", Width: 52},
	}, trows, ui.BlockOpts{Title: "Диагностика gcli"})
	a.ui.Println("  " + a.ui.Gray("подробно о ключе: /doctor key "+a.prov.ID))
	a.ui.Println("")
}

func (a *app) doctorKey(id string) {
	p := a.prov
	if id != "" {
		if cand := a.registry.Find(strings.ToLower(id)); cand != nil {
			p = cand
		} else {
			a.ui.Err("нет провайдера «" + id + "» — /provider list")
			return
		}
	}
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Head("Тест ключа") + " " + a.ui.Gray(p.ID+" → "+p.BaseURL))
	a.ui.SpinnerStart("Запрос к " + p.ID)
	ok, detail := testKeyFor(a, p)
	a.ui.SpinnerStop()
	if ok {
		a.ui.Ok(detail)
	} else {
		a.ui.Err(detail)
	}
	a.ui.Println("")
}
