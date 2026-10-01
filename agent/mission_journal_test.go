package agent

import (
	"os"
	"strings"
	"testing"

	"gcli/core"
)

// ---------- Вызовы инструментов не считаются дважды ----------

func TestMissionToolCallsNotCountedTwice(t *testing.T) {
	// Главная ошибка, которую эта проверка ловит: движок отдаёт трекеру
	// накопленные за ход вызовы, а Tick их ПРИБАВЛЯЕТ. Тогда десять
	// итераций по два вызова дали бы 2+4+6+…+20 = 110 вместо 20, и
	// потолок max_tool_calls срабатывал бы в разы раньше срока.
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime, MaxToolCalls: 1000}.Apply(), 0, 0)
	e := newMissionEngine(MissionDeps{Tracker: tr, Spent: func() int { return 0 }})
	for i := 0; i < 10; i++ {
		e.countTools(2)
		if got := e.tick(1); got != "" {
			t.Fatalf("остановка на итерации %d: %q", i, got)
		}
	}
	if got := tr.ToolCalls(); got != 20 {
		t.Errorf("вызовов инструментов %d, хотели 20 (двойной учёт)", got)
	}
}

func TestMissionToolCallsSurviveTurnBoundary(t *testing.T) {
	// Агент создаётся заново на каждый ход, а прогон переживает ходы.
	// Счётчик вызовов живёт в трекере, поэтому второй ход обязан
	// продолжить с накопленного, а не с нуля.
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime}.Apply(), 0, 0)
	first := newMissionEngine(MissionDeps{Tracker: tr, Spent: func() int { return 0 }})
	for i := 0; i < 3; i++ {
		first.countTools(2)
		first.tick(1)
	}
	if got := tr.ToolCalls(); got != 6 {
		t.Fatalf("после первого хода вызовов %d, хотели 6", got)
	}
	// Второй ход: новый движок с тем же трекером.
	second := newMissionEngine(MissionDeps{Tracker: tr, Spent: func() int { return 0 }})
	for i := 0; i < 2; i++ {
		second.countTools(2)
		second.tick(1)
	}
	if got := tr.ToolCalls(); got != 10 {
		t.Errorf("вызовов после двух ходов %d, хотели 10", got)
	}
	if got := tr.Iters(); got != 5 {
		t.Errorf("итераций прогона %d, хотели 5: счётчик не пережил границу хода", got)
	}
}

func TestMissionResumeCarriesCounters(t *testing.T) {
	// Продолжение после обрыва: счётчики прошлого прогона переносятся и
	// бюджет не начинается заново. Без Resume прогон на 500k токенов
	// после падения получил бы ещё 500k и обошёлся вдвое дороже.
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime, MaxIters: 10}.Apply(), 0, 0)
	tr.Resume(6, 40, 300_000)
	e := newMissionEngine(MissionDeps{Tracker: tr, Spent: func() int { return 0 }})
	if e.iters != 6 {
		t.Errorf("движок начал с %d итераций вместо 6", e.iters)
	}
	// Четыре оставшиеся итерации — и потолок должен сработать.
	var reason string
	for i := 0; i < 5; i++ {
		if reason = e.tick(1); reason != "" {
			break
		}
	}
	if reason != core.StopIters {
		t.Errorf("причина остановки %q, хотели %q", reason, core.StopIters)
	}
	if got := tr.Iters(); got != 10 {
		t.Errorf("дошли до %d итераций вместо 10", got)
	}
}

func TestMissionTickWithoutToolsKeepsCounter(t *testing.T) {
	// Итерация без вызовов инструментов не должна ни прибавлять, ни
	// обнулять счётчик вызовов: потолок по вызовам считается за весь
	// прогон, а не за последний шаг.
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime, MaxToolCalls: 4}.Apply(), 0, 0)
	e := newMissionEngine(MissionDeps{Tracker: tr, Spent: func() int { return 0 }})
	e.countTools(4)
	e.tick(1)
	if got := tr.ToolCalls(); got != 4 {
		t.Fatalf("вызовов %d, хотели 4", got)
	}
	// Пустые итерации идут, а счётчик стоит — потолок не достигнут.
	e.tick(1)
	e.tick(1)
	if got := tr.ToolCalls(); got != 4 {
		t.Errorf("пустые итерации изменили счётчик вызовов: %d", got)
	}
	e.countTools(1)
	if got := e.tick(1); got != core.StopTools {
		t.Errorf("причина остановки %q, хотели %q", got, core.StopTools)
	}
}

// ---------- Приглашение к продолжению ----------

func TestMissionResumeNoteOnlyOnce(t *testing.T) {
	// Приглашение уходит в историю один раз за прогон, а не на каждый
	// ход сеанса: десять одинаковых «продолжаем с прошлого места» в
	// истории сбивают модель с толку сильнее, чем отсутствие приглашения.
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime}.Apply(), 0, 0)
	deps := MissionDeps{
		Tracker: tr,
		Spent:   func() int { return 0 },
		Resume:  "сделано 40 итераций, остановился на падении тестов",
	}
	e := newMissionEngine(deps)
	if e.resumeNote() == "" {
		t.Fatal("первый вызов обязан дать приглашение")
	}
	if e.resumePending() {
		t.Error("после вставки приглашения ждать его снова нельзя")
	}
	if got := e.resumeNote(); got != "" {
		t.Errorf("повторное приглашение %q", got)
	}
	// Новый ход — новый движок с тем же трекером. Приглашения уже не
	// будет: оно ушло в историю в прошлый раз.
	next := newMissionEngine(MissionDeps{
		Tracker: tr,
		Spent:   func() int { return 0 },
		Resume:  deps.Resume,
	})
	if got := next.resumeNote(); got != "" {
		t.Errorf("на втором ходе приглашение повторилось: %q", got)
	}
}

func TestMissionResumeNoteWithoutResume(t *testing.T) {
	// Обычный прогон, который не продолжался: никакого приглашения.
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime}.Apply(), 0, 0)
	e := newMissionEngine(MissionDeps{Tracker: tr, Spent: func() int { return 0 }})
	if got := e.resumeNote(); got != "" {
		t.Errorf("приглашения без снимка быть не должно: %q", got)
	}
	if e.resumePending() {
		t.Error("ждать приглашения, которого не будет, незачем")
	}
}

// ---------- Журнал в прогоне ----------

func TestMissionJournalsCheckpoints(t *testing.T) {
	// Журнал наполняется по ходу работы, а не только на выходе: он
	// единственное, что переживает падение процесса.
	work := t.TempDir()
	j, err := core.OpenJournal(work)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()

	tr := core.NewTracker(core.Mission{
		Mode: core.MissionLongTime, CheckpointEvery: 1, Objective: "починить тесты",
	}.Apply(), 0, 0)
	saved := 0
	e := newMissionEngine(MissionDeps{
		Tracker: tr,
		Spent:   func() int { return 0 },
		Journal: j,
		Checkpoint: func(string) error {
			saved++
			return nil
		},
	})
	for i := 0; i < 3; i++ {
		e.tick(1)
		if !e.checkpoint() {
			t.Fatalf("чекпоинт на итерации %d не сделан", i+1)
		}
	}
	if saved != 3 {
		t.Errorf("снимков состояния %d, хотели 3", saved)
	}
	recs, ok, err := core.ReadJournal(core.JournalPath(work))
	if err != nil || !ok {
		t.Fatalf("чтение журнала: ok=%v err=%v", ok, err)
	}
	if len(recs) != 3 {
		t.Fatalf("записей в журнале %d, хотели 3", len(recs))
	}
	for i, rec := range recs {
		if rec.Kind != core.JournalCheckpoint {
			t.Errorf("запись %d имеет вид %q", i, rec.Kind)
		}
		if rec.Iters != i+1 {
			t.Errorf("в записи %d итераций %d вместо %d", i, rec.Iters, i+1)
		}
		if rec.Objective != "починить тесты" {
			t.Errorf("в записи %d цель %q потерялась", i, rec.Objective)
		}
	}
}

func TestMissionJournalNilIsSafe(t *testing.T) {
	// Журнала может не быть (не открылся диск, нет прав): прогон
	// обязан работать и без него.
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime, CheckpointEvery: 1}.Apply(), 0, 0)
	e := newMissionEngine(MissionDeps{
		Tracker:    tr,
		Spent:      func() int { return 0 },
		Checkpoint: func(string) error { return nil },
	})
	e.tick(1)
	e.journalCheckpoint("чекпоинт")
	if !e.checkpoint() {
		t.Error("чекпоинт без журнала всё равно должен делаться")
	}
}

func TestMissionCheckpointSurvivesFailingJournal(t *testing.T) {
	// Битый журнал не должен подменять снимок состояния: снимок важнее,
	// без него продолжение после перезапуска вообще не о чем.
	work := t.TempDir()
	j, err := core.OpenJournal(work)
	if err != nil {
		t.Fatal(err)
	}
	_ = j.Close()
	// Портим журнал чужой мусорой: чтение даст обрыв, а запись — ошибку.
	if err := os.WriteFile(core.JournalPath(work), []byte("это не журнал\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad, err := core.OpenJournal(work)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bad.Close() }()

	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime, CheckpointEvery: 1}.Apply(), 0, 0)
	saved := 0
	e := newMissionEngine(MissionDeps{
		Tracker:    tr,
		Spent:      func() int { return 0 },
		Journal:    bad,
		Checkpoint: func(string) error { saved++; return nil },
	})
	e.tick(1)
	if !e.checkpoint() {
		t.Error("снимок состояния обязан делаться даже при битом журнале")
	}
	if saved != 1 {
		t.Errorf("снимков %d, хотели 1", saved)
	}
}

func TestMissionJournalStopReasonVisible(t *testing.T) {
	// Причина остановки обязана попасть в журнал: без неё нельзя
	// отличить «работа кончена» от «срок вышел» постфактум.
	work := t.TempDir()
	j, err := core.OpenJournal(work)
	if err != nil {
		t.Fatal(err)
	}
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime, MaxIters: 2}.Apply(), 0, 0)
	e := newMissionEngine(MissionDeps{
		Tracker: tr,
		Spent:   func() int { return 0 },
		Journal: j,
		Stopped: func(string) {},
	})
	for i := 0; i < 3 && tr.Stopped() == ""; i++ {
		e.countTools(1)
		e.tick(1)
	}
	_ = j.Stop("прогон окончен", tr)
	_ = j.Close()

	recs, _, _ := core.ReadJournal(core.JournalPath(work))
	if len(recs) == 0 {
		t.Fatal("журнал пуст")
	}
	last := recs[len(recs)-1]
	if last.Reason != core.StopIters {
		t.Errorf("в последней записи причина %q, хотели %q", last.Reason, core.StopIters)
	}
	if !strings.Contains(last.Summary, "прогон окончен") {
		t.Errorf("описание %q", last.Summary)
	}
}
