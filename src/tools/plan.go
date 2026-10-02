package tools

import (
	"context"
	"fmt"
	"strings"

	"gcli/core"
)

// hTodoWrite — обновить план работ.
func (r *Registry) hTodoWrite(_ context.Context, m map[string]any) (Result, error) {
	todos := ParseTodos(m)
	if todos == nil {
		return Result{}, fmt.Errorf("укажи todos — массив объектов {content, status}")
	}
	if r.env.Session != nil {
		r.env.Session.SetTodos(todos)
	}
	if r.env.OnTodo != nil {
		r.env.OnTodo(todos)
	}
	var b strings.Builder
	for i, t := range todos {
		mark := "☐"
		switch t.Status {
		case core.TodoInProgress:
			mark = "▸"
		case core.TodoCompleted:
			mark = "✔"
		}
		fmt.Fprintf(&b, "%s %d. %s\n", mark, i+1, t.Content)
	}
	return Result{
		Text:    fmt.Sprintf("Список задач обновлён (%d позиций). Продолжай по плану.\n\n%s", len(todos), b.String()),
		Summary: fmt.Sprintf("план: %d задач", len(todos)),
	}, nil
}

// ParseTodos — разобрать аргумент todos.
func ParseTodos(m map[string]any) []TodoItem {
	raw, ok := m["todos"].([]any)
	if !ok {
		return nil
	}
	var out []TodoItem
	for _, it := range raw {
		im, ok := it.(map[string]any)
		if !ok {
			continue
		}
		t := TodoItem{Content: ArgStr(im, "content"), Status: core.ValidStatus(ArgStr(im, "status"))}
		if t.Content != "" {
			out = append(out, t)
		}
	}
	return out
}

// hThink — зафиксировать размышление (содержимое в модель не возвращается,
// чтобы не раздувать контекст).
func (r *Registry) hThink(_ context.Context, m map[string]any) (Result, error) {
	th := ArgStr(m, "thought")
	if strings.TrimSpace(th) == "" {
		return Result{}, fmt.Errorf("укажи thought")
	}
	return Result{
		Text:    fmt.Sprintf("Размышление зафиксировано (%d симв.). Продолжай к действиям.", len([]rune(th))),
		Summary: "размышление зафиксировано",
	}, nil
}

// hNote — важная заметка в памяти задачи.
func (r *Registry) hNote(_ context.Context, m map[string]any) (Result, error) {
	title := strings.TrimSpace(ArgStr(m, "title"))
	body := strings.TrimSpace(ArgStr(m, "body"))
	if title == "" {
		return Result{}, fmt.Errorf("укажи title")
	}
	if body == "" {
		return Result{}, fmt.Errorf("укажи body")
	}
	if r.env.OnNote != nil {
		r.env.OnNote(title, body)
	}
	return Result{
		Text:    fmt.Sprintf("Заметка «%s» сохранена в памяти задачи (%d симв.).", title, len([]rune(body))),
		Summary: "заметка: " + title,
	}, nil
}
