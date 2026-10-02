package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gcli/agent"
	"gcli/core"
)

// ---------- Автономный прогон: загрузка задания ----------

// setupMission — прочитать mission.json и наложить флаги поверх него.
//
// Порядок именно такой: файл даёт задание целиком (оно может лежать в
// репозитории и переживать перезапуск), а флаги — разовое переопределение
// под конкретный запуск. Обратный порядок дал бы «флаги перебили файл,
// которого человек не видел», и в многочасовом прогоне это молча меняет
// бюджет на половину.
//
// Ошибка разбора mission.json — повод не запускаться, а не работать по
// умолчанию: человек собрал прогон на 8 часов, а молчаливый откат к
// обычному режиму отдал бы ему 20 минут, и он узнал бы об этом только
// по факту невыполненной работы.
func (a *app) setupMission(mode, deadline, budget, objective, path string) error {
	// 1. Файл проекта.
	if path == "" {
		var err error
		a.mission, _, err = core.LoadMission(a.workDir)
		if err != nil {
			return err
		}
	} else {
		m, err := loadMissionFile(path)
		if err != nil {
			return err
		}
		a.mission = m
	}

	// 2. Флаг -mission: либо имя режима, либо путь к своему файлу.
	if s := strings.TrimSpace(mode); s != "" {
		if m, ok := core.ValidMissionMode(s); ok {
			a.mission.Mode = m
		} else {
			m, err := loadMissionFile(s)
			if err != nil {
				return fmt.Errorf("-mission %q: это не режим и не файл задания (%v)", s, err)
			}
			a.mission = m
		}
	}

	// 3. Срок и бюджеты поверх всего.
	if s := strings.TrimSpace(deadline); s != "" {
		d, err := core.ParseDur(s)
		if err != nil {
			return fmt.Errorf("-deadline: %w", err)
		}
		a.mission.Deadline = d
	}
	if s := strings.TrimSpace(budget); s != "" {
		tok, cost, err := parseBudget(s)
		if err != nil {
			return fmt.Errorf("-budget: %w", err)
		}
		if tok > 0 {
			a.mission.TokenBudget = tok
		}
		if cost > 0 {
			a.mission.CostBudget = cost
		}
	}
	if s := strings.TrimSpace(objective); s != "" {
		a.mission.Objective = s
	}

	a.mission = a.mission.Apply()
	return nil
}

// loadMissionFile — прочитать задание из произвольного пути.
func loadMissionFile(path string) (core.Mission, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		if f, err := os.Stat(abs); err == nil && f.IsDir() {
			abs = filepath.Join(abs, ".gcli", "mission.json")
		} else {
			abs = filepath.Join(a0WorkDir(), abs)
		}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return core.Mission{}, err
	}
	if core.HasJSONC(data) {
		data = core.StripJSONC(data)
	}
	var m core.Mission
	if err := json.Unmarshal(data, &m); err != nil {
		return core.Mission{}, fmt.Errorf("%s не разбирается: %w", abs, err)
	}
	if err := m.Normalize(); err != nil {
		return core.Mission{}, err
	}
	return m.Apply(), nil
}

// a0WorkDir — рабочий каталог процесса.
func a0WorkDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// parseBudget — разобрать «500000» или «500000,25» в токены и деньги.
//
// Одна строка вместо двух флагов: человек задаёт предел одним движением
// («не больше 500k токенов или 25 долларов»), и разбирать это в два флага
// неудобно. Денежная часть опознаётся точкой, а не символом валюты:
// «25» в контексте бюджета — доллары, «$25» — тоже доллары, а «25k»
// — токены.
func parseBudget(s string) (tokens int, cost float64, err error) {
	norm := strings.TrimSpace(s)
	for _, cut := range []string{"$", " ", "\t", "usd", "USD", "долларов", "долл", "руб"} {
		norm = strings.ReplaceAll(norm, cut, "")
	}
	norm = strings.TrimSpace(norm)
	if norm == "" {
		return 0, 0, fmt.Errorf("пустой бюджет")
	}
	// Два числа через запятую или плюс: токены, доллары.
	if sep := strings.IndexAny(norm, ",+"); sep > 0 {
		if tokens, err = parseTokenNum(norm[:sep]); err != nil {
			return 0, 0, err
		}
		c, err2 := strconv.ParseFloat(norm[sep+1:], 64)
		if err2 != nil {
			return 0, 0, fmt.Errorf("денежная часть %q не понята", norm[sep+1:])
		}
		return tokens, c, nil
	}
	// Суффиксы k/M сразу означают токены: «500k» — это полмиллиона
	// токенов, а не 500 тысяч долларов.
	if hasTokenSuffix(norm) {
		n, err := parseTokenNum(norm)
		if err != nil {
			return 0, 0, err
		}
		return n, 0, nil
	}
	// Голое целое без разделителей: токены, если оно большое, иначе
	// деньги. «500000» — токены, «25» — доллары: у человека в голове
	// «25» рядом со словом «бюджет» означает двадцать пять долларов,
	// а двадцать пять токенов — это опечатка, которую он увидит сразу.
	n, err := strconv.Atoi(norm)
	if err == nil {
		if n >= 1000 {
			return n, 0, nil
		}
		return 0, float64(n), nil
	}
	// Дробное без суффикса — деньги: «25.5».
	if c, e := strconv.ParseFloat(norm, 64); e == nil {
		return 0, c, nil
	}
	if n, e := parseTokenNum(norm); e == nil {
		return n, 0, nil
	}
	return 0, 0, fmt.Errorf("бюджет %q не понят: жду 500k, 25 или 500k,25", s)
}

// hasTokenSuffix — есть ли у числа суффикс токенов.
func hasTokenSuffix(s string) bool {
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case 'k', 'K', 'm', 'M':
		return true
	}
	return false
}

// parseTokenNum — разобрать «500000», «500k», «1.5M».
func parseTokenNum(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("пустое число токенов")
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"), strings.HasSuffix(s, "K"):
		mult, s = 1e3, s[:len(s)-1]
	case strings.HasSuffix(s, "m"), strings.HasSuffix(s, "M"):
		mult, s = 1e6, s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("число токенов %q не понято", s)
	}
	return int(f * mult), nil
}

// ---------- Трекер прогона ----------

// startTracker — создать трекер прогона, если задание на это тянется.
//
// Проверка «тянется ли» та же, что и у агента: пустое задание не должно
// плодить трекер с нулевыми потолками — он бы каждую итерацию спрашивал
// «не пора ли остановиться» и получал «нет».
func (a *app) startTracker() bool {
	m := a.mission.Apply()
	if !m.Long() && !m.HasAcceptance() && !m.Budgeted() {
		a.missionTr = nil
		return false
	}
	a.missionTr = core.NewTracker(m, a.sessionSpent(), a.sessionCost())
	if a.prov != nil {
		a.missionTr.WithModel(a.prov.ID, a.model)
	}
	// Флаги сбрасываются на новый прогон: трекер, который человек
	// перезапустил через /mission start, не должен унаследовать ни
	// счётчики прошлого, ни состояние «остановка уже записана».
	a.missionJournalEnd = false
	a.missionResumedOnce = false
	a.missionResumeJournalled = false
	// Счётчики прошлого прогона — до первого тика: иначе первый же Tick
	// прибавит к ним расход нового запуска и бюджет завысится.
	if a.missionResumeState != nil {
		a.missionTr.Resume(a.missionResumeState.Iters,
			a.missionResumeState.ToolCalls, a.missionResumeState.Tokens)
	}
	a.openMissionJournal()
	return true
}

// sessionCost — расход сессии в долларах (для поправки бюджета миссии).
func (a *app) sessionCost() float64 {
	if a.prov == nil {
		return 0
	}
	price, ok := core.ModelPrice(a.prov.ID, a.model)
	if !ok {
		return 0
	}
	sessMu.Lock()
	defer sessMu.Unlock()
	return price.Cost(a.sess.Usage)
}

// missionDeps — собрать зависимости движка прогона для агента.
func (a *app) missionDeps() agent.MissionDeps {
	d := agent.MissionDeps{
		Tracker: a.missionTr,
		Spent:   func() int { return a.sessionSpent() },
		Model:   a.model,
		Checkpoint: func(summary string) error {
			return a.saveMissionState(summary)
		},
		Stopped: func(reason string) {
			if !a.quiet {
				a.ui.Warn("автономный прогон остановлен: " + core.StopReasonLabel(reason))
			}
			a.missionJournalStop(reason)
			// Удачная миссия — источник опыта: навык в .gcli/skills.
			if reason == core.StopDone {
				a.distillMissionSkill()
			}
		},
		Resumed: a.missionResume != "",
		Resume:  a.missionResume,
		Journal: a.missionJournal,
	}
	if a.prov != nil {
		d.Provider = a.prov.ID
	}
	return d
}

// ---------- Журнал прогона ----------

// openMissionJournal — открыть журнал и записать начало прогона.
//
// Ошибка открытия не валит запуск: журнал — страховка, а не условие
// работы. Человек узнает о ней один раз за подсказкой и продолжает.
func (a *app) openMissionJournal() {
	if a.missionJournal != nil {
		return
	}
	j, err := core.OpenJournal(a.workDir)
	if err != nil {
		if !a.quiet {
			a.ui.Warn("журнал прогона не открыт: " + err.Error())
		}
		return
	}
	a.missionJournal = j
	// На новом прогоне, а не на подхвате: при продолжении журнал уже начат,
	// и вторая запись «start» означала бы, что прогон начался дважды.
	if a.missionResumeState == nil {
		_ = j.Start("прогон запущен: "+a.mission.Summary(), a.missionTr)
	}
}

// closeMissionJournal — закрыть журнал на выходе из сеанса.
//
// Причина здесь — JournalClose, а не JournalStop, и разница не в
// формулировке, а в том, что будет предложено при следующем запуске:
// после «сеанс закрыт» прогон можно продолжить, после «остановлен» —
// нельзя. Обычный Ctrl+D в 22:00 не означает «работа окончена», и
// записать его как окончание прогона значило бы выбросить честную
// точку возврата после первого же выхода.
//
// Отдельная запись Stop здесь берётся не напрямую, а через
// missionJournalStop: тот гасит повтор, когда остановка уже была
// записана. Иначе сеанс, где бюджет выбрало на середине, получил бы в
// журнале две разные причины остановки — «исчерпан потолок вызовов»
// от трекера и «остановлен: исчерпан потолок вызовов» отсюда, — а
// append-only журнал такие повторы не прощает.
func (a *app) closeMissionJournal() {
	if a.missionJournal == nil {
		return
	}
	// Прогон, остановленный по потолку, закрыт как оконченный: журнал не
	// должен предлагать продолжение после исчерпанного бюджета.
	if tr := a.missionTr; tr != nil && tr.Done() {
		a.missionJournalStop(tr.Stopped())
	} else if a.missionJournal.Wrote() {
		_ = a.missionJournal.CloseRecord("сеанс закрыт, прогон не окончен: "+a.missionStatusLine(), a.missionTr)
	}
	_ = a.missionJournal.Close()
	a.missionJournal = nil
}

// missionJournalStop — записать в журнал необратимую остановку.
//
// Флаг missionJournalEnd гасит повтор: к этому моменту запись об
// остановке уже могла быть сделана (например, из missionJournalStop при
// остановке по потолку), а при выходе из сеанса closeMissionJournal
// дописал бы то же самое ещё раз. В append-only журнале два одинаковых
// «остановлен» читаются как две разные причины, а это враньё.
func (a *app) missionJournalStop(reason string) {
	if a.missionJournal == nil || a.missionJournalEnd {
		return
	}
	a.missionJournalEnd = true
	_ = a.missionJournal.Stop("остановлен: "+core.StopReasonLabel(reason), a.missionTr)
}

// noteMissionResumed — записать в журнал, что прогон подхватили.
//
// Один раз за сеанс: журнал переживает перезапуск, и второе «resume» в
// том же запуске превратило бы честную историю продолжений в «продолжали
// много раз подряд» — а на самом деле это был один-единственный
// подхват после обрыва.
//
// Флаг ставится ПОСЛЕ записи, а не до: оборванный прогон, в который не
// удалось дописать ничего (диск занят, файл открыт другим процессом),
// не должен помечаться как подхваченный — иначе запись потеряется
// навсегда, и в журнале не останется ни одной записи о том, что
// продолжать было от чего.
func (a *app) noteMissionResumed() {
	if a.missionJournal == nil || a.missionResumeJournalled {
		return
	}
	if err := a.missionJournal.Resumed("продолжен после обрыва", a.missionTr); err != nil {
		return
	}
	a.missionResumeJournalled = true
}

// missionJournalCheckpoint — записать в журнал чекпоинт.
//
// Идёт после снимка состояния, а не вместо него: чекпоинт без снимка
// бесполезен после перезапуска, но снимок без журнала не переживает
// падение на записи. Порядок «дешёвое первым» означает, что при ошибке
// вторая половина не потеряет первую.
func (a *app) missionJournalCheckpoint(summary string) {
	if a.missionJournal == nil {
		return
	}
	_ = a.missionJournal.Checkpoint(summary, a.missionTr)
}

// ---------- Продолжение после обрыва ----------

// resumeWindow — насколько свежим должен быть журнал, чтобы предложить
// продолжение. Сутки: прогон, оставленный на ночь, утром продолжается,
// а журнал позапрошлой работы — уже другая работа.
const resumeWindow = 24 * time.Hour

// loadMissionResume — подхватить снимок прошлого прогона из журнала.
//
// Молчаливый отказ здесь опаснее, чем лишняя подсказка: человек после
// обрыва должен увидеть, что работа не начинается с нуля. Ничего не
// нашлось — это норма, и тогда в статусе просто нет строки о прошлом прогоне.
func (a *app) loadMissionResume() {
	if a.mission.Objective == "" && !a.mission.Long() && !a.mission.Budgeted() {
		// Прогона в этом запуске нет — подхватывать нечего.
		return
	}
	path := core.JournalPath(a.workDir)
	st, has, cut := core.MissionResumeFromJournal(path, time.Now(), resumeWindow)
	if !has {
		return
	}
	// Обрезанный хвост — это потерянный последний чекпоинт. Молчать об
	// этом нельзя: человек продолжит прогон, думая, что знает, где встали.
	if cut && !a.quiet {
		a.ui.Warn("журнал прогона оборван — последний чекпоинт потерян, продолжаю с предыдущего")
	}
	// Счётчики прошлого прогона переносятся в трекер: иначе продолжение
	// началось бы с полного бюджета, и человек получил бы работу вдвое
	// дороже, чем собирался.
	a.missionResumeState = &st
	a.missionResume = a.resumeLine(st)
	a.startupNotes = append(a.startupNotes,
		"Прошлый прогон прерван: "+core.OneLine(st.Summary))
}

// resumeLine — строка продолжения для истории агента.
func (a *app) resumeLine(st core.MissionState) string {
	var b strings.Builder
	b.WriteString("[Система] Продолжаем автономный прогон, прерванный ")
	b.WriteString(st.Updated)
	b.WriteString(".\nНа момент последнего сохранения:\n")
	b.WriteString(st.Summary)
	if st.Iters > 0 {
		fmt.Fprintf(&b, "\nСделано итераций: %d, вызовов инструментов: %d, токенов: %d.",
			st.Iters, st.ToolCalls, st.Tokens)
	}
	b.WriteString("\nНе начинай заново: сначала посмотри на состояние, потом продолжай с него.")
	return b.String()
}

// attachMission — начать (или продолжить) прогон на текущем агенте.
//
// Живёт отдельно от newAgent, потому что агент создаётся на каждый ход,
// а подхват прогона случается ровно один раз за сеанс: иначе запись
// «resume» дописывалась бы в журнал на каждом ходе, и история
// продолжений превратилась бы в «продолжали двадцать раз».
func (a *app) attachMission(ag *agent.Agent) {
	if ag == nil {
		return
	}
	// Трекер, созданный на старте сеанса или командой /mission start,
	// переиспользуется как есть. Создавать его заново нельзя: счётчики
	// итераций, вызовов и токенов живут в трекере, и новый трекер
	// обнулил бы прогон на каждом ходу — потолок в 200 итераций не был
	// бы достигнут никогда.
	if a.missionTr == nil {
		ag.StartMission(core.Mission{Mode: core.MissionNormal}, agent.MissionDeps{})
		return
	}
	// Приглашение к продолжению отдаётся ровно один раз за сеанс, и
	// именно в том ходе, где оно и нужно: обычный вопрос вроде «а что
	// было вчера» не должен начинаться с «продолжаем с прошлого места».
	//
	// Отметка ставится здесь, а не в движке: движок вставляет приглашение
	// лениво, уже во время работы, и если бы он решал это сам, то решал
	// бы по своим правилам каждый ход заново. Журнал об этом не знает
	// ничего — у него своя отметка, noteMissionResumed.
	deps := a.missionDeps()
	resumed := a.missionResume != "" && !a.missionResumedOnce
	a.missionResumedOnce = a.missionResumedOnce || resumed
	a.noteMissionResumed()
	if !resumed {
		// Строка уходит в историю один раз; держать её дальше незачем —
		// иначе первый же следующий ход начался бы с неё.
		deps.Resume = ""
	}
	// Снимок отпускается здесь, а не внутри агента: он принадлежит
	// сеансу хоста, и после передачи в историю его больше нечего помнить.
	// Пока строка жила в a.missionResume, любой следующий сбор
	// зависимостей тащил её обратно — и «продолжаем с прошлого места»
	// вернулось бы в сеанс, в котором никакого обрыва не было.
	a.missionResume = ""
	ag.StartMission(a.mission, deps)
	a.missionNote = a.mission.Summary()
}

// saveMissionState — записать состояние прогона на диск.
//
// Два файла: снимок состояния (что модель должна знать при продолжении)
// и сама миссия со счётчиками (чтобы /mission status после перезапуска
// показал, где прогон остановился). Ошибка возвращается наружу: вызов��щий
// решает, кричать ли об этом в модель.
func (a *app) saveMissionState(summary string) error {
	if a.tools == nil {
		return nil
	}
	if _, err := a.tools.AutoHandoff(summary); err != nil {
		return err
	}
	st := core.MissionState{
		Objective: a.mission.Objective,
		Mode:      string(a.mission.Mode),
		Summary:   summary,
		Updated:   time.Now().Format(time.RFC3339),
		Status:    a.missionStatusLine(),
	}
	if tr := a.missionTr; tr != nil {
		st.Iters = tr.Iters()
		st.ToolCalls = tr.ToolCalls()
		st.Tokens = tr.Spent()
		st.StopReason = tr.Stopped()
	}
	return core.SaveMissionState(core.MissionStatePath(a.workDir), st)
}

// missionStatusLine — строка «где мы» для снимка и статуса.
func (a *app) missionStatusLine() string {
	if a.missionTr == nil {
		return "прогон не запущен"
	}
	return a.missionTr.Status()
}

// ---------- /mission: команда ----------

// cmdMission — управление автономным прогоном.
//
// Подкоманды: start (запустить), stop (остановить), status (где мы),
// save (сохранить состояние сейчас), help.
//
// start принимает всё, что принимает -mission, плюс срок и бюджет:
// «/mission start long-time 2h 500k». Разбор тот же, что у флагов, чтобы
// правило «что можно задать» было одно на оба входа.
func (a *app) cmdMission(rest string) {
	rest = strings.TrimSpace(rest)
	switch {
	case rest == "" || rest == "status":
		a.missionStatus()
	case rest == "stop":
		a.missionStop()
	case rest == "save":
		a.missionSave()
	case rest == "report":
		a.missionReport()
	case rest == "chain" || strings.HasPrefix(rest, "chain "):
		a.cmdMissionChain(strings.TrimSpace(strings.TrimPrefix(rest, "chain")))
	case rest == "help" || rest == "?":
		a.missionHelp()
	default:
		// Слово start необязательно: это самая частая команда, и требовать
		// его — значит заставлять человека писать лишнее.
		a.missionStart(strings.TrimSpace(strings.TrimPrefix(rest, "start")))
	}
}

// missionHelp — что умеет /mission.
func (a *app) missionHelp() {
	a.ui.Println("")
	a.ui.Println("  /mission — автономный прогон: работа без новых промптов")
	a.ui.Println("")
	a.ui.Println("    /mission start [режим] [срок] [бюджет] [«цель»]   запустить")
	a.ui.Println("    /mission status                                 где сейчас прогон")
	a.ui.Println("    /mission stop                                   остановить")
	a.ui.Println("    /mission save                                   сохранить состояние")
	a.ui.Println("    /mission report                                 отчёт по прогону из журнала (markdown)")
	a.ui.Println("    /mission chain add|list|clear|run               цепочка шагов: run продолжает с прерванного")
	a.ui.Println("")
	a.ui.Println("  Режимы: " + missionModesHelp())
	a.ui.Println("  Срок: 4h, 90m, 120. Бюджет: 500k (токены), 25 (доллары), 500k,25 (оба).")
	a.ui.Println("  Критерии приёмки — в кавычках, каждый следующий капс: " +
		"/mission start long-time 4h 1m «цель» «go test ./... зелёные»")
	a.ui.Println("  Файл задания: " + core.MissionPath(a.workDir))
	a.ui.Println("")
}

// missionModesHelp — список режимов одной строкой.
func missionModesHelp() string {
	var names []string
	for _, m := range core.MissionModes() {
		names = append(names, string(m))
	}
	return strings.Join(names, " | ")
}

// missionStart — запустить прогон прямо из разговора.
func (a *app) missionStart(args string) {
	m, err := parseMissionArgs(args)
	if err != nil {
		a.ui.Err(err.Error())
		return
	}
	if m.Objective == "" {
		m.Objective = a.mission.Objective
	}
	if m.Mode == core.MissionNormal && !m.Budgeted() && m.Deadline <= 0 && len(m.Acceptance) == 0 {
		a.ui.Err("нечего запускать: задай режим, срок или бюджет — " +
			"например /mission start long-time 2h 500k")
		return
	}
	a.mission = m.Apply()
	if !a.startTracker() {
		a.ui.Err("задание не задаёт ни одного потолка — прогон вёл бы себя как обычный ход")
		return
	}
	// Состояние пишется сразу: человек уйдёт через минуту, и прогон
	// должен пережить перезапуск, а не начинаться заново.
	if err := a.saveMissionState("прогон запущен: " + a.mission.Summary()); err != nil {
		a.ui.Warn("состояние прогона не сохранено: " + err.Error())
	}
	a.ui.Info("прогон запущен: " + a.mission.Summary())
	a.ui.Println("  " + a.missionTr.Status())
}

// missionStop — остановить прогон.
func (a *app) missionStop() {
	if a.missionTr == nil {
		a.ui.Info("прогон не запущен")
		return
	}
	a.missionTr.Stop(core.StopCancelled)
	a.mu.Lock()
	if a.lastAgent != nil {
		a.lastAgent.StopMission()
	}
	a.mu.Unlock()
	_ = a.saveMissionState("прогон остановлен человеком: " + a.missionTr.Status())
	a.ui.Info("прогон остановлен, состояние сохранено")
}

// missionSave — сохранить состояние прогона вручную.
func (a *app) missionSave() {
	if a.missionTr == nil {
		a.ui.Info("прогон не запущен — сохранять нечего")
		return
	}
	if err := a.saveMissionState("сохранено по запросу: " + a.missionTr.Status()); err != nil {
		a.ui.Err("не сохранилось: " + err.Error())
		return
	}
	a.ui.Info("состояние прогона сохранено")
}

// missionStatus — показать, где прогон.
func (a *app) missionStatus() {
	if a.missionTr == nil {
		a.ui.Println("")
		a.ui.Println("  Автономный прогон не запущен.")
		a.ui.Println("  Обычный ход: столько итераций, сколько нужно модели.")
		a.ui.Println("  Задать прогон: /mission start long-time 4h 1m «цель»")
		a.ui.Println("  Файл задания: " + core.MissionPath(a.workDir))
		if st, ok, _ := core.LoadMissionState(core.MissionStatePath(a.workDir)); ok && st.Summary != "" {
			a.ui.Println("  Прошлый прогон: " + core.OneLine(st.Summary))
		}
		a.ui.Println("")
		return
	}
	tr := a.missionTr
	m := tr.Mission()
	a.ui.Println("")
	if m.Objective != "" {
		a.ui.Println("  Цель:  " + core.OneLine(m.Objective))
	}
	a.ui.Println("  Режим: " + string(m.Mode))
	a.ui.Println("  " + tr.Status())
	if tr.Done() {
		a.ui.Println("  Остановлен: " + core.StopReasonLabel(tr.Stopped()))
	}
	if m.HasAcceptance() {
		a.ui.Println("  Критерии приёмки:")
		for i, c := range m.Acceptance {
			a.ui.Printf("    %d. %s\n", i+1, core.OneLine(c))
		}
	}
	a.ui.Printf("  Сохранений состояния: %d\n", tr.Checkpoints())
	a.ui.Println("")
}

// parseMissionArgs — разобрать аргументы /mission start.
//
// Формат: [режим] [срок] [бюджет] [«цель»] [«критерий»...]. Всё, что не
// число и не режим, идёт в цель, а каждое следующее кавычечное — в
// критерии приёмки.
func parseMissionArgs(args string) (core.Mission, error) {
	var m core.Mission
	for _, f := range splitMissionArgs(args) {
		if f.quoted {
			if m.Objective == "" {
				m.Objective = f.text
			} else {
				m.Acceptance = append(m.Acceptance, f.text)
			}
			continue
		}
		if mode, ok := core.ValidMissionMode(f.text); ok && f.text != "" {
			m.Mode = mode
			continue
		}
		// Срок опознаётся по суффиксам: «4h», «90m», «1h30m».
		// Голое «120» — это токены, иначе «/mission start 4» означало бы
		// четыре минуты работы вместо четырёх токенов.
		if strings.ContainsAny(f.text, "hms") {
			if d, err := core.ParseDur(f.text); err == nil {
				m.Deadline = d
				continue
			}
		}
		// Деньги и токены в одной строке: «500k,25».
		// Пара «токены, доллары» разбирается и здесь, и в parseBudget:
		// правило одно, иначе «/mission start 500k,25» и
		// «gcli -budget 500k,25» означали бы разное.
		if tok, cost, err := parseBudget(f.text); err == nil && (tok > 0 || cost > 0) {
			if tok > 0 {
				m.TokenBudget = tok
			}
			if cost > 0 {
				m.CostBudget = cost
			}
			continue
		}
		if m.Objective == "" {
			m.Objective = f.text
		} else {
			m.Acceptance = append(m.Acceptance, f.text)
		}
	}
	if err := m.Normalize(); err != nil {
		return core.Mission{}, err
	}
	return m.Apply(), nil
}

// missionField — разобранный аргумент команды.
type missionField struct {
	text   string
	quoted bool
}

// openQuotes — символы, открывающие кавычку в аргументах команды.
const openQuotes = "«“"

// closeQuotes — символы, закрывающие её.
const closeQuotes = "»”"

// sameQuotes — симметричные кавычки: одна и та же с обеих сторон.
const sameQuotes = "\"'`"

// splitMissionArgs — разбить аргументы, уважая кавычки.
//
// Кавычки нужны для критериев приёмки: «go test ./... зелёные» — это один
// пункт, а без кавычек он распался бы на пять слов и превратился в пять
// критериев, каждый из которых выполнить нельзя.
//
// Симметричные кавычки требуют отдельного состояния: иначе закрывающая
// «» была бы неотличима от открывающей, и фраза в кавычках распадалась
// бы обратно на слова.
func splitMissionArgs(s string) []missionField {
	var out []missionField
	var cur strings.Builder
	var quoted, started bool
	// inQuote — мы внутри кавычек. Пробелы внутри них не разделяют поля:
	// без этого «go test ./... зелёные» распалось бы на пять пунктов.
	var inQuote bool
	flush := func() {
		if !started {
			return
		}
		out = append(out, missionField{text: cur.String(), quoted: quoted})
		cur.Reset()
		quoted, started, inQuote = false, false, false
	}
	for _, r := range s {
		switch {
		case (r == ' ' || r == '\t' || r == '\n') && !inQuote:
			flush()
		case strings.ContainsRune(openQuotes, r):
			quoted, started, inQuote = true, true, true
		case strings.ContainsRune(closeQuotes, r):
			flush()
		case strings.ContainsRune(sameQuotes, r):
			if inQuote {
				flush()
				continue
			}
			quoted, started, inQuote = true, true, true
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
}
