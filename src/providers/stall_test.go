package providers

// Регрессии watchdog тишины SSE. Сервер, который после 200 OK замолчал
// (перегруженный прокси, полумёртвое соединение), раньше держал чтение
// потока вечно: ход выглядел «думающим» до Ctrl+C. Теперь тишина дольше
// sseStallTimeout обрывает поток: до первого события — повторяемой
// ошибкой, после — финальной (часть ответа уже доставлена).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withStall — укороченный предел тишины на время теста.
func withStall(t *testing.T, d time.Duration) {
	t.Helper()
	old := sseStallTimeout
	sseStallTimeout = d
	t.Cleanup(func() { sseStallTimeout = old })
}

// TestSSEStallBeforeFirstEvent — тишина до первого события: ошибка
// повторяемая, агент сделает до трёх попыток с другим ключом или тем же.
func TestSSEStallBeforeFirstEvent(t *testing.T) {
	withStall(t, 150*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush() // заголовки ушли — дальше тишина
		}
		time.Sleep(time.Second)
	}))
	defer srv.Close()

	got, err := collect(t, NewClient(), srv.URL, context.Background())
	if err == nil {
		t.Fatalf("ожидался обрыв тишины, получены события %v", got)
	}
	if !IsRetryable(err) {
		t.Fatalf("до первого события повтор возможен: %v", err)
	}
	if !strings.Contains(err.Error(), "молчал") {
		t.Errorf("причина должна быть названа: %v", err)
	}
}

// TestSSEStallMidStreamFinal — тишина посреди ответа: ошибка финальная,
// повтор продублировал бы уже доставленный текст.
func TestSSEStallMidStreamFinal(t *testing.T) {
	withStall(t, 150*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: половина\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(time.Second)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = NewClient().doSSE(context.Background(), req, func(data string) error {
		got = append(got, data)
		return nil
	})
	if err == nil {
		t.Fatal("ожидался обрыв тишины посреди ответа")
	}
	if IsRetryable(err) {
		t.Fatalf("после доставки события повтор запрещён: %v", err)
	}
	if len(got) != 1 || got[0] != "половина" {
		t.Errorf("доставленное событие потерялось: %v", got)
	}
}

// TestSSEKeepaliveResetsStall — keepalive-комментарии и редкие события
// сбрасывают счётчик тишины: живой медленный поток не обрывается.
func TestSSEKeepaliveResetsStall(t *testing.T) {
	withStall(t, 150*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		for i := 0; i < 6; i++ {
			fmt.Fprint(w, ": ping\n\n") // пауза 40мс — много меньше предела
			f.Flush()
			time.Sleep(40 * time.Millisecond)
		}
		fmt.Fprint(w, "data: дошло\n\ndata: [DONE]\n\n")
		f.Flush()
	}))
	defer srv.Close()

	got, err := collect(t, NewClient(), srv.URL, context.Background())
	if err != nil {
		t.Fatalf("живой поток с keepalive не должен обрываться: %v", err)
	}
	if len(got) != 1 || got[0] != "дошло" {
		t.Errorf("получено %v, ждали [дошло]", got)
	}
}

// TestSSEParentCancelStillWins — Ctrl+C (отмена родительского контекста)
// по-прежнему разворачивает ход немедленно, а не ждёт предела тишины.
func TestSSEParentCancelStillWins(t *testing.T) {
	withStall(t, 150*time.Millisecond)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // держим поток открытым до конца теста
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := collect(t, NewClient(), srv.URL, ctx)
	if err == nil {
		t.Fatal("ожидалась отмена контекста")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("отмена человека заняла %v — ход не должен ждать предела тишины", elapsed)
	}
	if IsRetryable(err) {
		t.Errorf("отмена человека не повторяется: %v", err)
	}
}
