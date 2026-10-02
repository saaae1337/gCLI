package main

// Тесты веб-оболочки и истории: /ui/* отдаётся без токена (в ней нет
// данных), /v1/history — закрыт токеном и отдаёт переписку, включая
// размышления; чужая сессия — 404; reason публикуется в SSE-шину.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gcli/core"
)

func TestUIPageWithoutToken(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "secret", bus: newServeBus()}
	h := s.withAuth(s.mux())

	// Страница и её ассеты отдаются без токена: данных в них нет.
	for _, path := range []string{"/ui/", "/ui/style.css", "/ui/app.js"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s без токена: ждали 200, получили %d", path, rec.Code)
		}
	}
	if body := rec(h, "/ui/"); !strings.Contains(body, "gCLI") {
		t.Fatalf("в оболочке нет заголовка gCLI")
	}

	// Данные же по-прежнему закрыты: /v1/status без токена — 401.
	req := httptest.NewRequest("GET", "/v1/status", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("статус без токена: ждали 401, получили %d", rec2.Code)
	}
}

func rec(h http.Handler, path string) string {
	req := httptest.NewRequest("GET", path, nil)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	return r.Body.String()
}

func TestHistoryRequiresToken(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "secret", bus: newServeBus()}
	req := httptest.NewRequest("GET", "/v1/history", nil)
	r := httptest.NewRecorder()
	s.withAuth(s.mux()).ServeHTTP(r, req)
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("история без токена: ждали 401, получили %d", r.Code)
	}
}

func TestHistoryShowsMessagesAndReasoning(t *testing.T) {
	a := testApp(t)
	a.sess.Messages = []core.Message{
		{Role: core.RoleUser, Content: "вопрос"},
		{Role: core.RoleAssistant, Content: "ответ", Reasoning: "скрытые мысли",
			ToolCalls: []core.ToolCall{{ID: "t1", Name: "bash", Args: `{"command":"ls"}`}}},
		{Role: core.RoleTool, Name: "bash", Content: "file.go\nmain.go"},
		{Role: core.RoleUser, Content: "служебное", Hidden: true},
	}
	s := &serveServer{a: a, token: "t", bus: newServeBus()}
	body := rec(s.mux(), "/v1/history?token=t")

	var resp struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("не JSON: %v\n%s", err, body)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("ждали 3 сообщения (hidden скрыт), получили %d", len(resp.Messages))
	}
	asst := resp.Messages[1]
	if asst["reasoning"] != "скрытые мысли" {
		t.Errorf("размышления потерялись: %v", asst)
	}
	if asst["content"] != "ответ" {
		t.Errorf("текст ответа: %v", asst)
	}
	tool := resp.Messages[2]
	if tool["name"] != "bash" {
		t.Errorf("имя инструмента: %v", tool)
	}
}

func TestHistoryForeignSessionNotFound(t *testing.T) {
	a := testApp(t)
	s := &serveServer{a: a, token: "t", bus: newServeBus()}
	req := httptest.NewRequest("GET", "/v1/history?session=no-such", nil)
	r := httptest.NewRecorder()
	s.mux().ServeHTTP(r, req)
	if r.Code != http.StatusNotFound {
		t.Fatalf("чужая сессия: ждали 404, получили %d", r.Code)
	}
}

func TestReasonPublishedToSSEBus(t *testing.T) {
	a := testApp(t)
	ch := a.serveBus.subscribe()
	defer a.serveBus.unsubscribe(ch)

	// Тот же обработчик, что агенты получают как OnReason.
	a.onReasonPub("кусок мысли")

	select {
	case data := <-ch:
		if !strings.Contains(string(data), `"reason"`) {
			t.Fatalf("в шине ждали событие reason: %s", data)
		}
	default:
		t.Fatal("событие reason не дошло до подписчика")
	}
}
