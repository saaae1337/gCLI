package ui

// Тесты собственного интерфейса: панель окна, нижняя строка метрик и
// скруглённые панели.

import (
	"strings"
	"testing"
	"time"
)

// chromeUI — интерфейс фиксированной ширины для проверки вёрстки.
func chromeUI(t *testing.T, width int) (*UI, *strings.Builder) {
	t.Helper()
	buf := &strings.Builder{}
	return New(Options{Theme: "ember", Unicode: true, Color: false, Width: width, Out: buf}), buf
}

// TestMetricsBarShowsOutputRateAndRequestTime — в панели видно скорость
// ВЫВОДА и время запроса, а не скорость всего запроса и не время сессии.
func TestMetricsBarShowsOutputRateAndRequestTime(t *testing.T) {
	u, buf := chromeUI(t, 90)
	u.MetricsBar([]StatusItem{{Text: "glm-4.6", Kind: StatusMuted}}, Metrics{
		Model: "glm-4.6", Context: 12400, ContextMax: 128000,
		Prompt: 13000, Completion: 115, Rate: 19, Last: 6 * time.Second,
		Session: 3 * time.Minute, Cost: 0.012, CostKnown: true,
	})
	out := buf.String()
	for _, want := range []string{"вывод 19 tok/s", "запрос 6.0с", "ctx 9%"} {
		if !strings.Contains(out, want) {
			t.Errorf("в панели метрик нет %q:\n%s", want, out)
		}
	}
	// Время сессии в панели мешает: рядом с временем запроса его читают
	// как «сколько ждал этот ответ».
	if strings.Contains(out, "сессия") {
		t.Errorf("в панели осталось время сессии:\n%s", out)
	}
}

// TestMetricsBarDoesNotFillWidth — строка не должна кончаться ровно на
// краю терминала: там терминал оставляет курсор в состоянии «перенос
// отложен», и следующая запись уезжает в неожиданную колонку.
func TestMetricsBarDoesNotFillWidth(t *testing.T) {
	const width = 60
	u, buf := chromeUI(t, width)
	u.MetricsBar([]StatusItem{{Text: "модель", Kind: StatusMuted}}, Metrics{
		Model: "модель", Context: 12400, ContextMax: 128000,
		Prompt: 13000, Completion: 115, Rate: 19, Last: 6 * time.Second,
	})
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if w := cellWidth(l); w >= width {
			t.Errorf("строка шириной %d при ширине терминала %d: %q", w, width, l)
		}
	}
}

// TestTitleBarHasLightsAndRule — панель окна: «лампочки», название и
// линия под панелью.
func TestTitleBarHasLightsAndRule(t *testing.T) {
	u, buf := chromeUI(t, 80)
	u.TitleBar(TitleBarOpts{
		Title:    "gcli v0.6.2.30",
		Subtitle: "ИИ-агент в терминале",
		Right:    "ctx 12.4k/128k (10%)",
	})
	out := buf.String()
	if !strings.Contains(out, "●") {
		t.Errorf("в панели нет лампочек:\n%s", out)
	}
	if !strings.Contains(out, "gcli v0.6.2.30") {
		t.Errorf("в панели нет названия:\n%s", out)
	}
	if !strings.Contains(out, "12.4k/128k") {
		t.Errorf("в панели нет метрик контекста:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("панель без линии:\n%s", out)
	}
	if !strings.Contains(lines[1], "─") {
		t.Errorf("под панелью нет линии:\n%s", out)
	}
}

// TestTitleBarFitsWidth — панель не должна вылезать за ширину терминала.
func TestTitleBarFitsWidth(t *testing.T) {
	u, buf := chromeUI(t, 50)
	u.TitleBar(TitleBarOpts{
		Title:    "gcli",
		Subtitle: "ИИ-агент в терминале",
		Right:    "ctx 12.4k/128k (10%)",
	})
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if w := cellWidth(l); w > 50 {
			t.Errorf("строка шириной %d вместо 50: %q", w, l)
		}
	}
}

// TestMetricsBarShowsRateAndPercent — в нижней панели видно скорость
// токенов и процент занятого контекста.
func TestMetricsBarShowsRateAndPercent(t *testing.T) {
	u, buf := chromeUI(t, 80)
	u.MetricsBar([]StatusItem{{Text: "glm-4.6", Kind: StatusMuted}}, Metrics{
		Model: "glm-4.6", Context: 12400, ContextMax: 128000,
		Prompt: 1200, Completion: 340, Rate: 142, Last: 4200 * time.Millisecond,
	})
	out := buf.String()
	for _, want := range []string{"142 tok/s", "ctx 9%", "4.2с", "↑1.2k"} {
		if !strings.Contains(out, want) {
			t.Errorf("в панели метрик нет %q:\n%s", want, out)
		}
	}
}

// TestMetricsBarDropsLeastImportant — в узкий терминал панель не должна
// вылезать: лишние метрики отпадают, главные остаются.
func TestMetricsBarDropsLeastImportant(t *testing.T) {
	u, buf := chromeUI(t, 40)
	u.MetricsBar([]StatusItem{{Text: "glm-4.6", Kind: StatusMuted}}, Metrics{
		Model: "glm-4.6", Context: 12400, ContextMax: 128000,
		Prompt: 1200, Completion: 340, Rate: 142, Last: 4200 * time.Millisecond,
		Session: 4 * time.Minute, Cost: 0.01, CostKnown: true,
	})
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if w := cellWidth(l); w > 40 {
			t.Errorf("строка шириной %d вместо 40: %q", w, l)
		}
	}
}

// TestPanelBordersAlign — рамка панели должна сходиться по краям.
func TestPanelBordersAlign(t *testing.T) {
	u, buf := chromeUI(t, 60)
	u.Panel("первая строка\nвторая строка", PanelOpts{Title: "Контекст"})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("панель собрана неверно:\n%s", buf.String())
	}
	want := cellWidth(lines[0])
	for i, l := range lines {
		if w := cellWidth(l); w != want {
			t.Errorf("строка %d шириной %d вместо %d: %q", i, w, want, l)
		}
	}
	if !strings.Contains(lines[0], "Контекст") {
		t.Errorf("в верхней границе нет заголовка: %q", lines[0])
	}
}

// TestPanelASCIIFallback — без юникода рамка становится ASCII.
func TestPanelASCIIFallback(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{Theme: "ember", Unicode: false, Color: false, Width: 40, Out: buf})
	u.Panel("текст", PanelOpts{Title: "Панель"})
	out := buf.String()
	if strings.Contains(out, "╭") || strings.Contains(out, "│") {
		t.Errorf("в ASCII-режиме осталась юникодная рамка:\n%s", out)
	}
	if !strings.Contains(out, "+") {
		t.Errorf("в ASCII-режиме нет рамки:\n%s", out)
	}
}

// TestMetricsContextPct — процент занятого контекста считается по лимиту
// модели; без лимита процент неизвестен.
func TestMetricsContextPct(t *testing.T) {
	m := Metrics{Context: 12400, ContextMax: 128000}
	if got := m.ContextPct(); got != 9 {
		t.Errorf("процент = %d, ожидалось 9", got)
	}
	m.ContextMax = 0
	if got := m.ContextPct(); got != 0 {
		t.Errorf("без лимита процент = %d, ожидалось 0", got)
	}
	if got := m.ContextRatio(); got != 0 {
		t.Errorf("без лимита доля = %v, ожидалось 0", got)
	}
}

// TestMetricsRateText — скорость округляется и не показывается без
// запросов.
func TestMetricsRateText(t *testing.T) {
	if got := (Metrics{}).RateText(); got != "" {
		t.Errorf("без запросов скорость = %q", got)
	}
	if got := (Metrics{Rate: 142.4}).RateText(); got != "142 tok/s" {
		t.Errorf("скорость = %q, ожидалось «142 tok/s»", got)
	}
	if got := (Metrics{Rate: 3.5}).RateText(); got != "3.5 tok/s" {
		t.Errorf("скорость = %q, ожидалось «3.5 tok/s»", got)
	}
}

// TestUsageLineShowsRate — строка расхода под ответом показывает скорость
// и время запроса.
func TestUsageLineShowsRate(t *testing.T) {
	u, buf := chromeUI(t, 80)
	u.UsageLine(Metrics{
		Prompt: 1200, Completion: 340, Rate: 142, Last: 4200 * time.Millisecond,
		Context: 12400, ContextMax: 128000,
	})
	out := buf.String()
	for _, want := range []string{"142 tok/s", "4.2с", "ctx 12k/128k (9%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("в строке расхода нет %q:\n%s", want, out)
		}
	}
}

// TestKVPanelKeepsRows — панель со списком параметров сохраняет все строки.
func TestKVPanelKeepsRows(t *testing.T) {
	u, buf := chromeUI(t, 60)
	u.KVPanel("Контекст", [][2]string{
		{"Системный промпт", "~2.4k ток."},
		{"Инструменты", "~8.3k ток."},
	})
	out := buf.String()
	for _, want := range []string{"Системный промпт", "2.4k", "Инструменты", "8.3k"} {
		if !strings.Contains(out, want) {
			t.Errorf("в панели нет %q:\n%s", want, out)
		}
	}
}
