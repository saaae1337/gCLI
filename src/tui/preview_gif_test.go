package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPreviewGIF — кадры 88×26 для сверки с витринным GIF
// (.github/assets/iskra-demo.gif). Запускается только с
// GCLI_TUI_PREVIEW=1; текстовые кадры пишутся в тот же каталог
// превью, что и TestPreviewFrames.
func TestPreviewGIF(t *testing.T) {
	if os.Getenv("GCLI_TUI_PREVIEW") == "" {
		t.Skip("GCLI_TUI_PREVIEW не задан")
	}
	dir := filepath.Join(os.TempDir(), "gcli-tui-preview")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Кадр «пустая сессия»: шапка, заставка, поле, статус.
	a := New(Options{
		Version:  "0.9.0.0",
		Model:    "glm-4.6",
		Provider: "zai",
		WorkDir:  "~/demo",
		Commands: []Command{
			{Name: "/help", Desc: "справка по командам"},
			{Name: "/mascot", Desc: "настроить маскота"},
		},
		Color:   true,
		Unicode: true,
	})
	a.started = true
	a.w, a.h = 88, 26
	a.met = Metrics{ContextMax: 200000, Context: 4000}
	writeText(t, filepath.Join(dir, "gif-empty-88.txt"), a.buildLocked())

	// Кадр «диалог»: запрос, размышления, инструмент, ответ, метрики.
	a.doc.AddUser("собери changelog за неделю")
	a.doc.BeginThink()
	a.doc.ThinkText("Сначала посмотрю CHANGELOG.md и недавние коммиты. Потом соберу сводку за семь дней.")
	a.doc.EndThink()
	a.doc.ToolStart(ToolInfo{Name: "read", Args: "CHANGELOG.md", Kind: "read", Detail: "214 строк", Status: "running"})
	a.doc.ToolEnd(ToolInfo{Name: "read", Args: "CHANGELOG.md", Kind: "read", Detail: "214 строк", Status: "ok", Elapsed: 180 * time.Millisecond, Lines: 214})
	a.doc.BeginStream()
	a.doc.Text("За неделю маскот Искра переехал в колонку слева от поля ввода\nи меняет позу вместе с агентом; slash-команды больше не вешают\nинтерфейс (140с → 0.3мс); контекст считается по модели.\n")
	a.doc.EndStream()
	a.doc.SetMeta("18.4с  ·  41 tok/s  ·  ↑2.8k ↓512")
	a.met = Metrics{
		Context: 18000, ContextMax: 200000,
		Prompt: 2800, Completion: 512, Requests: 1,
		Rate: 38, Last: 18 * time.Second,
		Cost: 0.0042, CostKnown: true,
		Session: 12 * time.Minute,
	}
	a.busy = false
	a.tick = 3
	writeText(t, filepath.Join(dir, "gif-dialog-88.txt"), a.buildLocked())

	// Кадр «в работе»: спиннер, метка активности, кот думает.
	a.busy = true
	a.activity = "read CHANGELOG.md"
	a.doc.AddNotice("")
	a.doc.ToolStart(ToolInfo{Name: "read", Args: "git log", Kind: "exec", Status: "running"})
	a.met.Requests = 2
	writeText(t, filepath.Join(dir, "gif-busy-88.txt"), a.buildLocked())

	fmt.Printf("gif-кадры: %s\n", dir)
}
