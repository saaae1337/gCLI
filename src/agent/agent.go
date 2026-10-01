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
	// thinkMaxTokens — потолок для ходов с размышлениями.
	//
	// Размышления тратят тот же бюджет max_tokens, что и ответ. На 8k
	// модель с thinking на сложной задаче успевает размышлять до конца и
	// не отдаёт ни слова: приходит finish_reason=length, пустой content,
	// а провайдер оборачивает это в «Provider returned an empty response».
	// Лечится бюджетом, а не ретраем того же запроса.
	thinkMaxTokens = 32768
	// retryMaxTokens — потолок для повтора, которому уже не хватило.
	retryMaxTokens = 65536
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
	// MaxItersAbs — абсолютный потолок итераций с учётом продлений
	// (0 = DefaultExtendAbs).
	MaxItersAbs int
	// ExtendMax — сколько раз за ход можно продлить (0 = DefaultExtendMax).
	ExtendMax int
	// ExtendStep — размер одного продления в итерациях (0 = дефолт).
	ExtendStep int
	// Budget — текущий расход сессии и её потолок в токенах.
	// Заполняется хостом перед каждым вызовом инструмента, потому что
	// расход меняется на каждой итерации.
	Budget func() (spent, limit int)
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
	// OnExtend — callback при выдаче продления (для UI и заметок).
	OnExtend func(granted, total int, reason string)
	// ext — состояние продлений текущего хода.
	ext *ExtendState
	// loops — детектор петли текущего хода (для проверки прогресса).
	loops *LoopDetector
}

// ExtendTurn — модель просит продолжить ход.
//
// Возвращает решение с текстом для модели. Вызывается из инструмента
// extend_turns, поэтому потокобезопасно и не имеет побочных эффектов вне
// состояния продлений.
func (a *Agent) ExtendTurn(reason string, want int) ExtendVerdict {
	// Состояние продлений живёт только внутри хода: до первого вызова Run и
	// после его завершения a.ext равен nil. Инструмент при этом доступен —
	// реестр живёт дольше хода, — поэтому nil проверяем здесь, а не
	// полагаемся на «агент не успеет позвать».
	if a.ext == nil {
		return ExtendVerdict{
			Message: "Продление работает только во время хода агента: сейчас хода нет, " +
				"продлевать нечего. Начни работу инструментами — и лимит снова можно будет увеличить.",
		}
	}
	var progress float64
	if a.loops != nil {
		progress = a.loops.Progress()
	}
	spent, limit := 0, 0
	if a.d.Budget != nil {
		spent, limit = a.d.Budget()
	}
	// Финальные ходы: инструменты уже отключены, продлевать нечего. Проверка
	// до Request: вызвать ExtendDecision, а потом отозвать результат нельзя —
	// лимит к этому моменту уже вырос. На финальные ходы инструмент физически
	// не попадает (реестр подменяется пустым), но полагаться на это — значит
	// забыть проверку однажды и получить панику на следующем же вызове.
	if !a.AgentMode {
		return ExtendVerdict{
			Total:   a.ext.Limit(),
			Message: "Продление не работает на финальных ходах: лимит итераций исчерпан, инструменты отключены. Заканчивай отчёт.",
		}
	}

	v := a.ext.Request(progress, spent, limit, reason, want)
	if v.OK && a.OnExtend != nil {
		a.OnExtend(v.Granted, v.Total, v.Reason)
	}
	return v
}

// ExtendLog — журнал решений по продлениям за текущий ход.
func (a *Agent) ExtendLog() []string {
	if a.ext == nil {
		return nil
	}
	return a.ext.Log()
}

// ExtendUsed — выдавалось ли в этом ходе хотя бы одно продление.
func (a *Agent) ExtendUsed() bool { return a.ext != nil && a.ext.Used() }

// ExtendState — состояние продлений текущего хода (nil вне хода).
func (a *Agent) ExtendState() *ExtendState { return a.ext }

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
		MaxTokens: a.maxTokens(),
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

// maxTokens — потолок ответа для текущего запроса.
//
// Размышления съедают тот же бюджет, что и ответ, поэтому для моделей с
// thinking 8k не хватает на сложной задаче: модель молча размышляет до
// конца и не отдаёт ни слова ответа. Таким моделям даём заведомо
// достаточный потолок сразу — дешевле, чем платить за проваленный ход.
func (a *Agent) maxTokens() int {
	if a.d.Think == "off" {
		return defaultMaxTokens
	}
	return thinkMaxTokens
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

// isTruncatedFinish — генерация оборвалась по лимиту токенов.
//
// OpenAI-совместимые endpoint-ы пишут «length», Anthropic — «max_tokens».
// Размышления идут в том же бюджете, что и ответ, поэтому именно этот
// finish reason означает «модель думала, но не успела сказать».
func isTruncatedFinish(finish string) bool {
	switch strings.ToLower(strings.TrimSpace(finish)) {
	case "length", "max_tokens":
		return true
	}
	return false
}

// growTokens — следующий потолок ответа после неудачного по длине.
//
// Растём быстро: обрезанный размышлениями ответ стоит полного повтора
// запроса, а 8192 → 16384 в второй раз могло бы не спасти снова. Потолок
// жёсткий, иначе модель с неограниченным thinking утащит ход в вечность.
func growTokens(cur, attempt int) int {
	next := cur * 4
	if next < thinkMaxTokens {
		next = thinkMaxTokens
	}
	if attempt >= 2 && next < retryMaxTokens {
		next = retryMaxTokens
	}
	if next > retryMaxTokens {
		next = retryMaxTokens
	}
	return next
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

	// Учёт токенов — на любом выходе, а не только на успешном.
	//
	// Раньше счётчик пополнялся в самом конце функции, то есть только когда
	// всё прошло хорошо. Прерванный по Ctrl+C запрос, обрыв сети на середине
	// ответа, 500 от провайдера — всё это стоило пользователю денег, но в
	// счётчике не появлялось: /usage врал, а оценка контекста в /context
	// занижалась, и сжатие не срабатывало вовремя.
	defer func() {
		if quiet || a.d.Session == nil {
			return
		}
		a.d.Session.AddUsage(usage)
		if a.d.OnUsage != nil {
			a.d.OnUsage(usage, time.Since(t0))
		}
	}()

	for attempt := 1; ; attempt++ {
		var pText, pReason, pSig string
		pAcc := map[int]*core.ToolCall{}
		var pOrder []int
		gotAny := false
		// finishReason — чем закончилась генерация. Уходит в диагностику
		// обрезанных ответов: по нему видно, что кончился max_tokens.
		var finishReason string

		rctx, rcancel := context.WithTimeout(ctx, 10*time.Minute)
		ch := make(chan core.Delta, 512)
		var streamErr error
		// recover обязателен именно здесь: это единственная горутина на
		// пути к провайдеру, и паника в парсере ответа (кривой SSE, битый
		// ответ вебхука) без него убивала бы весь процесс вместе с
		// сессией — вместе с историей, которая уже оплачена.
		go func() {
			defer close(ch)
			defer func() {
				if r := recover(); r != nil {
					streamErr = fmt.Errorf("паника при разборе потока провайдера: %v", r)
				}
			}()
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
			if d.Finish != "" {
				finishReason = d.Finish
			}
			if d.Usage != nil {
				usage.PromptTokens = core.Max(usage.PromptTokens, d.Usage.PromptTokens)
				usage.CompletionTokens = core.Max(usage.CompletionTokens, d.Usage.CompletionTokens)
			}
		}
		rcancel()

		if streamErr == nil {
			// Модель размышляла впустую: потратила весь max_tokens на
			// reasoning и не отдала ни слова ответа. Так выглядит «Provider
			// returned an empty response» — и лечится бюджетом, а не повтором
			// того же запроса. Пробуем ещё раз с увеличенным потолком, и
			// размышления уже показанного хода не дублируем.
			if pText == "" && len(pAcc) == 0 && pReason != "" &&
				isTruncatedFinish(finishReason) && attempt < 3 && ctx.Err() == nil {
				creq.MaxTokens = growTokens(creq.MaxTokens, attempt)
				continue
			}
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

		// Провайдер прямо сказал, что ответ пуст, — это известная картина
		// «размышления съели max_tokens». Текста всё равно нет, поэтому повтор
		// ничего не испортит: меняем бюджет и пробуем снова. Второй шанс,
		// не бесконечный.
		if errors.Is(streamErr, providers.ErrEmptyResponse) && attempt < 3 && ctx.Err() == nil {
			creq.MaxTokens = growTokens(creq.MaxTokens, attempt)
			continue
		}
		return msg, streamErr
	}

	msg.Content = text
	msg.Reasoning = reason
	msg.ReasoningSig = sig
	for _, idx := range order {
		msg.ToolCalls = append(msg.ToolCalls, *acc[idx])
	}
	return msg, nil
}

// Run — выполнить ход агента целиком.
func (a *Agent) Run(ctx context.Context, userText string) (err error) {
	// Пользовательский recover на весь ход: спокойная замена паники понятным
	// текстом. Раньше он молча проглатывал панику (тело состояло из
	// условия и пустого return), и ход выглядел как «агент молча закончил
	// работу», хотя ответ не был получен вовсе.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("внутренняя ошибка агента: %v", r)
		}
	}()

	a.d.Session.AddMessage(core.Message{Role: core.RoleUser, Content: userText})

	maxIters := a.d.MaxIters
	if maxIters <= 0 {
		maxIters = DefaultMaxIters
	}

	ext := NewExtendState(maxIters, a.d.MaxItersAbs, a.d.ExtendMax, a.d.ExtendStep)
	a.ext = ext

	// Счётчик реально выполненных итераций (для отчётов и статистики).
	a.Turns = 0

	// Детектор петли: ловит повторяющиеся вызовы и одинаковые ошибки.
	loops := NewLoopDetector()
	a.loops = loops

	for iter := 1; ; iter++ {
		limit := ext.Limit()
		if iter > limit {
			// Лимит исчерпан: даём агенту несколько ходов, чтобы сдать
			// результат, вместо обрыва цикла на полуслове.
			a.Turns = iter - 1
			a.autoHandoff(limit)
			if err := a.finalize(ctx); err != nil {
				return err
			}
			if a.OnNote != nil {
				a.OnNote("лимит итераций", fmt.Sprintf("достигнут лимит %d итераций за ход, результат собран принудительно", limit))
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
		// Близко к лимиту: говорим заранее, а не когда он уже кончился.
		//
		// Раньше модель узнавала об исчерпании по одной фразе в self_status,
		// а сам self_status надо было догадаться вызвать. К этому моменту
		// времени на нормальную работу уже нет. Сообщение одно на ход:
		// повторять его каждую итерацию — значит жечь контекст.
		if warn := ext.Reminder(ext.Limit() - iter); warn != "" {
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
	// Результат на каждый вызов, строго по его позиции в ответе модели.
	//
	// Порядок обязателен: tool-сообщения идут в историю в порядке вызовов,
	// иначе модель сопоставляет результат с чужим инструментом. Раньше
	// параллельные результаты добавлялись в порядке завершения горутин, то
	// есть случайно, — при шести read_file в multi_read это была лотерея.
	out := make([]toolResult, len(calls))
	for i, tc := range calls {
		out[i] = toolResult{tc: tc}
	}

	// Разделяем на последовательные и параллельные.
	//
	// spawn_agent параллелится всегда, независимо от флага ParallelTools:
	// именно это делает делегирование нескольких независимых подзадач
	// быстрым, а пул всё равно ограничивает число одновременных запусков.
	type slot struct {
		pos int
		tc  core.ToolCall
	}
	var sequential, parallel []slot
	for i, tc := range calls {
		s := slot{pos: i, tc: tc}
		if a.canRunParallel(tc.Name) && (a.d.Parallel || tc.Name == "spawn_agent") {
			parallel = append(parallel, s)
		} else {
			sequential = append(sequential, s)
		}
	}

	if len(parallel) > 0 {
		limit := core.Clamp(a.d.MaxParallelTools, 1, 8)
		sem := make(chan struct{}, limit)
		type res struct {
			pos int
			r   toolResult
		}
		done := make(chan res, len(parallel))
		for _, s := range parallel {
			s := s
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				done <- res{pos: s.pos, r: a.execOne(ctx, s.tc)}
			}()
		}
		// Кладём результат по номеру вызова, а не «как пришли» и не по ID.
		//
		// По ID нельзя: два вызова с одинаковым (или пустым) ID схлопывались
		// бы в один элемент map, и второй результат либо терялся, либо
		// дублировался — в истории появлялись два tool-сообщения с одним
		// tool_call_id, и провайдер отвергал следующий запрос целиком.
		//
		// Раньше здесь собиралось только text, а images терялись совсем:
		// скриншот в параллельном пакете молча исчезал, и модель получала
		// «инструмент отработал, а картинки нет».
		for range parallel {
			r := <-done
			out[r.pos] = r.r
		}
	}

	for _, s := range sequential {
		if ctx.Err() != nil {
			// Оставшиеся вызовы всё равно должны получить ответ: tool-вызов
			// без tool-сообщения ломает следующий запрос к API.
			out[s.pos].text = "Ошибка: ход прерван, инструмент не выполнен"
			continue
		}
		out[s.pos] = a.execOne(ctx, s.tc)
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
	// Сокрытие секретов — здесь, а не в каждом инструменте.
	//
	// Маскировать нужно всё, что уходит в контекст модели: результат grep
	// по «api_key», вывод `env` в CI, содержимое .env. Правило «каждый
	// инструмент помнит про секреты» не работает: инструментов тридцать, и
	// забудут все, кроме того, который написан последним. Здесь — одна точка
	// на весь результат, и забыть её уже нельзя.
	text = tools.RedactSecrets(text)
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
