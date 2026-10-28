package main

// Регрессии гонок на общих полях app: ход агента зовётся не только из
// горутины ввода (TUI/REPL), но и из cron, watch и цепочки миссий, а
// слэш-команды читают то же самое из другой горутины. Без лока это
// ловится только гоночным детектором — поведение остаётся корректным
// годами, пока однажды не выстрелит.

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gcli/core"
	"gcli/providers"
)

// TestSnapErrRace — запись snapErr из «cron-хода» против чтения в /snapshots.
//
// Писатель имитирует turn() в cron-горутине, читатель — cmdSnapshots в
// горутине ввода. Если снять мьютекс из setSnapErr/lastSnapErr, «go test
// -race» падает на гонке записи и чтения интерфейса error.
func TestSnapErrRace(t *testing.T) {
	a := testApp(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			a.setSnapErr(fmt.Errorf("снимок не удался: шаг %d", i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			if err := a.lastSnapErr(); err != nil && err.Error() == "" {
				t.Error("ошибка снимка не может быть пустой строкой")
			}
		}
	}()
	wg.Wait()
	if err := a.lastSnapErr(); err == nil {
		t.Error("после 300 записей snapErr обязан быть установлен")
	}
}

// TestSessionTitleRace — запись заголовка сессии ходом против чтения
// командами (/export берёт sessionTitle, /fork снимает копию под sessMu).
//
// Писатель имитирует turn() из cron-горутины: под sessMu, как в turn.
// Читатель зовёт sessionTitle(). Без лока на заголовке «go test -race»
// ловит одновременные запись и чтение строки.
func TestSessionTitleRace(t *testing.T) {
	a := testApp(t)
	// Стартовый заголовок: устанавливаем до запуска горутин, иначе
	// быстрый читатель успевает отработать до первой записи писателя,
	// и ассерт ловил бы не гонку, а порядок планировщика.
	sessMu.Lock()
	a.sess.Title = "старт"
	sessMu.Unlock()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			sessMu.Lock()
			a.sess.Title = fmt.Sprintf("ход %d: правим гонки", i)
			sessMu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			if a.sessionTitle() == "" {
				t.Error("заголовок не может пропасть после установки")
				return
			}
		}
	}()
	wg.Wait()
}

// TestSnapErrOverwrittenKeepsLast — последняя ошибка снимков замещает
// предыдущую: /snapshots показывает именно свежую, а не первую.
func TestSnapErrOverwrittenKeepsLast(t *testing.T) {
	a := testApp(t)
	a.setSnapErr(errors.New("первая"))
	a.setSnapErr(errors.New("вторая"))
	if got := a.lastSnapErr().Error(); got != "вторая" {
		t.Errorf("snapErr = %q, ожидалось %q", got, "вторая")
	}
}

// TestSessionSwapRefusedDuringTurn — /fork, /clear и /resume во время хода
// отказывают с предупреждением и не трогают сессию: подменять a.sess под
// ногами идущего хода — потерянная история и usage в чужой сессии.
// Ход из cron/watch не прикрыт busy-очередью TUI, поэтому проверка нужна
// самой команде, а не только очереди ввода.
func TestSessionSwapRefusedDuringTurn(t *testing.T) {
	a, buf := cmdApp(t)
	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	old := a.sess.ID

	for _, cmd := range []struct {
		name string
		run  func()
	}{
		{"/fork", func() { a.cmdFork("") }},
		{"/clear", func() { a.cmdClear() }},
		{"/resume", func() { a.cmdResume("1") }},
	} {
		buf.Reset()
		cmd.run()
		if !strings.Contains(out(buf), "ход ещё идёт") {
			t.Errorf("%s во время хода обязан предупреждать, вывод:\n%s", cmd.name, out(buf))
		}
	}
	if a.sess.ID != old {
		t.Error("подмена сессии прошла, хотя ход ещё идёт")
	}
}

// TestForkWaitsForTurnBarrier — жёсткий барьер: между проверкой running и
// Lock мог начаться cron-ход, поэтому /fork обязан не подменять сессию,
// пока ход держит RLock, и выполнить подмену сразу после его снятия.
func TestForkWaitsForTurnBarrier(t *testing.T) {
	a, buf := cmdApp(t)
	a.sess.Messages = msgsOf(core.RoleUser, core.RoleAssistant)
	old := a.sess.ID

	a.sessSwap.RLock() // имитируем ход, удерживающий барьер
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.cmdFork("")
	}()
	time.Sleep(50 * time.Millisecond) // /fork обязан висеть на Lock
	if a.sess.ID != old {
		t.Fatal("подмена сессии произошла, пока ход держит барьер")
	}
	a.sessSwap.RUnlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("/fork не выполнился после снятия барьера")
	}
	if a.sess.ID == old {
		t.Fatal("после снятия барьера форк должен был состояться")
	}
	if !strings.Contains(out(buf), "форк создан") {
		t.Errorf("форк обязан отчитаться, вывод:\n%s", out(buf))
	}
}

// TestProviderModelPairRace — подмена пары «провайдер + модель» из очереди
// ввода против снимков из хода. Читатель обязан видеть согласованную пару:
// провайдер и модель от одной и той же подмены, а не нового провайдера со
// старой моделью (иначе у агента неверные лимиты контекста и цена).
func TestProviderModelPairRace(t *testing.T) {
	a := testApp(t)
	pairs := []struct {
		p *providers.Provider
		m string
	}{
		{&providers.Provider{ID: "zai"}, "glm-4.6"},
		{&providers.Provider{ID: "openai"}, "gpt-4.1"},
		{&providers.Provider{ID: "anthropic"}, "claude-sonnet"},
	}
	// Стартовая пара до запуска горутин: иначе быстрый читатель видит
	// нулевые a.prov/a.model до первой записи писателя.
	a.setProviderModel(pairs[0].p, pairs[0].m)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // писатель: /provider и /model из очереди ввода
		defer wg.Done()
		for i := 0; i < 300; i++ {
			want := pairs[i%len(pairs)]
			a.setProviderModel(want.p, want.m)
			a.setModel(want.m)
		}
	}()
	go func() { // читатель: агент в cron-ходе снимает пару целиком
		defer wg.Done()
		for i := 0; i < 300; i++ {
			p, m := a.providerModel()
			ok := false
			for _, want := range pairs {
				if p == want.p && m == want.m {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("рваная пара: провайдер %p · модель %q", p, m)
				return
			}
		}
	}()
	wg.Wait()
}

// TestSessionCountersRace — /status и /cost читают счётчики из очереди
// ввода, пока «cron-ход» и горутины субагентов пишут usage, экономию и
// журнал запусков. Команды зовутся целиком: снимки внутри них — часть
// контракта, и гонка в собранной панели ловится так же, как в accessors.
func TestSessionCountersRace(t *testing.T) {
	a, _ := cmdApp(t)

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				a.AddUsage(core.Usage{PromptTokens: 2, CompletionTokens: 3})
				a.RecordEconomy(core.EconomyStats{Requests: 1})
				a.recordSubagent(core.SubagentRecord{ID: "s", Name: "исследователь", Status: "done"})
			}
		}()
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				a.cmdStatus()
				a.cmdUsage()
				_ = a.sessionUsage()
				_ = a.sessionEconomy()
				_, _, _ = a.sessionCounters()
				_ = a.subagentRunCount()
			}
		}()
	}
	wg.Wait()

	if got, want := a.sessionUsage().Total(), 4*50*5; got != want {
		t.Errorf("итог usage = %d, ожидалось %d — часть обновлений потеряна", got, want)
	}
	if got := a.sessionEconomy().Requests; got != 200 {
		t.Errorf("запросов в экономии = %d, ожидалось 200", got)
	}
	if got := a.subagentRunCount(); got != 40 {
		t.Errorf("журнал субагентов = %d записей, ожидалось 40 (потолок)", got)
	}
}

// TestAgentModeRace — /agent переключает режим из очереди ввода, а
// newAgent читает его при сборке агента из хода (cron, watch, миссии).
// Писатели в конце каждого цикла пишут false, поэтому после барьера
// финальное значение предсказуемо.
func TestAgentModeRace(t *testing.T) {
	a, _ := cmdApp(t)
	a.setAgentMode(true)

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				a.setAgentMode(i%2 == 0)
				_ = a.agentMode()
			}
		}()
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_ = a.agentMode()
				a.cmdStatus()
			}
		}()
	}
	wg.Wait()

	if a.agentMode() {
		t.Error("последняя запись в каждом цикле — false, но флаг оказался true")
	}
}
