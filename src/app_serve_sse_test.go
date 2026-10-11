package main

// Тесты SSE-шины и эндпоинта /v1/events: от него зависят веб-оболочка
// и десктоп, и до этого он не был прикрыт ничем.

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- шина ----------

// TestBusPublishReachesSubscriber — опубликованное событие доходит до
// подписчика вместе с именем и данными.
func TestBusPublishReachesSubscriber(t *testing.T) {
	b := newServeBus()
	ch := b.subscribe()
	defer b.unsubscribe(ch)

	b.publish("delta", "кусок текста")

	select {
	case data := <-ch:
		if !strings.Contains(string(data), `"event":"delta"`) {
			t.Errorf("нет имени события: %s", data)
		}
		if !strings.Contains(string(data), "кусок текста") {
			t.Errorf("нет полезной нагрузки: %s", data)
		}
	case <-time.After(time.Second):
		t.Fatal("событие не дошло")
	}
}

// TestBusSlowSubscriberDoesNotBlockAgent — медленный подписчик
// пропускает события, но не тормозит агента. Иначе один зависший
// вкладка браузера встала бы колом всю работу.
func TestBusSlowSubscriberDoesNotBlockAgent(t *testing.T) {
	b := newServeBus()
	slow := b.subscribe()
	defer b.unsubscribe(slow)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			b.publish("delta", i)
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("публикация заблокировалась медленным подписчиком")
	}
}

// TestBusUnsubscribeStopsDelivery — отписавшийся подписчик больше не
// получает события: иначе каждое закрытие вкладки оставляло бы
// подписку в шине навсегда.
func TestBusUnsubscribeStopsDelivery(t *testing.T) {
	b := newServeBus()
	ch := b.subscribe()
	b.unsubscribe(ch)
	b.publish("delta", "после отписки")
	select {
	case data := <-ch:
		t.Errorf("отписавшийся получил событие: %s", data)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestBusPublishOnNilIsSafe — в TUI-режиме шины нет, и публикация в
// nil-приёмнике обязана быть безопасной: иначе каждый ход без
// -serve падал бы.
func TestBusPublishOnNilIsSafe(t *testing.T) {
	var b *serveBus
	b.publish("delta", "ничего")
}

// TestBusConcurrentPublishSubscribe — подписка и отписка идут параллельно
// с публикацией. Гонка здесь означала бы падение в середине хода.
func TestBusConcurrentPublishSubscribe(t *testing.T) {
	b := newServeBus()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(3)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				b.publish("delta", "много")
			}
		}
	}()
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				ch := b.subscribe()
				b.unsubscribe(ch)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// ---------- эндпоинт /v1/events ----------

// readSSEUntil — читать поток, пока не встретится подстрока или не
// кончится время.
func readSSEUntil(t *testing.T, resp *http.Response, want string, within time.Duration) string {
	t.Helper()
	type res struct{ s string }
	ch := make(chan res, 1)
	go func() {
		var b strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			b.WriteString(sc.Text())
			b.WriteByte('\n')
			if want == "" || strings.Contains(b.String(), want) {
				break
			}
		}
		ch <- res{b.String()}
	}()
	select {
	case r := <-ch:
		return r.s
	case <-time.After(within):
		t.Fatalf("в потоке не дождались %q", want)
		return ""
	}
}

// TestServeEventsHeaders — оболочка полагается на эти заголовки, а
// прокси по умолчанию буферизует поток.
func TestServeEventsHeaders(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "t", bus: newServeBus(), keepalive: 50 * time.Millisecond}
	srv := httptest.NewServer(s.withAuth(s.mux()))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events?token=t", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	cancel()

	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, ждали %q", k, got, want)
		}
	}
	if got := readSSEUntil(t, resp, "", 2*time.Second); !strings.Contains(got, "retry:") {
		t.Errorf("поток не начат с retry: %q", got)
	}
}

// TestServeEventsDelivers — событие, опубликованное после подписки,
// доезжает до клиента потоком.
func TestServeEventsDelivers(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "t", bus: a.serveBus, keepalive: 50 * time.Millisecond}
	srv := httptest.NewServer(s.withAuth(s.mux()))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events?token=t", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Подписка могла не успеть оформиться — повторяем публикацию.
	go func() {
		for i := 0; i < 20; i++ {
			a.serveBus.publish("delta", "событие дошло")
			time.Sleep(20 * time.Millisecond)
		}
	}()

	got := readSSEUntil(t, resp, "событие дошло", 3*time.Second)
	if !strings.Contains(got, `"event":"delta"`) {
		t.Errorf("в потоке нет имени события: %q", got)
	}
}

// TestServeEventsKeepaliveOnIdle — без событий поток всё равно должен
// что-то слать: иначе прокси между клиентом и gcli решит, что
// соединение мертво, и закроет его.
func TestServeEventsKeepaliveOnIdle(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "t", bus: newServeBus(), keepalive: 30 * time.Millisecond}
	srv := httptest.NewServer(s.withAuth(s.mux()))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events?token=t", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := readSSEUntil(t, resp, ": keepalive", 3*time.Second); !strings.Contains(got, ": keepalive") {
		t.Errorf("keepalive не отправлен: %q", got)
	}
}

// TestServeEventsNeedsAuth — поток отдаёт состояние агента, поэтому
// закрыт он так же, как остальные /v1.
func TestServeEventsNeedsAuth(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "secret", bus: newServeBus(), keepalive: time.Hour}
	srv := httptest.NewServer(s.withAuth(s.mux()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("без токена ждали 401, получили %d", resp.StatusCode)
	}
}
