package core

// Регрессионные тесты багфикс-прохода 6.2.1: id сессии из сети и
// параллельный доступ к трекеру прогона.

import (
	"os"
	"path/filepath"
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

// TestDeleteSessionAcceptsJSONSuffix — LoadSession принимает и «id», и
// «id.json»; удаление обязано понимать те же имена.
func TestDeleteSessionAcceptsJSONSuffix(t *testing.T) {
	r := testRepo(t)
	s := r.NewSession("провайдер", "модель", t.TempDir())
	if err := r.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteSession(s.ID + ".json"); err != nil {
		t.Fatalf("DeleteSession с суффиксом .json: %v", err)
	}
	if _, err := r.LoadSession(s.ID); err == nil {
		t.Error("сессия должна быть удалена")
	}
}

// TestUndoCheckpointReportsRemoveError — если созданный файл не удаляется,
// откат не состоялся, и молча искать более старый чекпоинт нельзя.
func TestUndoCheckpointReportsRemoveError(t *testing.T) {
	r := testRepo(t)
	// Непустой каталог: os.Remove на нём падает и на Windows, и на Unix.
	notFile := filepath.Join(t.TempDir(), "каталог")
	if err := os.MkdirAll(notFile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notFile, "внутри"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{Checkpoints: []CheckpointMeta{{Path: notFile, Existed: false}}}
	if _, ok, err := r.UndoCheckpoint(s); err == nil || ok {
		t.Errorf("ошибка удаления должна дойти до вызывающего: ok=%v err=%v", ok, err)
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
