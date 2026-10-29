package agent

// Регрессионные тесты багфикс-прохода: пустой ход больше не закрывается
// молча, отменённый до старта ход не тратит запрос, nil-зависимости не
// роняют агента, продление выдаёт остаток до абсолютного потолка, а
// спецификация субагента не теряет свой MaxTurns.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gcli/core"
	"gcli/providers"
	"gcli/subagents"
	"gcli/tools"
)

// TestRunEmptyTurnReturnsError — провайдер ответил успешно, но в ходе нет
// ни текста, ни вызовов, ни размышлений. Раньше такой ход считался
// законченным и Run возвращал nil: человек видел приглашение ввести
// следующий запрос и не понимал, что ответа не было.
func TestRunEmptyTurnReturnsError(t *testing.T) {
	sess := &fakeSession{}
	sp := &scriptProvider{steps: [][]string{textStep("")}}

	a := New(Deps{
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Model:     "test-model",
	}, ".")

	err := a.Run(context.Background(), "задача")
	if !errors.Is(err, ErrEmptyTurn) {
		t.Fatalf("пустой ход должен вернуть ErrEmptyTurn, получено: %v", err)
	}
	if sp.count() != 1 {
		t.Errorf("запросов к провайдеру %d, ожидался 1", sp.count())
	}
}

// TestTurnProblemWhitespaceOnlyIsEmpty — ответ из одних пробелов и переводов
// строк для пользователя так же пуст, как и полностью пустой.
func TestTurnProblemWhitespaceOnlyIsEmpty(t *testing.T) {
	if err := turnProblem("stop", " \n\t ", 0, " \n "); !errors.Is(err, ErrEmptyTurn) {
		t.Errorf("пробельный ответ не распознан как пустой: %v", err)
	}
}

// TestRunCanceledBeforeStartDoesNotCallProvider — отменённый до старта ход
// не должен ни писать в историю, ни платить за запрос к модели.
func TestRunCanceledBeforeStartDoesNotCallProvider(t *testing.T) {
	sess := &fakeSession{}
	sp := &scriptProvider{steps: [][]string{textStep("не должно выполниться")}}

	a := New(Deps{
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Model:     "test-model",
	}, ".")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := a.Run(ctx, "задача"); err != nil {
		t.Fatalf("отменённый ход вернул ошибку: %v", err)
	}
	if sp.count() != 0 {
		t.Errorf("отменённый ход сделал %d запрос(ов) к провайдеру", sp.count())
	}
	if len(sess.Messages()) != 0 {
		t.Errorf("отменённый ход добавил сообщения в историю: %d", len(sess.Messages()))
	}
}

// TestNilSessionAndRegistryAreSafe — агент без сессии и реестра не должен
// паниковать в путях, которые вызываются до/вне хода. Ошибка конфигурации
// лучше разыменования nil, но ещё лучше — корректный пустой результат там,
// где работа в принципе возможна.
func TestNilSessionAndRegistryAreSafe(t *testing.T) {
	a := New(Deps{}, ".")

	req := a.BuildRequest()
	if len(req.Messages) != 0 || len(req.ToolDefs) != 0 {
		t.Errorf("без сессии и реестра запрос должен быть пустым: %+v", req)
	}
	if a.canRunParallel("неизвестный_инструмент") {
		t.Error("без реестра неизвестный инструмент не может считаться параллельным")
	}
	res := a.execTools(context.Background(), []core.ToolCall{{ID: "c1", Name: "read_file"}})
	if len(res) != 1 || !strings.Contains(res[0].text, "неизвестный") {
		t.Errorf("без реестра вызов должен вернуть понятную ошибку: %+v", res)
	}
	if a.Compact(context.Background(), false) {
		t.Error("без сессии сжимать нечего")
	}
	if got := a.CollectReport(context.Background()); got != "" {
		t.Errorf("без сессии отчёт собирать некуда: %q", got)
	}
	if reg := emptyRegistry(nil); reg == nil {
		t.Error("emptyRegistry(nil) должен вернуть пустой реестр, а не nil")
	}

	if err := a.Run(context.Background(), "задача"); err == nil ||
		!strings.Contains(err.Error(), "сессии") {
		t.Errorf("ход без сессии должен вернуть понятную ошибку, получено: %v", err)
	}
}

// TestExtendPartialGrantAtAbsCeiling — если до абсолютного потолка осталось
// меньше шага, продление выдаётся на остаток. Раньше запрос «шаг 30 при
// остатке 5» отклонялся целиком, и последние итерации пропадали из-за
// формального сравнения с полным шагом.
func TestExtendPartialGrantAtAbsCeiling(t *testing.T) {
	v := ExtendDecision(40, ExtendOptions{
		Reason: goodReason, Granted: 155, Abs: 200, Step: 30, Progress: 100,
	})
	if !v.OK {
		t.Fatalf("остаток потолка должен выдаваться частично: %s", v.Message)
	}
	if v.Granted != 5 || v.Total != 200 {
		t.Errorf("выдано %d итераций, новый лимит %d; ждали 5 и 200", v.Granted, v.Total)
	}

	// Ровно достигнутый потолок — уже отказ: расти некуда.
	if v := ExtendDecision(40, ExtendOptions{
		Reason: goodReason, Granted: 160, Abs: 200, Step: 30, Progress: 100,
	}); v.OK {
		t.Errorf("на абсолютном потолке продление выдано: %+v", v)
	}
}

// TestSubagentSpecMaxTurnsAppliesWithoutHostLimit — спецификация задаёт
// MaxTurns, а хост общий лимит не задал. Раньше сравнение шло с нулём,
// условие не срабатывало, и субагент получал дефолтные 12 итераций
// вместо запрошенной одной.
func TestSubagentSpecMaxTurnsAppliesWithoutHostLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pool.go"), []byte("строка 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := &scriptProvider{steps: [][]string{
		toolStep("read_file", `{"path":"pool.go"}`),
		toolStep("read_file", `{"path":"pool.go"}`),
		textStep("## Итог\n- работа выполнена.\n- инструмент вернул файл."),
	}}

	out := RunSubagent(context.Background(), SubagentDeps{
		Provider:  sp.provider(t),
		Providers: providers.NewClient(),
		Registry: func(_ bool, _, _ []string) *tools.Registry {
			return tools.New(tools.Env{WorkDir: dir})
		},
		Model:   "test-model",
		WorkDir: dir,
		// MaxIters намеренно 0: лимит должен прийти из спецификации.
	}, subagents.Spec{Type: subagents.TypeExplorer, Task: "прочитай pool.go", MaxTurns: 1})

	// На нормальный ход остаётся ровно один вызов инструмента. Второй
	// toolStep достаётся уже финальному ходу (инструменты там отключены),
	// а третий — отчёту. С дефолтными 12 итерациями субагент успел бы
	// выполнить второй вызов, и Tools было бы 2.
	if out.Tools != 1 {
		t.Errorf("выполнено вызовов инструментов %d, ожидался 1", out.Tools)
	}
	if !strings.Contains(out.Full, "работа выполнена") {
		t.Errorf("отчёт субагента потерян: %q", out.Full)
	}
}

// TestLoopDetectorFresh — новая итерация с ранее не встречавшимся вызовом
// считается продвижением, повтор того же вызова — нет.
func TestLoopDetectorFresh(t *testing.T) {
	d := NewLoopDetector()
	if d.Fresh() {
		t.Error("до первой итерации продвижения нет")
	}
	d.Record([]core.ToolCall{tc("read_file", `{"path":"a.go"}`)}, nil)
	if !d.Fresh() {
		t.Error("первый вызов должен считаться новым")
	}
	d.Record([]core.ToolCall{tc("read_file", `{"path":"a.go"}`)}, nil)
	if d.Fresh() {
		t.Error("повтор того же вызова не является продвижением")
	}
	d.Record([]core.ToolCall{
		tc("read_file", `{"path":"a.go"}`),
		tc("read_file", `{"path":"b.go"}`),
	}, nil)
	if !d.Fresh() {
		t.Error("новый вызов рядом с повтором должен считаться продвижением")
	}
	d.Record(nil, nil)
	if d.Fresh() {
		t.Error("пустая итерация не является продвижением")
	}
}

// TestMissionProgressPreventsFalseStall — прогон, который каждый шаг делает
// новые вызовы, не должен упираться в StallLimit*3: отметка продвижения
// сдвигает точку отсчёта застоя.
func TestMissionProgressPreventsFalseStall(t *testing.T) {
	tr := core.NewTracker(core.Mission{
		Mode: core.MissionLongTime, StallLimit: 2,
	}.Apply(), 0, 0)
	e := newMissionEngine(MissionDeps{Tracker: tr, Spent: func() int { return 0 }})

	for i := 0; i < 12; i++ {
		if reason := e.tick(1); reason != "" {
			t.Fatalf("итерация %d остановлена как застой: %q", i+1, reason)
		}
		e.progress()
	}
	if tr.Stalled() {
		t.Error("прогон с продвижением на каждом шаге помечен застоем")
	}
}

// TestMissionStopKeepsTrackerReason — причина в уведомлении хосту должна
// совпадать с той, что уже записана в трекере. Иначе после остановки по
// бюджету человек получал бы второе уведомление «прервано человеком».
func TestMissionStopKeepsTrackerReason(t *testing.T) {
	tr := core.NewTracker(core.Mission{TokenBudget: 10}.Apply(), 0, 0)
	var got []string
	e := newMissionEngine(MissionDeps{
		Tracker: tr,
		Spent:   func() int { return 100 },
		Stopped: func(reason string) { got = append(got, reason) },
	})

	if reason := e.tick(1); reason != core.StopTokens {
		t.Fatalf("причина остановки %q, хотели %q", reason, core.StopTokens)
	}
	e.stop(core.StopCancelled)
	if len(got) != 1 || got[0] != core.StopTokens {
		t.Errorf("уведомления %v, ожидалось одно %q", got, core.StopTokens)
	}
}

// TestMissionNotifyConcurrentStopOnce — остановка из HTTP-горутины и тик
// агентного цикла могут прийти одновременно. Уведомление хосту должно
// прозвучать один раз, а поле notified не должно быть гонкой.
func TestMissionNotifyConcurrentStopOnce(t *testing.T) {
	tr := core.NewTracker(core.Mission{TokenBudget: 10}.Apply(), 0, 0)
	var calls atomic.Int32
	e := newMissionEngine(MissionDeps{
		Tracker: tr,
		Spent:   func() int { return 100 },
		Stopped: func(string) { calls.Add(1) },
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.tick(1)
		}
	}()
	go func() {
		defer wg.Done()
		e.stop(core.StopCancelled)
	}()
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("уведомлений остановки %d, хотели 1", got)
	}
}

// TestCompactCheckpointsMissionBeforeReplacingHistory — снимок прогона
// обязан описывать историю до сжатия: после замены детали, которые в него
// входили, уже потеряны.
func TestCompactCheckpointsMissionBeforeReplacingHistory(t *testing.T) {
	sess := &plainSession{msgs: []core.Message{
		{Role: core.RoleUser, Content: "почини падения"},
		{Role: core.RoleAssistant, Content: "смотрю"},
	}}
	a := compactAgent(t, sess, "выжимка")

	msgsAtCheckpoint := -1
	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime}.Apply(), 0, 0)
	a.mission = newMissionEngine(MissionDeps{
		Tracker: tr,
		Spent:   func() int { return 0 },
		Checkpoint: func(string) error {
			msgsAtCheckpoint = len(sess.msgs)
			return nil
		},
	})

	if !a.Compact(context.Background(), false) {
		t.Fatal("Compact вернул false")
	}
	if msgsAtCheckpoint != 2 {
		t.Errorf("чекпоинт увидел %d сообщений, ожидалось 2 (история до сжатия)", msgsAtCheckpoint)
	}
	if len(sess.msgs) <= 2 {
		t.Error("история не изменилась: сжатие не заменило контекст")
	}
}

// TestOnToolDonePanicDoesNotCorruptResult — паника в колбэке хоста не
// должна превращать успешный результат инструмента в ошибку и не должна
// вызывать колбэк повторно из recover.
func TestOnToolDonePanicDoesNotCorruptResult(t *testing.T) {
	dir := t.TempDir()
	reg := tools.New(tools.Env{WorkDir: dir})
	reg.Register(&tools.Tool{
		Def: tools.ToolDef{Name: "ok_tool", Description: "тестовый"},
		Handler: func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{Text: "результат"}, nil
		},
	})
	a := New(Deps{Registry: reg, Session: &fakeSession{}}, dir)
	a.d.OnToolDone = func(core.ToolCall, *tools.Tool, tools.Result, error, time.Duration) {
		panic("хост упал")
	}

	res := a.execOne(context.Background(), core.ToolCall{ID: "c1", Name: "ok_tool"}, &a.econ)
	if res.text != "результат" {
		t.Errorf("паника хоста испортила результат инструмента: %q", res.text)
	}
}
