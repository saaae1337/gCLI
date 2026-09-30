package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gcli/subagents"
)

// ---------- spawn_agents: параллельные субагенты ----------

// newSpawnReg — реестр с подменённым запуском субагентов и сбором прогресса.
func newSpawnReg(t *testing.T, spawn func(ctx context.Context, args SpawnArgs) (SpawnResult, error)) (*Registry, *[]ProgressEvent) {
	t.Helper()
	var mu sync.Mutex
	events := []ProgressEvent{}
	r := New(Env{
		WorkDir:  t.TempDir(),
		MaxDepth: 1,
		Spawn:    spawn,
		OnProgress: func(ev ProgressEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})
	return r, &events
}

// spawnTasks — N задач для spawn_agents.
func spawnTasks(n int) []any {
	out := make([]any, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, map[string]any{
			"type": "explorer",
			"task": fmt.Sprintf("задача %d", i),
			"name": fmt.Sprintf("agent%d", i),
		})
	}
	return out
}

func TestSpawnAgentsReportsInArgumentOrder(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		// Первый — самый долгий: порядок отчётов не должен зависеть от скорости.
		if a.Name == "agent1" {
			time.Sleep(60 * time.Millisecond)
		}
		return SpawnResult{Name: a.Name, Summary: "итог " + a.Name, Full: "итог " + a.Name}, nil
	})
	res, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": spawnTasks(3)})
	if err != nil {
		t.Fatal(err)
	}
	i1 := strings.Index(res.Text, "итог agent1")
	i2 := strings.Index(res.Text, "итог agent2")
	i3 := strings.Index(res.Text, "итог agent3")
	if i1 < 0 || i2 < 0 || i3 < 0 {
		t.Fatalf("в отчёте нет части отчётов:\n%s", res.Text)
	}
	if !(i1 < i2 && i2 < i3) {
		t.Errorf("порядок нарушен (agent1=%d agent2=%d agent3=%d):\n%s", i1, i2, i3, res.Text)
	}
	if !strings.Contains(res.Summary, "3 готовы") {
		t.Errorf("сводка: %s", res.Summary)
	}
}

func TestSpawnAgentsRunsConcurrently(t *testing.T) {
	const n = 4
	var mu sync.Mutex
	live, maxLive := 0, 0
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		mu.Lock()
		live++
		if live > maxLive {
			maxLive = live
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		live--
		mu.Unlock()
		return SpawnResult{Name: a.Name, Summary: "ok"}, nil
	})
	start := time.Now()
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents":   spawnTasks(n),
		"parallel": n,
	}); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	mu.Lock()
	got := maxLive
	mu.Unlock()
	if got < 2 {
		t.Errorf("субагенты шли последовательно (максимум одновременно %d)", got)
	}
	// 4 × 50 мс последовательно = 200 мс; параллельно — около 50.
	if elapsed > 150*time.Millisecond {
		t.Errorf("пачка заняла %v — похоже на последовательный запуск", elapsed)
	}
}

func TestSpawnAgentsIsolatesErrors(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		if a.Name == "agent2" {
			return SpawnResult{}, fmt.Errorf("модель недоступна")
		}
		return SpawnResult{Name: a.Name, Summary: "итог " + a.Name}, nil
	})
	res, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": spawnTasks(3)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "модель недоступна") {
		t.Errorf("ошибка цели не попала в отчёт:\n%s", res.Text)
	}
	for _, n := range []string{"agent1", "agent3"} {
		if !strings.Contains(res.Text, "итог "+n) {
			t.Errorf("отчёт %s потерян из-за ошибки соседа:\n%s", n, res.Text)
		}
	}
	if !strings.Contains(res.Summary, "2 готовы, 1 с ошибкой") {
		t.Errorf("сводка: %s", res.Summary)
	}
}

func TestSpawnAgentsRejectsOversizedBatch(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		return SpawnResult{Name: a.Name, Summary: "ok"}, nil
	})
	_, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": spawnTasks(spawnMaxAgents + 1),
	})
	if err == nil || !strings.Contains(err.Error(), "слишком много субагентов") {
		t.Errorf("лимит пачки не сработал: %v", err)
	}
}

func TestSpawnAgentsRequiresTask(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		return SpawnResult{Name: a.Name, Summary: "ok"}, nil
	})
	_, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": []any{map[string]any{"type": "explorer", "task": "  "}},
	})
	if err == nil || !strings.Contains(err.Error(), "нужна задача") {
		t.Errorf("пустая задача не отклонена: %v", err)
	}
}

func TestSpawnAgentsBlockedAtMaxDepth(t *testing.T) {
	spawned := 0
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		spawned++
		return SpawnResult{Name: a.Name, Summary: "ok"}, nil
	})
	// Depth == MaxDepth — субагенты запрещены.
	sub := r.Restrict(nil, []string{"write_file", "edit_file", "bash", "spawn_agent", "ask_user"})
	_ = sub
	deep := New(Env{WorkDir: t.TempDir(), Depth: 1, MaxDepth: 1, Spawn: func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		spawned++
		return SpawnResult{Name: a.Name, Summary: "ok"}, nil
	}})
	res, err := deep.hSpawnAgents(context.Background(), map[string]any{"agents": spawnTasks(2)})
	if err != nil {
		t.Fatal(err)
	}
	if spawned != 0 {
		t.Errorf("на предельной глубине запущено %d субагентов", spawned)
	}
	if !strings.Contains(res.Text, "максимальная глубина вложенности") {
		t.Errorf("не сказано про лимит глубины:\n%s", res.Text)
	}
}

// TestSpawnAgentsMissingInSubagentProfile — spawn_agents вырезан из набора
// инструментов каждого типа субагента (иначе 6×6×6 задач на третьем уровне).
func TestSpawnAgentsMissingInSubagentProfile(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	for _, tp := range subagents.Types {
		allow, deny := subagents.ToolsFor(tp)
		if inList(allow, "spawn_agents") {
			t.Errorf("тип %s: spawn_agents попал в белый список %v", tp, allow)
		}
		if !inList(deny, "spawn_agents") {
			t.Errorf("тип %s: spawn_agents не в чёрном списке %v", tp, deny)
		}
	}
	sub := r.Base().Restrict(nil, subagentsDeny())
	if sub.Has("spawn_agents") || sub.Has("spawn_agent") {
		t.Errorf("мета-инструменты остались в профиле субагента: %v", sub.Names())
	}
}

func TestSpawnAgentsOffByDefault(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	res, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": spawnTasks(1)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Errorf("без /agents on инструмент должен объяснять, что субагенты выключены:\n%s", res.Text)
	}
}

func TestSpawnAgentsFiresProgress(t *testing.T) {
	r, events := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		if a.Name == "agent2" {
			return SpawnResult{}, fmt.Errorf("бум")
		}
		return SpawnResult{Name: a.Name, Summary: "ок"}, nil
	})
	if _, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": spawnTasks(3)}); err != nil {
		t.Fatal(err)
	}
	evs := *events
	if len(evs) != 4 {
		t.Fatalf("событий %d, ожидалось 4 (3 цели + финал)", len(evs))
	}
	seen := map[int]bool{}
	var failed *ProgressEvent
	for i := range evs[:3] {
		ev := evs[i]
		if ev.Title != "spawn_agents" || ev.Total != 3 {
			t.Errorf("событие %d: %+v", i, ev)
		}
		if ev.Done < 1 || ev.Done > 3 {
			t.Errorf("событие %d: Done=%d вне диапазона", i, ev.Done)
		}
		if seen[ev.Done] {
			t.Errorf("номера прогресса повторяются: %d", ev.Done)
		}
		seen[ev.Done] = true
		if !ev.Ok {
			f := ev
			failed = &f
		}
	}
	// Порядок событий не задан (цели идут параллельно) — ищем упавшую по флагу.
	if failed == nil {
		t.Fatalf("ни одно событие не отметило ошибку: %+v", evs)
	}
	if failed.Err == "" {
		t.Errorf("у пачки нет текста ошибки цели: %+v", failed)
	}
	last := evs[3]
	if !last.Final || !last.Ok || last.Done != 3 {
		t.Errorf("финальное событие: %+v", last)
	}
}

func TestSpawnAgentsIsRegistered(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	r.RegisterSubagentTools()
	if !r.Has("spawn_agents") {
		t.Fatalf("spawn_agents не зарегистрирован")
	}
	if r.Get("spawn_agents").Def.Schema == "" {
		t.Errorf("у spawn_agents пустая схема")
	}
}

// subagentsDeny — список инструментов, недоступных субагенту.
func subagentsDeny() []string {
	return []string{"spawn_agent", "spawn_agents", "ask_user"}
}

// inList — есть ли значение в срезе строк.
func inList(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
