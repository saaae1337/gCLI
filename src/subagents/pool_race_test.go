package subagents

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gcli/core"
)

// mkSpec — заполнить обязательные поля Spec, чтобы время в тесте
// не уходило на разбор необязательных.
func mkSpec(name string) Spec {
	return Spec{Name: name, Type: "explorer", Task: "исследовать " + name, Depth: 1}
}

// readRun — снимок полей Run так, как их читает UI.
func readRun(r *Run) {
	if r == nil {
		return
	}
	_ = r.ID
	_ = r.Name
	_ = r.Type
	_ = r.Task
	_ = r.Model
	_ = r.Status
	_ = r.Err
	_ = r.Full
	_ = r.Summary
	_ = r.Turns
	_ = r.Tools
	_ = r.Usage
	_ = r.Started
	_ = r.Finished
	_ = r.Depth
}

// TestPoolConcurrentReadsWhileRunning — гонка между записью результата
// и чтением полей Run из UI. Без блокировки -race ловит data race.
func TestPoolConcurrentReadsWhileRunning(t *testing.T) {
	var peak, cur int
	var mu sync.Mutex

	p := NewPool(func(ctx context.Context, s Spec) (Outcome, error) {
		mu.Lock()
		cur++
		if cur > peak {
			peak = cur
		}
		mu.Unlock()

		time.Sleep(15 * time.Millisecond)

		mu.Lock()
		cur--
		mu.Unlock()

		return Outcome{
			Full:    fmt.Sprintf("# Отчёт по %s\n- пункт 1\n- пункт 2", s.Name),
			Summary: Summarize("отчёт"),
			Turns:   4,
			Tools:   9,
		}, nil
	}, PoolOptions{MaxParallel: 4, MaxDepth: 1, Enabled: true})

	// Наблюдатель имитирует UI: читает все поля Run, пока субагенты работа.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, r := range p.All() {
					readRun(r)
				}
				p.Running()
				p.All()
			}
		}()
	}

	const n = 24
	var runs sync.WaitGroup
	for i := 0; i < n; i++ {
		runs.Add(1)
		go func(i int) {
			defer runs.Done()
			if _, err := p.Spawn(context.Background(), mkSpec(fmt.Sprintf("sub-%02d", i))); err != nil {
				t.Errorf("запуск %d: %v", i, err)
			}
		}(i)
	}
	runs.Wait()
	close(stop)
	wg.Wait()

	if peak < 2 {
		t.Errorf("ожидалась параллельность (пик >= 2), пик = %d", peak)
	}
	if got := len(p.All()); got != n {
		t.Errorf("ожидалось %d запусков в журнале, получено %d", n, got)
	}
}

// TestPoolCancelWhileRunning — отмена контекста во время работы пула.
func TestPoolCancelWhileRunning(t *testing.T) {
	started := make(chan struct{}, 8)
	runner := func(ctx context.Context, s Spec) (Outcome, error) {
		started <- struct{}{}
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	}
	p := NewPool(runner, PoolOptions{MaxParallel: 2, MaxDepth: 1, Enabled: true})

	ctx, cancel := context.WithCancel(context.Background())
	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = p.Spawn(ctx, mkSpec(fmt.Sprintf("c-%d", i)))
		}(i)
	}
	// Ждём, пока хотя бы часть запусков реально стартует.
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("субагенты не стартовали")
		}
	}
	cancel()
	wg.Wait()

	// Слоты должны освободиться: иначе пул утекает слоты.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Running() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := p.Running(); got != 0 {
		t.Errorf("после отмены осталось занятых субагентов: %d", got)
	}
}

// TestPoolUsageAccumulation — параллельные запуски не теряют usage.
func TestPoolUsageAccumulation(t *testing.T) {
	p := NewPool(func(ctx context.Context, s Spec) (Outcome, error) {
		return Outcome{Full: "отчёт " + s.Name, Usage: core.Usage{PromptTokens: 10, CompletionTokens: 20}}, nil
	}, PoolOptions{MaxParallel: 4, MaxDepth: 1, Enabled: true})

	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := p.Spawn(context.Background(), mkSpec(fmt.Sprintf("u-%02d", i))); err != nil {
				t.Errorf("запуск %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	var total core.Usage
	for _, r := range p.All() {
		total.Add(r.Usage)
	}
	if total.PromptTokens != 10*n || total.CompletionTokens != 20*n {
		t.Errorf("usage потерян: получено %+v, ожидалось input=%d output=%d", total, 10*n, 20*n)
	}
}
