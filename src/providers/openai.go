package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"gcli/core"
)

// ---------- OpenAI-совместимый протокол ----------

type oaMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

// oaPart — часть многоконтентного сообщения (текст или изображение).
type oaPart struct {
	Type     string `json:"type"` // text | image_url
	Text     string `json:"text,omitempty"`
	ImageURL *oaImg `json:"image_url,omitempty"`
}

type oaImg struct {
	URL string `json:"url"`
}

type oaToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function oaFunc `json:"function"`
}

type oaFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaTool struct {
	Type     string     `json:"type"`
	Function oaToolFunc `json:"function"`
}

type oaToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type oaRequest struct {
	Model      string      `json:"model"`
	Messages   []oaMessage `json:"messages"`
	Tools      []oaTool    `json:"tools,omitempty"`
	Stream     bool        `json:"stream"`
	StreamOpts *oaStream   `json:"stream_options,omitempty"`
	MaxTokens  int         `json:"max_tokens,omitempty"`
	// MaxCompletionTokens — имя того же лимита для o-серии/gpt-5:
	// OpenAI отвергает max_tokens у этих моделей.
	MaxCompletionTokens int     `json:"max_completion_tokens,omitempty"`
	Temperature         float64 `json:"temperature,omitempty"`

	// Размышления: у каждого endpoint-а свой параметр.
	Thinking        *oaThinking  `json:"thinking,omitempty"`
	Reasoning       *oaReasoning `json:"reasoning,omitempty"`
	ReasoningEffort string       `json:"reasoning_effort,omitempty"`
	EnableThinking  *bool        `json:"enable_thinking,omitempty"`
}

type oaStream struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaThinking struct {
	Type string `json:"type"` // enabled | disabled
}

type oaReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type oaDelta struct {
	Role             string       `json:"role"`
	Content          string       `json:"content"`
	Refusal          string       `json:"refusal"`
	ReasoningContent string       `json:"reasoning_content"`
	Reasoning        string       `json:"reasoning"`
	Thinking         string       `json:"thinking"`
	ToolCalls        []oaToolCall `json:"tool_calls"`
}

type oaChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Index        int     `json:"index"`
		Delta        oaDelta `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// isOModel — модель OpenAI o-серии: temperature не поддерживается.
func isOModel(m string) bool {
	l := strings.ToLower(m)
	base := l
	if i := strings.LastIndexAny(l, "/:"); i >= 0 {
		base = l[i+1:]
	}
	return strings.HasPrefix(base, "o1") || strings.HasPrefix(base, "o3") ||
		strings.HasPrefix(base, "o4") || strings.HasPrefix(base, "gpt-5")
}

// buildOAMessages — конвертация универсальных сообщений в формат OpenAI.
func buildOAMessages(creq core.ChatRequest, keepReasoning bool) []oaMessage {
	msgs := make([]oaMessage, 0, len(creq.Messages)+1)
	if creq.System != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: creq.System})
	}
	for _, m := range creq.Messages {
		switch m.Role {
		case core.RoleUser:
			content := m.Content
			if m.Sub != "" {
				content = fmt.Sprintf("[субагент %s]\n%s", m.Sub, content)
			}
			if len(m.Images) > 0 {
				// Зрение: многоконтентное сообщение — текст + изображения
				// (data-URL с base64).
				parts := make([]oaPart, 0, len(m.Images)+1)
				if strings.TrimSpace(content) != "" {
					parts = append(parts, oaPart{Type: "text", Text: content})
				}
				for _, img := range m.Images {
					mime := img.MIME
					if mime == "" {
						mime = "image/png"
					}
					parts = append(parts, oaPart{Type: "image_url", ImageURL: &oaImg{
						URL: "data:" + mime + ";base64," + img.Data,
					}})
				}
				msgs = append(msgs, oaMessage{Role: "user", Content: parts})
				continue
			}
			msgs = append(msgs, oaMessage{Role: "user", Content: content})
		case core.RoleAssistant:
			om := oaMessage{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				args := tc.Args
				if strings.TrimSpace(args) == "" {
					// Пустые аргументы — невалидный JSON в истории вызова;
					// строгие OpenAI-совместимые API отвечают на него 400.
					args = "{}"
				}
				om.ToolCalls = append(om.ToolCalls, oaToolCall{
					ID: tc.ID, Type: "function",
					Function: oaFunc{Name: tc.Name, Arguments: args},
				})
			}
			// Пустое assistant-сообщение (ни текста, ни вызовов — например,
			// ответ, съеденный reasoning-бюджетом) в запрос API не идёт:
			// строгое API отвечает на {"role":"assistant"} без полей 400-й
			// ошибкой, и история отравляется на все следующие ходы.
			if om.Content == "" && om.ToolCalls == nil {
				continue
			}
			msgs = append(msgs, om)
		case core.RoleTool:
			msgs = append(msgs, oaMessage{Role: "tool", Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name})
		}
	}
	return msgs
}

// StreamOpenAI — стриминг через OpenAI-совместимый протокол.
func StreamOpenAI(ctx context.Context, c *Client, p *Provider, creq core.ChatRequest, think string, out chan<- core.Delta) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil {
		return fmt.Errorf("провайдер не задан")
	}
	body := oaRequest{
		Model:    creq.Model,
		Messages: buildOAMessages(creq, think != "off"),
		Stream:   true,
	}
	if isOModel(creq.Model) {
		body.MaxCompletionTokens = creq.MaxTokens
	} else {
		body.MaxTokens = creq.MaxTokens
	}
	// include_usage нужен и без инструментов: иначе расход обычного
	// текстового хода не доезжает до /usage вовсе.
	body.StreamOpts = &oaStream{IncludeUsage: true}
	if !isOModel(creq.Model) {
		body.Temperature = creq.Temp
	}
	applyThinkingOA(p, &body, creq.Model, think)
	for _, td := range creq.ToolDefs {
		body.Tools = append(body.Tools, oaTool{
			Type:     "function",
			Function: oaToolFunc{Name: td.Name, Description: td.Description, Parameters: toolSchema(td.Schema)},
		})
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if os.Getenv("GCLI_DEBUG_BODY") != "" {
		fmt.Fprintf(os.Stderr, "=== REQUEST BODY ===\n%s\n=== END ===\n", payload)
	}
	req, err := newChatRequest(ctx, p, strings.TrimRight(p.BaseURL, "/")+"/chat/completions", payload)
	if err != nil {
		return err
	}

	parseFails := 0
	return c.streamWithKeys(ctx, p, req, func(data string) error {
		var ch oaChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			parseFails++
			if parseFails > 8 {
				return fmt.Errorf("не удалось разобрать ответ API (похоже, не OpenAI-совместимый endpoint)")
			}
			return nil
		}
		if ch.Error != nil && ch.Error.Message != "" {
			err := apiError(ch.Error.Message)
			// Некоторые провайдеры отвечают кодом 200, а ошибку кладут
			// в тело: «insufficient_quota», «余额不足». По ключу это
			// выглядит как превышение лимита, и ключ надо менять.
			if reason, ok := quotaReason(ch.Error.Message, ch.Error.Type); ok {
				return &keyError{
					err:    &retryErr{err},
					pause:  KeyPauseLimit,
					reason: reason,
				}
			}
			if retryableStreamError(ch.Error.Code, ch.Error.Type) {
				return &retryErr{err}
			}
			return err
		}
		if ch.Usage != nil {
			out <- core.Delta{Usage: &core.Usage{
				PromptTokens:     ch.Usage.PromptTokens,
				CompletionTokens: ch.Usage.CompletionTokens,
			}}
		}
		for _, chc := range ch.Choices {
			d := chc.Delta
			if r := extractReasoning(d.ReasoningContent, d.Reasoning, d.Thinking); r != "" {
				out <- core.Delta{Reasoning: r}
			}
			if d.Content != "" {
				out <- core.Delta{Text: d.Content}
			}
			// Отказ модели — такой же видимый ответ, как и текст.
			// Без этого ход выглядел пустым, и человек не понимал,
			// почему модель промолчала.
			if d.Refusal != "" {
				out <- core.Delta{Text: d.Refusal}
			}
			for _, tc := range d.ToolCalls {
				out <- core.Delta{
					IsTool:  true,
					TCIndex: tc.Index,
					TCID:    tc.ID,
					TCName:  tc.Function.Name,
					TCArgs:  tc.Function.Arguments,
				}
			}
			if chc.FinishReason != nil && *chc.FinishReason != "" {
				out <- core.Delta{Finish: *chc.FinishReason}
			}
		}
		return nil
	})
}

// apiError — понятно объяснить ошибку, пришедшую от провайдера.
//
// Голые «API: …» строки бесполезны: половина таких ошибок — это не поломка,
// а следствие настройки. Отдельно разбираем пустой ответ: почти всегда это
// размышления, изъевшие весь max_tokens (finish_reason=length), и лечится
// увеличением бюджета, а не сменой ключа. Сам текст размышлений в ошибку
// не попадает — это приватная кухня модели, пользователю она не нужна.
func apiError(msg string) error {
	wrapped := fmt.Errorf("API: %s", msg)
	if !strings.Contains(strings.ToLower(msg), "empty response") {
		return wrapped
	}
	hint := "провайдер вернул пустой ответ. Чаще всего размышления " +
		"изъели весь лимит токенов ответа: попробуй /think off или модель без thinking. " +
		"Если не помогло — проверь, что у модели есть лимит на ответ " +
		"(max_tokens) и что она не отдаёт только thinking без текста"
	return fmt.Errorf("%w (%s): %w", ErrEmptyResponse, hint, wrapped)
}

// ErrEmptyResponse — провайдер не отдал ни текста, ни вызовов инструментов.
//
// Метка, а не разбор строки: агенту нужно отличить этот случай от прочих
// ошибок, чтобы повторить ход с другим бюджетом. Само сообщение провайдера
// может быть любым, а проверять его каждый раз по подстроке хрупко.
var ErrEmptyResponse = errors.New("пустой ответ модели")

// retryableStreamError — ошибка пришла внутри SSE-потока, но по смыслу
// это перегрузка/лимит: повтор до первого события уместен.
func retryableStreamError(code any, typ string) bool {
	switch v := code.(type) {
	case float64:
		return v == http.StatusTooManyRequests || v >= 500
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n == http.StatusTooManyRequests || n >= 500
		}
	}
	t := strings.ToLower(typ)
	return strings.Contains(t, "rate_limit") || strings.Contains(t, "overloaded") ||
		strings.Contains(t, "server_error")
}

// quotaReason — ошибка про исчерпанный лимит или баланс.
//
// Провайдеры пишут об этом по-разному, и в теле ответа код ответа часто
// 200: «insufficient_quota», «exceeded your current quota», «余额不足»
// (баланс), «限流» (лимит). По любому из них ключ надо менять — иначе
// сессия встанет намертво.
func quotaReason(msg, typ string) (string, bool) {
	m := strings.ToLower(msg + " " + typ)
	switch {
	case strings.Contains(m, "insufficient_quota"),
		strings.Contains(m, "exceeded your current quota"),
		strings.Contains(m, "quota exceeded"),
		strings.Contains(m, "insufficient balance"),
		strings.Contains(m, "insufficient funds"),
		strings.Contains(m, "billing"):
		return "квота исчерпана", true
	case strings.Contains(m, "余额不足"), strings.Contains(m, "余额"):
		return "нет средств на балансе", true
	case strings.Contains(m, "限流"), strings.Contains(m, "rate limit"),
		strings.Contains(m, "rate_limit"), strings.Contains(m, "too many requests"):
		return "лимит запросов", true
	}
	return "", false
}

// toolSchema — JSON-схема параметров инструмента.
//
// Пустая схема превращается в объект: null в parameters/input_schema
// строгие API отвергают, и инструмент становится невызываемым.
func toolSchema(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = `{"type":"object"}`
	}
	return json.RawMessage(raw)
}

// applyThinkingOA — добавить параметры размышлений под конкретный endpoint.
func applyThinkingOA(p *Provider, body *oaRequest, model, think string) {
	lm := strings.ToLower(model)
	isOpenRouter := p.ID == "openrouter" || strings.Contains(p.BaseURL, "openrouter")

	switch {
	case think == "off":
		if strings.Contains(lm, "glm") {
			body.Thinking = &oaThinking{Type: "disabled"}
		}
		return
	case strings.Contains(lm, "glm"):
		body.Thinking = &oaThinking{Type: "enabled"}
		return
	case think == "auto":
		return // остальные — просто показываем то, что пришлёт модель
	}

	// think == "on": просим thinking у тех, кто понимает параметры.
	switch {
	case strings.Contains(lm, "qwen"):
		t := true
		body.EnableThinking = &t
	case isOModel(model) || strings.Contains(lm, "gpt-5"):
		body.ReasoningEffort = "medium"
	case isOpenRouter:
		body.Reasoning = &oaReasoning{Effort: "medium"}
	}
}

// extractReasoning — достать размышления из любого известного поля.
func extractReasoning(reasoningContent, reasoning, thinking string) string {
	switch {
	case reasoningContent != "":
		return reasoningContent
	case reasoning != "":
		return reasoning
	}
	return thinking
}

// newChatRequest — HTTP-запрос к чату с авторизацией провайдера.
func newChatRequest(ctx context.Context, p *Provider, url string, payload []byte) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Ключ берётся из пула: активным может быть не первый.
	p.applyKey(req, p.CurrentKey())
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range p.ExtraHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}
