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
	msgs := core.EstimateContext(a.sess.Messages)

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
	if !a.sess.AgentMode {
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

	a.ui.Println("")
	a.ui.Section("Контекст")
	a.ui.KVPairs([][2]string{
		{"Системный промпт", "~" + core.Kfmt(sys) + " ток."},
		{"Инструменты", "~" + core.Kfmt(toolsTok) + " ток." +
			fmt.Sprintf("  (%d шт.)", a.tools.Count())},
		{"Память (GCLI.md)", "~" + core.Kfmt(mem) + " ток."},
		{"Навыки", "~" + core.Kfmt(skills) + " ток."},
		{"История диалога", "~" + core.Kfmt(msgs) + " ток." +
			fmt.Sprintf("  (%d сообщений)", len(a.sess.Messages))},
	})
	a.ui.Hint("итого ~" + core.Kfmt(total) + " из порога авто-сжатия " + core.Kfmt(a.autoCompactLimit()))
	a.ui.Hint("пополнить память: #текст в чате, /init или /memory")
	a.ui.Println("")
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
