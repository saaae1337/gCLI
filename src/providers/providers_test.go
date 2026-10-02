package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gcli/core"
)

func TestCleanKey(t *testing.T) {
	cases := map[string]string{
		"  sk-abc123  ":       "sk-abc123",
		`"sk-quoted"`:         "sk-quoted",
		`'sk-single'`:         "sk-single",
		"Bearer sk-bearer":    "sk-bearer",
		"bearer  sk-lower":    "sk-lower",
		"«sk-guillemets»":     "sk-guillemets",
		"sk-with-inner-space": "sk-with-inner-space",
		"":                    "",
	}
	for in, want := range cases {
		if got := CleanKey(in); got != want {
			t.Errorf("CleanKey(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestMaskKey(t *testing.T) {
	if got := MaskKey("sk-1234567890abcdef"); !strings.Contains(got, "…") {
		t.Errorf("MaskKey = %q", got)
	}
	if got := MaskKey("short"); strings.Contains(got, "s") {
		t.Errorf("короткий ключ должен маскироваться полностью: %q", got)
	}
}

func TestValidID(t *testing.T) {
	good := []string{"myapi", "my-api", "my_api2", "ab"}
	for _, id := range good {
		if !ValidID(id) {
			t.Errorf("должен быть валиден: %q", id)
		}
	}
	bad := []string{"a", "1abc", "My-API", "my api", "my.api", "тест", strings.Repeat("a", 30)}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("не должен быть валиден: %q", id)
		}
	}
}

func TestPresets(t *testing.T) {
	ps := Presets()
	if len(ps) < 10 {
		t.Fatalf("ожидалось ≥10 пресетов, получено %d", len(ps))
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if p.ID == "" || p.Label == "" || p.BaseURL == "" {
			t.Errorf("неполный пресет: %+v", p)
		}
		if p.Kind != ProtoOpenAI && p.Kind != ProtoAnthropic {
			t.Errorf("неизвестный протокол %q у %s", p.Kind, p.ID)
		}
		if seen[p.ID] {
			t.Errorf("дубликат id: %s", p.ID)
		}
		seen[p.ID] = true
		if p.DefaultModel == "" && len(p.Models) > 0 {
			t.Errorf("у %s есть модели, но нет дефолтной", p.ID)
		}
	}
	// Локальные провайдеры не требуют ключа.
	for _, id := range []string{"ollama", "lmstudio"} {
		p := findIn(ps, id)
		if p == nil {
			t.Fatalf("нет пресета %s", id)
		}
		if !p.NoKey {
			t.Errorf("%s должен быть без ключа", id)
		}
	}
}

func TestBuildMergesConfig(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Providers = map[string]*core.ProviderCfg{
		"custom": {BaseURL: "https://my.api/v1", APIKey: "sk-test", Model: "my-model", Protocol: ProtoOpenAI, Custom: true},
		"zai":    {Model: "glm-override"},
	}
	reg := Build(cfg)

	c := reg.Find("custom")
	if c == nil {
		t.Fatal("пользовательский провайдер не создан")
	}
	if c.BaseURL != "https://my.api/v1" || c.Key != "sk-test" {
		t.Errorf("конфиг не применён: %+v", c)
	}
	if c.DefaultModel != "my-model" {
		t.Errorf("модель: %q", c.DefaultModel)
	}

	z := reg.Find("zai")
	if z == nil {
		t.Fatal("пресет zai потерян")
	}
	if z.DefaultModel != "glm-override" {
		t.Errorf("переопределение модели не сработало: %q", z.DefaultModel)
	}
}

func TestRegistryPick(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Provider = "openai"
	reg := Build(cfg)
	if p := reg.Pick(""); p.ID != "openai" {
		t.Errorf("Pick по конфигу: %s", p.ID)
	}
	if p := reg.Pick("anthropic"); p.ID != "anthropic" {
		t.Errorf("Pick по флагу: %s", p.ID)
	}
	if p := reg.Pick("нет-такого"); p == nil {
		t.Error("Pick должен возвращать провайдера по умолчанию")
	}
}

func TestResolveModel(t *testing.T) {
	p := &Provider{Models: []string{"a", "b"}, DefaultModel: "a"}
	if got := ResolveModel(p, "flag", "b"); got != "flag" {
		t.Errorf("флаг должен побеждать: %q", got)
	}
	if got := ResolveModel(p, "", "b"); got != "b" {
		t.Errorf("конфиг должен использоваться: %q", got)
	}
	if got := ResolveModel(p, "", "нет-такой"); got != "a" {
		t.Errorf("неизвестная модель → дефолт: %q", got)
	}
	if got := ResolveModel(p, "", ""); got != "a" {
		t.Errorf("пусто → дефолт: %q", got)
	}
}

func TestThinkState(t *testing.T) {
	cfg := core.Config{Think: "on"}
	if ThinkState(cfg) != "on" {
		t.Error("on")
	}
	cfg.Think = "off"
	if ThinkState(cfg) != "off" {
		t.Error("off")
	}
	cfg.Think = "ерунда"
	if ThinkState(cfg) != "auto" {
		t.Error("неизвестное значение должно давать auto")
	}
	cfg.Think = ""
	if ThinkState(cfg) != "auto" {
		t.Error("пустое значение должно давать auto")
	}
}

func TestIsOModel(t *testing.T) {
	yes := []string{"o1", "o1-mini", "o3", "o4-mini", "gpt-5", "openai/o3"}
	for _, m := range yes {
		if !isOModel(m) {
			t.Errorf("%s должен считаться o-моделью", m)
		}
	}
	no := []string{"gpt-4o", "glm-4.6", "claude-sonnet-4-5", "text-embedding-3-small"}
	for _, m := range no {
		if isOModel(m) {
			t.Errorf("%s не должен считаться o-моделью", m)
		}
	}
}

func TestApplyThinkingOA(t *testing.T) {
	// GLM: off → thinking disabled.
	p := &Provider{ID: "zai"}
	body := oaRequest{}
	applyThinkingOA(p, &body, "glm-4.6", "off")
	if body.Thinking == nil || body.Thinking.Type != "disabled" {
		t.Errorf("GLM off: %+v", body.Thinking)
	}

	// GLM: auto → enabled.
	body = oaRequest{}
	applyThinkingOA(p, &body, "glm-4.6", "auto")
	if body.Thinking == nil || body.Thinking.Type != "enabled" {
		t.Errorf("GLM auto: %+v", body.Thinking)
	}

	// Qwen: on → enable_thinking.
	body = oaRequest{}
	applyThinkingOA(&Provider{ID: "dashscope"}, &body, "qwen3-max", "on")
	if body.EnableThinking == nil || !*body.EnableThinking {
		t.Error("Qwen on должен включать enable_thinking")
	}

	// OpenRouter: on → reasoning.effort.
	body = oaRequest{}
	applyThinkingOA(&Provider{ID: "openrouter"}, &body, "some/model", "on")
	if body.Reasoning == nil || body.Reasoning.Effort != "medium" {
		t.Errorf("OpenRouter on: %+v", body.Reasoning)
	}

	// OpenAI o-серия: on → reasoning_effort.
	body = oaRequest{}
	applyThinkingOA(&Provider{ID: "openai"}, &body, "o3-mini", "on")
	if body.ReasoningEffort != "medium" {
		t.Errorf("o3: %q", body.ReasoningEffort)
	}

	// Неизвестная модель при auto — ничего не добавляем.
	body = oaRequest{}
	applyThinkingOA(&Provider{ID: "custom"}, &body, "some-model", "auto")
	if body.Thinking != nil || body.Reasoning != nil || body.EnableThinking != nil || body.ReasoningEffort != "" {
		t.Error("для неизвестной модели при auto параметры не должны добавляться")
	}
}

func TestBuildOAMessages(t *testing.T) {
	creq := core.ChatRequest{
		System: "системный промпт",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: "вопрос"},
			{Role: core.RoleAssistant, Content: "ответ", ToolCalls: []core.ToolCall{
				{ID: "1", Name: "read_file", Args: `{"path":"a.go"}`},
			}},
			{Role: core.RoleTool, ToolCallID: "1", Name: "read_file", Content: "содержимое"},
		},
	}
	msgs := buildOAMessages(creq, true)
	if len(msgs) != 4 {
		t.Fatalf("ожидалось 4 сообщения, получено %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("первым должен идти system: %q", msgs[0].Role)
	}
	if len(msgs[2].ToolCalls) != 1 {
		t.Error("вызовы инструментов не перенесены")
	}
	if msgs[2].Role != "assistant" || msgs[2].ToolCalls[0].ID != "1" {
		t.Errorf("сообщение ассистента с вызовом: %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "1" {
		t.Errorf("сообщение инструмента: %+v", msgs[3])
	}
}

func TestBuildAnthropicMessages(t *testing.T) {
	msgs := buildAnthropicMessages([]core.Message{
		{Role: core.RoleUser, Content: "привет"},
		{Role: core.RoleAssistant, Content: "ответ", ToolCalls: []core.ToolCall{
			{ID: "t1", Name: "read_file", Args: `{"path":"a.go"}`},
		}},
		{Role: core.RoleTool, ToolCallID: "t1", Content: "результат"},
		{Role: core.RoleUser, Content: "ещё"},
	}, false)

	if len(msgs) != 3 {
		t.Fatalf("ожидалось 3 сообщения, получено %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("первым должен идти user: %q", msgs[0].Role)
	}
	// assistant + tool_result должны склеиться в user-сообщение.
	last := msgs[len(msgs)-1]
	blocks, ok := last.Content.([]anBlock)
	if !ok {
		t.Fatal("ожидались блоки")
	}
	found := false
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID == "t1" {
			found = true
		}
	}
	if !found {
		t.Error("блок tool_result не найден")
	}
}

func TestBuildAnthropicSkipsLeadingAssistant(t *testing.T) {
	msgs := buildAnthropicMessages([]core.Message{
		{Role: core.RoleAssistant, Content: "начнём с ассистента"},
		{Role: core.RoleUser, Content: "вопрос"},
	}, false)
	if len(msgs) == 0 || msgs[0].Role != "user" {
		t.Errorf("история должна начинаться с user: %+v", msgs)
	}
}

func TestBuildAnthropicDanglingToolUse(t *testing.T) {
	// assistant с tool_use без результата — Anthropic такое отвергает.
	msgs := buildAnthropicMessages([]core.Message{
		{Role: core.RoleUser, Content: "вопрос"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "t1", Name: "bash", Args: `{}`},
		}},
	}, false)
	last := msgs[len(msgs)-1]
	blocks, _ := last.Content.([]anBlock)
	if last.Role != "user" || !hasToolUse(blocks) {
		// Должен быть добавлен закрывающий user-блок.
		t.Logf("последнее сообщение: %s, блоков: %d", last.Role, len(blocks))
	}
}

// ---------- Тесты сетевого слоя на локальном сервере ----------

func TestFetchModelsOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("неверный путь: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("нет авторизации: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "model-b"}, {"id": "model-a"}},
		})
	}))
	defer srv.Close()

	p := &Provider{ID: "test", Kind: ProtoOpenAI, BaseURL: srv.URL + "/v1", Key: "sk-test"}
	models, err := FetchModels(context.Background(), NewClient(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("ожидалось 2 модели, получено %d", len(models))
	}
	// Должны быть отсортированы.
	if models[0] != "model-a" {
		t.Errorf("модели не отсортированы: %v", models)
	}
}

func TestFetchModelsArrayFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"x"},{"id":"y"}]`))
	}))
	defer srv.Close()

	p := &Provider{ID: "t", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k", NoKey: true}
	models, err := FetchModels(context.Background(), NewClient(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Errorf("массив-модели: %v", models)
	}
}

func TestFetchModelsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	p := &Provider{ID: "t", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "bad"}
	_, err := FetchModels(context.Background(), NewClient(), p)
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("код HTTP не указан: %v", err)
	}
}

func TestTestKeyMessages(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{http.StatusUnauthorized, "401"},
		{http.StatusPaymentRequired, "402"},
		{http.StatusForbidden, "403"},
		{http.StatusNotFound, "404"},
		{http.StatusTooManyRequests, "429"},
	}
	for _, c := range cases {
		code := c.code
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		p := &Provider{ID: "t", Kind: ProtoOpenAI, BaseURL: srv.URL, Key: "k"}
		ok, msg := TestKey(NewClient(), p)
		srv.Close()
		if ok {
			t.Errorf("HTTP %d должен давать ошибку", code)
		}
		if !strings.Contains(msg, c.want) {
			t.Errorf("HTTP %d: сообщение %q не содержит %q", code, msg, c.want)
		}
	}
}

func TestTestKeyNoKeyNeeded(t *testing.T) {
	p := &Provider{ID: "ollama", NoKey: true}
	ok, msg := TestKey(NewClient(), p)
	if !ok {
		t.Error("локальный провайдер должен проходить")
	}
	if !strings.Contains(msg, "локальный") {
		t.Errorf("сообщение: %q", msg)
	}
}

func TestTestKeyEmpty(t *testing.T) {
	p := &Provider{ID: "t", Key: ""}
	ok, _ := TestKey(NewClient(), p)
	if ok {
		t.Error("пустой ключ не должен проходить")
	}
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &Provider{ID: "t", Kind: ProtoOpenAI, BaseURL: srv.URL, NoKey: true}
	d, code, err := Ping(NewClient(), p)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Errorf("код: %d", code)
	}
	if d < 0 || d > 10*time.Second {
		t.Errorf("длительность: %v", d)
	}
}

func TestStreamRequiresModel(t *testing.T) {
	p := &Provider{ID: "t", Kind: ProtoOpenAI, BaseURL: "http://x", NoKey: true}
	err := Stream(context.Background(), NewClient(), p, core.ChatRequest{}, "auto", make(chan core.Delta, 1))
	if err == nil || !strings.Contains(err.Error(), "модель") {
		t.Errorf("ожидалась ошибка про модель: %v", err)
	}
}

func TestStreamRequiresKey(t *testing.T) {
	p := &Provider{ID: "t", Kind: ProtoOpenAI, BaseURL: "http://x", Key: ""}
	err := Stream(context.Background(), NewClient(), p, core.ChatRequest{Model: "m"}, "auto", make(chan core.Delta, 1))
	if err == nil || !strings.Contains(err.Error(), "ключ") {
		t.Errorf("ожидалась ошибка про ключ: %v", err)
	}
}

func TestContextLimits(t *testing.T) {
	p := &Provider{ContextLimit: 200_000}
	if p.CtxLimit() != 200_000 {
		t.Errorf("CtxLimit = %d", p.CtxLimit())
	}
	if p.CtxSoftLimit() != 170_000 {
		t.Errorf("CtxSoftLimit = %d, ожидалось 170000", p.CtxSoftLimit())
	}
	p2 := &Provider{}
	if p2.CtxLimit() != 128_000 {
		t.Errorf("лимит по умолчанию: %d", p2.CtxLimit())
	}
}

func findIn(ps []*Provider, id string) *Provider {
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	return nil
}
