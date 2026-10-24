package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gcli/core"
)

// TestProviderFixesSSEMultilineData — спецификация SSE разрешает делить
// одно событие на несколько строк data:. Склеивать их нужно через \n,
// иначе JSON-чанк разваливается на куски и теряется.
func TestProviderFixesSSEMultilineData(t *testing.T) {
	body := "event: message\ndata: {\"a\":\ndata: 1}\n\ndata: [DONE]\n\n"
	srv := sseServer(t, http.StatusOK, body)
	got, err := collect(t, NewClient(), srv.URL, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "{\"a\":\n1}" {
		t.Fatalf("событие не склеено: %#v", got)
	}
}

// TestProviderFixesSSETrailingEvent — поток может закончиться без
// завершающей пустой строки; последнее событие всё равно должно дойти.
func TestProviderFixesSSETrailingEvent(t *testing.T) {
	body := "data: последнее\n"
	srv := sseServer(t, http.StatusOK, body)
	got, err := collect(t, NewClient(), srv.URL, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "последнее" {
		t.Fatalf("хвостовое событие потеряно: %#v", got)
	}
}

// TestProviderFixesRetryableWrapped — retryErr может прийти в обёртке;
// IsRetryable обязан видеть его через errors.As, иначе повтор не сработает.
func TestProviderFixesRetryableWrapped(t *testing.T) {
	inner := &retryErr{errors.New("перегрузка")}
	wrapped := fmt.Errorf("запрос: %w", inner)
	if !IsRetryable(wrapped) {
		t.Fatal("обёрнутая retryErr не распознана")
	}
	if IsRetryable(fmt.Errorf("обычная: %w", errors.New("x"))) {
		t.Fatal("обычная ошибка признана повторяемой")
	}
}

// TestProviderFixesNilProvider — nil-провайдер не должен ронять процесс.
func TestProviderFixesNilProvider(t *testing.T) {
	if _, err := FetchModels(context.Background(), NewClient(), nil); err == nil {
		t.Error("FetchModels(nil) вернул успех")
	}
	err := Stream(context.Background(), NewClient(), nil, core.ChatRequest{Model: "m"}, "auto", make(chan core.Delta, 1))
	if err == nil {
		t.Error("Stream(nil) вернул успех")
	}
	if _, _, err := Ping(NewClient(), nil); err == nil {
		t.Error("Ping(nil) вернул успех")
	}
	if ok, _ := TestKey(NewClient(), nil); ok {
		t.Error("TestKey(nil) вернул успех")
	}
	if got := ResolveModel(nil, "", "m"); got != "m" {
		t.Errorf("ResolveModel(nil) = %q", got)
	}
	if got := (&Provider{}).CtxSoftLimit(); got <= 0 {
		t.Errorf("CtxSoftLimit = %d", got)
	}
	if (*Provider)(nil).HasKey() || (*Provider)(nil).Ready() {
		t.Error("nil-провайдер не может быть готов")
	}
}

// TestProviderFixesMaskKeyShort — девятисимвольный ключ раньше показывался
// целиком (5 + 4), то есть маскировка не маскировала ничего.
func TestProviderFixesMaskKeyShort(t *testing.T) {
	key := "abcdefghi"
	got := MaskKey(key)
	if strings.Contains(got, key) || strings.Contains(got, "abcdefgh") {
		t.Fatalf("короткий ключ раскрыт: %q", got)
	}
	if got == "" {
		t.Fatal("маска пустая")
	}
}

// TestProviderFixesOpenAIUsageWithoutTools — расход нужен и обычному
// текстовому ходу, а не только ходу с инструментами.
func TestProviderFixesOpenAIUsageWithoutTools(t *testing.T) {
	srv := newOAIServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ок\"}}]}\n\ndata: [DONE]\n\n")
	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k"}
	if _, err := drain(t, p, core.ChatRequest{Model: "glm-4.6", MaxTokens: 10}, "off"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(srv.got), `"stream_options":{"include_usage":true}`) {
		t.Fatalf("include_usage не запрошен:\n%s", srv.got)
	}
}

// TestProviderFixesOpenAIOModelTokenField — o-серия/gpt-5 отвергает
// max_tokens; лимит обязан уходить в max_completion_tokens.
func TestProviderFixesOpenAIOModelTokenField(t *testing.T) {
	srv := newOAIServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ок\"}}]}\n\ndata: [DONE]\n\n")
	p := &Provider{ID: "openai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k"}
	if _, err := drain(t, p, core.ChatRequest{Model: "o3-mini", MaxTokens: 123}, "off"); err != nil {
		t.Fatal(err)
	}
	body := string(srv.got)
	if !strings.Contains(body, `"max_completion_tokens":123`) {
		t.Fatalf("нет max_completion_tokens:\n%s", body)
	}
	if strings.Contains(body, `"max_tokens"`) {
		t.Fatalf("max_tokens недопустим для o-модели:\n%s", body)
	}
}

// TestProviderFixesOpenAIRefusal — отказ модели приходит отдельным полем
// delta.refusal и не должен теряться.
func TestProviderFixesOpenAIRefusal(t *testing.T) {
	srv := newOAIServer(t, "data: {\"choices\":[{\"delta\":{\"refusal\":\"не могу помочь\"}}]}\n\ndata: [DONE]\n\n")
	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k"}
	ds, err := drain(t, p, core.ChatRequest{Model: "glm-4.6", MaxTokens: 10}, "off")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOf(ds), "не могу помочь") {
		t.Fatalf("отказ потерян: %+v", ds)
	}
}

// TestProviderFixesOpenAIEmptyToolSchema — пустая схема не должна
// превращаться в parameters:null: строгие API такой инструмент отвергают.
func TestProviderFixesOpenAIEmptyToolSchema(t *testing.T) {
	srv := newOAIServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ок\"}}]}\n\ndata: [DONE]\n\n")
	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k"}
	creq := core.ChatRequest{
		Model: "glm-4.6", MaxTokens: 10,
		ToolDefs: []core.ToolDef{{Name: "noargs"}},
	}
	if _, err := drain(t, p, creq, "off"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(srv.got), `"parameters":{"type":"object"}`) {
		t.Fatalf("пустая схема ушла как null:\n%s", srv.got)
	}
}

// TestProviderFixesAnthropicCachedUsage — кэшированный вход входит в
// общий prompt-расход; иначе /usage занижает счёт.
func TestProviderFixesAnthropicCachedUsage(t *testing.T) {
	body := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":3,\"cache_read_input_tokens\":2}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	srv := sseServer(t, http.StatusOK, body)
	p := &Provider{ID: "anthropic", Kind: ProtoAnthropic, BaseURL: srv.URL, Key: "k"}
	ch := make(chan core.Delta, 32)
	err := StreamAnthropic(context.Background(), NewClient(), p, core.ChatRequest{
		Model: "claude-sonnet-4-5", MaxTokens: 100,
		Messages: []core.Message{{Role: core.RoleUser, Content: "привет"}},
	}, "off", ch)
	close(ch)
	if err != nil {
		t.Fatal(err)
	}
	var prompt, completion int
	for d := range ch {
		if d.Usage != nil {
			if d.Usage.PromptTokens > prompt {
				prompt = d.Usage.PromptTokens
			}
			if d.Usage.CompletionTokens > completion {
				completion = d.Usage.CompletionTokens
			}
		}
	}
	if prompt != 15 || completion != 7 {
		t.Fatalf("расход: prompt=%d completion=%d, ждали 15/7", prompt, completion)
	}
}

// TestProviderFixesAnthropicErrorText — ST1005: текст ошибки не должен
// начинаться с заглавной буквы. Перегрузка внутри потока при этом
// остаётся повторяемой.
func TestProviderFixesAnthropicErrorText(t *testing.T) {
	cases := []struct {
		name  string
		typ   string
		retry bool
	}{
		{"перегрузка", "overloaded_error", true},
		{"плохой-запрос", "invalid_request_error", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"" + c.typ + "\",\"message\":\"подробности\"}}\n\n"
			srv := sseServer(t, http.StatusOK, body)
			p := &Provider{ID: "anthropic", Kind: ProtoAnthropic, BaseURL: srv.URL, Key: "k"}
			ch := make(chan core.Delta, 8)
			err := StreamAnthropic(context.Background(), NewClient(), p, core.ChatRequest{
				Model: "claude-sonnet-4-5", MaxTokens: 100,
				Messages: []core.Message{{Role: core.RoleUser, Content: "привет"}},
			}, "off", ch)
			if err == nil {
				t.Fatal("ошибка потока не вернулась")
			}
			if strings.HasPrefix(err.Error(), "Anthropic") {
				t.Fatalf("ошибка начинается с заглавной: %v", err)
			}
			if !strings.Contains(err.Error(), "anthropic:") {
				t.Fatalf("ошибка без контекста протокола: %v", err)
			}
			if IsRetryable(err) != c.retry {
				t.Fatalf("IsRetryable=%v, ждали %v (%v)", IsRetryable(err), c.retry, err)
			}
		})
	}
}

// TestProviderFixesCustomModelsOrder — порядок моделей из конфига не
// должен переворачиваться, а первая модель — становиться дефолтной.
func TestProviderFixesCustomModelsOrder(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Providers = map[string]*core.ProviderCfg{
		"mine": {BaseURL: "https://api.example.com/v1", Models: []string{"b", "a"}},
	}
	p := Build(cfg).Find("mine")
	if p == nil {
		t.Fatal("провайдер не создан")
	}
	if p.DefaultModel != "b" {
		t.Errorf("DefaultModel = %q, ждали b", p.DefaultModel)
	}
	if len(p.Models) < 2 || p.Models[0] != "b" || p.Models[1] != "a" {
		t.Errorf("порядок моделей: %v", p.Models)
	}
}
