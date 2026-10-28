package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"gcli/core"
	"gcli/tools"
	"gcli/tui"
	"gcli/ui"
)

// ---------- Интерфейс сессии для агента ----------

// sessMu — сериализует доступ к состоянию сессии.
//
// Субагенты могут работать параллельно (spawn_agent выполняется конкурентно),
// и каждый из них пишет в сессию: токены, журнал запусков, сохранение на диск.
// Без блокировки это гонка данных.
var sessMu sync.Mutex

// Messages — история диалога.
//
// Отдаёт КОПИЮ под sessMu, а не сам слайц. Снаружи сессии историю читают из
// чужих горутин — /v1/history в HTTP-обработчике, агент при сборке запроса к
// модели, — и это идёт параллельно с AddMessage. Возврат живого слайца
// означал гонку чтения-записи на нём же: добавление элемента переписывает
// заголовок, и читатель видел половину старой и половину новой истории.
// Тест TestAddMessageConcurrentNoRace ловит это под -race.
func (a *app) Messages() []core.Message {
	sessMu.Lock()
	defer sessMu.Unlock()
	return append([]core.Message(nil), a.sess.Messages...)
}

// AddMessage — добавить сообщение в историю.
//
// Под sessMu: /v1/history и /v1/status читают Messages из HTTP-горутин,
// пока ход добавляет сообщения, — без блокировки это гонка чтения-записи
// на слайсе (второй клиент или вкладка, история в момент хода).
func (a *app) AddMessage(m core.Message) {
	sessMu.Lock()
	a.sess.Messages = append(a.sess.Messages, m)
	sessMu.Unlock()
	// Завершаем потоковый вывод, чтобы новая реплика начиналась с чистой строки.
	if a.stream != nil {
		a.stream.End()
	}
}

// ReplaceMessages — заменить историю (используется при сжатии).
func (a *app) ReplaceMessages(msgs []core.Message) {
	sessMu.Lock()
	a.sess.Messages = msgs
	sessMu.Unlock()
}

// AddUsage — учесть расход токенов.
func (a *app) AddUsage(u core.Usage) {
	sessMu.Lock()
	a.sess.Usage.Add(u)
	sessMu.Unlock()
	a.saveSession()
}

// RecordEconomy — принять счётчики экономии от агента.
//
// Вызывается в конце хода: агент знает, сколько токенов срезала подрезка
// истории, сколько повторов взялось из кеша и сколько запросов ушло к
// модели. Счётчики копятся в сессии и переживают перезапуск.
func (a *app) RecordEconomy(e core.EconomyStats) {
	sessMu.Lock()
	a.sess.Economy.Merge(e)
	sessMu.Unlock()
	a.saveSession()
}

// AddError — учесть неудачный запрос к модели.
func (a *app) AddError() {
	sessMu.Lock()
	a.sess.Stats.RecordError()
	sessMu.Unlock()
}

// AddRequest — учесть завершённый запрос: токены, время, счётчики.
func (a *app) AddRequest(u core.Usage, d time.Duration) {
	sessMu.Lock()
	a.sess.Usage.Add(u)
	a.sess.Stats.RecordRequest(u, d)
	sessMu.Unlock()
	a.saveSession()
}

// Turns — число ходов.
func (a *app) Turns() int { return a.sess.Turns }

// sessionUsage — снимок счётчиков токенов под sessMu.
//
// Пишут ход и горутины субагентов (AddUsage, AddRequest), а читают панели
// /status и /cost из очереди ввода, пока cron-ход идёт в фоне. Без снимка
// это гонка на числах.
func (a *app) sessionUsage() core.Usage {
	sessMu.Lock()
	defer sessMu.Unlock()
	return a.sess.Usage
}

// sessionEconomy — глубокий снимок счётчиков экономии под sessMu.
//
// Копия обязана быть глубокой: ToolCalls и ToolTokens — карты, RecordEconomy
// и AddCall пишут в них из горутин субагентов, а /cost зовёт TopTool,
// который идёт по карте. Мелкая копия делит карту с писателем — итерация
// против записи падает фатально («concurrent map iteration and map write»),
// а не просто ловится гонкой.
func (a *app) sessionEconomy() core.EconomyStats {
	sessMu.Lock()
	defer sessMu.Unlock()
	e := a.sess.Economy
	if n := len(e.ToolCalls); n > 0 {
		tc := make(map[string]int, n)
		for k, v := range e.ToolCalls {
			tc[k] = v
		}
		e.ToolCalls = tc
	}
	if n := len(e.ToolTokens); n > 0 {
		tt := make(map[string]int, n)
		for k, v := range e.ToolTokens {
			tt[k] = v
		}
		e.ToolTokens = tt
	}
	return e
}

// sessionCounters — ID, число чекпоинтов и ходов одним снимком под sessMu.
//
// Чекпоинты дописывает ход (снапшоты миссий), а /status и /cost читают
// числа из очереди ввода. Длину живого слайса без блокировки читать
// нельзя — append пишет заголовок.
func (a *app) sessionCounters() (id string, checkpoints, turns int) {
	sessMu.Lock()
	defer sessMu.Unlock()
	return a.sess.ID, len(a.sess.Checkpoints), a.sess.Turns
}

// subagentRunCount — число запусков субагентов под sessMu: журнал
// пополняют горутины субагентов (recordSubagent), /status читает из
// очереди ввода.
func (a *app) subagentRunCount() int {
	sessMu.Lock()
	defer sessMu.Unlock()
	return len(a.sess.SubagentRuns)
}

// ---------- Подтверждения ----------

// permDecision — что говорят правила разрешений об этом действии.
//
// Порядок именно такой: правило решает раньше, чем флаги сессии. Иначе
// «разрешил всё для файлов» перекрыло бы deny из gcli.json проекта —
// то есть неявное «да» из прошлого разговора отменило бы явное «нет»
// из файла, который человек отредактировал сам. Здесь наоборот: флаги
// сессии помогают, когда правил нет вовсе.
func (a *app) permDecision(req tools.ConfirmReq) (core.Permission, core.Rule, bool) {
	group := core.ToolsForPermission(string(req.Kind))
	subject := req.Detail
	if req.Kind == tools.ConfirmWrite && req.Path != "" {
		subject = core.PatternForPath(a.workDir, req.Path)
	}
	// Решение по группе целиком: внутри неё действует «последнее совпавшее
	// правило выигрывает», а правила без имени инструмента проверяются
	// только в самом конце — см. core.Rules.DecideAny.
	return a.rules.DecideAny(group, subject)
}

// confirm — единая точка подтверждений пользователя.
func (a *app) confirm(req tools.ConfirmReq) bool {
	// Правила конфигов — выше всего остального, включая автопилот.
	// Автопилот ведь тоже про разрешение «на этот раз», и он не должен
	// перебивать явный запрет из файла.
	if mode, rule, ok := a.permDecision(req); ok {
		switch mode {
		case core.PermDeny:
			a.announceDeny(req, rule)
			return false
		case core.PermAllow:
			// Правило явно разрешило — вопрос не задаётся. Раньше здесь
			// был «if AutopilotAll { return true }; return true»: обе
			// ветки возвращали true, и if только притворялся, что
			// автопилот тут чем-то управляет.
			return true
		}
	}

	// В машинном режиме подтверждаем только явно безопасные операции.
	if a.quiet {
		return a.quietConfirm(req)
	}

	// Автопилот: безопасные операции одобряются самим агентом, вопросы
	// пользователю задаются только для опасных команд и сетевых запросов.
	if a.sess.Perms.Autopilot {
		if a.autoApprove(req) {
			a.announceAuto(req)
			return true
		}
	}

	switch req.Kind {
	case tools.ConfirmWrite:
		if a.sess.Perms.FileWrite {
			return true
		}
		if ch, ok := a.tuiConfirm(tui.Confirm{
			Title:       "Изменить файл?",
			Path:        a.relPath(req.Path),
			Note:        req.Reason,
			Old:         req.Old,
			New:         req.New,
			AllowAlways: true,
		}); ok {
			switch ch {
			case tui.ChoiceYes:
				return true
			case tui.ChoiceAlways:
				a.sess.Perms.FileWrite = true
				a.saveSession()
				return true
			}
			return false
		}
		a.showWritePreview(req)
		a.ui.Prompt2("Разрешить изменение файла?", "[y]да [n]нет [a]всегда для файлов: ")
		switch a.readAns(true, false) {
		case confirmYes:
			return true
		case confirmAlways:
			a.sess.Perms.FileWrite = true
			a.saveSession()
			return true
		}
		return false

	case tools.ConfirmExec:
		danger := tools.IsDangerous(req.Detail)
		allowed := (a.sess.Perms.BashAll && !danger) || (a.sess.Perms.BashExact[req.Detail] && !danger)
		if allowed {
			return true
		}
		if ch, ok := a.tuiConfirm(tui.Confirm{
			Title:       "Выполнить команду?",
			Detail:      req.Detail,
			Note:        req.Reason,
			Danger:      danger,
			AllowAlways: !danger,
			AllowAll:    !danger,
		}); ok {
			switch ch {
			case tui.ChoiceYes:
				return true
			case tui.ChoiceAlways:
				a.sess.Perms.BashExact[req.Detail] = true
				a.saveSession()
				return true
			case tui.ChoiceAll:
				a.sess.Perms.BashAll = true
				a.saveSession()
				return true
			}
			return false
		}
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Yellow("$ ") + req.Detail)
		if req.Reason != "" {
			a.ui.Println("  " + a.ui.Gray(req.Reason))
		}
		if danger {
			a.ui.Println("  " + a.ui.Red("⚠ потенциально опасная команда"))
			a.ui.Prompt2("Разрешить выполнение?", "[y]да [n]нет: ")
			return a.readAns(false, false) == confirmYes
		}
		a.ui.Prompt2("Разрешить выполнение?", "[y]да [n]нет [a]всегда эта команда [A]все: ")
		switch a.readAns(true, true) {
		case confirmYes:
			return true
		case confirmAlways:
			a.sess.Perms.BashExact[req.Detail] = true
			a.saveSession()
			return true
		case confirmAll:
			a.sess.Perms.BashAll = true
			a.saveSession()
			return true
		}
		return false

	case tools.ConfirmNet:
		if a.sess.Perms.WebFetch {
			return true
		}
		if ch, ok := a.tuiConfirm(tui.Confirm{
			Title:       "Сетевой запрос?",
			Detail:      core.Truncate(req.Detail, 120),
			Note:        req.Reason,
			AllowAlways: true,
		}); ok {
			switch ch {
			case tui.ChoiceYes:
				return true
			case tui.ChoiceAlways:
				a.sess.Perms.WebFetch = true
				a.saveSession()
				return true
			}
			return false
		}
		a.ui.Println("")
		a.ui.Println("  " + a.ui.Blue("⇩ ") + core.Truncate(req.Detail, 80))
		a.ui.Prompt2("Разрешить сетевой запрос?", "[y]да [n]нет [a]всегда: ")
		switch a.readAns(true, false) {
		case confirmYes:
			return true
		case confirmAlways:
			a.sess.Perms.WebFetch = true
			a.saveSession()
			return true
		}
		return false
	}
	return true
}

// showWritePreview — показать, что именно собирается измениться.
//
// Новый файл показываем превью: показывать его диффом бессмысленно (все
// строки новые), а полстраницы «+» в выводе только мешает увидеть
// остальное. Правку показываем диффом, но коротким: человек смотрит, что
// меняется, а не перечитывает файл.
func (a *app) showWritePreview(req tools.ConfirmReq) {
	rel := core.RelToWD(a.workDir, req.Path)
	if req.Old != "" {
		a.ui.PrintDiff(ui.Diff(req.Old, req.New), ui.DiffOpts{
			Path:     rel,
			MaxLines: 40,
		})
		return
	}
	lines := core.SplitLines(req.New)
	a.ui.Println("  " + a.ui.Gray("новый файл: "+rel+
		fmt.Sprintf(" · %d строк", len(lines))))
	// Превью — первые строки файла: их достаточно, чтобы понять, что за
	// файл собирается появиться.
	limit := 12
	for i, l := range lines {
		if i >= limit {
			a.ui.Println("    " + a.ui.Gray(fmt.Sprintf("… ещё %d строк", len(lines)-limit)))
			break
		}
		a.ui.Println("    " + a.ui.Gray(core.Truncate(core.OneLine(l), 100)))
	}
}

// announceDeny — показать, что действие запрещено правилом.
//
// Молчаливый отказ выглядит как зависший агент: он позвал инструмент,
// получил пустоту и пробует снова. Причина обязана быть видна, и
// обязана быть видна ДО вопроса — иначе человек нажмёт «да» на то, что
// запрещено, и будет искать ошибку в другом месте.
func (a *app) announceDeny(req tools.ConfirmReq, rule core.Rule) {
	if a.quiet {
		return
	}
	a.ui.Println("")
	switch req.Kind {
	case tools.ConfirmExec:
		a.ui.Println("  " + a.ui.Yellow("$ ") + req.Detail)
	case tools.ConfirmWrite:
		a.ui.Println("  " + a.ui.Yellow("✎ ") + core.RelToWD(a.workDir, req.Path))
	case tools.ConfirmNet:
		a.ui.Println("  " + a.ui.Blue("⇩ ") + core.Truncate(req.Detail, 80))
	}
	a.ui.Warn("запрещено правилом: " + rule.Label())
	a.ui.Hint("изменить правило: " + rule.Tool + "(" + rule.Pattern + ") в gcli.json или ~/.gcli/config.json")
}

// autoApprove — можно ли одобрить операцию без участия пользователя.
func (a *app) autoApprove(req tools.ConfirmReq) bool {
	if a.sess.Perms.AutopilotAll {
		return true
	}
	switch req.Kind {
	case tools.ConfirmWrite:
		return true // изменения файлов в автопилоте всегда разрешены
	case tools.ConfirmNet:
		return true // сетевые запросы (поиск/загрузка страниц) безопасны
	case tools.ConfirmExec:
		// Потенциально опасные команды требуют явного согласия.
		return !tools.IsDangerous(req.Detail)
	}
	return false
}

// announceAuto — коротко показать, что операция одобрена автопилотом.
func (a *app) announceAuto(req tools.ConfirmReq) {
	if a.quiet {
		return
	}
	detail := core.OneLine(req.Detail)
	if detail == "" {
		detail = req.Kind.String()
	}
	if len([]rune(detail)) > 72 {
		detail = string([]rune(detail)[:72]) + "…"
	}
	switch req.Kind {
	case tools.ConfirmWrite:
		a.ui.AutoOK(core.RelToWD(a.workDir, req.Path))
	case tools.ConfirmExec:
		a.ui.Println("  " + a.ui.Gray("$ ") + detail + "  " + a.ui.AutoTag())
	case tools.ConfirmNet:
		a.ui.Println("  " + a.ui.Gray("⇩ ") + detail + "  " + a.ui.AutoTag())
	}
}

// quietConfirm — авторешение в режиме -p/--json.
func (a *app) quietConfirm(req tools.ConfirmReq) bool {
	if a.sess.Perms.Autopilot {
		return a.autoApprove(req)
	}
	// BashAll разрешает все небезопасные (нежёсткие) действия: и
	// не-команды, и команды без признаков опасности. Опасная команда
	// требует явного согласия даже с BashAll — тихий режим не должен
	// молча делать rm -rf.
	if a.sess.Perms.BashAll &&
		(req.Kind != tools.ConfirmExec || !tools.IsDangerous(req.Detail)) {
		return true
	}
	if req.Kind == tools.ConfirmWrite && a.sess.Perms.FileWrite {
		return true
	}
	return false
}

// ---------- Автопилот ----------

// setAutopilot — включить/выключить автопилот (сессия + конфиг).
func (a *app) setAutopilot(on bool) {
	a.sess.Perms.Autopilot = on
	if !on {
		a.sess.Perms.AutopilotAll = false
		a.repo.Cfg.AutopilotAll = false
	}
	a.repo.Cfg.Autopilot = on
	a.saveConfig()
	a.saveSession()
}

// setAutopilotAll — усиленный режим: одобряется вообще всё, включая
// потенциально разрушительные команды. Это явный риск, поэтому режим
// всегда берёт верх над обычным автопилотом.
func (a *app) setAutopilotAll(on bool) {
	a.sess.Perms.AutopilotAll = on
	a.sess.Perms.Autopilot = on
	a.repo.Cfg.AutopilotAll = on
	a.repo.Cfg.Autopilot = on
	a.saveConfig()
	a.saveSession()
}

// autopilotWord — короткое имя режима для /status и строки статуса.
func (a *app) autopilotWord() string {
	switch {
	case a.sess.Perms.AutopilotAll:
		return "all"
	case a.sess.Perms.Autopilot:
		return "on"
	}
	return "off"
}

// Виды ответа на подтверждение.
const (
	confirmNo = iota
	confirmYes
	confirmAlways
	confirmAll
)

// readAns — прочитать ответ пользователя.
func (a *app) readAns(allowAlways, allowAll bool) int {
	if a.stdin == nil {
		// Полноэкранный интерфейс не должен доходить сюда: подтверждения
		// идут окнами (см. confirm). Отказ безопаснее паники.
		return confirmNo
	}
	for {
		if !a.stdin.Scan() {
			return confirmNo
		}
		// Сравнение с исходным регистром внутри: «A» (заглавная) — «все»,
		// «a» — «всегда». ToLower целиком делал «A» недостижимой веткой,
		// и пользователь получал более слабое «всегда» молча.
		raw := strings.TrimSpace(a.stdin.Text())
		low := strings.ToLower(raw)
		switch low {
		case "y", "д", "да", "yes", "1":
			return confirmYes
		case "n", "н", "нет", "no", "0", "":
			return confirmNo
		case "a", "а", "в", "всегда", "always":
			if raw == "A" || raw == "А" {
				// Заглавная A/А — «все», если она вообще предложена.
				if allowAll {
					return confirmAll
				}
				a.ui.Println(a.ui.Gray("    «все» недоступно; ответь y или n"))
				continue
			}
			if allowAlways {
				return confirmAlways
			}
			a.ui.Println(a.ui.Gray("    недоступно; ответь y или n"))
		case "all", "все", "всё":
			if allowAll {
				return confirmAll
			}
			a.ui.Println(a.ui.Gray("    недоступно; ответь y или n"))
		default:
			a.ui.Println(a.ui.Gray("    ответь y / n / a (или A)"))
		}
	}
}

// askUser — задать вопрос пользователю (инструмент ask_user).
func (a *app) askUser(question string, options []string) (string, error) {
	if a.quiet {
		return "", nil
	}
	// В автопилоте задача должна решаться без пользователя: если вариантов
	// немного, выбираем первый разумный, иначе просим агента решить самому.
	if a.sess.Perms.Autopilot {
		if len(options) > 0 {
			ans := options[0]
			a.ui.Info("автопилот: выбран вариант «" + ans + "» — " + core.Truncate(core.OneLine(question), 70))
			return ans, nil
		}
		a.ui.Info("автопилот: вопрос пропущен, решай задачу самостоятельно — " +
			core.Truncate(core.OneLine(question), 70))
		return "[автопилот: реши самостоятельно]", nil
	}
	// Полноэкранный интерфейс спрашивает окном: старые Prompt2/Scan
	// писали бы мимо ленты, а stdin в этом режиме нет.
	if ans, ok := a.tuiAskUser(question, options); ok {
		return strings.TrimSpace(ans), nil
	} else if t := a.tuiApp(); t != nil && t.Running() {
		return "", nil
	}
	a.ui.Println("")
	a.ui.Println("  " + a.ui.Cyan("? ") + question)
	if len(options) > 0 {
		a.ui.Println("  " + a.ui.Gray("варианты: "+strings.Join(options, " | ")))
	}
	a.ui.Prompt2("Ваш ответ", "> ")
	if a.stdin == nil || !a.stdin.Scan() {
		return "", nil
	}
	return strings.TrimSpace(a.stdin.Text()), nil
}

// ---------- Чекпоинты ----------

// recordCheckpoint — сохранить копию файла перед изменением.
func (a *app) recordCheckpoint(path, label string) {
	a.repo.RecordCheckpoint(a.sess, path, label)
}

// Проверка id сессии живёт в core.ValidSessionID: она была продублирована
// здесь дословно, а security-функция в двух экземплярах — уязвимость с
// отсрочкой (правишь одну — вторая молча остаётся дырявой).
