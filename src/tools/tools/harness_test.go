package tools

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- ask_trace ----------

// TestAskTraceEmpty — без вопросов инструмент объясняет, что вопрос не был
// задан. Это и есть главный диагноз: агент ждёт ответа, которого не просил.
func TestAskTraceEmpty(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	res := mustRun(t, r, "ask_trace", nil)
	if !strings.Contains(res.Text, "не было") {
		t.Errorf("пустой след описан неверно:\n%s", res.Text)
	}
}

// TestAskTraceRecordsAnswered — вопрос и ответ попадают в след.
func TestAskTraceRecordsAnswered(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	r.recordAsk("какой стиль выбрать?", "тёмный", false)

	res := mustRun(t, r, "ask_trace", nil)
	if !strings.Contains(res.Text, "какой стиль выбрать?") {
		t.Errorf("вопрос не показан:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "тёмный") {
		t.Errorf("ответ не показан:\n%s", res.Text)
	}
}

// TestAskTraceMarksPending — вопрос без ответа помечен: ответ сам не придёт,
// и ход не должен висеть в ожидании.
func TestAskTraceMarksPending(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	r.recordAsk("оставить как есть?", "", true)

	res := mustRun(t, r, "ask_trace", nil)
	if !strings.Contains(res.Text, "ответа нет") {
		t.Errorf("вопрос без ответа не помечен:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "Ответ не придёт сам") {
		t.Errorf("нет предостережения о висящем вопросе:\n%s", res.Text)
	}
}

// TestAskTraceIsolatedPerRegistry — след принадлежит реестру, а не процессу.
// Иначе главный агент видел бы в своём ask_trace вопросы субагентов и решал
// бы свою задачу по чужому контексту.
func TestAskTraceIsolatedPerRegistry(t *testing.T) {
	r := newTestReg(t, t.TempDir())
	sub := r.Base()

	r.recordAsk("вопрос главного", "ответ главного", false)
	sub.recordAsk("вопрос субагента", "ответ субагента", false)

	main := mustRun(t, r, "ask_trace", nil)
	if !strings.Contains(main.Text, "вопрос главного") {
		t.Errorf("свой вопрос потерян:\n%s", main.Text)
	}
	if strings.Contains(main.Text, "вопрос субагента") {
		t.Errorf("след субагента протекает в главный:\n%s", main.Text)
	}

	subRes := mustRun(t, sub, "ask_trace", nil)
	if strings.Contains(subRes.Text, "вопрос главного") {
		t.Errorf("субагент видит чужой след:\n%s", subRes.Text)
	}
}

// TestAskTraceNilLogSurvives — реестр, собранный вручную, не падает.
func TestAskTraceNilLogSurvives(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{byName: map[string]*Tool{}, workDir: dir, env: Env{WorkDir: dir}}
	r.registerBuiltins()

	r.recordAsk("вопрос", "ответ", false)
	res := mustRun(t, r, "ask_trace", nil)
	if !strings.Contains(res.Text, "вопрос") {
		t.Errorf("след не записался в реестр без журнала:\n%s", res.Text)
	}
}

// TestAskLogKeepsRecentOnly — след не растёт бесконечно, но свежие вопросы
// остаются: старые всё равно не влияют на текущий ход.
func TestAskLogKeepsRecentOnly(t *testing.T) {
	l := &askLog{}
	for i := 0; i < 40; i++ {
		l.add("вопрос", "ответ", false)
	}
	items := l.all()
	if len(items) != 20 {
		t.Errorf("в следе %d вопросов вместо 20", len(items))
	}
}

// ---------- Фоновые процессы ----------

// waitJob — дождаться завершения задачи.
func waitJob(t *testing.T, j *Job) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !j.Done() {
		if time.Now().After(deadline) {
			t.Fatalf("задача %s не завершилась за 20 с", j.ID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestJobStartAndOutput — фоновая задача пишет вывод в отдельный лог,
// который переживает ход, и по нему виден код завершения.
func TestJobStartAndOutput(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(overrideHome(home))

	r := newTestReg(t, t.TempDir())
	res := mustRun(t, r, "job", map[string]any{
		"action": "run", "command": "echo готово",
	})
	if res.Error != "" {
		t.Fatalf("job run вернул ошибку: %s", res.Error)
	}
	if !strings.Contains(res.Text, "ID:") {
		t.Errorf("не выдан дескриптор задачи:\n%s", res.Text)
	}

	list := jobList()
	if len(list) == 0 {
		t.Fatal("задача не попала в реестр")
	}
	j := list[0]
	waitJob(t, j)

	if code, ok := j.Exit(); !ok || code != 0 {
		t.Errorf("код завершения = %d (ok=%v), ожидался 0", code, ok)
	}
	if st := j.Status(); st != jobDone {
		t.Errorf("состояние = %q, ожидалось %q", st, jobDone)
	}

	out := mustRun(t, r, "job", map[string]any{"action": "output", "id": j.ID})
	if !strings.Contains(out.Text, "готово") {
		t.Errorf("вывод задачи не показан:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "успех") {
		t.Errorf("код завершения не показан:\n%s", out.Text)
	}
}

// TestJobFailureKeepsExitCode — упавшая задача отличается от убитой: код
// выхода нужен, чтобы отличить «тесты красные» от «тесты не дошли до конца».
func TestJobFailureKeepsExitCode(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(overrideHome(home))

	r := newTestReg(t, t.TempDir())
	mustRun(t, r, "job", map[string]any{"action": "run", "command": "exit 3"})

	list := jobList()
	if len(list) == 0 {
		t.Fatal("задача не запущена")
	}
	j := list[0]
	waitJob(t, j)

	if st := j.Status(); st != jobFailed {
		t.Errorf("состояние = %q, ожидалось %q", st, jobFailed)
	}
	out := mustRun(t, r, "job", map[string]any{"action": "output", "id": j.ID})
	if !strings.Contains(out.Text, "ОШИБКА") {
		t.Errorf("падение не показано как ошибка:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "3") {
		t.Errorf("код выхода 3 не показан:\n%s", out.Text)
	}
}

// TestJobStop — остановленная задача помечена и не висит вечно.
func TestJobStop(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(overrideHome(home))

	r := newTestReg(t, t.TempDir())
	mustRun(t, r, "job", map[string]any{
		"action": "run", "command": "sleep 30", "timeout_sec": 0,
	})
	list := jobList()
	if len(list) == 0 {
		t.Fatal("задача не запущена")
	}
	j := list[0]

	stop := mustRun(t, r, "job", map[string]any{"action": "stop", "id": j.ID})
	if !strings.Contains(stop.Text, "остановлен") {
		t.Errorf("задача не остановлена:\n%s", stop.Text)
	}
	waitJob(t, j)
	if st := j.Status(); st != jobStopped {
		t.Errorf("состояние после stop = %q, ожидалось %q", st, jobStopped)
	}

	// Повторная остановка завершённой задачи — понятный отказ, а не ошибка.
	again := mustRun(t, r, "job", map[string]any{"action": "stop", "id": j.ID})
	if !strings.Contains(again.Text, "останавливать нечего") {
		t.Errorf("повторная остановка описана неверно:\n%s", again.Text)
	}
}

// TestJobOutputWhileRunning — незавершённая задача не выдаётся за упавшую.
func TestJobOutputWhileRunning(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(overrideHome(home))

	r := newTestReg(t, t.TempDir())
	mustRun(t, r, "job", map[string]any{"action": "run", "command": "sleep 30"})

	list := jobList()
	j := list[0]
	out := mustRun(t, r, "job", map[string]any{"action": "output", "id": j.ID})
	if !strings.Contains(out.Text, "ещё выполняется") {
		t.Errorf("выполняющаяся задача не отмечена как незавершённая:\n%s", out.Text)
	}
	if st := j.Status(); st != jobRunning {
		t.Errorf("состояние = %q, ожидалось %q", st, jobRunning)
	}
	mustRun(t, r, "job", map[string]any{"action": "stop", "id": j.ID})
	waitJob(t, j)
}

// TestJobStatusAndClear — список задач виден, завершённые убираются.
func TestJobStatusAndClear(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(overrideHome(home))

	r := newTestReg(t, t.TempDir())
	mustRun(t, r, "job", map[string]any{"action": "run", "command": "exit 0"})
	list := jobList()
	waitJob(t, list[0])

	st := mustRun(t, r, "job", map[string]any{"action": "status"})
	if !strings.Contains(st.Text, "Всего:") {
		t.Errorf("список задач не показан:\n%s", st.Text)
	}
	cl := mustRun(t, r, "job", map[string]any{"action": "clear"})
	if !strings.Contains(cl.Text, "убрано") && !strings.Contains(cl.Text, "Убрано") {
		t.Errorf("очистка не отчиталась:\n%s", cl.Text)
	}
}

// TestJobBadArgs — внятные ошибки на пустых и несуществующих входных данных.
func TestJobBadArgs(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(overrideHome(home))

	r := newTestReg(t, t.TempDir())
	if _, err := run(t, r, "job", map[string]any{"action": "run"}); err == nil {
		t.Error("ожидалась ошибка «укажи command»")
	}
	if _, err := run(t, r, "job", map[string]any{"action": "output", "id": "нет-такого"}); err == nil {
		t.Error("ожидалась ошибка для несуществующей задачи")
	}
	if _, err := run(t, r, "job", map[string]any{"action": "stop", "id": "нет-такого"}); err == nil {
		t.Error("ожидалась ошибка при остановке несуществующей задачи")
	}
	if _, err := run(t, r, "job", map[string]any{"action": "выдумка"}); err == nil {
		t.Error("ожидалась ошибка для неизвестного действия")
	}
	// Пустой реестр задач: статус не падает.
	mustRun(t, r, "job", map[string]any{"action": "status"})
}

// TestJobRegistryBounded — список задач ограничен: за пару часов он иначе
// превращается в свалку, и сигнал «что сейчас работает» тонет в мусоре.
func TestJobRegistryBounded(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(overrideHome(home))

	jobRegistry.mu.Lock()
	jobRegistry.jobs = map[string]*Job{}
	jobRegistry.mu.Unlock()

	for i := 0; i < maxJobs+5; i++ {
		j := &Job{
			ID: "job" + string(rune('a'+i)), Command: "x",
			Started: time.Now().Add(-time.Duration(i) * time.Second),
		}
		registerJob(j)
	}
	jobRegistry.mu.Lock()
	n := len(jobRegistry.jobs)
	jobRegistry.mu.Unlock()
	if n > maxJobs {
		t.Errorf("в реестре %d задач, максимум %d", n, maxJobs)
	}
}

// TestJobConcurrentStatusIsSafe — Status/Exit/Done читают состояние задачи из
// разных горутин (агент, UI, сборщик вывода). Без блокировки это гонка.
func TestJobConcurrentStatusIsSafe(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(overrideHome(home))

	j := startJob("exit 0", t.TempDir(), 0)
	waitJob(t, j)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = j.Status()
			_, _ = j.Exit()
			_ = j.Done()
		}()
	}
	wg.Wait()
}
