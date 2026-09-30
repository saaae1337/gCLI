package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gcli/core"
	"gcli/providers"
	"gcli/subagents"
	"gcli/tools"
)

// SubagentDeps — зависимости для субагента.
type SubagentDeps struct {
	Provider  *providers.Provider
	Providers *providers.Client
	// Registry — собрать новый реестр инструментов с ограничениями по типу.
	Registry func(readOnly bool, allow, deny []string) *tools.Registry
	Model    string
	SubModel string
	Think    string
	Memory   func() string
	Skills   func() string
	// MaxIters — лимит итераций агентного цикла субагента.
	MaxIters int
	// SwitchModel — временно переключить модель на время работы субагента.
	SwitchModel func(model string) (restore func(), err error)
	// OnEvent — события для UI.
	OnEvent func(kind, detail string)
}

// subagentSession — изолированная сессия субагента.
type subagentSession struct {
	msgs  []core.Message
	usage core.Usage
	todos []tools.TodoItem
}

func (s *subagentSession) Messages() []core.Message         { return s.msgs }
func (s *subagentSession) AddMessage(m core.Message)        { s.msgs = append(s.msgs, m) }
func (s *subagentSession) AddUsage(u core.Usage)            { s.usage.Add(u) }
func (s *subagentSession) Turns() int                       { return 0 }
func (s *subagentSession) SetTodos(t []tools.TodoItem)      { s.todos = t }
func (s *subagentSession) Todos() []tools.TodoItem          { return s.todos }
func (s *subagentSession) ReplaceMessages(m []core.Message) { s.msgs = m }
func (s *subagentSession) Usage() core.Usage                { return s.usage }

// CollectReport — принудительно собрать отчёт из уже собранного контекста.
//
// Ключевая идея прочности: модель часто уже знает ответ к моменту сбоя,
// но не успевает его сформулировать — цикл рвётся по лимиту итераций или
// по обрыву связи на полуслове. Здесь инструменты отключаются, и модели
// предлагается единственная задача: сдать отчёт. Если и это не выходит,
// отчёт собирается детерминированно из истории — но это уже аварийный
// вариант, и его текст честно помечается как неполный.
func (a *Agent) CollectReport(ctx context.Context) string {
	if !a.AgentMode {
		return ""
	}
	const demand = "[Система] Инструменты недоступны. Сдай ИТОГОВЫЙ ОТЧЁТ по исходной задаче: " +
		"что сделано, что не получилось и почему. Отчёт самодостаточный — заказчик не видит твоего контекста."

	prevSys, prevReg, prevMode := a.System, a.d.Registry, a.AgentMode
	a.System = prevSys + finalizePrompt
	a.d.Registry = emptyRegistry(prevReg)
	a.AgentMode = false
	defer func() {
		a.System, a.d.Registry, a.AgentMode = prevSys, prevReg, prevMode
	}()

	a.d.Session.AddMessage(core.Message{Role: core.RoleUser, Content: demand})
	for i := 0; i < finalizeTurns; i++ {
		assistant, err := a.callModel(ctx, a.BuildRequest(), true)
		if err != nil {
			break
		}
		a.Turns++
		assistant.ToolCalls = nil
		a.d.Session.AddMessage(assistant)
		if txt := strings.TrimSpace(assistant.Content); txt != "" {
			return txt
		}
	}
	return ""
}

// RunSubagent — выполнить задачу субагента и вернуть отчёт.
func RunSubagent(ctx context.Context, d SubagentDeps, spec subagents.Spec) subagents.Outcome {
	sess := &subagentSession{}

	model := spec.Model
	if model == "" {
		model = d.SubModel
	}
	if model == "" {
		model = d.Model
	}
	restore := func() {}
	if spec.Model != "" && d.SwitchModel != nil {
		r, err := d.SwitchModel(spec.Model)
		if err != nil {
			return subagents.Outcome{Full: "Не удалось переключить модель: " + err.Error()}
		}
		if r != nil {
			restore = r
			defer restore()
		}
		model = spec.Model
	}
	if model == "" {
		model = d.Model
	}

	allow, deny := subagents.ToolsFor(spec.Type)
	if len(spec.ToolsAllow) > 0 {
		// Пользовательский агент: белый список из front matter файла.
		allow = spec.ToolsAllow
		deny = []string{"spawn_agent"}
	}
	var reg *tools.Registry
	if d.Registry != nil {
		reg = d.Registry(spec.ReadOnly, allow, deny)
		if reg != nil {
			// Глубина обязана совпадать с глубиной субагента, иначе он
			// наследует Depth главного агента (0) и обходит лимит вложенности.
			reg.SetDepth(spec.Depth)
		}
	}

	maxTurns := d.MaxIters
	if spec.MaxTurns > 0 && spec.MaxTurns < maxTurns {
		maxTurns = spec.MaxTurns
	}
	if maxTurns <= 0 {
		maxTurns = 12
	}

	ag := New(Deps{
		Providers:    d.Providers,
		Provider:     d.Provider,
		Registry:     reg,
		Session:      sess,
		Model:        model,
		Think:        d.Think,
		Memory:       d.Memory,
		SkillsPrompt: d.Skills,
		MaxIters:     maxTurns,
		Quiet:        true,
	}, ".")
	ag.WithAgentMode(true)
	ag.SubagentName = spec.Name
	ag.Depth = spec.Depth
	if spec.Prompt != "" {
		// Пользовательский агент: промпт собран из его .md-файла.
		ag.System = spec.Prompt
	} else {
		ag.System = subagents.Type(spec.Type).Prompt(subagents.PromptContext{
			WorkDir:  ".",
			Summary:  spec.Summary,
			Notes:    spec.Notes,
			Model:    model,
			Depth:    spec.Depth,
			MaxDepth: spec.Depth,
		})
	}
	ag.Notes = spec.Notes
	ag.NotesList = splitNotes(spec.Notes)

	// Первый ход: задача. При повторной попытке добавляем объяснение,
	// почему прошлый запуск не удался, — иначе субагент повторит
	// ту же неудачную тактику и снова вернёт обрывок.
	task := spec.Task
	if spec.Summary != "" {
		task = "Контекст задачи:\n" + spec.Summary + "\n\nТвоя задача:\n" + spec.Task
	}
	if spec.RetryHint != "" {
		task += spec.RetryHint
	}

	var toolCalls int
	ag.d.OnToolStart = func(tc core.ToolCall, tool *tools.Tool) {
		toolCalls++
		if d.OnEvent != nil {
			d.OnEvent("tool", fmt.Sprintf("%s → %s", spec.Name, tc.Name))
		}
	}

	if err := ag.Run(ctx, task); err != nil {
		// Обрыв связи — не повод отдавать главному агенту пустоту.
		// Сеть может лечь уже после того, как субагент собрал всё нужное
		// в своей истории, поэтому даём ему один финальный ход без
		// инструментов: требуем отчёт из того, что уже есть.
		if ctx.Err() != nil {
			return subagents.Outcome{
				Full:  fmt.Sprintf("Субагент прерван: %v\n\nЧто успел сделать:\n%s", err, summarize(sess.msgs, 40)),
				Turns: ag.Turns,
				Tools: toolCalls,
				Usage: sess.usage,
			}
		}
		// Не context.Background(): если сессию прерывает Ctrl+C или по
		// истечении общего таймаута, «собрать отчёт» не должно было
		// обойти отмену и висеть на сети ещё минуту. Даём короткий
		// собственный бюджет поверх того контекста, который пришёл.
		rctx, rcancel := context.WithTimeout(ctx, 45*time.Second)
		defer rcancel()
		report := ag.CollectReport(rctx)
		if strings.TrimSpace(report) == "" {
			return subagents.Outcome{
				Full:  fmt.Sprintf("Субагент прерван: %v\n\nЧто успел сделать:\n%s", err, summarize(sess.msgs, 40)),
				Turns: ag.Turns,
				Tools: toolCalls,
				Usage: sess.usage,
			}
		}
		return subagents.Outcome{
			Full:    report,
			Summary: subagents.Summarize(report),
			Turns:   ag.Turns,
			Tools:   toolCalls,
			Usage:   sess.usage,
		}
	}

	full := lastAssistantText(sess.msgs)
	// Отчёт может оказаться пустым, обрезанным или мыслью вслух.
	// В этом случае собираем его принудительно, пока есть что сказать.
	for attempt := 0; attempt < 2; attempt++ {
		if subagents.AssessReport(full) == subagents.ReportOK {
			break
		}
		rctx, rcancel := context.WithTimeout(ctx, 45*time.Second)
		defer rcancel()
		report := ag.CollectReport(rctx)
		if strings.TrimSpace(report) == "" || report == full {
			break
		}
		full = report
	}
	if strings.TrimSpace(full) == "" {
		full = summarize(sess.msgs, 40)
	}
	// Модель могла закончить фразой вроде «сейчас ещё посмотрю…» — это не отчёт.
	// Проверяем, что субагент вообще что-то сказал своей репликой.
	if isNarrativeOnly(full) {
		return subagents.Outcome{
			Full:  "Субагент не смог сформулировать отчёт: закончил работу без результата.\n\nЧто было в его сообщениях:\n" + summarize(sess.msgs, 20),
			Turns: ag.Turns,
			Tools: toolCalls,
			Usage: sess.usage,
		}
	}
	return subagents.Outcome{
		Full:    full,
		Summary: subagents.Summarize(full),
		Turns:   ag.Turns,
		Tools:   toolCalls,
		Usage:   sess.usage,
	}
}

// isNarrativeOnly — отчёт ли это, или модель просто «думала вслух».
//
// Симптом дефекта: лимит итераций обрывал цикл, и последним уходил текст
// в середине мысли («Let me get the remaining pieces…»). Такой текст
// бесполезен главному агенту, поэтому пускаем его в отчёт как явный сбой.
//
// Детектор намеренно осторожен: срабатывает только на короткий текст без
// структуры и без ссылок на конкретные места. Ложное срабатывание (брошен
// нормальный отчёт) хуже пропущенного.
func isNarrativeOnly(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || t == "(пусто)" {
		return true
	}
	if len([]rune(t)) >= 120 {
		return false
	}
	// Структура отчёта: заголовок или пункт списка.
	for _, l := range core.SplitLines(t) {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "#") {
			return false
		}
		if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || strings.HasPrefix(l, "• ") {
			return false
		}
	}
	// Упоминание конкретного места в файле (pool.go, main.go:42) — это факт.
	if hasFileRef(t) {
		return false
	}
	return true
}

// hasFileRef — есть ли в тексте ссылка на файл по расширению.
func hasFileRef(s string) bool {
	r := []rune(s)
	for i := 0; i+2 < len(r); i++ {
		if r[i] != '.' {
			continue
		}
		j := i + 1
		for j < len(r) && isExtRune(r[j]) {
			j++
		}
		if j-i-1 >= 2 && j-i-1 <= 4 {
			return true
		}
	}
	return false
}

func isExtRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func splitNotes(s string) []string {
	var out []string
	for _, l := range core.SplitLines(s) {
		t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*• "))
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func lastAssistantText(msgs []core.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == core.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	return ""
}

func summarize(msgs []core.Message, max int) string {
	var b strings.Builder
	n := 0
	for _, m := range msgs {
		if n >= max {
			break
		}
		switch m.Role {
		case core.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString("- " + core.Truncate(core.OneLine(m.Content), 160) + "\n")
				n++
			}
		case core.RoleTool:
			b.WriteString("- [" + m.Name + "] " + core.Truncate(core.OneLine(m.Content), 120) + "\n")
			n++
		}
	}
	if b.Len() == 0 {
		return "(пусто)"
	}
	return b.String()
}
