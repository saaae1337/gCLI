package tools

import (
	"context"
	"fmt"
	"strings"

	"gcli/core"
	"gcli/subagents"
)

// registerSubagentTools — инструменты делегирования (доступны в агентном режиме).
func (r *Registry) registerSubagentTools() {
	r.registerBound("spawn_agent", "Запустить субагента — отдельного ИИ-агента с собственным контекстом и (возможно) другой моделью. "+
		"Указывай task — этого достаточно, роль подберётся сама по тексту задачи (исправь → coder, тест → tester, найди баги → reviewer, план → planner, вёрстка → frontend, найди в интернете → researcher). "+
		"Переопредели через type, если роль очевидна иначе: explorer (карта кода), reviewer (баги/безопасность), planner (план), coder (реализация), "+
		"tester (тесты), frontend (верстка с проверкой по скриншотам), researcher (веб/доки), docs (документация), general (универсал). "+
		"Также работают имена твоих агентов из .gcli/agents/*.md. Используй для независимых подзадач; субагент вернёт отчёт — дождись и используй его.",
		schemaSpawn, "agent", false, func(r *Registry) Handler { return r.hSpawnAgent })
	r.registerBound("agent_status", "Сводка по субагентам: status — что сейчас работает, list — все запуски сессии, result — итог по имени.",
		schemaAgents, "agent", false, func(r *Registry) Handler { return r.hAgentStatus })
	r.registerBound("spawn_agents", "Запустить пачку субагентов параллельно — независимые подзадачи одним вызовом вместо N последовательных. "+
		"Каждому передай task (что сделать и что вернуть) и по возможности name. Порядок отчётов совпадает с порядком в agents. "+
		"Если задачи связаны (план → реализация → тесты, ревью после правок), укажи depends_on: имя или «#N» субагента, чей вывод нужен ДО старта — "+
		"его вывод подставится в task, а зависимые запустятся позже. Нужны несколько выводов — depends_on принимает список имён. "+
		"Без depends_on всё идёт одной волной параллельно.",
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
	if strings.TrimSpace(args.Task) == "" {
		return Result{}, fmt.Errorf("укажи task — что именно должен сделать субагент")
	}

	res, err := r.env.Spawn(ctx, args)
	if err != nil {
		return Result{}, err
	}
	summary := res.Summary
	if summary == "" {
		summary = coreTruncate(coreOneLine(res.Full), 800)
	}
	// Тип мог не совпасть с запрошенным: автовыбор роли в spawnAgent подставляет
	// специализацию по тексту задачи, и модель должна знать, кем реально работал
	// субагент. Иначе отчёт придёт без своего заголовка и припишется не туда.
	typeNote := ""
	if res.Dispatched {
		typeNote = fmt.Sprintf("\n(тип выбран автоматически по тексту задачи: %s)", res.Type)
	}
	// Отчёт мог достаться из кеша повторов или из уже идущей точно такой же
	// работы. Сообщаем об этом прямо и с именем источника: главный агент иначе
	// решит, что подзадача отработана заново, и станет опираться на выводы,
	// полученные до последних правок в коде.
	if res.Reuse != "" {
		note := "\n(отчёт переиспользован, новый запуск НЕ выполнялся"
		if res.ReusedFrom != "" {
			note += ": взят у субагента " + res.ReusedFrom
		}
		note += ")"
		typeNote += note
	}
	// Подсказываем, где взять полный отчёт: раньше модель получала только
	// сводку и не знала, что полный текст доступен через agent_status.
	return Result{
		Text: fmt.Sprintf("Субагент %s (%s) завершил работу.%s\n\nИтог:\n%s\n"+
			"\nПолный отчёт, если нужны детали: agent_status action=result name=%s",
			res.Name, res.Type, typeNote, summary, res.Name),
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

	// Собираем узлы пачки и параллельно строим план зависимостей. Порядок
	// specs навсегда остаётся порядком аргументов: отчёты сопоставляются
	// моделью по номеру, и перестановка сделала бы вывод бессмысленным.
	specs := make([]spawnSpec, 0, len(raw))
	names := make([]string, 0, len(raw))
	depsRaw := make([][]string, 0, len(raw))
	for i, v := range raw {
		am, ok := v.(map[string]any)
		if !ok {
			return Result{}, fmt.Errorf("agents[%d]: ожидался объект {type, task, name, depends_on}", i)
		}
		typ := strings.ToLower(ArgStr(am, "type"))
		task := ArgStr(am, "task")
		if strings.TrimSpace(task) == "" {
			return Result{}, fmt.Errorf("agents[%d]: нужна задача (task)", i)
		}
		name := ArgStr(am, "name")
		// depends_on — имена или номера («#2») субагентов этой же пачки, чьи
		// выводы нужны до старта этого. Список разрешён: две независимые ссылки
		// не «зависают» в первой волне, как это кажется на первый взгляд, —
		// узел попадает в волну, где разрешён уже ВЕСЬ его набор предшественников,
		// то есть строго позже обоих. Реальный пример: ревью, которому нужны и
		// карта кода, и список правок, — это один узел с двумя входами, а не две
		// копии ревью, спорящие за один и тот же вывод.
		//
		// Строка принимается наравне со списком: модели гораздо чаще пишут
		// depends_on: "plan", чем depends_on: ["plan"], и отклонять это значит
		// гонять её по кругу с ошибкой «одна ссылка».
		deps := ArgStrSlice(am, "depends_on")
		if one := ArgStr(am, "depends_on"); one != "" {
			deps = append([]string{one}, deps...)
		}
		// Пустой type НЕ превращается в "explorer" здесь: автовыбор роли живёт
		// в app.spawnAgent и смотрит на текст задачи. Подстановка дефолта в
		// этом месте тихо перебивала бы его — и в пакетном режиме все шесть
		// субагентов уходили бы в роль только для чтения.
		label := name
		if label == "" {
			label = typ
			if label == "" {
				label = "auto"
			}
			label += fmt.Sprintf("#%d", i+1)
		}
		// Адрес узла — всегда name, а для безымянного — номер. Подпись label
		// в адрес не годится: «explorer#3» и «auto#3» — один и тот же узел с
		// разной ролью, и ссылка на первый не нашла бы второй.
		key := name
		if key == "" {
			key = fmt.Sprintf("#%d", i+1)
		}
		args := SpawnArgs{
			Type:     typ,
			Task:     task,
			Name:     name,
			Model:    ArgStr(am, "model"),
			ReadOnly: ArgBool(am, "read_only"),
			Depth:    r.env.Depth + 1,
		}
		specs = append(specs, spawnSpec{
			label: label,
			run: func(ctx context.Context, depsText string) (Result, error) {
				taskspec := args.Task
				if depsText != "" {
					taskspec += "\n\n" + depsText
				}
				res, err := r.env.Spawn(ctx, SpawnArgs{
					Type: args.Type, Task: taskspec, Name: args.Name,
					Model: args.Model, ReadOnly: args.ReadOnly, Depth: args.Depth,
				})
				if err != nil {
					return Result{}, err
				}
				body := res.Summary
				if body == "" {
					body = coreTruncate(coreOneLine(res.Full), 800)
				}
				// Тип показываем фактический: при автовыборе он не совпадает
				// с запрошенным (или вовсе не был запрошен).
				shown := res.Type
				if shown == "" {
					shown = args.Type
				}
				// Пометка о переиспользовании обязательна и здесь: в пачке
				// дедупликация срабатывает чаще всего (соседние агенты просят
				// одно и то же), и молчаливый отчёт из кеша выглядел бы как
				// отдельная полноценная работа.
				if res.Reuse != "" {
					from := res.ReusedFrom
					if from == "" {
						from = "другой запуск"
					}
					body += fmt.Sprintf("\n\n[Отчёт переиспользован, новый запуск не выполнялся: %s]", from)
				}
				return Result{
					Text: body,
					Summary: fmt.Sprintf("%s (%s): %s", res.Name, shown,
						coreTruncate(coreOneLine(res.Full), 90)),
				}, nil
			},
		})
		names = append(names, key)
		depsRaw = append(depsRaw, deps)
	}

	dag, err := subagents.BuildDAG(names, depsRaw)
	if err != nil {
		return Result{}, fmt.Errorf("неверные зависимости в пачке: %w", err)
	}
	// Зависимости кладём в узлы один раз, здесь: и запуск, и отчёт о пропусках
	// читают один и тот же список, а не разбирают ссылки повторно.
	for i := range specs {
		specs[i].depsIdx = dag.DepsOf(i)
	}
	if dag.WaveCount() <= 1 {
		// Ни одной зависимости — обычный параллельный запуск: без волн,
		// без подстановки входов и без платы за планирование.
		return r.runBatch(ctx, "spawn_agents", r.parallelism(m), plainTargets(specs), renderSpawnBatch)
	}
	return r.runSpawnWaves(ctx, dag, specs, r.parallelism(m))
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
