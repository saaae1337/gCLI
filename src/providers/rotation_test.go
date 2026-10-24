package providers

// Тесты переключения ключей на лету: запрос не должен падать из-за лимита
// одного ключа, пока в пуле есть запасные.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gcli/core"
)

// keyGate — сервер, который первому ключу отвечает отказом по лимиту,
// а второму — обычным потоком.
type keyGate struct {
	mu       sync.Mutex
	used     []string
	rotated  int
	from, to int
}

func (g *keyGate) handler(w http.ResponseWriter, r *http.Request) {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	g.mu.Lock()
	g.used = append(g.used, auth)
	g.mu.Unlock()
	if auth == "key1" {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit exceeded"}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ок\"}}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// TestStreamRotatesKeyOnLimit — первый ключ упёрся в лимит, запрос
// ушёл со вторым, и ход продолжился.
func TestStreamRotatesKeyOnLimit(t *testing.T) {
	g := &keyGate{}
	srv := httptest.NewServer(http.HandlerFunc(g.handler))
	defer srv.Close()

	p := &Provider{ID: "test", Kind: ProtoOpenAI, BaseURL: srv.URL, Pool: NewKeyPool([]string{"key1", "key2"})}
	p.OnRotate = func(from, to int, reason string) {
		g.mu.Lock()
		g.rotated++
		g.from, g.to = from, to
		g.mu.Unlock()
	}

	ch := make(chan core.Delta, 16)
	err := StreamOpenAI(context.Background(), NewClient(), p,
		core.ChatRequest{Model: "test-model", MaxTokens: 100}, "off", ch)
	close(ch)
	if err != nil {
		t.Fatalf("запрос должен был продолжиться со вторым ключом: %v", err)
	}
	var text string
	for d := range ch {
		text += d.Text
	}
	if !strings.Contains(text, "ок") {
		t.Errorf("ответ не получен: %q", text)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.used) != 2 || g.used[0] != "key1" || g.used[1] != "key2" {
		t.Errorf("ключи использованы неверно: %v", g.used)
	}
	if g.rotated != 1 {
		t.Errorf("уведомление о смене ключа %d раз, ожидалось 1", g.rotated)
	}
	if g.from != 1 || g.to != 2 {
		t.Errorf("смена ключа %d→%d, ожидалось 1→2", g.from, g.to)
	}
}

// TestStreamFailsWhenNoKeysLeft — ключ один, и он упёрся в лимит: ошибка
// должна быть внятной, а не молчаливой.
func TestStreamFailsWhenNoKeysLeft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit exceeded"}}`)
	}))
	defer srv.Close()

	p := &Provider{ID: "test", Kind: ProtoOpenAI, BaseURL: srv.URL, Pool: NewKeyPool([]string{"key1"})}
	ch := make(chan core.Delta, 16)
	err := StreamOpenAI(context.Background(), NewClient(), p,
		core.ChatRequest{Model: "test-model", MaxTokens: 100}, "off", ch)
	close(ch)
	if err == nil {
		t.Fatal("ожидалась ошибка: ключей больше нет")
	}
	if !strings.Contains(err.Error(), "лимит") && !strings.Contains(err.Error(), "паузе") {
		t.Errorf("ошибка не объясняет, что произошло: %v", err)
	}
}

// TestStreamRotatesOnRejectedKey — ключ отвергнут (401): следующий
// подхватывается, а отвергнутый больше не используется.
func TestStreamRotatesOnRejectedKey(t *testing.T) {
	var used []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		used = append(used, auth)
		if auth == "bad" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ок\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &Provider{ID: "test", Kind: ProtoOpenAI, BaseURL: srv.URL, Pool: NewKeyPool([]string{"bad", "good"})}
	ch := make(chan core.Delta, 16)
	if err := StreamOpenAI(context.Background(), NewClient(), p,
		core.ChatRequest{Model: "test-model", MaxTokens: 100}, "off", ch); err != nil {
		t.Fatalf("запрос не продолжился: %v", err)
	}
	close(ch)
	if len(used) != 2 || used[1] != "good" {
		t.Errorf("ключи использованы неверно: %v", used)
	}
}

// TestTestKeyChecksWholePool — проверка ключей перебирает весь пул:
// человек должен видеть, какой ключ годен, а какой отвергнут.
func TestTestKeyChecksWholePool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if auth == "bad" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"data":[{"id":"model-1"}]}`)
	}))
	defer srv.Close()

	p := &Provider{ID: "test", Kind: ProtoOpenAI, BaseURL: srv.URL, Pool: NewKeyPool([]string{"bad", "good"})}
	ok, msg := TestKey(NewClient(), p)
	if !ok {
		t.Fatalf("пул с одним рабочим ключом должен считаться рабочим: %s", msg)
	}
	if !strings.Contains(msg, "1") {
		t.Errorf("в ответе нет числа рабочих ключей: %s", msg)
	}
}

// TestTestKeyFailsWhenAllRejected — все ключи отвергнуты: объяснение
// должно быть про ключ, а не про сеть.
func TestTestKeyFailsWhenAllRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()

	p := &Provider{ID: "test", Kind: ProtoOpenAI, BaseURL: srv.URL, Pool: NewKeyPool([]string{"bad1", "bad2"})}
	ok, msg := TestKey(NewClient(), p)
	if ok {
		t.Fatal("отвергнутые ключи не должны считаться рабочими")
	}
	if !strings.Contains(msg, "401") {
		t.Errorf("объяснение не про ключ: %s", msg)
	}
}

// TestStreamRotatesOnQuotaInBody — провайдер отвечает кодом 200, а ошибку
// про квоту кладёт в тело: такой ответ тоже должен переключать ключ.
func TestStreamRotatesOnQuotaInBody(t *testing.T) {
	var used []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		used = append(used, auth)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if auth == "key1" {
			fmt.Fprint(w, "data: {\"error\":{\"message\":\"You exceeded your current quota\",\"type\":\"insufficient_quota\"}}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ок\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &Provider{ID: "test", Kind: ProtoOpenAI, BaseURL: srv.URL, Pool: NewKeyPool([]string{"key1", "key2"})}
	ch := make(chan core.Delta, 16)
	if err := StreamOpenAI(context.Background(), NewClient(), p,
		core.ChatRequest{Model: "test-model", MaxTokens: 100}, "off", ch); err != nil {
		t.Fatalf("квота в теле не переключила ключ: %v", err)
	}
	close(ch)
	if len(used) != 2 || used[1] != "key2" {
		t.Errorf("ключи использованы неверно: %v", used)
	}
}
