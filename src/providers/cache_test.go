package providers

// Тесты кеша префикса Anthropic: неизменная часть запроса (системный
// промпт и схемы инструментов) должна уходить с меткой кеша, иначе она
// оплачивается полной стоимостью на каждом запросе.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gcli/core"
)

// captureAnthropic — сервер, который запоминает тело запроса.
func captureAnthropic(t *testing.T, raw *[]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*raw = body
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ок\"}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAnthropicCacheMarksSystemAndTools — с кешем системный промпт
// уходит блоком с cache_control, а метка на инструментах стоит на
// последнем: так в кеш попадает весь блок схем.
func TestAnthropicCacheMarksSystemAndTools(t *testing.T) {
	var raw []byte
	srv := captureAnthropic(t, &raw)
	p := &Provider{ID: "anthropic", Kind: ProtoAnthropic, BaseURL: srv.URL, PromptCache: "on"}
	creq := core.ChatRequest{
		Model:     "claude-sonnet-4-5",
		System:    "системный промпт",
		MaxTokens: 1024,
		ToolDefs: []core.ToolDef{
			{Name: "read_file", Description: "чтение", Schema: `{"type":"object"}`},
			{Name: "bash", Description: "команда", Schema: `{"type":"object"}`},
		},
	}
	out := make(chan core.Delta, 16)
	if err := StreamAnthropic(context.Background(), NewClient(), p, creq, "off", out); err != nil {
		t.Fatal(err)
	}
	close(out)

	var body struct {
		System json.RawMessage `json:"system"`
		Tools  []struct {
			Name         string          `json:"name"`
			CacheControl json.RawMessage `json:"cache_control"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("тело запроса не разобрано: %v\n%s", err, raw)
	}
	// Системный промпт — массив блоков с меткой кеша.
	var blocks []anBlock
	if err := json.Unmarshal(body.System, &blocks); err != nil {
		t.Fatalf("system не массив блоков: %s", body.System)
	}
	if len(blocks) != 1 || blocks[0].CacheControl == nil {
		t.Errorf("в системном промпте нет метки кеша: %s", body.System)
	}
	// Метка на последнем инструменте, на первом — нет.
	if len(body.Tools) != 2 {
		t.Fatalf("инструментов в запросе %d, ожидалось 2", len(body.Tools))
	}
	if len(body.Tools[0].CacheControl) != 0 {
		t.Errorf("метка кеша стоит не на последнем инструменте: %s", body.Tools[0].CacheControl)
	}
	if len(body.Tools[1].CacheControl) == 0 {
		t.Error("на последнем инструменте нет метки кеша")
	}
}

// TestAnthropicWithoutCacheKeepsPlainSystem — без кеша тело запроса не
// меняется: совместимые прокси ждут обычную строку в system.
func TestAnthropicWithoutCacheKeepsPlainSystem(t *testing.T) {
	var raw []byte
	srv := captureAnthropic(t, &raw)
	p := &Provider{ID: "anthropic", Kind: ProtoAnthropic, BaseURL: srv.URL, PromptCache: "off"}
	creq := core.ChatRequest{Model: "claude-sonnet-4-5", System: "системный промпт", MaxTokens: 1024}
	out := make(chan core.Delta, 16)
	if err := StreamAnthropic(context.Background(), NewClient(), p, creq, "off", out); err != nil {
		t.Fatal(err)
	}
	close(out)

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	sys, ok := body["system"].(string)
	if !ok {
		t.Errorf("system не строка без кеша: %T", body["system"])
	}
	if sys != "системный промпт" {
		t.Errorf("системный промпт изменён: %q", sys)
	}
}

// TestPromptCacheOnRules — кеш включается у официального API, у своего
// адреса — нет, но явное «on» побеждает.
func TestPromptCacheOnRules(t *testing.T) {
	official := &Provider{ID: "anthropic", Kind: ProtoAnthropic, BaseURL: "https://api.anthropic.com"}
	if !promptCacheOn(official) {
		t.Error("у официального API кеш не включился")
	}
	custom := &Provider{ID: "свой", Kind: ProtoAnthropic, BaseURL: "https://прокси.пример/v1"}
	if promptCacheOn(custom) {
		t.Error("у своего адреса кеш включился без разрешения")
	}
	custom.PromptCache = "on"
	if !promptCacheOn(custom) {
		t.Error("явное on не сработало")
	}
	official.PromptCache = "off"
	if promptCacheOn(official) {
		t.Error("явное off не сработало")
	}
}

// TestAnthropicCacheOffByDefaultForProxy — у прокси без явного указания
// кеша в теле запроса не должно появляться незнакомых полей.
func TestAnthropicCacheOffByDefaultForProxy(t *testing.T) {
	var raw []byte
	srv := captureAnthropic(t, &raw)
	p := &Provider{ID: "свой", Kind: ProtoAnthropic, BaseURL: srv.URL}
	creq := core.ChatRequest{Model: "claude-sonnet-4-5", System: "промпт", MaxTokens: 1024}
	out := make(chan core.Delta, 16)
	if err := StreamAnthropic(context.Background(), NewClient(), p, creq, "off", out); err != nil {
		t.Fatal(err)
	}
	close(out)
	if strings.Contains(string(raw), "cache_control") {
		t.Errorf("у прокси появилась метка кеша без разрешения: %s", raw)
	}
}
