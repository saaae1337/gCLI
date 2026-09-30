package tools

import (
	"context"
	"fmt"
	"strings"

	"gcli/core"
)

// registerSubagentTools — инструменты делегирования (доступны в агентном режиме).
func (r *Registry) registerSubagentTools() {
	r.registerBound("spawn_agent", "Запустить субагента — отдельного ИИ-агента с собственным контекстом и (возможно) другой моделью. "+
		"Специализации: explorer (карта кода), reviewer (баги/безопасность), planner (план реализации), coder (реализация), "+
		"tester (тесты), frontend (верстка с проверкой по скриншотам), researcher (веб/доки), docs (документация), general (универсал). "+
		"Также работают имена твоих агентов из .gcli/agents/*.md. Используй для независимых подзадач; субагент вернёт отчёт — дождись и используй его.",
		schemaSpawn, "agent", false, func(r *Registry) Handler { return r.hSpawnAgent })
	r.registerBound("agent_status", "Сводка по субагентам: status — что сейчас работает, list — все запуски сессии, result — итог по имени.",
		schemaAgents, "agent", false, func(r *Registry) Handler { return r.hAgentStatus })
	r.registerBound("spawn_agents", "Запустить пачку субагентов параллельно — независимые подзадачи одним вызовом вместо N последовательных. "+
		"Каждому передай task (что сделать и что вернуть) и по возможности name. Порядок отчётов совпадает с порядком в agents.",
		schemaSpawnMany, "agent", false, func(r *Registry) Handler { return r.hSpawnAgents })
	r.registerBound("ask_user", "Задать вопрос пользователю, когда без его решения задачу нельзя продолжить. Используй редко — только для развилок.",
		schemaAsk, "agent", false, func(r *Registry) Handler { return r.hAskUser })
}

// RegisterSubagentTools — публично зарегистрировать инструменты субагентов.
func (r *Registry) RegisterSubagentTools() { r.registerSubagentTools() }

// hSpawnAgent — делегировать подзадачу субагенту.
func (r *Registry) hSpawnAgent(ctx context.Context, m map[string]any) (Result, error) {
	if r.env.Spawn == nil {
		return Result{Error: "субагенты отключены — включи: /agents on"}, nil
	}
	// Глубина считается от главного агента (0). Субагент запускается на 1,
	// поэтому лимит «1» означает: субагенты разрешены, но вложенные — нет.
	if r.env.Depth >= r.env.MaxDepth {
		return Result{
			Text: fmt.Sprintf("Достигнута максимальная глубина вложенности (%d) — выполни задачу самостоятельно. "+
				"Если нужен свежий взгляд или параллельная работа, скажи об этом пользователю.", r.env.MaxDepth),
			Summary: "лимит глубины",
		}, nil
	}

	args := SpawnArgs{
		Type:     strings.ToLower(ArgStr(m, "type")),
		Task:     ArgStr(m, "task"),
		Name:     ArgStr(m, "name"),
		Model:    ArgStr(m, "model"),
		ReadOnly: ArgBool(m, "read_only"),
		Depth:    r.env.Depth + 1,
	}
	if args.Type == "" {
		args.Type = "explorer"
	}
	if strings.TrimSpace(args.Task) == "" {
		return Result{}, fmt.Errorf("укажи task — что именно должен сделать субагент")
	}
	if args.Name == "" {
		args.Name = ""
	}

	res, err := r.env.Spawn(ctx, args)
	if err != nil {
		return Result{}, err
	}
	summary := res.Summary
	if summary == "" {
		summary = coreTruncate(coreOneLine(res.Full), 800)
	}
	// Подсказываем, где взять полный отчёт: раньше модель получала только
	// сводку и не знала, что полный текст доступен через agent_status.
	return Result{
		Text: fmt.Sprintf("Субагент %s (%s) завершил работу.\n\nИтог:\n%s\n"+
			"\nПолный отчёт, если нужны детали: agent_status action=result name=%s",
			res.Name, args.Type, summary, res.Name),
		Summary: fmt.Sprintf("%s: %s", res.Name, coreTruncate(coreOneLine(res.Full), 90)),
	}, nil
}

// spawnMaxAgents — максимум субагентов в одной пачке.
//
// Ограничение не про ресурсы (пул всё равно ограничен SubMaxPar), а про
// контекст: шесть отчётов по 800 символов — это уже половина окна, и модель
// начинает выбирать «самый громкий» отчёт вместо того, чтобы прочитать все.
const spawnMaxAgents = 6

// hSpawnAgents — запустить пачку субагентов.
//
// Отчёты собираются в порядке аргументов, а не в порядке завершения: модель
// сопоставляет их с поставленными задачами по номеру, и перестановка
// «случайно» сделала бы вывод бессмысленным.
func (r *Registry) hSpawnAgents(ctx context.Context, m map[string]any) (Result, error) {
	if r.env.Spawn == nil {
		return Result{Error: "субагенты отключены — включи: /agents on"}, nil
	}
	raw, _ := m["agents"].([]any)
	if len(raw) == 0 {
		return Result{}, fmt.Errorf("укажи agents — массив задач для субагентов (до %d)", spawnMaxAgents)
	}
	if len(raw) > spawnMaxAgents {
		return Result{}, fmt.Errorf("слишком много субагентов: %d, максимум %d за вызов — разбей на два вызова",
			len(raw), spawnMaxAgents)
	}
	// Глубина — та же проверка, что у одиночного запуска: пачка на предельной
	// глубине это N отказов вместо одного.
	if r.env.Depth >= r.env.MaxDepth {
		return Result{
			Text: fmt.Sprintf("Достигнута максимальная глубина вложенности (%d) — выполни задачи самостоятельно. "+
				"Если нужны параллельная работа или свежий взгляд, скажи об этом пользователю.", r.env.MaxDepth),
			Summary: "лимит глубины",
		}, nil
	}

	var specs []target
	for i, v := range raw {
		am, ok := v.(map[string]any)
		if !ok {
			return Result{}, fmt.Errorf("agents[%d]: ожидался объект {type, task, name}", i)
		}
		typ := strings.ToLower(ArgStr(am, "type"))
		if typ == "" {
			typ = "explorer"
		}
		task := ArgStr(am, "task")
		if strings.TrimSpace(task) == "" {
			return Result{}, fmt.Errorf("agents[%d]: нужна задача (task)", i)
		}
		name := ArgStr(am, "name")
		label := name
		if label == "" {
			label = typ + fmt.Sprintf("#%d", i+1)
		}
		args := SpawnArgs{
			Type:     typ,
			Task:     task,
			Name:     name,
			Model:    ArgStr(am, "model"),
			ReadOnly: ArgBool(am, "read_only"),
			Depth:    r.env.Depth + 1,
		}
		specs = append(specs, target{
			label: label,
			run: func(ctx context.Context) (Result, error) {
				res, err := r.env.Spawn(ctx, args)
				if err != nil {
					return Result{}, err
				}
				body := res.Summary
				if body == "" {
					body = coreTruncate(coreOneLine(res.Full), 800)
				}
				return Result{
					Text: body,
					Summary: fmt.Sprintf("%s (%s): %s", res.Name, args.Type,
						coreTruncate(coreOneLine(res.Full), 90)),
				}, nil
			},
		})
	}

	return r.runBatch(ctx, "spawn_agents", r.parallelism(m), specs,
		func(items []batchItem) (string, string) {
			return renderSpawnBatch(items)
		})
}

// renderSpawnBatch — собрать отчёты пачки субагентов.
func renderSpawnBatch(items []batchItem) (string, string) {
	var b strings.Builder
	ok, failed := 0, 0
	b.WriteString(fmt.Sprintf("# spawn_agents: %d субагентов\n\n", len(items)))
	for i, it := range items {
		n := i + 1
		b.WriteString(fmt.Sprintf("## [%d] %s\n", n, it.label))
		switch {
		case it.Failed():
			failed++
			fmt.Fprintf(&b, "ОШИБКА: %v\n\n", it.err)
		case strings.TrimSpace(it.warn) != "":
			failed++
			fmt.Fprintf(&b, "%s\n\n", it.warn)
		default:
			ok++
			fmt.Fprintf(&b, "%s\n\n", it.res.Text)
		}
	}
	return strings.TrimRight(b.String(), "\n"),
		fmt.Sprintf("%d готовы, %d с ошибкой", ok, failed)
}

// hAgentStatus — сводка по субагентам.
func (r *Registry) hAgentStatus(_ context.Context, m map[string]any) (Result, error) {
	if r.env.Agents == nil {
		return Result{Text: "Субагенты не запускались в этой сессии", Summary: "нет субагентов"}, nil
	}
	action := strings.ToLower(ArgStr(m, "action"))
	if action == "" {
		action = "status"
	}
	out := r.env.Agents(action, ArgStr(m, "name"))
	if strings.TrimSpace(out) == "" {
		out = "нет данных"
	}
	return Result{Text: out, Summary: "сводка субагентов"}, nil
}

// hAskUser — вопрос пользователю.
func (r *Registry) hAskUser(_ context.Context, m map[string]any) (Result, error) {
	if r.env.Ask == nil {
		return Result{Error: "интерактивный режим недоступен (работа в режиме -p) — реши задачу самостоятельно"}, nil
	}
	q := ArgStr(m, "question")
	if strings.TrimSpace(q) == "" {
		return Result{}, fmt.Errorf("укажи question")
	}
	opts := ArgStrSlice(m, "options")
	ans, err := r.env.Ask(q, opts)
	if err != nil {
		// Вопрос задан, ответа нет — след обязателен, иначе агент будет ждать
		// ответа, которого не существует, и потеряет ход.
		r.recordAsk(q, "", true)
		return Result{}, err
	}
	r.recordAsk(q, ans, false)
	return Result{
		Text:    fmt.Sprintf("Ответ пользователя: %s", ans),
		Summary: "ответ: " + coreTruncate(coreOneLine(ans), 70),
	}, nil
}

func coreOneLine(s string) string { return core.Truncate(core.OneLine(s), 400) }

func coreTruncate(s string, n int) string { return core.Truncate(s, n) }
