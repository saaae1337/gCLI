package agent

import (
	"strings"
	"testing"
	"time"

	"gcli/core"
)

// ---------- Движок прогона ----------

func newTestEngine(t *testing.T, m core.Mission, spent int) (*missionEngine, *int) {
	t.Helper()
	tr := core.NewTracker(m.Apply(), 0, 0)
	calls := 0
	e := newMissionEngine(MissionDeps{
		Tracker: tr,
		Spent:   func() int { return spent },
		Checkpoint: func(string) error {
			calls++
			return nil
		},
	})
	return e, &calls
}

func TestStartMissionSkipsPlainTurn(t *testing.T) {
	// Обычный ход не должен получать движок: пустой трекер каждую
	// итерацию спрашивал бы «не пора ли остановиться» и получал «нет».
	if e := startMission(core.Mission{Mode: core.MissionNormal}, MissionDeps{}); e != nil {
		t.Error("обычный ход обязан идти без движка прогона")
	}
}

func TestStartMissionAcceptsBudgetOnly(t *testing.T) {
	// Бюджет без режима — тоже прогон: «не больше 500k токенов» человек
	// задаёт чаще, чем «режим long-time на 4 часа».
	tr := core.NewTracker(core.Mission{TokenBudget: 500_000}.Apply(), 0, 0)
	if e := startMission(core.Mission{TokenBudget: 500_000}, MissionDeps{Tracker: tr}); e == nil {
		t.Error("бюджет без режима всё равно должен включать прогон")
	}
}

func TestMissionTickStopsOnTokenBudget(t *testing.T) {
	e, _ := newTestEngine(t, core.Mission{TokenBudget: 1000}, 500)
	if got := e.tick(1); got != "" {
		t.Fatalf("на 500 токенах из 1000 стоп не пора, получили %q", got)
	}
	e.deps.Spent = func() int { return 1000 }
	if got := e.tick(1); got != core.StopTokens {
		t.Errorf("причина остановки %q, хотели %q", got, core.StopTokens)
	}
}

func TestMissionTickStopsOnDeadline(t *testing.T) {
	now := time.Now()
	tr := core.NewTrackerAt(core.Mission{Mode: core.MissionLongTime}, 0, 0,
		func() time.Time { return now })
	e := newMissionEngine(MissionDeps{Tracker: tr, Spent: func() int { return 0 }})
	if got := e.tick(1); got != "" {
		t.Fatalf("до срока останавливать рано: %q", got)
	}
	// Часы поехали: четыре часа прошло за одну итерацию.
	tr.SetClock(func() time.Time { return now.Add(4*time.Hour + time.Minute) })
	if got := e.tick(1); got != core.StopDeadline {
		t.Errorf("причина остановки %q, хотели %q", got, core.StopDeadline)
	}
}

func TestMissionCountsToolCalls(t *testing.T) {
	e, _ := newTestEngine(t, core.Mission{MaxToolCalls: 3}, 0)
	e.countTools(2)
	if got := e.tick(1); got != "" {
		t.Fatalf("на втором вызове из трёх стоп не пора: %q", got)
	}
	e.countTools(1)
	if got := e.tick(1); got != core.StopTools {
		t.Errorf("причина остановки %q, хотели %q", got, core.StopTools)
	}
}

func TestMissionStallHardStops(t *testing.T) {
	// Застой, переросший втрое, останавливает прогон: честная тяжёлая
	// задача получит предупреждение, а не тишину.
	m := core.Mission{Mode: core.MissionLongTime, StallLimit: 2}
	e, _ := newTestEngine(t, m, 0)
	if got := e.tick(1); got != "" {
		t.Fatalf("на первой итерации рано: %q", got)
	}
	e.deps.Tracker.Progress()
	// С застоем в 2 итерации предупреждение приходит на третьей, а
	// остановка — на седьмой (2*3). Проверяем ровно эти точки, иначе
	// тест ловит не застой, а свою арифметику.
	e.tick(1)
	e.tick(1)
	if msg := e.stallMessage(); msg == "" {
		t.Error("на застое должно быть предупреждение модели")
	}
	for e.deps.Tracker.Iters() < 6 {
		if got := e.tick(1); got != "" {
			t.Fatalf("остановка слишком рано, на итерации %d: %q", e.deps.Tracker.Iters(), got)
		}
	}
	if got := e.tick(1); got != core.StopStalled {
		t.Errorf("причина остановки %q, хотели %q", got, core.StopStalled)
	}
}

func TestMissionStallMessageOnlyOnce(t *testing.T) {
	m := core.Mission{Mode: core.MissionLongTime, StallLimit: 2}
	e, _ := newTestEngine(t, m, 0)
	e.deps.Tracker.Progress()
	e.tick(1)
	e.tick(1)
	if e.stallMessage() == "" {
		t.Fatal("первое предупреждение о застое обязано быть")
	}
	if again := e.stallMessage(); again != "" {
		t.Error("повторять одно и то же предупреждение каждую итерацию — жечь контекст")
	}
}

func TestMissionVerifyGate(t *testing.T) {
	// С критериями приёмки «работа кончена» без проверки не принимается.
	m := core.Mission{Mode: core.MissionLongTime, Acceptance: []string{"go test ./... зелёные"}}
	e, _ := newTestEngine(t, m, 0)
	if msg := e.verifyMessage(); !strings.Contains(msg, "ПРОВЕРЬ") {
		t.Errorf("нет требования проверить критерии: %q", msg)
	}
	if again := e.verifyMessage(); !strings.Contains(again, "Попытка") && !strings.Contains(again, "попытка") {
		t.Errorf("второе требование должно упоминать повтор: %q", again)
	}
}

func TestMissionNoVerifyWithoutAcceptance(t *testing.T) {
	// Без критериев требование проверки бессмысленно: проверять нечего.
	m := core.Mission{Mode: core.MissionLongTime}
	e, _ := newTestEngine(t, m, 0)
	if msg := e.verifyMessage(); msg != "" {
		t.Errorf("без критериев проверять нечего, получили: %q", msg)
	}
}

func TestMissionContinueOnIdleToolFree(t *testing.T) {
	// Ключевое отличие от обычного хода: модель перестала звать
	// инструменты, но прогон говорит «продолжай» вместо остановки.
	m := core.Mission{Mode: core.MissionLongTime, Acceptance: []string{"тесты зелёные"}}
	e, _ := newTestEngine(t, m, 0)
	if msg := e.continueMessage(); !strings.Contains(msg, "продолжается") {
		t.Errorf("прогон должен просить продолжить, получили %q", msg)
	}
}

func TestMissionContinueLimited(t *testing.T) {
	// Продолжения ограничены: модель, которая без единого вызова
	// инструмента рапортует о готовности, не станет готова и от третьего
	// «продолжай». Потолок намеренно жёстче, чем «раз», и проверяется
	// циклом до отказа: он же ловит и бесконечный повтор.
	m := core.Mission{Mode: core.MissionLongTime, Acceptance: []string{"критерий"}}
	e, _ := newTestEngine(t, m, 0)
	asked := 0
	for i := 0; i < 20; i++ {
		if e.continueMessage() == "" {
			break
		}
		asked++
	}
	if asked == 0 {
		t.Fatal("прогон обязан хотя бы раз попросить продолжить")
	}
	if asked >= 20 {
		t.Error("продолжения обязаны быть ограничены, иначе бюджет не предсказуем")
	}
}

func TestMissionSystemBlock(t *testing.T) {
	m := core.Mission{Mode: core.MissionLongTime, Objective: "починить тесты"}
	e, _ := newTestEngine(t, m, 0)
	block := e.systemBlock()
	for _, want := range []string{"починить тесты", "4h", "ПРОВЕРЬ"} {
		if want == "ПРОВЕРЬ" {
			continue
		}
		if !strings.Contains(block, want) {
			t.Errorf("в блоке миссии нет %q", want)
		}
	}
	var none *missionEngine
	if none.systemBlock() != "" {
		t.Error("без прогона блок должен быть пустым")
	}
}

func TestMissionCheckpointCadence(t *testing.T) {
	m := core.Mission{Mode: core.MissionLongTime}
	e, calls := newTestEngine(t, m, 0)
	every := e.tracker().Mission().CheckpointEvery
	if every <= 0 {
		t.Fatalf("у long-time должен быть интервал сохранения, получили %d", every)
	}
	for i := 0; i < every-1; i++ {
		e.tick(1)
		if e.checkpoint() {
			t.Fatalf("на итерации %d сохранять рано", i+1)
		}
	}
	e.tick(1)
	if !e.checkpoint() {
		t.Fatalf("на итерации %d сохранять пора", every)
	}
	if *calls != 1 {
		t.Errorf("сохранений %d, хотели 1", *calls)
	}
}

func TestMissionStopMessage(t *testing.T) {
	m := core.Mission{Mode: core.MissionLongTime}
	e, _ := newTestEngine(t, m, 0)
	if msg := e.stopMessage(); msg != "" {
		t.Errorf("до остановки сообщения быть не должно: %q", msg)
	}
	e.stop(core.StopCancelled)
	msg := e.stopMessage()
	if !strings.Contains(msg, "прервано") {
		t.Errorf("в сообщении нет причины: %q", msg)
	}
}

func TestMissionStopNotifiesOnce(t *testing.T) {
	seen := 0
	tr := core.NewTracker(core.Mission{TokenBudget: 10}.Apply(), 0, 0)
	e := newMissionEngine(MissionDeps{
		Tracker: tr,
		Spent:   func() int { return 100 },
		Stopped: func(string) { seen++ },
	})
	// Три тика подряд после исчерпания бюджета: уведомление одно.
	e.tick(1)
	e.tick(1)
	e.tick(1)
	if seen != 1 {
		t.Errorf("уведомлений остановки %d, хотели 1", seen)
	}
}

func TestMissionNilSafe(t *testing.T) {
	// nil-движок не должен паниковать: агент обращается к нему на каждой
	// итерации цикла, в том числе в тестах без прогона.
	var e *missionEngine
	if got := e.tick(1); got != "" {
		t.Errorf("nil-движок вернул %q", got)
	}
	e.countTools(5)
	if e.checkpoint() {
		t.Error("nil-движок не должен сохранять")
	}
	if e.systemBlock() != "" || e.stopMessage() != "" || e.verifyMessage() != "" ||
		e.continueMessage() != "" || e.stallMessage() != "" {
		t.Error("nil-движок обязан отвечать пустыми строками")
	}
	if e.tracker() != nil {
		t.Error("nil-движок не должен выдавать трекер")
	}
}
