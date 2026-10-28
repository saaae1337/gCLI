package main

// Регрессии сериализации ходов. Ход запускается из двух миров: читатель
// ввода (REPL-цикл, TUI-горутина) и фон (cron). Без общего замка cronTick,
// проскочивший между проверкой running и её установкой в turn, начинал
// второй ход параллельно первому: два агента писали в один поток, второй
// затирал a.cancel — Ctrl+C отменял только последнего, а сессия
// сохранялась дважды наперегонки.

import (
	"sync"
	"testing"
	"time"
)

// TestCronTurnYieldsToActiveUser — пользователь только что активен
// (ввод или ход обновили cronLastActive): cron-итерация не запускается,
// даже если тикер успел проскочить между проверками в cronTick.
func TestCronTurnYieldsToActiveUser(t *testing.T) {
	a, _ := cmdApp(t)
	a.cronMu.Lock()
	a.cronEnabled = true
	a.cronInterval = 15 * time.Minute
	a.cronLastActive = time.Now() // только что был ввод пользователя
	a.cronMu.Unlock()

	if err := a.cronTurn("тест"); err != nil {
		t.Fatalf("cronTurn вернул ошибку на активном пользователе: %v", err)
	}
	a.mu.Lock()
	running, interactive := a.running, a.turnInteractive
	a.mu.Unlock()
	if running {
		t.Error("cron-итерация не должна была начаться: пользователь активен")
	}
	if interactive {
		t.Error("ход не начинался — флаг интерактивности не мог выставиться")
	}
}

// TestCronTurnWaitsForLockAndRechecks — пока cron ждёт turnMu под идущим
// ходом, пользователь вернулся и ввёл что-то, а затем выключил /cron off:
// после снятия замка итерация обязана отказаться, а не бить вдогонку.
// Заодно проверяет сам замок: cronTurn не может пройти, пока turnMu занят.
func TestCronTurnWaitsForLockAndRechecks(t *testing.T) {
	a, _ := cmdApp(t)
	a.cronMu.Lock()
	a.cronEnabled = true
	a.cronInterval = 15 * time.Minute
	a.cronLastActive = time.Now().Add(-time.Hour) // тишина — время пришло
	a.cronMu.Unlock()

	a.turnMu.Lock() // имитируем идущий ход
	done := make(chan error, 1)
	go func() {
		done <- a.cronTurn("тест")
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("cronTurn прошёл под занятым turnMu: %v", err)
	default:
		// Ждёт очереди — правильно.
	}

	// Пользователь активничал и выключил cron, пока итерация ждала.
	a.cronMu.Lock()
	a.cronEnabled = false
	a.cronMu.Unlock()
	a.turnMu.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cronTurn вернул ошибку после отказа: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cronTurn не вернулся после снятия turnMu")
	}
	a.mu.Lock()
	running := a.running
	a.mu.Unlock()
	if running {
		t.Error("после /cron off итерация не должна была начаться")
	}
}

// TestCronTurnAfterIdleUserAcquiresLock — пользователь молчал дольше
// интервала, но вернулся прямо перед ходом: перепроверка под замком
// обязана увидеть свежую метку и отменить итерацию. Замок здесь
// свободен — проверяется именно отмена по активности, а не ожидание.
func TestCronTurnAfterIdleUserAcquiresLock(t *testing.T) {
	a, _ := cmdApp(t)
	a.cronMu.Lock()
	a.cronEnabled = true
	a.cronInterval = 15 * time.Minute
	a.cronLastActive = time.Now().Add(-time.Hour)
	a.cronMu.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Пользователь вернулся прямо перед ходом: метка свежее интервала.
		a.cronMu.Lock()
		a.cronLastActive = time.Now()
		a.cronMu.Unlock()
		if err := a.cronTurn("тест"); err != nil {
			t.Errorf("cronTurn вернул ошибку: %v", err)
		}
	}()
	wg.Wait()

	a.mu.Lock()
	running := a.running
	a.mu.Unlock()
	if running {
		t.Error("итерация началась, хотя пользователь вернулся перед ходом")
	}
}
