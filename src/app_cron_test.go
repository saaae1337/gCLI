package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCronNextInterval — интервал между итерациями: 15–20 минут.
func TestCronNextInterval(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := cronNextInterval()
		if d < 15*time.Minute || d > 20*time.Minute {
			t.Fatalf("интервал %v вне диапазона 15–20 мин", d)
		}
	}
}

// TestCronUniversalPrompt — промпт не пустой и содержит ключевые разделы.
func TestCronUniversalPrompt(t *testing.T) {
	p := cronUniversalPrompt()
	if p == "" {
		t.Fatal("пустой промпт")
	}
	for _, want := range []string{"баги", "уязвимости", "проект", "отчёт"} {
		if !strings.Contains(p, want) {
			t.Errorf("промпт не содержит раздел %q", want)
		}
	}
}

// TestShutdownCronConcurrent — регрессия 0.9.1: shutdown() закрывал
// a.cronDone без cronMu и наперегонку с /cron off (тоже close под
// cronMu) падал «close of closed channel», а cronLoop читал поле
// a.cronDone в select без мьютекса. Теперь close — только под cronMu,
// горутина снимает канал один раз при старте. Восемь параллельных
// shutdown должны завершиться без паники и гонок (проверяется -race).
func TestShutdownCronConcurrent(t *testing.T) {
	a := &app{}
	a.cronDone = make(chan struct{})
	go a.cronLoop()
	go a.cronTouch()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.shutdown()
		}()
	}
	wg.Wait()

	a.cronMu.Lock()
	closed := a.cronDone == nil
	a.cronMu.Unlock()
	if !closed {
		t.Fatal("shutdown не остановил cron-горутину")
	}
}
