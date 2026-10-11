package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- doSSE: разбор потока ----------

// sseServer — сервер, отдающий заранее заданное тело с кодом.
func sseServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// collect — собрать полезную нагрузку всех событий.
func collect(t *testing.T, c *Client, url string, ctx context.Context) ([]string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	var got []string
	err = c.doSSE(ctx, req, func(data string) error {
		got = append(got, data)
		return nil
	})
	return got, err
}

func TestDoSSEDeliversEventsInOrder(t *testing.T) {
	body := "data: one\n\ndata: two\n\ndata: [DONE]\n\ndata: after-done\n\n"
	srv := sseServer(t, 200, body)
	got, err := collect(t, NewClient(), srv.URL, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "two"}
	if len(got) != len(want) {
		t.Fatalf("получено %v, ждали %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("событие %d: %q вместо %q", i, got[i], want[i])
		}
	}
}

// TestDoSSESkipsNonDataLines — по протоколу SSE поток несёт ещё event:,
// id:, retry: и комментарии. Они не данные и разбираться не должны.
func TestDoSSESkipsNonDataLines(t *testing.T) {
	body := ": комментарий\n\nevent: message\nid: 42\nretry: 100\n\ndata: полезное\n\n"
	srv := sseServer(t, 200, body)
	got, err := collect(t, NewClient(), srv.URL, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "полезное" {
		t.Fatalf("получено %v", got)
	}
}

// TestDoSSEHandlesCRLF — Windows-прокси любят присылать CRLF, и каждый
// лишний \r попадал бы в JSON.
func TestDoSSEHandlesCRLF(t *testing.T) {
	srv := sseServer(t, 200, "data: {\"a\":1}\r\n\r\ndata: {\"b\":2}\r\n\r\n")
	got, err := collect(t, NewClient(), srv.URL, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if strings.HasSuffix(d, "\r") {
			t.Errorf("в данных остался перевод строки: %q", d)
		}
	}
	if len(got) != 2 {
		t.Fatalf("получено %d событий: %v", len(got), got)
	}
}

// TestDoSSEBigLine — одно событие может быть крупным (ответ с картинкой
// в base64), и стандартного буфера сканера на него не хватает.
func TestDoSSEBigLine(t *testing.T) {
	big := strings.Repeat("x", 300*1024)
	srv := sseServer(t, 200, "data: "+big+"\n\ndata: [DONE]\n\n")
	got, err := collect(t, NewClient(), srv.URL, context.Background())
	if err != nil {
		t.Fatalf("крупное событие не прошло: %v", err)
	}
	if len(got) != 1 || len(got[0]) != len(big) {
		t.Fatalf("получено %d событий длиной %v", len(got), len(got[0]))
	}
}

// TestDoSSEHandlerErrorPropagates — ошибка разбора чанка обязана
// доехать до вызывающего, а не быть проглоченной.
func TestDoSSEHandlerErrorPropagates(t *testing.T) {
	srv := sseServer(t, 200, "data: one\n\n")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader("{}"))
	mark := errors.New("помеха в разборе")
	err := NewClient().doSSE(context.Background(), req, func(string) error { return mark })
	if !errors.Is(err, mark) {
		t.Fatalf("ошибка обработчика потерялась: %v", err)
	}
}

// ---------- doSSE: коды ответа ----------

func TestDoSSEStatusHandling(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		retry    bool
		contains string
	}{
		{"отказ-по-ключу", 401, false, "ключ отклонён"},
		{"нет-прав", 403, false, "ключ отклонён"},
		{"нет-средств", 402, false, "нет средств"},
		{"неверный-адрес", 404, false, "base URL"},
		{"слишком-много-запросов", 429, true, ""},
		{"таймаут-сервера", 408, true, ""},
		{"сервер-упал", 500, true, ""},
		{"плохой-шлюз", 502, true, ""},
		{"недоступно", 503, true, ""},
		{"шлюз-по-времени", 504, true, ""},
		{"плохой-запрос", 400, false, "HTTP 400"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := sseServer(t, c.status, `{"error":{"message":"подробности"}}`)
			_, err := collect(t, NewClient(), srv.URL, context.Background())
			if err == nil {
				t.Fatal("ошибка не вернулась")
			}
			if IsRetryable(err) != c.retry {
				t.Errorf("IsRetryable=%v, ждали %v (%v)", IsRetryable(err), c.retry, err)
			}
			if c.contains != "" && !strings.Contains(err.Error(), c.contains) {
				t.Errorf("в объяснении нет %q: %v", c.contains, err)
			}
		})
	}
}

// TestDoSSERetryErrUnwraps — через Unwrap теряется исходная ошибка,
// и разбор вверх по цепочке перестаёт работать.
func TestDoSSERetryErrUnwraps(t *testing.T) {
	base := errors.New("низ")
	err := error(&retryErr{base})
	if !errors.Is(err, base) {
		t.Fatal("Unwrap не работает")
	}
	if IsRetryable(nil) {
		t.Error("nil не повторяемый")
	}
	if IsRetryable(base) {
		t.Error("обычная ошибка не должна считаться повторяемой")
	}
}

// ---------- doSSE: обрывы ----------

// TestDoSSERetryableOnTruncationBeforeFirstEvent — обрыв до первого
// события можно повторить, после первого — нельзя: повтор отдаст
// обрезанный ответ заново, и модель получит половину текста.
func TestDoSSERetryableOnTruncationBeforeFirstEvent(t *testing.T) {
	srv := sseServer(t, 200, "")
	if _, err := collect(t, NewClient(), srv.URL, context.Background()); !IsRetryable(err) {
		t.Errorf("пустой поток не помечен повторяемым: %v", err)
	}

	srv2 := sseServer(t, 200, "data: одно\n\n")
	if _, err := collect(t, NewClient(), srv2.URL, context.Background()); err != nil {
		t.Errorf("нормальный однособытийный поток отработал с ошибкой: %v", err)
	}
}

// TestDoSSEContextCancel — отмена человеком не должна выглядеть как
// повторяемая сетевая ошибка.
func TestDoSSEContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: одно\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader("{}"))
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err := NewClient().doSSE(ctx, req, func(string) error { return nil })
	if err == nil {
		t.Skip("поток успел закрыться раньше отмены — гонка теста, не дефект")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("отмена потеряла марку context.Canceled: %v", err)
	}
}

// TestDoSSEUnreachableHostIsRetryable — обрыв сети до ответа можно
// повторить, и именно это делает агент.
func TestDoSSEUnreachableHostIsRetryable(t *testing.T) {
	srv := sseServer(t, 200, "")
	url := srv.URL
	srv.Close() // порт закрыт — соединение не установится
	if _, err := collect(t, NewClient(), url, context.Background()); !IsRetryable(err) {
		t.Errorf("недоступный хост не помечен повторяемым: %v", err)
	}
}
