package tui

import (
	"io"
	"strings"
	"testing"
)

// Бенчмарки кадра — базовая линия стоимости отрисовки. Задача итерации
// 0.9.0.2: измерить сборку кадра (buildLocked) и полный кадр с выводом
// в пустой терминал (drawLocked) на типичных размерах окна.
//
// rend.ready поднимается руками: Start() пишет escape-последовательности
// в реальный терминал, а бенчмарку нужен только рабочий путь Draw.

func benchApp(b *testing.B, w, h int) *App {
	b.Helper()
	app := newTestApp(strings.NewReader(""), io.Discard)
	app.mu.Lock()
	defer app.mu.Unlock()
	app.started = true
	app.w, app.h = w, h
	app.rend.mu.Lock()
	app.rend.ready = true
	app.rend.mu.Unlock()
	return app
}

func seedFeed(a *App) {
	// Типичная лента: вопрос, ответ с разметкой, заметка, размышление
	// и стрим-хвост. Составные символы держат вычислитель ширины
	// в рабочем режиме.
	a.doc.AddUser("Покажи, как устроен расчёт ширины в wcwidth?")
	a.doc.AddNotice("инструмент read: wcwidth/width.go — 750 строк")
	a.doc.BeginThink()
	a.doc.StreamText("Смотрю на кластеры: ZWJ склеивает руны, флаги парные…")
	a.doc.EndStream()
	a.doc.Text("# Ширина кластера\n\n" +
		"Каждый кластер оценивается как **одна ячейка** либо две:\n\n" +
		"- `🇷🇺` — два региональных индикатора, ширина 2;\n" +
		"- `👨‍👩‍👧‍👦` — семья на ZWJ, ширина 2, а не 8;\n" +
		"- `👍🏽` — эмодзи с тоном кожи, ширина 2.\n\n" +
		"ANSI-коды (\x1b[1mжирный\x1b[0m) ширины не добавляют.")
	a.doc.BeginStream()
	a.doc.StreamText("Итог: инвариант — ширина строки с escape-кодами равна ширине после чистки.")
	a.doc.EndStream()
	a.doc.SetMeta("модель test-model · 1234 токенов")
}

func BenchmarkFrameBuild(b *testing.B) {
	for _, tc := range []struct {
		name string
		w, h int
		feed bool
	}{
		{"пусто 80x24", 80, 24, false},
		{"лента 80x24", 80, 24, true},
		{"лента 120x40", 120, 40, true},
		{"лента 240x60", 240, 60, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			app := benchApp(b, tc.w, tc.h)
			if tc.feed {
				app.mu.Lock()
				seedFeed(app)
				app.mu.Unlock()
			}
			app.mu.Lock()
			defer app.mu.Unlock()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = app.buildLocked()
			}
		})
	}
}

func BenchmarkFrameDraw(b *testing.B) {
	for _, tc := range []struct {
		name string
		w, h int
		feed bool
	}{
		{"пусто 80x24", 80, 24, false},
		{"лента 80x24", 80, 24, true},
		{"лента 120x40", 120, 40, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			app := benchApp(b, tc.w, tc.h)
			if tc.feed {
				app.mu.Lock()
				seedFeed(app)
				app.mu.Unlock()
			}
			app.mu.Lock()
			defer app.mu.Unlock()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				app.drawLocked()
			}
		})
	}
}
