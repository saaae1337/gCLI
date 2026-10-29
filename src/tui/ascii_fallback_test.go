package tui

import (
	"strings"
	"testing"
)

// TestASCIIFallback — в ASCII-режиме в кадре не остаётся фирменных
// юникодных знаков: у каждого есть замена из чистого ASCII.
func TestASCIIFallback(t *testing.T) {
	a := New(Options{
		Version: "0.9.0.1", Model: "glm-4.6", Provider: "zai",
		WorkDir: "~/demo", Color: true, Unicode: false,
	})
	a.started = true
	a.w, a.h = 88, 26
	a.met = Metrics{ContextMax: 200000, Context: 4000}

	// Заставка: шапка со звёздами, подсказки, поле.
	frames := []*Screen{a.buildLocked()}

	// Диалог: ромбы блоков, спиннер размышлений, статус с бегунком.
	a.doc.AddUser("проверка рамок ◆ ✦ ❯ и длинный запрос, который точно переносится на вторую строку поля")
	a.doc.BeginThink()
	a.doc.ThinkText("думаю про кадр")
	a.doc.EndThink()
	a.doc.ToolStart(ToolInfo{Name: "read", Args: "x.go", Kind: "read", Status: "running"})
	a.doc.ToolEnd(ToolInfo{Name: "read", Args: "x.go", Kind: "read", Status: "ok"})
	a.doc.BeginStream()
	a.doc.Text("ответ\nна две строки\n")
	a.doc.EndStream()
	a.doc.SetMeta("18.4с  ·  41 tok/s  ·  ↑2.8k ↓512")
	a.busy = true
	a.activity = "read x.go"
	frames = append(frames, a.buildLocked())
	a.busy = false

	for i, s := range frames {
		for _, c := range s.Cells {
			if c.Cont {
				continue
			}
			if _, bad := asciiFallbackMap[c.R]; bad {
				t.Fatalf("кадр %d: незамещённый фирменный знак %q", i, c.R)
			}
			if strings.ContainsRune(c.Rest, '\u200d') || strings.ContainsRune(c.Rest, '\ufe0f') {
				t.Fatalf("кадр %d: кластер %q с хвостом %q без замены", i, c.R, c.Rest)
			}
		}
	}
}
