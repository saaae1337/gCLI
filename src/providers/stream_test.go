package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gcli/core"
)

// ---------- StreamOpenAI: сквозной путь хода ----------

// oaiServer — сервер, отдающий заранее заданный SSE-поток и запоминающий
// тело запроса.
type oaiServer struct {
	*httptest.Server
	body string
	got  []byte
	hits int
}

func newOAIServer(t *testing.T, stream string) *oaiServer {
	t.Helper()
	s := &oaiServer{body: stream}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits++
		b, _ := io.ReadAll(r.Body)
		s.got = b
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, s.body)
	}))
	t.Cleanup(s.Close)
	return s
}

// drain — прогнать поток и собрать все дельты вместе с ошибкой.
func drain(t *testing.T, p *Provider, creq core.ChatRequest, think string) ([]core.Delta, error) {
	t.Helper()
	ch := make(chan core.Delta, 4096)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		errCh <- StreamOpenAI(context.Background(), NewClient(), p, creq, think, ch)
	}()
	var out []core.Delta
	for d := range ch {
		out = append(out, d)
	}
	return out, <-errCh
}

func textOf(ds []core.Delta) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString(d.Text)
	}
	return b.String()
}

// TestStreamOpenAITextAndFinish — базовый ход: текст собирается по
// кускам, причина завершения доезжает, расход приходит.
func TestStreamOpenAITextAndFinish(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"Привет\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\", мир\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	srv := newOAIServer(t, stream)
	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k", DefaultModel: "glm-4.6"}
	ds, err := drain(t, p, core.ChatRequest{Model: "glm-4.6", MaxTokens: 100}, "off")
	if err != nil {
		t.Fatal(err)
	}
	if got := textOf(ds); got != "Привет, мир" {
		t.Errorf("текст %q", got)
	}
	var finish string
	var usage bool
	for _, d := range ds {
		if d.Finish != "" {
			finish = d.Finish
		}
		if d.Usage != nil {
			usage = d.Usage.PromptTokens == 10 && d.Usage.CompletionTokens == 5
		}
	}
	if finish != "stop" {
		t.Errorf("finish_reason %q", finish)
	}
	if !usage {
		t.Error("расход токенов не доехал")
	}
}

// TestStreamOpenAIAssemblesToolCalls — вызовы инструментов приходят
// кусками: сначала имя с идентификатором, потом аргументы по частям,
// иногда с индексом. Куски одного вызова обязаны сложиться в один, а
// разные вызовы — остаться разными.
func TestStreamOpenAIAssemblesToolCalls(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"read_file\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"a.go\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_b\",\"function\":{\"name\":\"grep\",\"arguments\":\"{\\\"pattern\\\":\\\"x\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	srv := newOAIServer(t, stream)
	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k", DefaultModel: "glm-4.6"}
	ds, err := drain(t, p, core.ChatRequest{Model: "glm-4.6", MaxTokens: 100}, "off")
	if err != nil {
		t.Fatal(err)
	}
	type piece struct {
		idx, id, name, args string
	}
	var got []piece
	for _, d := range ds {
		if d.IsTool {
			got = append(got, piece{
				idx:  fmt.Sprint(d.TCIndex),
				id:   d.TCID,
				name: d.TCName,
				args: d.TCArgs,
			})
		}
	}
	if len(got) != 4 {
		t.Fatalf("кусков %d, ждали 4: %+v", len(got), got)
	}
	if got[0].id != "call_a" || got[0].name != "read_file" {
		t.Errorf("первый кусок: %+v", got[0])
	}
	if got[1].args != `{"path":` || got[2].args != `"a.go"}` {
		t.Errorf("аргументы не пришли по частям: %+v", got[1:3])
	}
	if got[3].idx != "1" || got[3].id != "call_b" {
		t.Errorf("второй вызов смешался с первым: %+v", got[3])
	}
}

// TestStreamOpenAIReasoningVariants — размышления приходят в трёх
// разных полях в зависимости от endpoint-а, и молча терять их нельзя.
func TestStreamOpenAIReasoningVariants(t *testing.T) {
	cases := []struct {
		name  string
		delta string
	}{
		{"reasoning_content", `{"choices":[{"delta":{"reasoning_content":"мысль"}}]}`},
		{"reasoning", `{"choices":[{"delta":{"reasoning":"мысль"}}]}`},
		{"thinking", `{"choices":[{"delta":{"thinking":"мысль"}}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newOAIServer(t, "data: "+c.delta+"\n\ndata: [DONE]\n\n")
			p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k", DefaultModel: "glm-4.6"}
			ds, err := drain(t, p, core.ChatRequest{Model: "glm-4.6", MaxTokens: 100}, "auto")
			if err != nil {
				t.Fatal(err)
			}
			var reason strings.Builder
			for _, d := range ds {
				reason.WriteString(d.Reasoning)
			}
			if reason.String() != "мысль" {
				t.Errorf("размышления не извлечены: %q", reason.String())
			}
		})
	}
}

// TestStreamOpenAIErrorChunk — ошибка может прийти уже в потоке, а не
// кодом ответа. Молча проглотить её нельзя: ход продолжится вслепую.
func TestStreamOpenAIErrorChunk(t *testing.T) {
	srv := newOAIServer(t, `data: {"error":{"message":"контекст переполнен"}}`+"\n\n")
	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k", DefaultModel: "glm-4.6"}
	_, err := drain(t, p, core.ChatRequest{Model: "glm-4.6", MaxTokens: 100}, "off")
	if err == nil {
		t.Fatal("ошибка в потоке не доехала")
	}
	if !strings.Contains(err.Error(), "контекст переполнен") {
		t.Errorf("текст ошибки потерян: %v", err)
	}
}

// TestStreamOpenAIBadEndpointExplained — endpoint, который отдаёт не
// OpenAI-совместимый поток, должен быть назван, а не молчать.
func TestStreamOpenAIBadEndpointExplains(t *testing.T) {
	// Много нечитаемых кусков подряд: счётчик неудачных разборов
	// обязан сработать и объяснить, что не так с адресом.
	var b strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "data: это не json %d\n\n", i)
	}
	srv := newOAIServer(t, b.String())
	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k", DefaultModel: "glm-4.6"}
	_, err := drain(t, p, core.ChatRequest{Model: "glm-4.6", MaxTokens: 100}, "off")
	if err == nil {
		t.Fatal("мусорный endpoint не объяснён")
	}
	if !strings.Contains(err.Error(), "OpenAI") {
		t.Errorf("в объяснении нет намёка на протокол: %v", err)
	}
}

// TestStreamOpenAISendsToolSchemas — схемы инструментов обязаны уехать
// провайдеру: без них модель не знает, что у неё есть инструменты, и
// ход превращается в чат.
func TestStreamOpenAISendsToolSchemas(t *testing.T) {
	srv := newOAIServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ок\"}}]}\n\ndata: [DONE]\n\n")
	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k", DefaultModel: "glm-4.6"}
	creq := core.ChatRequest{
		Model:     "glm-4.6",
		MaxTokens: 100,
		ToolDefs:  []core.ToolDef{{Name: "read_file", Description: "читать", Schema: `{"type":"object"}`}},
	}
	if _, err := drain(t, p, creq, "off"); err != nil {
		t.Fatal(err)
	}
	body := string(srv.got)
	for _, want := range []string{`"tools"`, `"read_file"`, `"parameters"`} {
		if !strings.Contains(body, want) {
			t.Errorf("в запросе нет %s:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `"stream_options"`) {
		t.Errorf("при инструментах расход не запрашивается:\n%s", body)
	}
}

// TestStreamOpenAIRequestHeaders — авторизация и версия протокола.
func TestStreamOpenAIRequestHeaders(t *testing.T) {
	var auth, accept, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		accept = r.Header.Get("Accept")
		ctype = r.Header.Get("Content-Type")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &Provider{ID: "zai", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "sekret", DefaultModel: "glm-4.6"}
	ch := make(chan core.Delta, 8)
	_ = StreamOpenAI(context.Background(), NewClient(), p, core.ChatRequest{Model: "glm-4.6", MaxTokens: 10}, "off", ch)
	close(ch)
	for range ch {
	}
	if auth != "Bearer sekret" {
		t.Errorf("Authorization %q", auth)
	}
	if accept != "text/event-stream" {
		t.Errorf("Accept %q", accept)
	}
	if ctype != "application/json" {
		t.Errorf("Content-Type %q", ctype)
	}
}

// TestStreamAnthropicUsesOwnAuth — у Anthropic другой заголовок ключа
// и обязательна версия протокола. Если перепутать, каждый запрос
// получает 401 с непонятным текстом.
func TestStreamAnthropicUsesOwnAuth(t *testing.T) {
	var key, version, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("x-api-key")
		version = r.Header.Get("anthropic-version")
		auth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := &Provider{ID: "anthropic", Kind: ProtoAnthropic, BaseURL: srv.URL, Key: "секрет", DefaultModel: "claude"}
	ch := make(chan core.Delta, 64)
	_ = StreamAnthropic(context.Background(), NewClient(), p, core.ChatRequest{
		Model: "claude-sonnet-4-5", MaxTokens: 100,
		Messages: []core.Message{{Role: core.RoleUser, Content: "привет"}},
	}, "off", ch)
	close(ch)
	for range ch {
	}
	if key != "секрет" {
		t.Errorf("x-api-key %q", key)
	}
	if version == "" {
		t.Error("anthropic-version не отправлена")
	}
	if auth != "" {
		t.Errorf("Authorization не должен слаться в протоколе Anthropic: %q", auth)
	}
}

// TestAnthropicBaseKeepsSingleV1 — пользователь вводит адрес уже с /v1
// (прокси, шлюз), и без проверки получался «…/v1/v1/messages»: 404 на
// каждый запрос при рабочем /models.
func TestAnthropicBaseKeepsSingleV1(t *testing.T) {
	cases := map[string]string{
		"https://api.anthropic.com":    "https://api.anthropic.com/v1",
		"https://api.anthropic.com/":   "https://api.anthropic.com/v1",
		"https://api.anthropic.com/v1": "https://api.anthropic.com/v1",
		"https://прокси/v1/":           "https://прокси/v1",
		"https://шлюз/api":             "https://шлюз/api/v1",
	}
	for in, want := range cases {
		if got := anthropicBase(in); got != want {
			t.Errorf("anthropicBase(%q) = %q, ждали %q", in, got, want)
		}
	}
}

// TestExtractReasoningPrefersFirstNonEmpty — endpoint-ы называют поле
// по-разному, и при нескольких заполненных берётся первое в
// фиксированном порядке, а не случайное.
func TestExtractReasoningPrefersFirstNonEmpty(t *testing.T) {
	if got := extractReasoning("a", "b", "c"); got != "a" {
		t.Errorf("%q", got)
	}
	if got := extractReasoning("", "b", "c"); got != "b" {
		t.Errorf("%q", got)
	}
	if got := extractReasoning("", "", "c"); got != "c" {
		t.Errorf("%q", got)
	}
	if got := extractReasoning("", "", ""); got != "" {
		t.Errorf("%q", got)
	}
}
