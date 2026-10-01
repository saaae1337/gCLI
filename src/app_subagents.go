package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gcli/agent"
	"gcli/core"
	"gcli/providers"
	"gcli/subagents"
	"gcli/tools"
)

// customAgents — пользовательские агенты (.gcli/agents/*.md и
// ~/.gcli/agents/*.md). Читаются на каждый вызов: файл мог появиться
// только что (/agents new), кешировать нельзя.
func (a *app) customAgents() []subagents.CustomAgent {
	return subagents.LoadCustomAgents(a.workDir, a.store.Root)
}

// spawnAgent — обработка вызова инструмента spawn_agent.
func (a *app) spawnAgent(ctx context.Context, args tools.SpawnArgs) (tools.SpawnResult, error) {
	if !a.pool.Enabled() {
		return tools.SpawnResult{}, fmt.Errorf("субагенты отключены — включи: /agents on")
	}
	// Имена пользовательских агентов читаются один раз: список нужен и для
	// разбора типа, и для проверки «а не пользовательский ли это агент», а
	// LoadCustomAgents ходит в файлы при каждом вызове.
	customs := a.customAgents()
	customNames := make([]string, 0, len(customs))
	for i := range customs {
		customNames = append(customNames, customs[i].Name)
	}
	// Автовыбор роли: если модель не указала тип, он выводится из текста
	// задачи. Раньше тут стоял молчаливый дефолт explorer — то есть любая
	// задача без явного типа уходила в роль без инструментов записи.
	t, dv := subagents.ResolveType(args.Type, args.Task, customNames)
	hint := ""
	if args.Type == "" {
		hint = dv.Reason()
	}

	// Пользовательские агенты: имя из .gcli/agents/*.md перекрывает
	// универсальный custom — промпт и инструменты берутся из файла.
	var custom *subagents.CustomAgent
	if args.Type != "" {
		for i := range customs {
			if strings.EqualFold(customs[i].Name, strings.TrimSpace(args.Type)) {
				custom = &customs[i]
				break
			}
		}
		if custom == nil && t == subagents.TypeCustom {
			return tools.SpawnResult{}, fmt.Errorf(
				"тип «%s» не найден; типы: %s — или создай своего: /agents new <имя>",
				args.Type, strings.Join(subagents.TypeNames(), ", "))
		}
	}
	// Автовыбор не должен подменять роль, если задача очевидно пишущая, а
	// выбранная роль только читает: тогда доводить работу нечем. Понижаем до
	// general, а не возвращаем отказ — задача-то обычно выполнима.
	if custom == nil && args.Type == "" && t.ReadOnly() && !args.ReadOnly {
		t = subagents.TypeGeneral
		hint += "; роль только для чтения понижена до general — задаче нужен исполнитель"
	}
	readOnly := args.ReadOnly || t.ReadOnly()
	if custom != nil {
		readOnly = readOnly || custom.ReadOnly
	}
	spec := subagents.Spec{
		Type:     t,
		Task:     args.Task,
		Name:     args.Name,
		Model:    args.Model,
		ReadOnly: readOnly,
		Depth:    args.Depth,
		Summary:  a.taskSummary(),
		Notes:    a.notesText(),
		MaxTurns: core.Clamp(a.repo.Cfg.SubMaxTurns, 1, 60),
	}
	if hint != "" {
		spec.Notes = strings.TrimSpace(spec.Notes + "\n\n[Подбор роли] " + hint)
	}
	if custom != nil {
		spec.Prompt = subagents.CustomPrompt(custom.Prompt, subagents.PromptContext{
			WorkDir: a.workDir,
			Summary: spec.Summary,
			Notes:   spec.Notes,
		})
		spec.ToolsAllow = custom.Tools
		if spec.Model == "" {
			spec.Model = custom.Model
		}
		if custom.Name != "" && spec.Name == "" {
			spec.Name = custom.Name
		}
	}
	// Жёсткий потолок времени на одного субагента. Без него 12 итераций по
	// 10 минут на модель дают до 2 часов на субагента, а пул из 3 таких —
	// это очередь, в которой главный агент просто зависает.
	timeout := time.Duration(core.Clamp(a.repo.Cfg.SubTimeoutMin, 1, 60)) * time.Minute
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	before := a.pool.Running()
	name := spec.Name
	if name == "" {
		name = t.Label()
	}
	if custom != nil {
		name = custom.Name
	}
	if before == 0 && !a.quiet {
		a.ui.Println("  " + a.ui.Magenta("◆ запускаю субагента: ") + a.ui.Bold(name) +
			a.ui.Gray("  ("+string(t)+")"))
	}
	out, err := a.pool.Spawn(sctx, spec)
	runName := name
	if runs := a.pool.Get(name); len(runs) > 0 {
		runName = runs[0].Name
	}
	a.recordSubagent(core.SubagentRecord{
		ID:      core.RandID(6),
		Name:    runName,
		Type:    string(t),
		Task:    spec.Task,
		Model:   firstNonEmpty(spec.Model, a.repo.Cfg.SubModel, a.model),
		Status:  string(statusOf(err)),
		Depth:   spec.Depth,
		Turns:   out.Turns,
		Tools:   out.Tools,
		Summary: out.Summary,
		Err:     errText(err),
	})
	if err != nil {
		return tools.SpawnResult{}, err
	}
	return tools.SpawnResult{
		Name: runName,
		Type: string(t),
		// Помечаем автовыбор, а не любой вызов без type: в hSpawnAgent это
		// единственный способ отличить «модель выбрала general сама» от
		// «роль вывел диспетчер».
		Dispatched: args.Type == "",
		Reuse:      string(out.Reuse),
		ReusedFrom: out.ReusedFrom,
		Summary:    out.Summary,
		Full:       out.Full,
		Usage:      core.Usage{PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens},
		Turns:      out.Turns,
		ToolCall:   out.Tools,
	}, nil
}

func statusOf(err error) subagents.Status {
	if err != nil {
		return subagents.StatusError
	}
	return subagents.StatusDone
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// runSubagent — собственный цикл субагента.
func (a *app) runSubagent(ctx context.Context, spec subagents.Spec) (subagents.Outcome, error) {
	d := agent.SubagentDeps{
		Provider:  a.prov,
		Providers: a.client,
		Registry:  a.registryForSubagent,
		Model:     a.model,
		SubModel:  a.repo.Cfg.SubModel,
		WorkDir:   a.workDir,
		Think:     providers.ThinkState(a.repo.Cfg),
		Memory:    a.memory.Collect,
		Skills:    func() string { return a.tools.SkillsPromptBlock() },
		MaxIters:  core.Clamp(a.repo.Cfg.SubMaxTurns, 1, 60),
		OnEvent: func(kind, detail string) {
			if kind == "tool" && !a.quiet {
				a.ui.Println("    " + a.ui.Gray("└ "+detail))
			}
		},
	}
	out := agent.RunSubagent(ctx, d, spec)
	a.sess.AddUsage(out.Usage)
	return out, nil
}

// registryForSubagent — собрать ограниченный реестр инструментов для субагента.
func (a *app) registryForSubagent(readOnly bool, allow, deny []string) *tools.Registry {
	a.mu.Lock()
	defer a.mu.Unlock()
	base := a.tools.Base()
	return base.Restrict(allow, deny)
}

// agentsInfo — сводка по субагентам для agent_status.
func (a *app) agentsInfo(action, name string) string {
	switch action {
	case "result":
		if name == "" {
			return a.pool.Summary()
		}
		return a.pool.Details(name)
	case "list":
		return a.pool.Summary()
	default:
		return a.pool.Summary()
	}
}

// taskSummary — краткое описание текущей задачи (для контекста субагента).
func (a *app) taskSummary() string {
	// Последнее сообщение пользователя + заголовок сессии.
	for i := len(a.sess.Messages) - 1; i >= 0; i-- {
		if a.sess.Messages[i].Role == core.RoleUser {
			u := a.sess.Messages[i].Content
			if i := strings.Index(u, "\n\n--- Приложенные файлы"); i > 0 {
				u = u[:i]
			}
			return core.Truncate(core.OneLine(u), 400)
		}
	}
	return a.sess.Title
}

// recordSubagent — записать запуск в журнал сессии.
func (a *app) recordSubagent(r core.SubagentRecord) {
	sessMu.Lock()
	a.sess.SubagentRuns = append(a.sess.SubagentRuns, r)
	if len(a.sess.SubagentRuns) > 40 {
		a.sess.SubagentRuns = a.sess.SubagentRuns[len(a.sess.SubagentRuns)-40:]
	}
	sessMu.Unlock()
	a.saveSession()
}

// subagentReports — отчёты всех субагентов сессии (для /export).
//
// Снимок под sessMu: журнал пополняется из горутин субагентов
// (recordSubagent), а читается из UI-потока — без блокировки это гонка.
func (a *app) subagentReports() []core.SubagentRecord {
	sessMu.Lock()
	defer sessMu.Unlock()
	out := make([]core.SubagentRecord, len(a.sess.SubagentRuns))
	copy(out, a.sess.SubagentRuns)
	return out
}
