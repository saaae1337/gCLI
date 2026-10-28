package main

// Регрессии гонок на общих полях app: ход агента зовётся не только из
// горутины ввода (TUI/REPL), но и из cron, watch и цепочки миссий, а
// слэш-команды читают то же самое из другой горутины. Без лока это
// ловится только гоночным детектором — поведение остаётся корректным
// годами, пока однажды не выстрелит.

import (
	"errors"
	"fmt"
	"sync"
	"testing"
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
