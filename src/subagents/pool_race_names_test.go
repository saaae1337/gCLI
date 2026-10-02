package subagents

// Регрессии 5.5.0: гонки при параллельных субагентах и честная классификация
// отменённого запуска.

import (
	"context"
	"sync"
	"testing"
	"time"
)

// raceRunner — заглушка, всегда возвращающая отчёт.
func raceRunner() Runner {
	return func(ctx context.Context, spec Spec) (Outcome, error) {
		return Outcome{Full: "отчёт по задаче: сделано"}, nil
	}
}

// racePool — пул для проверок гонок: параллельность заведомо выше единицы.
func racePool(r Runner) *Pool {
	return NewPool(r, PoolOptions{Enabled: true, MaxParallel: 8, WorkDir: ""})
}

// TestConcurrentAutoNamesAreUnique — параллельные запуски без имени получают
// разные имена. Счётчик берётся из nameSeq под mu, поэтому раньше два
// вызова успевали высчитать одно и то же «explorer-1».
func TestConcurrentAutoNamesAreUnique(t *testing.T) {
	p := racePool(raceRunner())
	const n = 24

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := p.Spawn(context.Background(), Spec{
				Type: TypeExplorer,
				Task: "задача " + string(rune('a'+i)),
			}); err != nil {
				t.Errorf("Spawn: %v", err)
			}
		}(i)
	}
	wg.Wait()

	runs := p.All()
	if len(runs) != n {
		t.Fatalf("запусков в журнале %d, ждали %d", len(runs), n)
	}
	uniq := map[string]bool{}
	for _, r := range runs {
		if uniq[r.Name] {
			t.Errorf("в журнале два запуска с именем %q", r.Name)
		}
		uniq[r.Name] = true
	}
}

// TestAutoNameSequential — обычный порядок имён не сломан счётчиком.
func TestAutoNameSequential(t *testing.T) {
	p := racePool(raceRunner())
	want := []string{"explorer-1", "explorer-2", "explorer-3"}
	for i := range want {
		if _, err := p.Spawn(context.Background(), Spec{
			Type: TypeExplorer,
			Task: "задача " + string(rune('1'+i)),
		}); err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
	}
	runs := p.All()
	if len(runs) != len(want) {
		t.Fatalf("запусков %d, ждали %d", len(runs), len(want))
	}
	// All() отдаёт новые первыми: последний запуск стоит в начале списка.
	order := []string{"explorer-3", "explorer-2", "explorer-1"}
	for i, w := range order {
		if got := runs[i].Name; got != w {
			t.Errorf("запуск %d: имя %q, ждали %q", i, got, w)
		}
	}
}

// TestCanceledRunIsNotDone — прерванный субагент не должен попасть в журнал
// как успешный. Agent.Run глотает context.Canceled и отдаёт частичный отчёт
// без ошибки, поэтому проверка только err давала StatusDone, и главный агент
// опирался на текст, оборванный на полуслове.
func TestCanceledRunIsNotDone(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	p := NewPool(func(ctx context.Context, spec Spec) (Outcome, error) {
		close(started)
		<-release
		// Отчёт непустой, ошибки нет — как при реальной отмене.
		return Outcome{Full: "начал и не дописал"}, nil
	}, PoolOptions{Enabled: true, MaxParallel: 2, WorkDir: ""})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Spawn(ctx, Spec{Type: TypeExplorer, Task: "долгая задача"})
		done <- err
	}()
	<-started
	cancel()
	close(release)
	<-done

	runs := p.All()
	if len(runs) != 1 {
		t.Fatalf("запусков в журнале %d, ждали 1", len(runs))
	}
	if runs[0].Status == StatusDone {
		t.Errorf("прерванный субагент записан как готовый: Status=%q, Full=%q",
			runs[0].Status, runs[0].Full)
	}
}

// TestCanceledBeforeSpawnDoesNotRegisterRun — отмена до старта не должна
// оставлять «полуготовый» запуск в журнале и не должна занимать кеш.
func TestCanceledBeforeSpawnDoesNotRegisterRun(t *testing.T) {
	called := 0
	p := NewPool(func(ctx context.Context, spec Spec) (Outcome, error) {
		called++
		return Outcome{Full: "отчёт"}, nil
	}, PoolOptions{Enabled: true, MaxParallel: 2, WorkDir: ""})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Spawn(ctx, Spec{Type: TypeExplorer, Task: "задача"}); err == nil {
		t.Error("Spawn на отменённом контексте должен вернуть ошибку")
	}
	if called != 0 {
		t.Errorf("субагент запущен %d раз при отменённом контексте", called)
	}
	if runs := p.All(); len(runs) != 0 {
		t.Errorf("в журнале %d запусков при отмене до старта", len(runs))
	}
}

// TestCancelDuringRunNotCached — прерванный отчёт не должен попасть в кеш:
// иначе повтор той же задачи получил бы оборванный текст как годный ответ.
func TestCancelDuringRunNotCached(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	p := NewPool(func(ctx context.Context, spec Spec) (Outcome, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		if ctx.Err() != nil {
			// Так ведёт себя RunSubagent при отмене: отчёт есть, ошибки нет.
			return Outcome{Full: "Субагент прерван: context canceled\n\nЧто успел: мало"}, nil
		}
		return Outcome{Full: "полный честный отчёт: сделано всё, проверено"}, nil
	}, PoolOptions{Enabled: true, MaxParallel: 2, WorkDir: ""})

	// Первая попытка — отменённая.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Spawn(ctx, Spec{Type: TypeExplorer, Task: "одна и та же задача"}); err == nil {
		t.Fatal("ожидалась ошибка отмены")
	}

	// Вторая попытка с живым контекстом обязана реально запустить субагента.
	out, err := p.Spawn(context.Background(), Spec{Type: TypeExplorer, Task: "одна и та же задача"})
	if err != nil {
		t.Fatalf("повторная попытка: %v", err)
	}
	if out.Full == "" {
		t.Error("повторная попытка вернула пустой отчёт")
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("субагент выполнен %d раз, ждали 1 (отменённый отчёт попал в кеш)", got)
	}
}

// TestSpawnSlotLimitWithRaces — лимит параллельности держится под нагрузкой.
func TestSpawnSlotLimitWithRaces(t *testing.T) {
	var mu sync.Mutex
	var live, peak int
	p := NewPool(func(ctx context.Context, spec Spec) (Outcome, error) {
		mu.Lock()
		live++
		if live > peak {
			peak = live
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		live--
		mu.Unlock()
		return Outcome{Full: "отчёт: сделано"}, nil
	}, PoolOptions{Enabled: true, MaxParallel: 3, WorkDir: ""})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = p.Spawn(context.Background(), Spec{
				Type: TypeExplorer,
				Task: "задача " + string(rune('a'+i)),
			})
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if peak > 3 {
		t.Errorf("пик одновременных запусков %d при лимите 3", peak)
	}
}
