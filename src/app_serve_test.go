package main

// Тесты HTTP-обвязки -serve: auth, статус, гонка одновременных ходов,
// шина SSE. Агентский ход не зовётся — это HTTP-слой.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gcli/core"
	"gcli/providers"
	"gcli/ui"
)

// serveApp — минимальное приложение для HTTP-слоя: агент не запускается,
// но статус и миссии должны отвечать на настоящих данных.
func testApp(t *testing.T) *app {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	store := core.NewStore()
	a := &app{
		repo:    &core.Repo{Store: store, Cfg: core.DefaultConfig()},
		store:   store,
		ui:      ui.New(ui.Options{Theme: "ember", Unicode: true, Color: false, Width: 80, Out: &strings.Builder{}}),
		workDir: work,
		prov:    &providers.Provider{ID: "zai", Label: "Z.ai", NoKey: true},
		model:   "glm-4.6",
		sess:    &core.Session{ID: "s1"},
	}
	a.serveBus = newServeBus()
	return a
}

func TestServeAuth(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "secret", bus: newServeBus()}
	h := s.withAuth(s.mux())

	req := httptest.NewRequest("GET", "/v1/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("без токена ждали 401, получили %d", rec.Code)
	}

	req2 := httptest.NewRequest("GET", "/v1/status", nil)
	req2.Header.Set("Authorization", "Bearer secret")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("с токеном ждали 200, получили %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("не JSON: %v", err)
	}
	if resp["version"] == "" || resp["model"] == "" {
		t.Fatalf("статус неполный: %v", resp)
	}
}

func TestServeStatusContent(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "t", bus: newServeBus()}
	req := httptest.NewRequest("GET", "/v1/status?token=t", nil)
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"running":false`) {
		t.Fatalf("поле running: %s", rec.Body.String())
	}
}

func TestServeMessageConflict(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "t", bus: newServeBus()}
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	req := httptest.NewRequest("POST", "/v1/message?token=t", strings.NewReader(`{"text":"привет"}`))
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("второй ход во время первого: ждали 409, получили %d", rec.Code)
	}
	// Плохое тело.
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	req2 := httptest.NewRequest("POST", "/v1/message?token=t", strings.NewReader(`{}`))
	rec2 := httptest.NewRecorder()
	s.mux().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("пустой text: ждали 400, получили %d", rec2.Code)
	}
}

func TestServeBusPublishSubscribe(t *testing.T) {
	b := newServeBus()
	ch := b.subscribe()
	b.publish("delta", "привет")
	select {
	case data := <-ch:
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("событие не JSON: %v", err)
		}
		if m["event"] != "delta" {
			t.Fatalf("событие: %v", m["event"])
		}
		if m["data"] != "привет" {
			t.Fatalf("данные: %v", m["data"])
		}
	default:
		t.Fatal("подписчик не получил событие")
	}
	b.unsubscribe(ch)
	b.publish("delta", "ушло")
	select {
	case <-ch:
		t.Fatal("после отписки события не должны приходить")
	default:
	}
	// publish на nil-шине безопасен (TUI-режим).
	var nilBus *serveBus
	nilBus.publish("delta", "тишина")
}

func TestServeTokenGenerated(t *testing.T) {
	t1, t2 := serveToken(), serveToken()
	if t1 == "" || t1 == t2 {
		t.Fatal("токены должны генерироваться непустыми и разными")
	}
}
