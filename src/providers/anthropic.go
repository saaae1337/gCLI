package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gcli/core"
)

// ---------- Протокол Anthropic ----------

type anMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// Зрение: блок изображения (type=image).
	Source *anImageSource `json:"source,omitempty"`
}

// anImageSource — base64-источник изображения для Anthropic.
type anImageSource struct {
	Type      string `json:"type"` // base64
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anRequest struct {
	Model       string      `json:"model"`
	System      string      `json:"system,omitempty"`
	Messages    []anMessage `json:"messages"`
	Tools       []anTool    `json:"tools,omitempty"`
	MaxTokens   int         `json:"max_tokens"`
	Temperature float64     `json:"temperature,omitempty"`
	Stream      bool        `json:"stream"`
	Thinking    *anThinking `json:"thinking,omitempty"`
}

type anThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
	ContentBlock *struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input any    `json:"input"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Message *struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// buildAnthropicMessages — конвертация сообщений в формат Anthropic.
//
// Особенности протокола:
//   - соседние сообщения одной роли склеиваются;
//   - assistant с thinking должен содержать подписанный блок thinking;
//   - результат инструмента — это блок tool_result в сообщении пользователя;
//   - первым обязательно идёт user.
func buildAnthropicMessages(msgs []core.Message, withThinking bool) []anMessage {
	var out []anMessage
	push := func(role string, blocks []anBlock) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			if prev, ok := out[n-1].Content.([]anBlock); ok {
				out[n-1].Content = append(prev, blocks...)
				return
			}
		}
		out = append(out, anMessage{Role: role, Content: blocks})
	}
	for _, m := range msgs {
		switch m.Role {
		case core.RoleUser:
			if strings.TrimSpace(m.Content) == "" && len(m.Images) == 0 {
				continue
			}
			text := m.Content
			if m.Sub != "" {
				text = fmt.Sprintf("[субагент %s]\n%s", m.Sub, text)
			}
			var bl []anBlock
			if strings.TrimSpace(text) != "" {
				bl = append(bl, anBlock{Type: "text", Text: text})
			}
			// Зрение: изображения как блоки image с base64-источником.
			for _, img := range m.Images {
				mime := img.MIME
				if mime == "" {
					mime = "image/png"
				}
				bl = append(bl, anBlock{Type: "image", Source: &anImageSource{
					Type: "base64", MediaType: mime, Data: img.Data,
				}})
			}
			if len(bl) == 0 {
				continue
			}
			push("user", bl)
		case core.RoleAssistant:
			var bl []anBlock
			if withThinking && m.Reasoning != "" && m.ReasoningSig != "" {
				bl = append(bl, anBlock{Type: "thinking", Thinking: m.Reasoning, Signature: m.ReasoningSig})
			}
			if m.Content != "" {
				bl = append(bl, anBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input map[string]any
				_ = json.Unmarshal([]byte(tc.Args), &input)
				if input == nil {
					input = map[string]any{}
				}
				bl = append(bl, anBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			push("assistant", bl)
		case core.RoleTool:
			tr := anBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			// Изображения в результате инструмента — массив блоков вместо строки.
			if len(m.Images) > 0 {
				var tb []anBlock
				if strings.TrimSpace(m.Content) != "" {
					tb = append(tb, anBlock{Type: "text", Text: m.Content})
				}
				for _, img := range m.Images {
					mime := img.MIME
					if mime == "" {
						mime = "image/png"
					}
					tb = append(tb, anBlock{Type: "image", Source: &anImageSource{
						Type: "base64", MediaType: mime, Data: img.Data,
					}})
				}
				if len(tb) > 0 {
					tr.Content = tb
				}
			}
			push("user", []anBlock{tr})
		}
	}
	for len(out) > 0 && out[0].Role != "user" {
		out = out[1:]
	}
	// Последнее сообщение не должно оставаться «висящим» tool_use без результата —
	// Anthropic отвергает такой запрос. Добавляем пустой user-блок.
	if n := len(out); n > 0 {
		if bl, ok := out[n-1].Content.([]anBlock); ok && hasToolUse(bl) {
			out = append(out, anMessage{Role: "user", Content: []anBlock{{Type: "text", Text: "(продолжай)"}}})
		}
	}
	return out
}

func hasToolUse(bl []anBlock) bool {
	for _, b := range bl {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

// StreamAnthropic — стриминг через протокол Anthropic.
func StreamAnthropic(ctx context.Context, c *Client, p *Provider, creq core.ChatRequest, think string, out chan<- core.Delta) error {
	body := anRequest{
		Model:     creq.Model,
		System:    creq.System,
		Messages:  buildAnthropicMessages(creq.Messages, think == "on"),
		MaxTokens: creq.MaxTokens,
		Stream:    true,
	}
	if !isOModel(creq.Model) {
		body.Temperature = creq.Temp
	}
	if think == "on" {
		// Extended thinking: temperature не задаётся, max_tokens > budget.
		budget := 8192
		body.Thinking = &anThinking{Type: "enabled", BudgetTokens: budget}
		if body.MaxTokens <= budget {
			body.MaxTokens = budget * 2
		}
		body.Temperature = 0 // omitempty уберёт поле из JSON
	}
	for _, td := range creq.ToolDefs {
		body.Tools = append(body.Tools, anTool{
			Name: td.Name, Description: td.Description, InputSchema: json.RawMessage(td.Schema),
		})
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := newChatRequest(ctx, p, strings.TrimRight(p.BaseURL, "/")+"/v1/messages", payload)
	if err != nil {
		return err
	}

	return c.doSSE(ctx, req, func(data string) error {
		var e anEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return nil
		}
		switch e.Type {
		case "message_start":
			if e.Message != nil && e.Message.Usage != nil {
				out <- core.Delta{Usage: &core.Usage{PromptTokens: e.Message.Usage.InputTokens}}
			}
		case "content_block_start":
			if e.ContentBlock != nil && e.ContentBlock.Type == "tool_use" {
				out <- core.Delta{IsTool: true, TCIndex: e.Index, TCID: e.ContentBlock.ID, TCName: e.ContentBlock.Name}
			}
		case "content_block_delta":
			if e.Delta != nil {
				switch e.Delta.Type {
				case "text_delta":
					if e.Delta.Text != "" {
						out <- core.Delta{Text: e.Delta.Text}
					}
				case "thinking_delta":
					if e.Delta.Thinking != "" {
						out <- core.Delta{Reasoning: e.Delta.Thinking}
					}
				case "signature_delta":
					if e.Delta.Signature != "" {
						out <- core.Delta{Sig: e.Delta.Signature}
					}
				case "input_json_delta":
					if e.Delta.PartialJSON != "" {
						out <- core.Delta{IsTool: true, TCIndex: e.Index, TCArgs: e.Delta.PartialJSON}
					}
				}
			}
		case "message_delta":
			if e.Delta != nil && e.Delta.StopReason != "" {
				out <- core.Delta{Finish: e.Delta.StopReason}
			}
			if e.Usage != nil {
				out <- core.Delta{Usage: &core.Usage{CompletionTokens: e.Usage.OutputTokens}}
			}
		case "error":
			msg := "ошибка API"
			if e.Error != nil && e.Error.Message != "" {
				msg = e.Error.Message
			}
			return fmt.Errorf("Anthropic: %s", msg)
		}
		return nil
	})
}
