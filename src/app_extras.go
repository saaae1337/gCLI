package main

import (
	"fmt"
	"strings"

	"gcli/agent"
	"gcli/core"
)

// ---------- Лучшие практики Claude Code: /context и # ----------

// cmdContext — разбивка контекста по частям: из чего складывается то
// число «N ctx», что видно в строке состояния. Помогает понять, что
// съедает окно: история, инструменты, память или навыки.
func (a *app) cmdContext() {
	// Снимок истории под sessMu: команду зовут из очереди ввода, а ход
	// (в том числе cron) дописывает сообщения параллельно.
	hist := a.Messages()
	msgs := core.EstimateContext(hist)

	// Инструменты: имена + описания + схемы — постояльцы каждого запроса.
	var td strings.Builder
	for _, t := range a.tools.All() {
		td.WriteString(t.Def.Name)
		td.WriteString(t.Def.Description)
		td.WriteString(t.Def.Schema)
	}
	toolsTok := core.ApproxTokens(td.String())

	mem := 0
	if a.memory != nil {
		mem = core.ApproxTokens(a.memory.Collect())
	}
	skills := core.ApproxTokens(a.tools.SkillsPromptBlock())

	// Системный промпт: база + надстройки (план, заметки).
	sysText := agent.MainSystemBase()
	if !a.agentMode() {
		sysText = agent.ChatSystemBase()
	}
	if a.repo.Cfg.PlanMode {
		sysText += "\n\n" + planModeSystem
	}
	if n := a.notesText(); n != "" {
		sysText += "\n\n" + n
	}
	sys := core.ApproxTokens(sysText)

	total := msgs + toolsTok + mem + skills + sys

	// Что реально уходит в запрос: подрезанная история плюс постоянная
	// часть. Разница с «итого» — это и есть экономия подрезки и
	// экономного блока навыков.
	var defs []core.ToolDef
	for _, t := range a.tools.All() {
		defs = append(defs, t.Def)
	}
	skillsLean := core.ApproxTokens(a.tools.SkillsPromptBlockFor(a.lastUserText()))
	reqTok, saved := agent.RequestEstimate(sysText, defs, a.sess.Messages)

	a.ui.Println("")
	a.ui.Section("Контекст")
	a.ui.KVPanel("Разбивка", [][2]string{
		{"Системный промпт", "~" + core.Kfmt(sys) + " ток."},
		{"Инструменты", "~" + core.Kfmt(toolsTok) + " ток." +
			fmt.Sprintf("  (%d шт.)", a.tools.Count())},
		{"Память (GCLI.md)", "~" + core.Kfmt(mem) + " ток."},
		{"Навыки", "~" + core.Kfmt(skills) + " ток." +
			fmt.Sprintf("  (в запросе ~%s)", core.Kfmt(skillsLean))},
		{"История диалога", "~" + core.Kfmt(msgs) + " ток." +
			fmt.Sprintf("  (%d сообщений)", len(a.sess.Messages))},
		{"Уходит в запрос", "~" + core.Kfmt(reqTok) + " ток."},
	})
	if saved > 0 {
		a.ui.Hint("подрезка истории экономит ~" + core.Kfmt(saved) + " ток. на каждом запросе")
	}
	a.ui.Hint("итого ~" + core.Kfmt(total) + " из порога авто-сжатия " + core.Kfmt(a.autoCompactLimit()))
	a.ui.Hint("пополнить память: #текст в чате, /init или /memory")
	a.ui.Println("")
}

// lastUserText — текст последнего сообщения пользователя: по нему
// подбираются навыки под задачу.
func (a *app) lastUserText() string {
	msgs := a.sess.Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == core.RoleUser && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	return ""
}

// appendMemoryNote — записать факт «#текст» в память проекта (GCLI.md).
// Приём из Claude Code: лучше записать один раз, чем надеяться на память.
func (a *app) appendMemoryNote(text string) {
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "#"))
	if text == "" {
		a.ui.Warn("формат: #текст — фраза попадёт в GCLI.md (память проекта)")
		return
	}
	path, err := a.memory.AppendFact(text)
	if err != nil {
		a.ui.Err("не удалось дописать в память: " + err.Error())
		return
	}
	a.ui.Ok("записал в память (" + core.RelToWD(a.workDir, path) + "): " +
		core.Truncate(core.OneLine(text), 70))
}
