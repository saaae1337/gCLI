package tui

import (
	"io"
	"sync"
	"testing"
	"time"
)

// TestAsyncUIMethodsRace — Toast/SetMascot/SetCronEnabled обязаны
// трогать dirty под мьютексом. Раньше markDirty звался после Unlock:
// -race ловил гонку с циклом Run, читающим тот же флаг каждый тик.
func TestAsyncUIMethodsRace(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	app := newTestApp(pr, &out)
	done := make(chan error, 1)
	go func() { done <- app.Run(Handlers{}) }()
	waitFor(t, "запуск", app.Running)

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
				app.Toast("т")
				app.SetMascot(true)
				app.SetCronEnabled(true)
				app.SetMascot(false)
				app.SetCronEnabled(false)
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	app.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился")
	}
}
