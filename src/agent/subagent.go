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
	// WorkDir — корень проекта. Нужен журналу доказательств: без него нельзя
	// отличить «субагент выдумал файл» от «файл есть, но он его не открывал».
	WorkDir string
	Memory  func() string
	Skills  func() string
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
	// Grounding — журнал доказательств: что субагент реально открывал и
	// выполнял. Наполняется хуком OnToolDone и используется для проверки
	// итогового отчёта перед возвратом главному агенту.
	ground := subagents.NewGrounding(d.WorkDir)
	ag.d.OnToolStart = func(tc core.ToolCall, tool *tools.Tool) {
		toolCalls++
		if d.OnEvent != nil {
			d.OnEvent("tool", fmt.Sprintf("%s → %s", spec.Name, tc.Name))
		}
	}
	ag.d.OnToolDone = func(tc core.ToolCall, tool *tools.Tool, res tools.Result, err error, elapsed time.Duration) {
		// Ошибка инструмента доказательством не является: после неудачного
		// grep субагент физически не мог увидеть содержимое файла.
		ground.Observe(tc.Name, tc.Args, res.Text, err == nil && res.Error == "")
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
		// Прозвучавшая как отчёт фраза проверяется тем же способом, что и
		// нормальный отчёт: в выжимке видно, что субагент вообще делал, и
		// главный агент должен видеть, подтверждено это или нет.
		audit0 := ground.Audit(full)
		// Аудит считается и здесь, до подстановки: иначе главный агент получил бы
		// выжимку без вердикта и без списка неподтверждённых мест — то есть
		// ровно то, ради чего проверка и делается.
		body := "Субагент не смог сформулировать отчёт: закончил работу без результата." +
			"\n\nЧто было в его сообщениях:\n" + summarize(sess.msgs, 20)
		return subagents.Outcome{
			Full:  body + ground.Report(audit0),
			Turns: ag.Turns,
			Tools: toolCalls,
			Usage: sess.usage,
			Audit: audit0,
		}
	}

	// Проверка отчёта по журналу доказательств.
	//
	// Сначала — адресная добивка: субагент ещё жив, инструменты доступны, и
	// две итерации RepairReport дешевле, чем перезапуск всего задания.
	//
	// Дальше повтор возможен, но уже на уровне слоя прочности (Wrap) и только
	// по вердикту JudgeReport: при слабой опоре (фантомы либо меньше половины
	// подтверждённых ссылок) запуск повторяется целиком, с адресным
	// RetryHintAudit. Отчёт без замечаний остаётся чистым — подтверждать
	// исправность работы субагента в каждом отчёте значит засорять контекст
	// главного агента новостями, которых он не просил.
	audit := ground.Audit(full)
	fixed := ag.RepairReport(ctx, full, audit)
	if fixed != "" && fixed != full {
		full = fixed
		audit = ground.Audit(full)
	}
	return subagents.Outcome{
		Full:    full + ground.Report(audit),
		Summary: subagents.Summarize(full),
		Turns:   ag.Turns,
		Tools:   toolCalls,
		Usage:   sess.usage,
		// Audit уезжает в слой прочности: без него Wrap не может отличить
		// «формально гладкий, но выдуманный» отчёт от честного и повторил бы
		// запуск вслепую. Нужны и вердикт, и список мест для подсказки.
		Audit: audit,
	}
}

// repairBudget — сколько итераций даётся на адресную добивку отчёта.
//
// Ровно две: первая итерация уходит на открытие спорных мест, вторая — на
// переписывание отчёта. Больше не нужно, а каждый лишний ход — это оплаченный
// запрос к модели.
const repairBudget = 2

// RepairReport — адресная добивка отчёта: не «попробуй ещё раз», а конкретный
// список мест, которые субагент упомянул, но не открывал.
//
// Зачем это нужно. Проверка «отчёт ссылается на непрочитанные строки» без
// реакции бесполезна: главный агент получает непроверяемый текст и принимает
// выдумку за факт. Дать субагенту открыть именно эти строки и переписать отчёт
// дёшево и адресно: обычно достаточно одного точечного read_file. Если он и
// после этого повторяет непроверенную ссылку, отчёт помечается, но всё равно
// возвращается — частично верный результат лучше пустоты.
//
// Инструменты на этих ходах остаются включёнными (в отличие от CollectReport):
// иначе «открой и проверь» превратится в «убери всё, чего не помнишь», и
// модель выбросит вместе с выдумками настоящие находки.
func (a *Agent) RepairReport(ctx context.Context, full string, audit *subagents.GroundingReport) string {
	if audit == nil || audit.Trustworthy {
		return ""
	}
	problems := audit.Problems()
	if len(problems) == 0 {
		return ""
	}
	// Если субагент вообще ничего не читал, «добивка» — это полноценное
	// исследование с нуля, а не починка отчёта. Это уже работа для главного
	// агента, и тратить на неё финальные ходы бессмысленно.
	if audit.FilesRead == 0 {
		return ""
	}

	// Свой таймаут: общий контекст может уже почти истечь, и тогда добивка
	// съест остаток бюджета главного агента на вызов, который ничего не даст.
	rctx, rcancel := context.WithTimeout(ctx, repairTimeout)
	defer rcancel()

	a.d.Session.AddMessage(core.Message{Role: core.RoleUser, Content: repairDemand(problems, audit)})
	// Цикл написан здесь, а не через a.Run, намеренно: Run на исчерпании
	// лимита пишет снимок состояния на диск (autoHandoff), а при починке
	// отчёта это лишний побочный эффект. Здесь нужен ровно один короткий цикл:
	// открыть спорные места → переписать отчёт.
	for i := 0; i < repairBudget; i++ {
		assistant, err := a.callModel(rctx, a.BuildRequest(), true)
		if err != nil {
			break
		}
		a.Turns++
		a.d.Session.AddMessage(assistant)
		if len(assistant.ToolCalls) == 0 || !a.AgentMode {
			break
		}
		for _, r := range a.execTools(rctx, assistant.ToolCalls) {
			a.d.Session.AddMessage(core.Message{
				Role:       core.RoleTool,
				ToolCallID: r.tc.ID,
				Name:       r.tc.Name,
				Content:    r.text,
			})
		}
	}
	fixed := lastAssistantText(sessMessages(a))
	if strings.TrimSpace(fixed) == "" || fixed == full || isNarrativeOnly(fixed) {
		return ""
	}
	return fixed
}

// sessMessages — история сессии агента (нужна для последнего сообщения).
func sessMessages(a *Agent) []core.Message {
	if a == nil || a.d.Session == nil {
		return nil
	}
	return a.d.Session.Messages()
}

// repairTimeout — потолок на всю добивку вместе с её ходами.
const repairTimeout = 90 * time.Second

// repairDemand — требование к добивке со списком проблемных мест.
func repairDemand(problems []string, audit *subagents.GroundingReport) string {
	var b strings.Builder
	b.WriteString("[Система] Твой отчёт проверен автоматически. Часть ссылок не подтверждается.\n")
	fmt.Fprintf(&b, "Прочитал ты %d файл(ов). Проблемные места:\n", audit.FilesRead)
	for _, p := range problems {
		b.WriteString("  - " + p + "\n")
	}
	b.WriteString("\nПерепиши ИТОГОВЫЙ ОТЧЁТ целиком: подтверди то, что видел, " +
		"убери или честно помечай то, чего не видел. Ничего нового не выдумывай.")
	return b.String()
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
