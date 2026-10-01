package main

// Интеграционные тесты хоста и агента на продолжение автономного прогона:
// журнал, подхват после обрыва и семантика «сеанс закрыт» против
// «прогон окончен».
//
// Тесты проходят весь путь целиком — запись в журнал на настоящем диске,
// чтение его новым экземпляром, сборка агента заново, — потому что ценность
// именно в склейке: по кускам (журнал отдельно, resume отдельно) каждая
// половина выглядит рабочей, а ломается стык.

import (
	"os"
	"strings"
	"testing"
	"time"

	"gcli/agent"
	"gcli/core"
	"gcli/ui"
)

// missionApp — хост, пригодный для прогона: с сессией (счётчик расхода),
// интерфейсом и рабочим каталогом во временной папке.
func missionApp(t *testing.T) (*app, *strings.Builder) {
	t.Helper()
	work := t.TempDir()
	buf := &strings.Builder{}
	store := core.NewStore()
	store.Ensure()
	t.Setenv("GCLI_HOME", t.TempDir())
	a := &app{
		repo:    &core.Repo{Store: store, Cfg: core.DefaultConfig()},
		store:   store,
		ui:      ui.New(ui.Options{Theme: "ember", Width: 80, Out: buf}),
		workDir: work,
		quiet:   true,
		mission: core.Mission{Mode: core.MissionLongTime, Objective: "дописать журнал"}.Apply(),
		sess:    &core.Session{ID: "test"},
	}
	return a, buf
}

// journalKinds — виды событий журнала в порядке записи.
//
// Виды, а не формулировки: тест должен пережить переписывание сообщений и
// упасть только тогда, когда событие исчезло или поменяло смысл.
func journalKinds(t *testing.T, work string) []string {
	t.Helper()
	recs, _, err := core.ReadJournal(core.JournalPath(work))
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	kinds := make([]string, 0, len(recs))
	for _, r := range recs {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

// countKind — сколько раз событие такого вида встретилось.
func countKind(kinds []string, want string) int {
	n := 0
	for _, k := range kinds {
		if k == want {
			n++
		}
	}
	return n
}

// mentions — есть ли в списке заметок строка с подстрокой.
func mentions(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// ---------- Обычный выход: сеанс закрыт, но прогон не окончен ----------

func TestMissionCloseKeepsRunResumable(t *testing.T) {
	a, _ := missionApp(t)
	if !a.startTracker() {
		t.Fatal("задание long-time обязано завести трекер")
	}
	if n := countKind(journalKinds(t, a.workDir), core.JournalStart); n != 1 {
		t.Fatalf("начало прогона в журнале %d раз, ждали одно", n)
	}
	// Чекпоинт: без него «продолжать» нечего, и закрывать такой прогон
	// незачем — продолжать пришлось бы с нуля.
	if err := a.saveMissionState("сделано 12 итераций"); err != nil {
		t.Fatalf("снимок состояния: %v", err)
	}
	a.missionJournalCheckpoint("сделано 12 итераций")
	a.closeMissionJournal()

	kinds := journalKinds(t, a.workDir)
	if countKind(kinds, core.JournalClose) != 1 {
		t.Errorf("обычный выход должен писать ровно один close: %v", kinds)
	}
	if countKind(kinds, core.JournalStop) != 0 {
		t.Errorf("Ctrl+D не означает конец прогона, а журнал говорит stop: %v", kinds)
	}
	// И главное: прогон обязан продолжиться. Смысл записи «сеанс закрыт»
	// ровно в том, что следующий запуск подхватит работу.
	st, has, _ := core.MissionResumeFromJournal(core.JournalPath(a.workDir), time.Now(), resumeWindow)
	if !has {
		t.Fatal("после обычного выхода прогон обязан остаться продолжаемым")
	}
	if st.Summary == "" && st.Status == "" {
		t.Errorf("снимок пуст, продолжать нечего: %+v", st)
	}
}

// ---------- Необратимая остановка ----------

func TestMissionStopBlocksResume(t *testing.T) {
	a, _ := missionApp(t)
	if !a.startTracker() {
		t.Fatal("задание long-time обязано завести трекер")
	}
	if err := a.saveMissionState("до остановки"); err != nil {
		t.Fatalf("снимок состояния: %v", err)
	}
	a.missionJournalCheckpoint("до остановки")
	// Остановка по исчерпанному бюджету — необратимая: её пишет и
	// обработчик трекера, и выход из сеанса. Вторая запись молчала бы,
	// а в append-only журнале два одинаковых «остановлен» читаются как
	// две разные причины.
	a.missionTr.Stop(core.StopTools)
	a.missionJournalStop(core.StopTools)
	a.closeMissionJournal()

	kinds := journalKinds(t, a.workDir)
	if countKind(kinds, core.JournalStop) != 1 {
		t.Errorf("остановок записано %d, ждали ровно одну: %v", countKind(kinds, core.JournalStop), kinds)
	}
	if countKind(kinds, core.JournalClose) != 0 {
		t.Errorf("после stop журнал не должен предлагать продолжение: %v", kinds)
	}
	if _, has, _ := core.MissionResumeFromJournal(core.JournalPath(a.workDir), time.Now(), resumeWindow); has {
		t.Error("остановленный прогон продолжаться не должен")
	}
}

// ---------- Подхват после обрыва ----------

func TestMissionResumeAfterCrash(t *testing.T) {
	work := t.TempDir()

	// --- Прогон, который оборвался: пишем журнал, но НЕ закрываем его
	// (ни close, ни stop) — так выглядит убитый процесс.
	crashed, _ := missionApp(t)
	crashed.workDir = work
	crashed.sess.Usage.PromptTokens = 3000
	crashed.sess.Usage.CompletionTokens = 1000
	if !crashed.startTracker() {
		t.Fatal("задание long-time обязано завести трекер")
	}
	// Счётчики прогона, накопленные до обрыва.
	crashed.missionTr.Tick(40, 55, crashed.sessionSpent())
	if got := crashed.missionTr.ToolCalls(); got != 55 {
		t.Fatalf("вызовов инструментов %d, ждали 55", got)
	}
	if err := crashed.saveMissionState("сделано 40 итераций, тесты красные"); err != nil {
		t.Fatalf("снимок состояния: %v", err)
	}
	crashed.missionJournalCheckpoint("сделано 40 итераций, тесты красные")
	// Падение процесса — это отсутствие Close, а не отказ закрыть файл.
	// Настоящий краш закрывает дескрипторы сам (спинки ядра, обработчик
	// ОС), поэтому в тесте файл закрывается руками: иначе на Windows он
	// остаётся занятым и временная папка не удаляется. Записи close при
	// этом нет — ровно то, что нужно для подхвата.
	_ = crashed.missionJournal.Close()
	crashed.missionJournal = nil

	// --- Новый запуск: журнал читается, работа продолжается.
	fresh, _ := missionApp(t)
	fresh.workDir = work
	fresh.loadMissionResume()
	if fresh.missionResumeState == nil {
		t.Fatal("после обрыва снимок обязан подхватиться")
	}
	if got := fresh.missionResumeState.Iters; got != 40 {
		t.Errorf("в снимке %d итераций, ждали 40", got)
	}
	if !strings.Contains(fresh.missionResume, "40 итераций") {
		t.Errorf("в приглашении нет счётчиков: %q", fresh.missionResume)
	}
	// Человеку обязано быть сказано, что прогон прерван, а не начат
	// с нуля. Заметка идёт в startupNotes — она печатается под баннером,
	// а не сразу в поток, поэтому проверяем её там.
	if !mentions(fresh.startupNotes, "прерван") {
		t.Errorf("человеку не сказано, что прогон прерван: %q", fresh.startupNotes)
	}
	// Счётчики обязаны переехать в трекер ДО первого тика, иначе первый
	// же Tick прибавит к ним расход нового запуска.
	if !fresh.startTracker() {
		t.Fatal("продолжение должно завести трекер")
	}
	if got := fresh.missionTr.Iters(); got != 40 {
		t.Errorf("после Resume в трекере %d итераций, ждали 40", got)
	}
	if got := fresh.missionTr.ToolCalls(); got != 55 {
		t.Errorf("пос��де Resume в трекере %d вызовов, ждали 55", got)
	}
	// Бюджет прогона обязан считать от восстановленного: иначе подхват
	// стоил бы человеку полного бюджета заново.
	if lim := fresh.mission.MaxIters; lim <= 40 {
		t.Errorf("потолок %d не позволяет проверить: 40 итераций — путь к нему, а не сам потолок", lim)
	}

	// --- Приглашение уходит в историю ровно один раз за сеанс.
	first := agent.New(agent.Deps{Model: "test"}, fresh.workDir)
	fresh.attachMission(first)
	if !fresh.missionResumedOnce {
		t.Fatal("подхват должен отметиться один раз за сеанс")
	}
	if n := countKind(journalKinds(t, work), core.JournalResume); n != 1 {
		t.Errorf("в журнале «продолжено» %d раз, ждали одно", n)
	}
	// Строка приглашения уходит в историю один раз: держать её дальше
	// незачем, иначе второй ход сеанса начался бы с «продолжаем с
	// прошлого места» — то есть с напоминанием, что у модели «пропала
	// нить», хотя она ничего не теряла.
	if got := fresh.missionDeps().Resume; got != "" {
		t.Errorf("после первого хода приглашение всё ещё в зависимостях: %q", got)
	}
	// Второй ход сеанса: новый агент, тот же трекер. Ни новой записи в
	// журнал, ни нового приглашения — подхват был один.
	second := agent.New(agent.Deps{Model: "test"}, fresh.workDir)
	fresh.attachMission(second)
	if n := countKind(journalKinds(t, work), core.JournalResume); n != 1 {
		t.Errorf("событий «продолжено» %d, ждали одно: подхват был один", n)
	}
	// И третий: счётчик приглашения живёт в трекере, а не в агенте,
	// поэтому новый агент не начинает прогон заново с нуля.
	third := agent.New(agent.Deps{Model: "test"}, fresh.workDir)
	fresh.attachMission(third)
	if n := countKind(journalKinds(t, work), core.JournalResume); n != 1 {
		t.Errorf("после трёх ходов «продолжено» %d раз, ждали одно", n)
	}
	if got := fresh.missionTr.Iters(); got != 40 {
		t.Errorf("счётчик итераций сбился на новом агенте: %d", got)
	}
	// Хост закрывает журнал один раз на весь сеанс: незакрытый файл на
	// Windows держится процессом и не даёт удалить рабочую папку.
	fresh.closeMissionJournal()
}

// ---------- Обрыв хвоста журнала ----------

func TestMissionResumeWarnsAboutTruncatedTail(t *testing.T) {
	work := t.TempDir()
	a, _ := missionApp(t)
	a.workDir = work
	if !a.startTracker() {
		t.Fatal("задание long-time обязано завести трекер")
	}
	if err := a.saveMissionState("человеческая проверка"); err != nil {
		t.Fatalf("снимок состояния: %v", err)
	}
	a.missionJournalCheckpoint("человеческая проверка")
	a.closeMissionJournal()

	// Хвост обрезан на середине записи — так выглядит падение при записи.
	path := core.JournalPath(work)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	if err := os.WriteFile(path, data[:len(data)-12], 0o600); err != nil {
		t.Fatal(err)
	}

	fresh, buf := missionApp(t)
	fresh.workDir = work
	fresh.quiet = false
	fresh.loadMissionResume()
	if fresh.missionResumeState == nil {
		t.Fatal("даже после обрезанного хвоста прогон должен продолжаться — по предыдущей записи")
	}
	// Молчать об обрезанном хвосте нельзя: человек продолжит прогон,
	// думая, что знает, где встали, а потерян был как раз последний
	// чекпоинт — самое интересное.
	if !strings.Contains(out(buf), "оборван") {
		t.Errorf("человеку не сказано про потерянный чекпоинт: %q", out(buf))
	}
}
