package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- spawn_agents: зависимости между субагентами ----------

// spawnOrder — журнал запусков: имя → порядковый номер старта.
type spawnOrder struct {
	mu    sync.Mutex
	seq   int
	names []string
}

func (o *spawnOrder) start(name string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	o.names = append(o.names, name)
	return o.seq
}

func (o *spawnOrder) list() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.names))
	copy(out, o.names)
	return out
}

// after — номер запуска name в журнале (0, если не запускался).
func (o *spawnOrder) after(name string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, n := range o.names {
		if n == name {
			return i
		}
	}
	return 0
}

// waveReg — реестр, где каждый запуск запоминает задачу и порядок старта.
func waveReg(t *testing.T, order *spawnOrder, body func(a SpawnArgs) (SpawnResult, error)) (*Registry, *[]ProgressEvent) {
	t.Helper()
	r, events := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		name := a.Name
		if name == "" {
			name = "(без имени)"
		}
		order.start(name)
		if body != nil {
			return body(a)
		}
		return SpawnResult{Name: a.Name, Summary: "итог " + a.Name, Full: "итог " + a.Name}, nil
	})
	return r, events
}

// chainBatch — три узла: план → реализация → тесты.
func chainBatch() []any {
	return []any{
		map[string]any{"type": "planner", "task": "составь план", "name": "plan"},
		map[string]any{"type": "coder", "task": "реализуй по плану", "name": "impl", "depends_on": "plan"},
		map[string]any{"type": "tester", "task": "напиши тесты", "name": "tests", "depends_on": "impl"},
	}
}

// TestSpawnAgentsWavesRespectOrder — цепочка обязана выполняться по порядку:
// иначе «тесты» стартуют раньше «реализации» и пишут тесты к коду, которого
// ещё нет.
func TestSpawnAgentsWavesRespectOrder(t *testing.T) {
	var order spawnOrder
	r, _ := waveReg(t, &order, nil)
	res, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": chainBatch()})
	if err != nil {
		t.Fatal(err)
	}
	got := order.list()
	want := []string{"plan", "impl", "tests"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("порядок запуска %v, ожидался %v", got, want)
	}
	if !strings.Contains(res.Text, "волн зависимостей") {
		t.Errorf("отчёт не упоминает волны:\n%s", res.Text)
	}
}

// TestSpawnAgentsIndependentStillParallel — узлы без зависимостей обязаны идти
// вместе. Иначе DAG превратил бы пачку в последовательные вызовы.
func TestSpawnAgentsIndependentStillParallel(t *testing.T) {
	var mu sync.Mutex
	var running, maxRunning int
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		mu.Lock()
		running++
		if running > maxRunning {
			maxRunning = running
		}
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return SpawnResult{Name: a.Name, Summary: "ок " + a.Name, Full: "ок"}, nil
	})
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{
			map[string]any{"task": "раз", "name": "a"},
			map[string]any{"task": "два", "name": "b"},
			map[string]any{"task": "три", "name": "c"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if maxRunning < 2 {
		t.Errorf("одновременно работало %d субагента — независимые узлы должны идти параллельно", maxRunning)
	}
}

// TestSpawnAgentsDepsOutputPassedToNext — вывод зависимости должен попасть в
// задачу следующего субагента. Иначе depends_on означал бы «подожди», но не
// «используй результат».
func TestSpawnAgentsDepsOutputPassedToNext(t *testing.T) {
	var mu sync.Mutex
	tasks := map[string]string{}
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		mu.Lock()
		tasks[a.Name] = a.Task
		mu.Unlock()
		return SpawnResult{Name: a.Name, Summary: "ПЛАН: три шага", Full: "ПЛАН: три шага"}, nil
	})
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": chainBatch()}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(tasks["impl"], "ПЛАН: три шага") {
		t.Errorf("в задаче реализации нет вывода плана:\n%s", tasks["impl"])
	}
	if !strings.Contains(tasks["impl"], "Выводы субагентов") {
		t.Errorf("в задаче реализации нет шапки входных данных:\n%s", tasks["impl"])
	}
	// У плана нет зависимостей — блока входа быть не должно.
	if strings.Contains(tasks["plan"], "Выводы субагентов") {
		t.Errorf("у плана не должно быть блока входных данных:\n%s", tasks["plan"])
	}
	// Реализация зависит только от плана, но не от плана И тестов одновременно.
	if strings.Contains(tasks["tests"], "шаг") && strings.Contains(tasks["tests"], "Выводы субагентов") == false {
		t.Errorf("у тестов должен быть блок выводов реализации:\n%s", tasks["tests"])
	}
}

// TestSpawnAgentsDiamondOneWave — два независимых узла после общего источника
// идут вместе.
func TestSpawnAgentsDiamondOneWave(t *testing.T) {
	var mu sync.Mutex
	var running, maxRunning int
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		mu.Lock()
		running++
		if running > maxRunning {
			maxRunning = running
		}
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return SpawnResult{Name: a.Name, Summary: "ок", Full: "ок"}, nil
	})
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{
			map[string]any{"task": "карта", "name": "map"},
			map[string]any{"task": "тесты", "name": "tests", "depends_on": "map"},
			map[string]any{"task": "ревью", "name": "rev", "depends_on": "map"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if got := order.list(); got[0] != "map" {
		t.Errorf("первым должен идти map, порядок %v", got)
	}
	if maxRunning < 2 {
		t.Errorf("во второй волне работало %d — tests и rev независимы", maxRunning)
	}
}

// TestSpawnAgentsFailedDepDoesNotSkip — ошибка зависимости не повод пропускать
// зависимых: частичный отчёт всё равно полезен, а пустой вход вреден. Узел,
// ждавший ЧТО-ТО, чего не будет вовсе (пропуск, отмена), — наоборот пропускается.
func TestSpawnAgentsFailedDepDoesNotSkip(t *testing.T) {
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		if a.Name == "impl" {
			return SpawnResult{}, fmt.Errorf("модель недоступна")
		}
		return SpawnResult{Name: a.Name, Summary: "ок " + a.Name, Full: "ок"}, nil
	})
	res, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": chainBatch()})
	if err != nil {
		t.Fatal(err)
	}
	if got := order.list(); strings.Join(got, ",") != "plan,impl,tests" {
		t.Errorf("запускались %v — после ошибки зависимые всё равно нужны", got)
	}
	if !strings.Contains(res.Text, "ОШИБКА: модель недоступна") {
		t.Errorf("в отчёте нет ошибки реализации:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "ПРОПУЩЕН") {
		t.Errorf("ошибка зависимости не должна давать пропуск:\n%s", res.Text)
	}
	// Порядок отчётов — по аргументам, а не по факту запуска.
	iPlan := strings.Index(res.Text, "## [1] plan")
	iImpl := strings.Index(res.Text, "## [2] impl")
	iTest := strings.Index(res.Text, "## [3] tests")
	if iPlan < 0 || iImpl < 0 || iTest < 0 || !(iPlan < iImpl && iImpl < iTest) {
		t.Errorf("порядок разделов нарушен:\n%s", res.Text)
	}
}

// TestSpawnAgentsRejectsCycle — цикл отклоняется до запуска кого-либо:
//
// Молча пропустить узел нельзя: модель получила бы «2 готовы» вместо трёх и
// вывела бы вывод по неполным данным.
func TestSpawnAgentsRejectsCycle(t *testing.T) {
	var order spawnOrder
	r, _ := waveReg(t, &order, nil)
	_, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{
			map[string]any{"task": "раз", "name": "a", "depends_on": "b"},
			map[string]any{"task": "два", "name": "b", "depends_on": "a"},
		},
	})
	if err == nil {
		t.Fatal("цикл должен отклоняться")
	}
	if !strings.Contains(err.Error(), "цикл") {
		t.Errorf("ошибка не называет цикл: %v", err)
	}
	if got := order.list(); len(got) != 0 {
		t.Errorf("кто-то запустился при цикле: %v", got)
	}
}

// TestSpawnAgentsRejectsUnknownDep — ссылка в пустоту отклоняется до запуска.
func TestSpawnAgentsRejectsUnknownDep(t *testing.T) {
	var order spawnOrder
	r, _ := waveReg(t, &order, nil)
	_, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{
			map[string]any{"task": "раз", "name": "a"},
			map[string]any{"task": "два", "name": "b", "depends_on": "nope"},
		},
	})
	if err == nil {
		t.Fatal("зависимость в пустоту должна отклоняться")
	}
	if got := order.list(); len(got) != 0 {
		t.Errorf("кто-то запустился при плохой зависимости: %v", got)
	}
}

// TestSpawnAgentsPositionalDep — агенты без имени адресуются по «#N».
func TestSpawnAgentsPositionalDep(t *testing.T) {
	var mu sync.Mutex
	secondTask := ""
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		mu.Lock()
		if a.Name == "" {
			secondTask = a.Task
		}
		mu.Unlock()
		return SpawnResult{Summary: "ПЕРВЫЙ ВЫВОД", Full: "ПЕРВЫЙ ВЫВОД"}, nil
	})
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{
			map[string]any{"task": "первый"},
			map[string]any{"task": "второй", "depends_on": "#1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(secondTask, "ПЕРВЫЙ ВЫВОД") {
		t.Errorf("второй узел не получил вывод первого:\n%s", secondTask)
	}
}

// TestSpawnAgentsMergeDeps — узел с двумя предшественниками получает ОБА вывода
// и стартует после обоих, а не в первой волне из-за «первой же ссылки».
func TestSpawnAgentsMergeDeps(t *testing.T) {
	var mu sync.Mutex
	tasks := map[string]string{}
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		mu.Lock()
		tasks[a.Name] = a.Task
		mu.Unlock()
		return SpawnResult{Name: a.Name, Summary: "ВЫВОД " + a.Name, Full: "ВЫВОД " + a.Name}, nil
	})
	res, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{
			map[string]any{"task": "карта кода", "name": "map"},
			map[string]any{"task": "список правок", "name": "diff"},
			map[string]any{"task": "ревью", "name": "rev", "depends_on": []any{"map", "diff"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	revTask := tasks["rev"]
	mu.Unlock()
	if !strings.Contains(revTask, "ВЫВОД map") || !strings.Contains(revTask, "ВЫВОД diff") {
		t.Errorf("ревью не получило оба вывода:\n%s", revTask)
	}
	// Внутри волны порядок не задан — это параллельные горутины. Задано
	// только одно: rev стартует после обоих, то есть последним.
	got := order.list()
	if len(got) != 3 || got[2] != "rev" {
		t.Errorf("порядок запуска %v — rev обязан ждать обоих и идти последним", got)
	}
	if !strings.Contains(res.Text, "2 волн") {
		t.Errorf("отчёт не показывает слияние в 2 волны:\n%s", res.Text)
	}
}

// TestSpawnAgentsMergeSkipsOnOneMissingDep — если хоть одна зависимость
// не выполнилась, узел пропускается: половинчатый вход хуже явного пропуска.
func TestSpawnAgentsMergeSkipsOnOneMissingDep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		if a.Name == "diff" {
			cancel() // вторая ветвь не выполнится
		}
		return SpawnResult{Name: a.Name, Summary: "ок", Full: "ок"}, nil
	})
	res, err := r.hSpawnAgents(ctx, map[string]any{
		"agents": []any{
			map[string]any{"task": "карта кода", "name": "map"},
			map[string]any{"task": "список правок", "name": "diff"},
			map[string]any{"task": "ревью", "name": "rev", "depends_on": []any{"map", "diff"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order.list(), ","); strings.Contains(got, "rev") {
		t.Errorf("ревью запустилось без обеих зависимостей: %q", got)
	}
	if !strings.Contains(res.Text, "ПРОПУЩЕН") {
		t.Errorf("в отчёте нет пропуска ревью:\n%s", res.Text)
	}
	// Узлы волны сопоставляются со своими разделами: «map» не должен получить
	// отчёт «diff» (и наоборот) — регрессия на карту индексов.
	if !strings.Contains(res.Text, "## [1] map") || !strings.Contains(res.Text, "## [2] diff") {
		t.Errorf("разделы отчёта не совпали с порядком аргументов:\n%s", res.Text)
	}
}

// TestSpawnAgentsMergeBudgetShared — бюджет входа общий на узел: два длинных
// вывода не занимают каждый по spawnDepsBudget.
func TestSpawnAgentsMergeBudgetShared(t *testing.T) {
	long := strings.Repeat("я", spawnDepsBudget)
	var mu sync.Mutex
	revTask := ""
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		mu.Lock()
		if a.Name == "rev" {
			revTask = a.Task
		}
		mu.Unlock()
		return SpawnResult{Name: a.Name, Summary: long, Full: long}, nil
	})
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{
			map[string]any{"task": "карта кода", "name": "map"},
			map[string]any{"task": "список правок", "name": "diff"},
			map[string]any{"task": "ревью", "name": "rev", "depends_on": []any{"map", "diff"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if n := strings.Count(revTask, "я"); n > spawnDepsBudget {
		t.Errorf("в задаче %d символов вывода, общий бюджет %d", n, spawnDepsBudget)
	}
	if !strings.Contains(revTask, "map") {
		t.Errorf("первый вывод должен попасть в задачу:\n%s", revTask[:min(len(revTask), 200)])
	}
	if strings.Contains(revTask, "--- diff ---") {
		t.Error("второй вывод не должен попасть — бюджет уже исчерпан")
	}
}

// TestSpawnAgentsCancelledBatchSkipped — после отмены оставшиеся волны
// пропускаются, и пачка закрывается отчётом.
func TestSpawnAgentsCancelledBatchSkipped(t *testing.T) {
	var order spawnOrder
	ctx, cancel := context.WithCancel(context.Background())
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		cancel()
		return SpawnResult{Name: a.Name, Summary: "ок", Full: "ок"}, nil
	})
	res, err := r.hSpawnAgents(ctx, map[string]any{"agents": chainBatch()})
	if err != nil {
		t.Fatal(err)
	}
	if got := order.list(); strings.Join(got, ",") != "plan" {
		t.Errorf("после отмены запустились %v, ожидался только plan", got)
	}
	if !strings.Contains(res.Text, "ПРОПУЩЕН") {
		t.Errorf("в отчёте нет пропуска после отмены:\n%s", res.Text)
	}
}

// TestSpawnDepsBudgetTruncates — бюджет входа общий на узел: длинные отчёты
// обрезаются, иначе окно субагента переполняется чужими словами.
func TestSpawnDepsBudgetTruncates(t *testing.T) {
	long := strings.Repeat("я", spawnDepsBudget*2)
	var mu sync.Mutex
	secondTask := ""
	var order spawnOrder
	r, _ := waveReg(t, &order, func(a SpawnArgs) (SpawnResult, error) {
		mu.Lock()
		if a.Name == "" {
			secondTask = a.Task
		}
		mu.Unlock()
		return SpawnResult{Summary: long, Full: long}, nil
	})
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{
			map[string]any{"task": "первый"},
			map[string]any{"task": "второй", "depends_on": "#1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(secondTask, long) {
		t.Error("вывод зависимости не обрезан — окно субагента переполнится")
	}
	if n := strings.Count(secondTask, "я"); n > spawnDepsBudget {
		t.Errorf("в задаче %d символов вывода, бюджет %d", n, spawnDepsBudget)
	}
}

// TestSpawnAgentsWaveProgressReported — пользователь должен видеть движение по
// волнам, иначе при трёх волнах он смотрит в пустоту до конца.
func TestSpawnAgentsWaveProgressReported(t *testing.T) {
	var order spawnOrder
	r, events := waveReg(t, &order, nil)
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": chainBatch()}); err != nil {
		t.Fatal(err)
	}
	waves := 0
	for _, ev := range *events {
		if strings.Contains(ev.Label, "волна") {
			waves++
		}
	}
	if waves != 3 {
		t.Errorf("событий прогресса про волны: %d, ожидалось 3", waves)
	}
	var final bool
	for _, ev := range *events {
		if ev.Final {
			final = true
		}
	}
	if !final {
		t.Error("нет финального события прогресса")
	}
}
