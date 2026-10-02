package agent

import (
	"fmt"
	"strings"

	"gcli/core"
)

// ---------- Mission: автономный прогон внутри агентного цикла ----------
//
// missionEngine — тонкий слой между mission.json и агентным циклом. Он не
// дублирует логику бюджетов (она в core.Tracker), а решает ровно три вещи,
// которые цикл сам решить не может:
//
//  1. Когда прогон пора остановить — по времени, токенам, деньгам.
//  2. Почему цикл нельзя закрывать на «модель перестала звать инструменты».
//  3. Когда состояние надо записать на диск, чтобы пережить перезапуск.
//
// Отдельный файл, потому что обычный ход должен читаться целиком без
// оглядки на миссию: engine == nil означает «никаких проверок».
//
// Счётчик итераций здесь, а не в трекере, по одной причине: mission.json
// переживает перезапуск, а агентный ход — нет. Итерации прогона копятся
// между вызовами Run, и счётчик хода их не увидит.

// missionContinuePrompt — требование продолжить вместо остановки.
//
// Обычный цикл закрывается, когда модель перестала звать инструменты: в
// обычном ходе это верный сигнал «работа кончена». В многочасовом прогоне
// тот же сигнал означает другое — модель устала, потеряла нить на длинной
// дистанции или посчитала задачу выполненной по памяти. Поэтому вместо
// остановки прогон говорит: вот цель, вот критерии — продолжай.
const missionContinuePrompt = "[Система] Автономный прогон продолжается: работа ещё не закончена.\n" +
	"Проверь по цели и критериям приёмки, что осталось, и продолжай работу инструментами. " +
	"Не пересказывай достигнутое — сделай следующий шаг."

// missionVerifyPrompt — требование проверить критерии перед остановкой.
const missionVerifyPrompt = "[Система] Ты собираешься закончить работу.\n" +
	"Но в задании есть критерии приёмки, и «выглядит готовым» — не проверка.\n" +
	"ПРОВЕРЬ каждый пункт инструментом (сборка, тесты, чтение файла — что уместно) " +
	"и перечисли в ответе: пункт → каким вызовом проверен → результат.\n" +
	"Если пункт не выполнен — не заявляй готовность, а исправь его."

// missionStallPrompt — предупреждение о застое.
const missionStallPrompt = "[Система] Похоже, работа стоит на месте: последние итерации повторяют " +
	"одно и то же. Прежде чем продолжать, смени подход: разберись, почему прошлый шаг не дал " +
	"результата, и попробуй другой путь."

// missionStopMessage — сообщение модели при остановке по потолку.
//
// Прогон обрывается не молча: модель должна уйти с понятным состоянием,
// а не с ощущением, что её бросили посреди работы.
const missionStopMessage = "[Система] Автономный прогон остановлен: %s.\n" +
	"Незавершённое осталось незавершённым — напиши кратко, что сделано, что осталось " +
	"и с чего продолжать."

// missionResumedNote — что сказать при старте продолженного прогона.
const missionResumedNote = "[Система] Продолжаем автономный прогон, начатый %s назад. " +
	"Ниже — состояние на момент последнего сохранения.\n\n%s"

// MissionDeps — что движку прогона нужно от хоста.
type MissionDeps struct {
	// Tracker — состояние прогона. Создаётся хостом: он знает расход
	// сессии и провайдера с моделью, агент знает только свой ход.
	Tracker *core.Tracker
	// Spent — расход сессии в токенах на сейчас.
	Spent func() int
	// Provider и Model — для честного расчёта бюджета денег.
	Provider string
	Model    string
	// Checkpoint — сохранить состояние на диск (вызывается по расписанию).
	Checkpoint func(summary string) error
	// Stopped — сообщить хосту причину остановки (для UI и /mission status).
	Stopped func(reason string)
	// Journal — журнал прогона. Пишется напрямую, потому что он переживает
	// падение процесса: пока жив агент, запись не случилась, а при падении
	// её уже не сделает никто.
	//
	// nil-журнал допустим и означает «журнала нет»: работа идёт, а
	// подстраховка на диске ограничивается снимком состояния.
	Journal *core.Journal
	// Resumed — продолжается ли прогон после обрыва. Модели об этом надо
	// сказать явно, иначе она решит, что забыла нить, и начнёт ту же
	// работу заново — потратив бюджет на повтор.
	Resumed bool
	// Resume — снимок прошлого прогона (из журнала). Подставляется
	// в историю в начале хода.
	Resume string
}

// missionEngine — движок прогона внутри хода агента.
type missionEngine struct {
	deps MissionDeps
	// pendingTools — вызовы инструментов с прошлой отметки бюджета.
	//
	// Именно с прошлой отметки, а не за весь ход: Tick эти вызовы
	// ПРИБАВЛЯЕТ к счётчику трекера. Если бы движок отдавал накопленное
	// за ход, десять итераций по два вызова посчитали бы двадцать
	// вызовов вместо двадцати же — но уже на второй итерации счётчик
	// удвоился бы, и потолок max_tool_calls срабатывал бы втрое раньше.
	pendingTools int
	// iters — итерации прогона, а не текущего хода.
	//
	// Стартует не с нуля, а с того, что уже накоплено в трекере: агент
	// создаётся заново на каждый ход, а прогон переживает ходы. Сброс
	// здесь означал бы, что многочасовой прогон с потолком в 200 итераций
	// никогда бы его не достиг — каждый ход начинался бы с нуля.
	iters int
	// notified — причина, о которой уже сообщили хосту: сообщение об
	// остановке должно прозвучать один раз, а не на каждой проверке.
	//
	// Локально, в отличие от флага приглашения к продолжению: движок живёт
	// один ход, а остановка прогон завершает — на следующем ходе её и так
	// нечего сообщать, трекер помнит причину и без движка.
	notified string
}

// newMissionEngine — собрать движок. nil-трекер означает обычный ход: все
// проверки уходят в no-op, и цикл ведёт себя ровно как раньше.
func newMissionEngine(d MissionDeps) *missionEngine {
	if d.Tracker == nil {
		return nil
	}
	if d.Spent == nil {
		d.Spent = func() int { return 0 }
	}
	d.Tracker.WithModel(d.Provider, d.Model)
	// Продолжение с накопленного: счётчик трекера — единственный источник
	// правды о том, сколько прогон уже сделал.
	return &missionEngine{deps: d, iters: d.Tracker.Iters()}
}

// startMission — начать прогон по заданию. Возвращает nil, если задание
// обычное: пустой движок лучше, чем движок, который на каждой итерации
// проверяет нулевые потолки.
func startMission(m core.Mission, d MissionDeps) *missionEngine {
	m = m.Apply()
	if !m.Long() && !m.HasAcceptance() && !m.Budgeted() {
		return nil
	}
	return newMissionEngine(d)
}

// tick — отметить состояние прогона и спросить, пора ли останавливаться.
func (e *missionEngine) tick(itersDelta int) string {
	if e == nil {
		return ""
	}
	e.iters += itersDelta
	// pendingTools сбрасывается ПОСЛЕ отметки: трекер прибавил их к
	// своему счётчику, и повторная передача на следующей итерации
	// посчитала бы их дважды.
	pending := e.pendingTools
	e.pendingTools = 0
	reason := e.deps.Tracker.Tick(e.iters, pending, e.spent())
	if reason == "" && e.deps.Tracker.StallHard() {
		reason = e.deps.Tracker.Stop(core.StopStalled)
	}
	if reason != "" {
		e.notify(reason)
	}
	return reason
}

// spent — расход сессии, попадающий в трекер.
func (e *missionEngine) spent() int {
	if e == nil || e.deps.Spent == nil {
		return 0
	}
	return e.deps.Spent()
}

// notify — один раз сообщить хосту причину остановки.
func (e *missionEngine) notify(reason string) {
	if e == nil || e.deps.Stopped == nil || e.notified == reason {
		return
	}
	e.notified = reason
	e.deps.Stopped(reason)
}

// countTools — отметить вызовы инструментов текущей итерации.
func (e *missionEngine) countTools(n int) {
	if e != nil && n > 0 {
		e.pendingTools += n
	}
}

// stop — остановить прогон по воле человека или по решению хоста.
func (e *missionEngine) stop(reason string) {
	if e == nil {
		return
	}
	e.deps.Tracker.Stop(reason)
	e.notify(reason)
}

// tracker — трекер для UI и команд.
func (e *missionEngine) tracker() *core.Tracker {
	if e == nil {
		return nil
	}
	return e.deps.Tracker
}

// ---------- Сохранение состояния ----------

// checkpoint — сохранить состояние, если подошла очередь.
//
// Ошибка сохранения не убивает прогон: работа продолжается, а потеря
// страховки — не повод остановиться. Пропущенное сохранение не должно
// превращаться в тишину, поэтому о нём говорит вызывающий.
func (e *missionEngine) checkpoint() bool {
	if e == nil || e.deps.Checkpoint == nil {
		return false
	}
	if !e.deps.Tracker.CheckpointDue() {
		return false
	}
	summary := e.summary("чекпоинт")
	if err := e.deps.Checkpoint(summary); err != nil {
		return false
	}
	// Журнал пишется после снимка: снимок переживает сжатие контекста,
	// журнал — падение процесса. Ошибка снимка важнее, потому что без
	// него продолжение после перезапуска вообще не о чем.
	e.journalCheckpoint(summary)
	e.deps.Tracker.CountCheckpoint()
	return true
}

// journalCheckpoint — дописать чекпоинт в журнал.
func (e *missionEngine) journalCheckpoint(summary string) {
	if e == nil || e.deps.Journal == nil {
		return
	}
	_ = e.deps.Journal.Checkpoint(summary, e.deps.Tracker)
}

// checkpointNow — сохранить немедленно (при остановке, перед сжатием).
func (e *missionEngine) checkpointNow(what string) error {
	if e == nil || e.deps.Checkpoint == nil {
		return nil
	}
	return e.deps.Checkpoint(e.summary(what))
}

// summary — короткая строка о состоянии для снимка и журнала.
func (e *missionEngine) summary(what string) string {
	if e == nil {
		return ""
	}
	tr := e.deps.Tracker
	if what == "" {
		what = "чекпоинт"
	}
	m := tr.Mission()
	line := fmt.Sprintf("%s: %s", what, tr.Status())
	if m.Objective != "" {
		line = m.Objective + " — " + line
	}
	return line
}

// systemBlock — блок про миссию в системный промпт.
func (e *missionEngine) systemBlock() string {
	if e == nil {
		return ""
	}
	return e.deps.Tracker.Mission().PromptBlock()
}

// resumeNote — сообщение о старте продолженного прогона.
//
// Один раз за прогон, а не за ход: сообщение объясняет, что модель
// продолжает, а повторять его перед каждым ходом значит каждый раз
// заново напоминать ей, что у неё «пропала нить». Первое попадание
// возвращает состояние в контекст, а дальше оно там и живёт.
func (e *missionEngine) resumeNote() string {
	if e == nil || strings.TrimSpace(e.deps.Resume) == "" || e.deps.Tracker.ResumeShown() {
		return ""
	}
	e.deps.Tracker.MarkResumeShown()
	ago := core.FormatDur(e.deps.Tracker.Elapsed())
	return fmt.Sprintf(missionResumedNote, ago, e.deps.Resume)
}

// resumePending — ждёт ли прогон вставки приглашения к продолжению.
//
// Отдельно от resumeNote, потому что приглашение обязано появиться
// именно в том ходе, где прогон реально пойдёт дальше: вставка в
// историю обычного хода была бы сообщением без работы.
func (e *missionEngine) resumePending() bool {
	return e != nil && !e.deps.Tracker.ResumeShown() && strings.TrimSpace(e.deps.Resume) != ""
}

// continueMessage — попросить модель продолжить вместо остановки.
func (e *missionEngine) continueMessage() string {
	if e == nil || !e.deps.Tracker.CanContinue() {
		return ""
	}
	n := e.deps.Tracker.Continued()
	if n == 1 {
		return missionContinuePrompt
	}
	return fmt.Sprintf("[Система] Продолжение %d. Работа ещё не закончена — вернись к инструментам "+
		"и доведи критерии приёмки.", n)
}

// verifyMessage — попросить проверить критерии перед остановкой.
func (e *missionEngine) verifyMessage() string {
	if e == nil || !e.deps.Tracker.NeedVerify() {
		return ""
	}
	n := e.deps.Tracker.AskVerify()
	if n == 1 {
		return missionVerifyPrompt
	}
	return fmt.Sprintf("[Система] Проверка критериев не выполнена (попытка %d). Без неё прогон "+
		"не остановится как «готово»: проверь критерии вызовами инструментов или прямо назови, "+
		"какой пункт выполнить нельзя и почему.", n)
}

// stallMessage — предупредить о застое (один раз на застой).
func (e *missionEngine) stallMessage() string {
	if e == nil {
		return ""
	}
	tr := e.deps.Tracker
	if !tr.Stalled() || tr.StallHard() || tr.StallWarned() > 0 {
		return ""
	}
	tr.WarnStall()
	return missionStallPrompt
}

// stopMessage — сообщение о причине остановки.
func (e *missionEngine) stopMessage() string {
	if e == nil {
		return ""
	}
	reason := e.deps.Tracker.Stopped()
	if reason == "" {
		return ""
	}
	return fmt.Sprintf(missionStopMessage, core.StopReasonLabel(reason))
}
