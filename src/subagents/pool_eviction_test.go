package subagents

import (
	"fmt"
	"strings"
	"testing"
)

// Регрессия 0.9.1: история запусков субагентов не должна расти без предела.
// Каждый Run держит полный отчёт (Full) для agent_status — без вытеснения
// долгая сессия с активным делегированием копила их без границ.
func TestPoolRunsHistoryEviction(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: true, MaxDepth: 1})

	p.mu.Lock()
	for i := 0; i < 150; i++ {
		id := fmt.Sprintf("r%d", i)
		p.runs[id] = &Run{ID: id, Status: StatusDone, Full: strings.Repeat("x", 100)}
		p.order = append(p.order, id)
	}
	// Идущий запуск в середине истории вытеснять нельзя: его держат UI и Cancel.
	p.runs["live"] = &Run{ID: "live", Status: StatusRunning}
	p.order = append(p.order, "live")
	p.trimRunsLocked()
	kept := len(p.order)
	liveKept := false
	for _, id := range p.order {
		if id == "live" {
			liveKept = true
		}
	}
	_, oldestStillThere := p.runs["r0"]
	p.mu.Unlock()

	if kept != maxRunsHistory {
		t.Fatalf("в истории %d запусков, ожидалось %d", kept, maxRunsHistory)
	}
	if !liveKept {
		t.Fatal("идущий запуск вытеснен из истории")
	}
	if oldestStillThere {
		t.Fatal("старейший завершённый запуск не вытеснен")
	}
}
