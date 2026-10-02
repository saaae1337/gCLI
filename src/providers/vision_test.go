package providers

import (
	"encoding/json"
	"strings"
	"testing"

	"gcli/core"
)

// TestOAMessagesWithImages — изображения пользователя превращаются в
// многоконтентное сообщение с data-URL.
func TestOAMessagesWithImages(t *testing.T) {
	creq := core.ChatRequest{
		System: "системный",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: "что на картинке?", Images: []core.Image{
				{MIME: "image/png", Data: "AAA="},
			}},
			{Role: core.RoleUser, Content: "текст без картинки"},
		},
	}
	msgs := buildOAMessages(creq, false)
	if len(msgs) != 3 {
		t.Fatalf("сообщений: %d, ожидалось 3 (system + 2 user)", len(msgs))
	}
	// Первое user-сообщение — массив частей.
	parts, ok := msgs[1].Content.([]oaPart)
	if !ok {
		t.Fatalf("ожидался массив oaPart, получено %T", msgs[1].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("частей: %d, ожидалось 2", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "что на картинке?" {
		t.Errorf("текстовая часть: %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil ||
		parts[1].ImageURL.URL != "data:image/png;base64,AAA=" {
		t.Errorf("картинка: %+v", parts[1])
	}
	// Обычное сообщение остаётся строкой.
	if s, ok := msgs[2].Content.(string); !ok || s != "текст без картинки" {
		t.Errorf("текстовое сообщение: %v (%T)", msgs[2].Content, msgs[2].Content)
	}
}

// TestOAMessagesImageOnly — сообщение только с картинкой (без текста).
func TestOAMessagesImageOnly(t *testing.T) {
	creq := core.ChatRequest{Messages: []core.Message{
		{Role: core.RoleUser, Images: []core.Image{{MIME: "image/jpeg", Data: "BBB="}}},
	}}
	msgs := buildOAMessages(creq, false)
	if len(msgs) != 1 {
		t.Fatalf("сообщений: %d", len(msgs))
	}
	parts, ok := msgs[0].Content.([]oaPart)
	if !ok || len(parts) != 1 || parts[0].Type != "image_url" {
		t.Fatalf("ожидалась одна картинка: %+v (%T)", msgs[0].Content, msgs[0].Content)
	}
	if !strings.Contains(parts[0].ImageURL.URL, "image/jpeg") {
		t.Errorf("mime потерян: %q", parts[0].ImageURL.URL)
	}
}

// TestAnthropicMessagesWithImages — картинки в формате Anthropic:
// блок image с base64-источником, текст — блоком text.
func TestAnthropicMessagesWithImages(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "гляди", Images: []core.Image{
			{MIME: "image/webp", Data: "CCC="},
		}},
	}
	out := buildAnthropicMessages(msgs, false)
	if len(out) != 1 {
		t.Fatalf("сообщений: %d", len(out))
	}
	blocks, ok := out[0].Content.([]anBlock)
	if !ok {
		t.Fatalf("ожидались блоки, получено %T", out[0].Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("блоков: %d, ожидалось 2", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "гляди" {
		t.Errorf("текстовый блок: %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil {
		t.Fatalf("image-блок: %+v", blocks[1])
	}
	if blocks[1].Source.MediaType != "image/webp" || blocks[1].Source.Data != "CCC=" {
		t.Errorf("источник: %+v", blocks[1].Source)
	}
	// Блоки сериализуются корректно (json-теги source).
	raw, _ := json.Marshal(blocks[1])
	if !strings.Contains(string(raw), `"media_type":"image/webp"`) {
		t.Errorf("json image-блока: %s", raw)
	}
}

// TestAnthropicToolResultWithImages — результат инструмента с картинкой
// сериализуется как массив блоков в tool_result.
func TestAnthropicToolResultWithImages(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "сделай скриншот"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "t1", Name: "screenshot", Args: "{}"}}},
		{Role: core.RoleTool, ToolCallID: "t1", Content: "снимок готов", Images: []core.Image{
			{MIME: "image/png", Data: "DDD="},
		}},
	}
	out := buildAnthropicMessages(msgs, false)
	// user + assistant с tool_use + user с tool_result.
	if len(out) != 3 {
		t.Fatalf("сообщений: %d", len(out))
	}
	blocks, ok := out[2].Content.([]anBlock)
	if !ok || len(blocks) != 1 || blocks[0].Type != "tool_result" {
		t.Fatalf("ожидался tool_result: %+v", out[1].Content)
	}
	tb, ok := blocks[0].Content.([]anBlock)
	if !ok || len(tb) != 2 {
		t.Fatalf("tool_result должен быть массивом блоков: %T %v", blocks[0].Content, blocks[0].Content)
	}
	if tb[0].Type != "text" || tb[0].Text != "снимок готов" {
		t.Errorf("текст в tool_result: %+v", tb[0])
	}
	if tb[1].Type != "image" || tb[1].Source == nil || tb[1].Source.Data != "DDD=" {
		t.Errorf("картинка в tool_result: %+v", tb[1])
	}
}
