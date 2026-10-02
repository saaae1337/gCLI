package main

import (
	"gcli/agent"
	"gcli/core"
	"gcli/tools"
)

// selfReport — снимок собственного состояния агента для инструмента
// self_status.
//
// Замысел: агент, который видит свой бюджет, экономит сам. До этого
// расход и заполнение контекста были видны только человеку через /status,
// то есть модель работала вслепую и упиралась в порог сжатия уже после
// того, как потратила лишнее.
func (a *app) selfReport() tools.SelfReport {
	rep := tools.SelfReport{
		CompactAt: a.autoCompactLimit(),
		MaxIters:  a.maxIters(),
		Model:     a.model,
		Provider:  a.prov.ID,
		Since:     a.sess.Stats.SessionTime(),
	}

	// Расход и статистику снимаем под блокировкой: их пишут горутины
	// субагентов параллельно с этим вызовом.
	sessMu.Lock()
	rep.Usage = a.sess.Usage
	rep.Stats = a.sess.Stats
	sessMu.Unlock()
	rep.History = a.sess.Messages

	// Системный промпт берём у того агента, который сейчас работает,
	// иначе агент сравнивал бы свой контекст с чужим.
	rep.ExtendAbs = a.maxItersAbs()
	if ag := a.currentAgent(); ag != nil {
		rep.SystemPrompt = ag.SystemPrompt()
		rep.Turns = ag.Turns
		rep.Subagent = ag.SubagentName
		rep.Depth = ag.Depth
		// Живой лимит важнее базового: после продления именно он определяет,
		// сколько итераций осталось. В self_status попадает оба — и сколько
		// использовано, и до какого потолка вообще.
		if st := ag.ExtendState(); st != nil {
			rep.MaxIters = st.Limit()
			rep.Extends = st.Extends()
		}
	} else {
		// Агент ещё не собран — считаем хотя бы примерно, по базовому.
		rep.SystemPrompt = agent.MainSystemBase()
	}

	rep.Notes = a.notesCopy()
	rep.ReadFiles = a.tools.CountRead()

	// Прогон под a.mu не держим: он меняется на каждой итерации, а
	// self_status зовут из инструмента, то есть изнутри хода агента.
	// Строка собирается под тем же локом, что и счётчики трекера.
	if tr := a.currentMissionTr(); tr != nil {
		a.mu.Lock()
		rep.Mission = tr.SelfLine()
		a.mu.Unlock()
	}

	if a.pool != nil {
		rep.Agents = a.pool.Summary()
	}
	return rep
}

// notesCopy — копия заметок задачи (без блокировки на время сборки отчёта).
func (a *app) notesCopy() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.notes) == 0 {
		return nil
	}
	out := make([]string, len(a.notes))
	copy(out, a.notes)
	return out
}

// currentAgent — агент текущего хода, если он есть.
func (a *app) currentAgent() *agent.Agent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastAgent
}

// SelfTokens — примерный вес системного промпта в токенах (для /context).
func SelfTokens(p string) int { return core.ApproxTokens(p) }
