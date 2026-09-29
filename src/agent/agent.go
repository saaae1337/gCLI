// Package agent — агентный цикл gcli: промпты, стриминг, вызовы инструментов,
// параллельное выполнение, восстановление после паник, авто-сжатие контекста.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
)

// Ограничения цикла.
const (
	// DefaultMaxIters — максимум итераций «модель → инструменты» за один ход.
	DefaultMaxIters = 40
	// maxToolResult — сколько символов результата инструмента уходит в контекст.
	maxToolResult = 24000
	// DefaultAutoCompact — порог авто-сжатия контекста.
	DefaultAutoCompact = 80_000
	// defaultMaxTokens — потолок ответа модели.
	defaultMaxTokens = 8192
	// finalizeTurns — сколько дополнительных ходов даётся агенту после
	// исчерпания лимита, чтобы он всё-таки сдал результат.
	//
	// Без этого лимит итераций обрывал цикл на полуслове: последнее
	// сообщение ассистента — это нарратив в середине работы («сейчас ещё
	// посмотрю…»), и именно оно уходило вверх как «результат». Субагенты
	// стабильно возвращали обрывки рассуждений вместо отчёта.
	finalizeTurns = 2
)

// finalizePrompt — добавка к системному промпту на финальных ходах.
const finalizePrompt = "" +
	"\n\n# Финальный ход — сдай результат\n" +
	"Лимит итераций исчерпан. Больше инструменты вызывать не нужно.\n" +
	"Сейчас напиши ИТОГОВЫЙ ОТЧЁТ — это единственное, что увидит заказчик, " +
	"и он не видит твоего контекста.\n" +
	"Требования к отчёту:\n" +
	"- Пиши по-русски, конкретно: файлы, строки, команды, факты.\n" +
	"- Не пересказывай ход работы и не пиши «я посмотрел» — только результат.\n" +
	"- Отчёт должен быть самодостаточным и готовым к использованию.\n" +
	"- Если задача не выполнена — прямо напиши, что не получилось и почему."

// Deps — зависимости агента (внедряются для тестируемости).
type Deps struct {
	Providers *providers.Client
	Provider  *providers.Provider
	Registry  *tools.Registry
	Session   SessionRef
	Model     string
	Think     string
	// OnDelta — потоковый вывод текста (nil = не печатать).
	OnDelta func(text string)
	// OnReason — потоковый вывод размышлений.
	OnReason func(text string)
	// OnToolStart — начало выполнения инструмента.
	OnToolStart func(tc core.ToolCall, tool *tools.Tool)
	// OnToolDone — результат инструмента.
	OnToolDone func(tc core.ToolCall, tool *tools.Tool, res tools.Result, err error, elapsed time.Duration)
	// OnUsage — расход токенов за вызов.
	OnUsage func(u core.Usage, d time.Duration)
	// Memory — собрать память проекта для промпта.
	Memory func() string
	// SkillsPrompt — блок со списком навыков.
	SkillsPrompt func() string
	// MaxIters — лимит итераций (0 = DefaultMaxIters).
	MaxIters int
	// AutoCompact — порог авто-сжатия (0 = выключить).
	AutoCompact int
	// Parallel — выполнять независимые вызовы инструментов параллельно.
	Parallel bool
	// MaxParallelTools — сколько инструментов одновременно.
	MaxParallelTools int
	// Quiet — не печатать ничего (машинный режим).
	Quiet bool
	// ModelSwitcher — сменить модель по имени (для субагентов).
	ModelSwitcher func(model string) error
}

// SessionRef — минимальный интерфейс сессии.
type SessionRef interface {
	Messages() []core.Message
	AddMessage(m core.Message)
	AddUsage(u core.Usage)
	Turns() int
	SetTodos(t []tools.TodoItem)
	Todos() []tools.TodoItem
}

// Agent — агент gcli.
type Agent struct {
	d Deps
	// WorkDir — рабочий каталог (для системного промпта).
	WorkDir string
	// System — дополнительные указания для главного агента.
	System string
	// AgentMode — использовать инструменты.
	AgentMode bool
	// SubagentName — имя субагента (пусто у главного).
	SubagentName string
	// Turns — реально выполненное число итераций агентного цикла.
	Turns int
	// Depth — глубина вложенности.
	Depth int
	// Notes — заметки для подмешивания в промпт.
	Notes string
	// NotesList — накопленные заметки (task_note).
	NotesList []string
	// OnNote — callback при добавлении заметки.
	OnNote func(title, body string)
}

// New — создать агента.
func New(d Deps, workDir string) *Agent {
	if d.MaxIters <= 0 {
		d.MaxIters = DefaultMaxIters
	}
	if d.MaxParallelTools <= 0 {
		d.MaxParallelTools = 4
	}
	return &Agent{d: d, WorkDir: workDir, AgentMode: true}
}

// WithAgentMode — включить/выключить инструменты.
func (a *Agent) WithAgentMode(on bool) *Agent { a.AgentMode = on; return a }

// WithSubagent — настроить агента как субагента.
func (a *Agent) WithSubagent(name string, depth int) *Agent {
	a.SubagentName = name
	a.Depth = depth
	return a
}

// WithNotes — добавить заметки в промпт.
func (a *Agent) WithNotes(notes string) *Agent { a.Notes = notes; return a }

// SetModel — сменить модель.
func (a *Agent) SetModel(m string) error {
	if a.d.ModelSwitcher != nil {
		if err := a.d.ModelSwitcher(m); err != nil {
			return err
		}
	}
	a.d.Model = m
	return nil
}

// SystemPrompt — системный промпт агента.
func (a *Agent) SystemPrompt() string {
	if a.SubagentName != "" {
		return a.subagentPrompt()
	}
	if !a.AgentMode {
		return chatPrompt
	}

	var b strings.Builder
	b.WriteString(mainPrompt)
	if a.System != "" {
		b.WriteString("\n\n" + a.System)
	}
	if a.Notes != "" {
		b.WriteString("\n\n# Заметки по текущей задаче\n" + a.Notes)
	}
	if len(a.NotesList) > 0 {
		b.WriteString("\n\n# Важные факты, зафиксированные по ходу работы\n")
		for _, n := range a.NotesList {
			b.WriteString("- " + n + "\n")
		}
	}
	b.WriteString("\n\nРабочий каталог: " + a.WorkDir)
	return b.String()
}

func (a *Agent) subagentPrompt() string { return a.System }

// BuildRequest — собрать запрос к модели.
func (a *Agent) BuildRequest() core.ChatRequest {
	creq := core.ChatRequest{
		Model:     a.d.Model,
		System:    a.SystemPrompt(),
		Messages:  a.d.Session.Messages(),
		MaxTokens: defaultMaxTokens,
		Temp:      0.4,
	}
	if a.AgentMode {
		creq.ToolDefs = a.d.Registry.Defs()
	}
	if mem := a.memoryBlock(); mem != "" {
		creq.System += "\n\n# Память проекта (GCLI.md)\n\n" + mem
	}
	// Долговременная память агента: факты, которые он сам выяснил
	// в прошлых сессиях. Идёт после проектной памяти, чтобы её
	// содержимое считалось более важным.
	if lm := tools.MemoryDigest(24); lm != "" {
		creq.System += "\n\n" + lm
	}
	if sp := a.skillsBlock(); sp != "" {
		creq.System += "\n\n" + sp
	}
	// Чем проверять этот проект. Без этой строки агент гадает команду
	// или вовсе её не запускает, и правка уходит непроверенной.
	if h := tools.VerifyHint(a.WorkDir); h != "" {
		creq.System += "\n\n# Проверка результата\n\n" + h
	}
	return creq
}

func (a *Agent) memoryBlock() string {
	if a.d.Memory == nil {
		return ""
	}
	return a.d.Memory()
}

func (a *Agent) skillsBlock() string {
	if a.d.SkillsPrompt == nil {
		return ""
	}
	return a.d.SkillsPrompt()
}

// callModel — один проход стриминга с накоплением текста и вызовов инструментов.
func (a *Agent) callModel(ctx context.Context, creq core.ChatRequest, quiet bool) (msg core.Message, err error) {
	// Защита от паники в хендлерах инструментов и парсерах.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("внутренняя ошибка при обработке ответа: %v", r)
		}
	}()

	msg = core.Message{Role: core.RoleAssistant, Sub: a.SubagentName}
	var text, reason, sig string
	acc := map[int]*core.ToolCall{}
	var order []int
	var usage core.Usage
	t0 := time.Now()
	showReason := a.d.Think != "off"

	for attempt := 1; ; attempt++ {
		var pText, pReason, pSig string
		pAcc := map[int]*core.ToolCall{}
		var pOrder []int
		gotAny := false

		rctx, rcancel := context.WithTimeout(ctx, 10*time.Minute)
		ch := make(chan core.Delta, 512)
		var streamErr error
		go func() {
			defer close(ch)
			streamErr = providers.Stream(rctx, a.d.Providers, a.d.Provider, creq, a.d.Think, ch)
		}()

		reasonOpen := false
		for d := range ch {
			if d.Reasoning != "" {
				pReason += d.Reasoning
				if !quiet && showReason && a.d.OnReason != nil {
					if !reasonOpen {
						reasonOpen = true
					}
					a.d.OnReason(d.Reasoning)
				}
			}
			if d.Sig != "" {
				pSig += d.Sig
			}
			if d.Text != "" {
				if gotAny && !reasonOpen {
					reasonOpen = false
				}
				gotAny = true
				pText += d.Text
				if !quiet && a.d.OnDelta != nil {
					a.d.OnDelta(d.Text)
				}
			}
			if d.IsTool {
				tc, ok := pAcc[d.TCIndex]
				if !ok {
					tc = &core.ToolCall{ID: d.TCID, Name: d.TCName}
					pAcc[d.TCIndex] = tc
					pOrder = append(pOrder, d.TCIndex)
					gotAny = true
				}
				if d.TCID != "" {
					tc.ID = d.TCID
				}
				if d.TCName != "" {
					tc.Name = d.TCName
				}
				tc.Args += d.TCArgs
			}
			if d.Usage != nil {
				usage.PromptTokens = core.Max(usage.PromptTokens, d.Usage.PromptTokens)
				usage.CompletionTokens = core.Max(usage.CompletionTokens, d.Usage.CompletionTokens)
			}
		}
		rcancel()

		if streamErr == nil {
			text += pText
			reason += pReason
			sig += pSig
			acc = pAcc
			order = pOrder
			break
		}

		if ctx.Err() != nil {
			text += pText
			msg.Content = text
			return msg, ctx.Err()
		}

		// Повторяем только если поток не начался.
		if providers.IsRetryable(streamErr) && !gotAny && attempt < 3 {
			select {
			case <-ctx.Done():
				return msg, ctx.Err()
			case <-time.After(time.Duration(attempt*2) * time.Second):
			}
			continue
		}

		if gotAny && pText != "" && len(pAcc) == 0 {
			text += pText
			reason += pReason
			sig += pSig
			msg.Content = text
			return msg, nil
		}
		return msg, streamErr
	}

	msg.Content = text
	msg.Reasoning = reason
	msg.ReasoningSig = sig
	for _, idx := range order {
		msg.ToolCalls = append(msg.ToolCalls, *acc[idx])
	}
	if !quiet {
		a.d.Session.AddUsage(usage)
		if a.d.OnUsage != nil {
			a.d.OnUsage(usage, time.Since(t0))
		}
	}
	return msg, nil
}

// Run — выполнить ход агента целиком.
func (a *Agent) Run(ctx context.Context, userText string) error {
	defer func() {
		if r := recover(); r != nil {
			if a.d.OnToolDone == nil {
				return
			}
		}
	}()

	a.d.Session.AddMessage(core.Message{Role: core.RoleUser, Content: userText})

	maxIters := a.d.MaxIters
	if maxIters <= 0 {
		maxIters = DefaultMaxIters
	}

	// Счётчик реально выполненных итераций (для отчётов и статистики).
	a.Turns = 0

	// Детектор петли: ловит повторяющиеся вызовы и одинаковые ошибки.
	loops := NewLoopDetector()

	for iter := 1; ; iter++ {
		if iter > maxIters {
			// Лимит исчерпан: даём агенту несколько ходов, чтобы сдать
			// результат, вместо обрыва цикла на полуслове.
			a.Turns = iter - 1
			a.autoHandoff(maxIters)
			if err := a.finalize(ctx); err != nil {
				return err
			}
			if a.OnNote != nil {
				a.OnNote("лимит итераций", fmt.Sprintf("достигнут лимит %d итераций за ход, результат собран принудительно", maxIters))
			}
			break
		}
		assistant, err := a.callModel(ctx, a.BuildRequest(), false)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if strings.TrimSpace(assistant.Content) != "" {
					assistant.Content += "\n\n_(прервано пользователем)_"
					a.d.Session.AddMessage(assistant)
				}
				return nil
			}
			return err
		}
		a.Turns++
		a.d.Session.AddMessage(assistant)

		if len(assistant.ToolCalls) == 0 || !a.AgentMode {
			break
		}

		results := a.execTools(ctx, assistant.ToolCalls)
		for _, r := range results {
			a.d.Session.AddMessage(core.Message{
				Role:       core.RoleTool,
				ToolCallID: r.tc.ID,
				Name:       r.tc.Name,
				Content:    r.text,
			})
		}
		// Петля: предупреждаем модель, если она топчется на месте.
		// Не прерываем цикл — решение остаётся за ней, но теперь у неё
		// есть факты: что именно повторяется и сколько раз.
		if warn := loops.Record(assistant.ToolCalls, results); warn != "" {
			a.d.Session.AddMessage(core.Message{Role: core.RoleUser, Content: warn})
		}
		// Зрение: изображения из результатов (screenshot, read_image)
		// прикладываются следующим сообщением пользователя — оба протокола
		// принимают картинки в user-сообщениях без исключений.
		var imgs []core.Image
		for _, r := range results {
			for _, im := range r.images {
				if len(imgs) < 4 {
					imgs = append(imgs, im)
				}
			}
		}
		if len(imgs) > 0 {
			a.d.Session.AddMessage(core.Message{
				Role:    core.RoleUser,
				Content: "[Система] К контексту приложены изображения от инструментов — рассмотри их.",
				Images:  imgs,
			})
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	a.MaybeAutoCompact()
	return nil
}

// autoHandoff — сохранить снимок состояния при обрыве хода по лимиту итераций.
//
// Делается машинно, а не по просьбе модели, потому что ровно в этот момент
// модель уже не способна оценить, что важно: ход обрывается на середине
// работы. Снимок кладётся и в историю (следующий ход читает его как
// пользовательское сообщение), и на диск (переживает сжатие контекста).
// Ошибка записи не должна ломать ход: снимок — страховка, а не условие работы.
func (a *Agent) autoHandoff(maxIters int) {
	if a.d.Registry == nil || !a.AgentMode {
		return
	}
	txt, err := a.d.Registry.AutoHandoff(fmt.Sprintf("ход оборвался на лимите итераций (%d)", maxIters))
	if err != nil {
		if a.OnNote != nil {
			a.OnNote("handoff", "не удалось сохранить снимок состояния: "+err.Error())
		}
		return
	}
	a.d.Session.AddMessage(core.Message{
		Role:    core.RoleUser,
		Content: "[Система] Ход оборвался на лимите итераций. Ниже — автоматический снимок состояния.\n\n" + txt,
	})
	if a.OnNote != nil {
		a.OnNote("handoff", "сохранён снимок состояния: следующий ход продолжит с него, а не с нуля")
	}
}

// finalize — принудительно собрать результат после исчерпания лимита итераций.
//
// Без этого шага последнее сообщение ассистента (обычно нарратив в середине
// работы: «сейчас ещё посмотрю…») уходило наверх как результат. Здесь в промпт
// добавляется явное требование сдать отчёт, а число ходов ограничивается,
// чтобы принудительный сбор не превращался в новый цикл работы.
func (a *Agent) finalize(ctx context.Context) error {
	if !a.AgentMode {
		return nil
	}
	a.d.Session.AddMessage(core.Message{
		Role:    core.RoleUser,
		Content: "Лимит итераций исчерпан. Больше инструменты вызывать нельзя. Напиши ИТОГОВЫЙ ОТЧЁТ о том, что сделано, по-русски и по делу.",
	})

	// Инструменты физически отключаются на время финальных ходов.
	// Иначе модель продолжает исследование, а в истории остаются
	// assistant-сообщения с вызовами инструментов без ответов на них —
	// такой запрос API отвергает, и цикл зависает до таймаута.
	prevSys, prevReg, prevMode := a.System, a.d.Registry, a.AgentMode
	a.System = prevSys + finalizePrompt
	a.d.Registry = emptyRegistry(prevReg)
	a.AgentMode = false
	defer func() {
		a.System, a.d.Registry, a.AgentMode = prevSys, prevReg, prevMode
	}()

	for i := 0; i < finalizeTurns; i++ {
		assistant, err := a.callModel(ctx, a.BuildRequest(), false)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if strings.TrimSpace(assistant.Content) != "" {
					a.d.Session.AddMessage(assistant)
				}
				return nil
			}
			return err
		}
		a.Turns++
		// Хвост вызовов в отчёт не тащим.
		assistant.ToolCalls = nil
		a.d.Session.AddMessage(assistant)
		if strings.TrimSpace(assistant.Content) != "" {
			return nil
		}
	}
	return nil
}

// emptyRegistry — реестр без инструментов для финальных ходов.
func emptyRegistry(base *tools.Registry) *tools.Registry {
	return tools.New(tools.Env{WorkDir: base.WorkDir(), ReadFiles: map[string]bool{}})
}

// toolResult — результат одного вызова инструмента.
type toolResult struct {
	tc     core.ToolCall
	text   string
	images []core.Image
}

// execTools — выполнить вызовы инструментов (параллельно, если безопасно).
func (a *Agent) execTools(ctx context.Context, calls []core.ToolCall) []toolResult {
	out := make([]toolResult, 0, len(calls))

	// Разделяем на последовательные и параллельные.
	//
	// spawn_agent параллелится всегда, независимо от флага ParallelTools:
	// именно это делает делегирование нескольких независимых подзадач
	// быстрым, а пул всё равно ограничивает число одновременных запусков.
	var sequential []core.ToolCall
	var parallel []core.ToolCall
	for _, tc := range calls {
		if a.canRunParallel(tc.Name) && (a.d.Parallel || tc.Name == "spawn_agent") {
			parallel = append(parallel, tc)
		} else {
			sequential = append(sequential, tc)
		}
	}

	if len(parallel) > 0 {
		limit := core.Clamp(a.d.MaxParallelTools, 1, 8)
		sem := make(chan struct{}, limit)
		done := make(chan toolResult, len(parallel))
		for _, tc := range parallel {
			tc := tc
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				done <- a.execOne(ctx, tc)
			}()
		}
		got := map[string]string{}
		order := make([]string, 0, len(parallel))
		for range parallel {
			r := <-done
			got[r.tc.ID] = r.text
			order = append(order, r.tc.ID)
		}
		for _, tc := range parallel {
			out = append(out, toolResult{tc: tc, text: got[tc.ID]})
		}
	} else {
		for _, tc := range parallel {
			out = append(out, a.execOne(ctx, tc))
		}
	}

	for _, tc := range sequential {
		if ctx.Err() != nil {
			break
		}
		out = append(out, a.execOne(ctx, tc))
	}
	return out
}

// canRunParallel — инструмент безопасно выполнять параллельно.
func (a *Agent) canRunParallel(name string) bool {
	switch name {
	case "read_file", "list_dir", "glob", "grep", "web_search", "web_fetch", "think":
		return true
	case "todo_write", "task_note":
		// Порядок важен: план и заметки должны попасть в контекст по порядку.
		return false
	case "spawn_agent":
		// Делегирование независимых подзадач — главный сценарий параллельности.
		// Пуло само ограничивает число одновременных запусков (SubMaxPar),
		// а каждый запуск изолирован своим контекстом. Порядок результатов
		// сохраняется при объединении (см. execTools).
		return true
	}
	// Инструменты расширений: параллелим только HTTP (быстрые и идемпотентные).
	t := a.d.Registry.Get(name)
	if t != nil && t.ExtName != "" && t.Category == "ext" {
		return true
	}
	return false
}

// execOne — выполнить один инструмент с защитой от паники.
func (a *Agent) execOne(ctx context.Context, tc core.ToolCall) (res toolResult) {
	res.tc = tc
	t0 := time.Now()

	defer func() {
		if r := recover(); r != nil {
			res.text = fmt.Sprintf("Ошибка: инструмент %s завершился с ошибкой: %v", tc.Name, r)
			if a.d.OnToolDone != nil {
				a.d.OnToolDone(tc, nil, tools.Result{}, fmt.Errorf("panic: %v", r), time.Since(t0))
			}
		}
	}()

	tool := a.d.Registry.Get(tc.Name)
	if tool == nil {
		res.text = "Ошибка: неизвестный инструмент " + tc.Name
		return
	}
	if ctx.Err() != nil {
		res.text = "прервано"
		return
	}

	if a.d.OnToolStart != nil {
		a.d.OnToolStart(tc, tool)
	}

	args := tools.ParseArgs(tc.Args)
	out, err := tool.Handler(ctx, args)
	elapsed := time.Since(t0)

	text := out.Text
	if out.Error != "" {
		text = "Предупреждение: " + out.Error
	}
	if err != nil {
		text = "Ошибка: " + err.Error()
	}
	if text == "" {
		text = "(инструмент не вернул результат)"
	}
	text = a.clampResult(text)

	res.text = text
	res.images = out.Images
	if a.d.OnToolDone != nil {
		a.d.OnToolDone(tc, tool, out, err, elapsed)
	}
	return
}

// clampResult — обрезать слишком большой результат, сохранив начало и конец.
func (a *Agent) clampResult(s string) string {
	if len([]rune(s)) <= maxToolResult {
		return s
	}
	head := maxToolResult / 2
	tail := maxToolResult / 2
	r := []rune(s)
	dropped := len(r) - head - tail
	return string(r[:head]) +
		fmt.Sprintf("\n\n…[результат обрезан: пропущено %d символов]…\n\n", dropped) +
		string(r[len(r)-tail:])
}

// MaybeAutoCompact — сжать контекст, если он близок к порогу.
func (a *Agent) MaybeAutoCompact() {
	if a.d.AutoCompact <= 0 {
		return
	}
	// Считаем полный контекст: системный промпт и схемы инструментов тоже
	// давят на окно, хоть и не меняются от хода к ходу.
	var defs []tools.ToolDef
	if a.AgentMode && a.d.Registry != nil {
		defs = a.d.Registry.Defs()
	}
	est := core.FullContext(a.SystemPrompt(), defs, a.d.Session.Messages())
	if est < a.d.AutoCompact || len(a.d.Session.Messages()) < 8 {
		return
	}
	a.Compact(true)
}

// Compact — сжать историю диалога в выжимку.
func (a *Agent) Compact(auto bool) bool {
	msgs := a.d.Session.Messages()
	if len(msgs) < 2 {
		return false
	}
	creq := core.ChatRequest{
		Model:     a.d.Model,
		MaxTokens: 4096,
		Temp:      0.2,
		System: "Ты — ассистент, сжимающий историю диалога. Составь краткую выжимку для продолжения работы: " +
			"задача пользователя, ключевые факты, что сделано (файлы, команды, изменения), что осталось, важные выводы субагентов. " +
			"Только выжимка, без вступлений.",
		Messages: msgs,
	}
	summary, err := a.callModel(context.Background(), creq, true)
	if err != nil || strings.TrimSpace(summary.Content) == "" {
		return false
	}
	a.replaceMessages([]core.Message{{
		Role: core.RoleUser,
		Content: "[Система] Предыдущая часть диалога сжата в выжимку (сохрани её как контекст):\n\n" +
			summary.Content,
	}})
	_ = auto
	return true
}

// replaceMessages — заменить историю (используется при сжатии).
func (a *Agent) replaceMessages(msgs []core.Message) {
	if rs, ok := a.d.Session.(interface{ ReplaceMessages([]core.Message) }); ok {
		rs.ReplaceMessages(msgs)
		return
	}
	// Запасной путь: добавляем выжимку как первое сообщение.
	a.d.Session.AddMessage(core.Message{
		Role:    core.RoleUser,
		Content: "[Система] История сжата:\n\n" + core.OneLine(msgs[0].Content),
	})
}
