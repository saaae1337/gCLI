package core

// Регрессионные тесты багфикс-прохода 6.2.1: id сессии из сети и
// параллельный доступ к трекеру прогона.

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLoadSessionRejectsBadID — id приходит и из HTTP (?session=...):
// «../../x» раньше дотягивался до произвольных json-файлов машины.
func TestLoadSessionRejectsBadID(t *testing.T) {
	r := testRepo(t)
	for _, bad := range []string{"../../config", "a/b", "..", "", ".x"} {
		if _, err := r.LoadSession(bad); err == nil {
			t.Errorf("LoadSession(%q) обязан отказать", bad)
		}
	}
	if _, err := r.LoadSession("нет-такой-сессии-123"); err == nil {
		t.Error("несуществующая сессия должна вернуть ошибку")
	}
}

// TestDeleteSessionRejectsBadID — RemoveAll по id из сети без проверки —
// это удаление произвольного каталога чекпоинтов.
func TestDeleteSessionRejectsBadID(t *testing.T) {
	r := testRepo(t)
	if err := r.DeleteSession("../не-туда"); err == nil {
		t.Error("DeleteSession с «..» обязан отказать")
	}
}

func TestTrackerStopRacesTick(t *testing.T) {
	// Дым от -race: Tick из агентной горутины против Stop/Status из HTTP.
	tr := NewTracker(Mission{Mode: MissionLongTime, TokenBudget: 10_000_000}.Apply(), 0, 0)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					tr.Tick(1, 1, 1000)
				}
			}
		}()
	}
	deadline := time.After(120 * time.Millisecond)
	for {
		select {
		case <-deadline:
			close(stop)
			wg.Wait()
			if tr.Stopped() == "" {
				tr.Stop(StopCancelled)
			}
			_ = tr.Status()
			return
		default:
			_ = tr.Status()
			_ = tr.Iters()
			tr.Stop(StopCancelled)
		}
	}
}

func testRepo(t *testing.T) *Repo {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	r := Open()
	r.Store.Ensure()
	if r == nil {
		t.Fatal("репозиторий не открылся")
	}
	if strings.TrimSpace(home) == "" {
		t.Fatal("пустой GCLI_HOME")
	}
	return r
}
