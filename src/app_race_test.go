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
	"gcli/subagents"
	"gcli/tools"
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

// TestPermsRace — confirm() выдаёт гранты («всегда») из хода, а /yolo и
// /perms читают и меняют те же флаги из очереди ввода, не блокируясь
// busy-очередью (cron-ход идёт фоном). Итерация по карте BashExact в
// панели против гранта из хода падала бы фатально, не просто гонкой.
func TestPermsRace(t *testing.T) {
	a, _ := cmdApp(t)

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				a.grantBashExact(fmt.Sprintf("cmd-%d", w))
				a.grantFileWrite()
				a.grantWebFetch()
				a.grantBashAll()
				_ = a.permBash("cmd-x")
				_ = a.permBashAll()
				_ = a.permFileWrite()
				_ = a.permWebFetch()
				_ = a.permAutopilot()
				_ = a.permAutopilotAll()
				_ = a.permBashExactList()
			}
		}(w)
	}
	wg.Wait()

	for w := 0; w < 4; w++ {
		if !a.permBash(fmt.Sprintf("cmd-%d", w)) {
			t.Errorf("грант cmd-%d потерялся", w)
		}
	}
	if !a.permFileWrite() || !a.permWebFetch() || !a.permBashAll() {
		t.Error("гранты yolo-семейства потерялись")
	}
	if got := a.permBashExactList(); len(got) != 4 || got[0] != "cmd-0" {
		t.Errorf("список точных команд = %v, ожидалось 4 отсортированных", got)
	}

	// /yolo: включение открывает файлы и сеть, выключение не отзывает
	// уже выданные гранты.
	a2, _ := cmdApp(t)
	if !a2.toggleBashAll() {
		t.Error("первый /yolo должен включить BashAll")
	}
	if !a2.permFileWrite() || !a2.permWebFetch() {
		t.Error("включение /yolo должно открыть запись файлов и сеть")
	}
	if a2.toggleBashAll() {
		t.Error("второй /yolo должен выключить BashAll")
	}
	if !a2.permFileWrite() || !a2.permWebFetch() {
		t.Error("выключение /yolo не должно отзывать выданные гранты")
	}
}

// TestCfgRace — команды ввода пишут конфиг (/think, /plan, /autopilot,
// /skills…), а ход и субагенты читают его из чужих горутин: newAgent и
// runSubagent копируют всю структуру (providers.ThinkState), Budget-колбэк
// и спавн берут поля по одному. Снимок обязан идти под RLock против
// всех писателей, SkillsOff в снимке — глубокая копия, иначе /skills
// append писал бы в общий массив под ногами читателя. Писатели здесь
// один горутиной — как в проде: команды сериализованы очередью ввода,
// а читатели имитируют ход и субагентов, идущих параллельно.
func TestCfgRace(t *testing.T) {
	a, _ := cmdApp(t)
	cfgMu.Lock()
	a.repo.Cfg.SkillsOff = []string{"base"}
	cfgMu.Unlock()

	thinkStates := []string{"on", "off", "auto"}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			a.setAutopilot(i%2 == 0)
			a.cmdPlan([]string{"on", "off"}[i%2])
			a.cmdThink(thinkStates[i%3])
			if i%10 == 0 {
				cfgMu.Lock()
				a.repo.Cfg.SkillsOff = append(a.repo.Cfg.SkillsOff, fmt.Sprintf("skill-%d", i))
				cfgMu.Unlock()
			}
		}
	}()
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				cfg := a.cfgSnap()
				switch providers.ThinkState(cfg) {
				case "on", "off", "auto":
				default:
					t.Errorf("недопустимое состояние Think: %q", providers.ThinkState(cfg))
				}
				if cfg.SubBudget < 0 {
					t.Errorf("бюджет не может быть отрицательным: %d", cfg.SubBudget)
				}
				_ = a.cfgSubBudget()
				_ = a.cfgPlanMode()
			}
		}()
	}
	wg.Wait()

	// Глубокая копия SkillsOff: снимок не едет на общем массиве конфига,
	// иначе append писателя дописал бы в уже выданный срез.
	cfgMu.Lock()
	a.repo.Cfg.SkillsOff = append(a.repo.Cfg.SkillsOff, "extra")
	cfgMu.Unlock()
	snap := a.cfgSnap()
	if len(snap.SkillsOff) == 0 || snap.SkillsOff[0] != "base" {
		t.Errorf("снимок SkillsOff = %v, ожидалось начало с base — копия не глубокая", snap.SkillsOff)
	}
	if n := len(snap.SkillsOff); n > 0 {
		for _, s := range snap.SkillsOff {
			if s == "" {
				t.Errorf("пустой элемент в снимке SkillsOff: %v", snap.SkillsOff)
			}
		}
	}
	// Финальное состояние согласовано: Think из корректного набора.
	if s := providers.ThinkState(a.cfgSnap()); s != "on" && s != "off" && s != "auto" {
		t.Errorf("итоговое Think = %q", s)
	}
}

// TestStatusBarsRace — нижняя строка состояния и панель метрик читают
// сессию, провайдера и конфиг вчистую, а писать их может кто угодно:
// счётчики и AgentMode — ход из cron-горутины, Perms — confirm в ходе
// против /yolo, пару провайдер+модель — /model, план — /plan. REPL
// вызывает statusItems/metrics после хода из своей горутины и не
// прикрыт busy-очередью TUI, поэтому без снимков под sessMu/provMu/
// cfgMu «go test -race» ловит чтение против записи.
func TestStatusBarsRace(t *testing.T) {
	a, _ := cmdApp(t)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Имитация хода из cron-горутины: счётчики, режим, гранты.
		for i := 0; i < 80; i++ {
			a.AddUsage(core.Usage{PromptTokens: 2, CompletionTokens: 3})
			a.RecordEconomy(core.EconomyStats{Requests: 1})
			a.setAgentMode(i%2 == 0)
			a.grantBashAll()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Имитация очереди ввода: /plan меняет план под cfgMu.
		for i := 0; i < 40; i++ {
			a.cmdPlan([]string{"on", "off"}[i%2])
		}
	}()
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				items := a.statusItems()
				if len(items) < 2 {
					t.Errorf("строка состояния пуста: %d элементов", len(items))
				}
				m := a.metrics()
				if m.Context < 0 {
					t.Errorf("оценка контекста не может быть отрицательной: %d", m.Context)
				}
			}
		}()
	}
	wg.Wait()

	// Грант yolo, выданный «в ходе», обязан дожить до конца: статусная
	// строка его только читает, писать не имеет права.
	if !a.permBashAll() {
		t.Error("грант BashAll потерялся против чтений статусной строки")
	}
	if got := a.sessionEconomy().Requests; got != 80 {
		t.Errorf("запросов в экономии = %d, ожидалось 80", got)
	}
}

// TestMissionRace — /mission start пишет задание из очереди ввода и
// из HTTP-горутины, chainRun — из цикла цепочки, а attachMission и
// missionDeps читают то же самое в ходе из cron-горутины; Stopped
// движка и /mission stop пишут missionJournalEnd. Без msnMu «go test
// -race» ловит гонку структуры Mission и флагов журнала.
func TestMissionRace(t *testing.T) {
	a, _ := cmdApp(t)

	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				m := core.Mission{Objective: fmt.Sprintf("цель-%d-%d", w, i), TokenBudget: 1000}
				a.setMissionJob(m.Apply())
				a.startTracker()
			}
		}(w)
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				job := a.missionJob()
				_ = job.Summary()
				a.missionJournalStop(core.StopCancelled)
			}
		}()
	}
	wg.Wait()

	if job := a.missionJob(); job.Objective == "" {
		t.Error("после 100 записей задание не может быть пустым")
	}
}

// TestSharedObjectsRace — /agents par|depth подменяет a.pool, а buildTools
// (/sandbox, /perms reset, /ext trust) подменяет a.tools из очереди ввода;
// ход и субагенты в это время берут те же объекты (спавн, self_report,
// agent_status, колбэки скиллов, ограниченный реестр субагента). Гонка не
// на содержимом объектов (они сами синхронизированы), а на указателе поля:
// без svcMu «go test -race» ловит чтение поля против записи.
func TestSharedObjectsRace(t *testing.T) {
	a, _ := cmdApp(t)
	// Два разных реестра и два пула — как их делают /sandbox и /agents par.
	a.buildTools()
	r1 := a.toolsReg()
	a.buildTools()
	r2 := a.toolsReg()
	if r1 == r2 {
		t.Fatal("ожидались два разных реестра после двух buildTools")
	}
	p1, p2 := a.newPool(), a.newPool()
	if p1 == p2 {
		t.Fatal("ожидались два разных пула после двух newPool")
	}

	regs := []*tools.Registry{r1, r2}
	pools := []*subagents.Pool{p1, p2}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			a.setToolsReg(regs[i%2])
			a.setAgentsPool(pools[i%2])
		}
	}()
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				if pool := a.agentsPool(); pool == nil {
					t.Error("пул не может быть nil")
				} else {
					_ = pool.Enabled()
					_ = pool.Summary()
				}
				if reg := a.toolsReg(); reg == nil {
					t.Error("реестр не может быть nil")
				} else {
					_ = reg.Count()
					_ = reg.SkillsPromptBlockFor("как прочитать файл")
					if sub := a.registryForSubagent(false, []string{"read_file"}, nil); sub == nil {
						t.Error("ограниченный реестр субагента не может быть nil")
					}
				}
			}
		}()
	}
	wg.Wait()

	if a.agentsPool() == nil || a.toolsReg() == nil {
		t.Error("после подмен объекты должны остаться на месте")
	}
}

// TestContextCmdRace — /context целиком против хода. cmdContext брал
// снимок истории для оценки контекста, но хвост — RequestEstimate,
// счётчик сообщений и lastUserText — читал a.sess.Messages вчистую,
// а ход из cron-горутины дописывает сообщения параллельно: «go test
// -race» ловит гонку заголовка слайса. История толстая: без неё
// разблокированное окно команды микросекундное, и a.Messages() в начале
// каждого вызова закрывает все прошлые append'ы — на старом коде гонка
// не успевала пойматься. Снимки внутри команды — часть контракта,
// поэтому зовём команду целиком, как TestSessionCountersRace.
func TestContextCmdRace(t *testing.T) {
	a, _ := cmdApp(t)
	big := strings.Repeat("слово ", 8)
	sessMu.Lock()
	a.sess.Messages = make([]core.Message, 8000)
	for i := range a.sess.Messages {
		a.sess.Messages[i] = core.Message{Role: core.RoleAssistant, Content: big}
	}
	sessMu.Unlock()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // ход из cron-горутины: дописывает историю, ПОКА идёт
		// /context. Писатель обязан жить всю длину теста: append'ы
		// микросекундные, и короткий писатель отрабатывает до первого
		// разблокированного окна читателя — дальше всё накрыто HB-краем
		// от a.Messages() в начале каждого вызова.
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			a.AddMessage(core.Message{Role: core.RoleAssistant, Content: fmt.Sprintf("ответ %d", i)})
			i++
			time.Sleep(2 * time.Millisecond)
		}
	}()
	for i := 0; i < 60; i++ { // очередь ввода: /context
		a.cmdContext()
	}
	close(stop)
	wg.Wait()
}
