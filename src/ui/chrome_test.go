package ui

// Тесты хрома интерфейса: скруглённые панели и живая нижняя область
// (бокс ввода, pop-up меню, строка подсказок) в стиле Claude Code.

import (
	"strings"
	"testing"
	"time"
)

// chromeUI - интерфейс фиксированной ширины для проверки вёрстки.
func chromeUI(t *testing.T, width int) (*UI, *strings.Builder) {
	t.Helper()
	buf := &strings.Builder{}
	return New(Options{Theme: "ember", Unicode: true, Color: false, Width: width, Out: buf}), buf
}

// TestPanelBordersAlign - рамка панели должна сходиться по краям.
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
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "╮") {
		t.Errorf("верхняя граница без скруглённых углов: %q", lines[0])
	}
}

// TestPanelASCIIFallback - без юникода рамка становится ASCII.
func TestPanelASCIIFallback(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{Theme: "ember", Unicode: false, Color: false, Width: 40, Out: buf})
	u.Panel("текст", PanelOpts{Title: "Панель"})
	out := buf.String()
	if !isASCIIString(out) {
		t.Errorf("в ASCII-режиме осталась юникодная рамка:\n%s", out)
	}
	if !strings.Contains(out, "+") {
		t.Errorf("в ASCII-режиме нет рамки:\n%s", out)
	}
}

// TestMetricsContextPct - процент занятого контекста считается по лимиту
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

// TestMetricsRateText - скорость округляется и не показывается без
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

// TestUsageLineShowsRate - строка расхода под ответом показывает скорость
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

// TestKVPanelKeepsRows - панель со списком параметров сохраняет все строки.
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

// ---------- Живая область ----------

// TestLiveRegionDrawsInputBox - включение живой области рисует скруглённый
// бокс ввода с курсором-блоком и строкой подсказок.
func TestLiveRegionDrawsInputBox(t *testing.T) {
	u, buf := chromeUI(t, 60)
	u.LiveStart(`Try "/help"`)
	u.LiveFooter("? for shortcuts", "glm-4.6")
	out := buf.String()
	for _, want := range []string{"╭", "╮", "╰", "╯", `Try "/help"`, "? for shortcuts"} {
		if !strings.Contains(out, want) {
			t.Errorf("в живой области нет %q:\n%s", want, out)
		}
	}
	// Текст и курсор-блок (обратное видео).
	buf.Reset()
	u.LiveInput("почини тесты", len("почини тесты"))
	out = buf.String()
	if !strings.Contains(out, "почини тесты") {
		t.Errorf("текст не попал в бокс:\n%s", out)
	}
	if !strings.Contains(out, ">") {
		t.Errorf("в боксе нет промпта «>»:\n%s", out)
	}
	// Курсор - блок на следующем символе: обратное видео. chromeUI без
	// цвета, поэтому блок проверяем на цветном интерфейсе.
	cbuf := &strings.Builder{}
	cu := New(Options{Theme: "ember", Unicode: true, Color: true, Grade: Color16, Width: 60, Out: cbuf})
	cu.LiveStart(`Try "/help"`)
	cu.LiveInput("почини тесты", 2)
	if !strings.Contains(cbuf.String(), "\x1b[7m") {
		t.Errorf("курсор-блок не нарисован:\n%s", StripANSI(cbuf.String()))
	}
	u.LiveStop()
	if strings.Count(buf.String(), "\x1b[J") == 0 {
		t.Errorf("LiveStop не стёр регион:\n%s", StripANSI(buf.String()))
	}
}

// TestLiveRegionMenu - pop-up меню команд рисуется над полем ввода,
// выбранный пункт отмечается «>» и акцентом.
func TestLiveRegionMenu(t *testing.T) {
	u, buf := chromeUI(t, 60)
	u.LiveStart(`Try "/help"`)
	buf.Reset()
	u.LiveMenu([]MenuItem{
		{Name: "/help", Desc: "эта справка"},
		{Name: "/status", Desc: "состояние сессии"},
	}, 1)
	out := StripANSI(buf.String())
	for _, want := range []string{"/help", "эта справка", "/status", ">"} {
		if !strings.Contains(out, want) {
			t.Errorf("в меню нет %q:\n%s", want, out)
		}
	}
	// Выбор на втором пункте: он идёт с курсором, первый - без.
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "/status") && !strings.Contains(l, "> ") {
			t.Errorf("выбранный пункт без курсора (строка %d):\n%s", i, out)
		}
	}
}

// TestLiveRegionFooterFitsWidth - строка подсказок не вылезает за ширину.
func TestLiveRegionFooterFitsWidth(t *testing.T) {
	const width = 44
	u, buf := chromeUI(t, width)
	u.LiveStart(`Try "/help"`)
	buf.Reset()
	u.LiveFooter("? for shortcuts", "autopilot: all · glm-4.6 · ctx 12.4k/128k (9%)")
	out := buf.String()
	for _, l := range strings.Split(StripANSI(strings.TrimRight(out, "\n")), "\n") {
		if w := cellWidth(l); w > width {
			t.Errorf("строка шириной %d вместо %d: %q", w, width, l)
		}
	}
}

// TestLiveRegionBusyLine - строка ожидания живёт над полем ввода и
// называет клавишу прерывания.
func TestLiveRegionBusyLine(t *testing.T) {
	u, buf := chromeUI(t, 60)
	u.LiveStart(`Try "/help"`)
	u.mu.Lock()
	u.spin = true
	u.spinT0 = time.Now().Add(-2 * time.Second)
	u.liveBusy = "Pondering"
	u.liveMark = "|"
	u.flushLiveLocked()
	u.mu.Unlock()
	out := StripANSI(buf.String())
	if !strings.Contains(out, "Pondering") {
		t.Errorf("в строке ожидания нет глагола:\n%s", out)
	}
	if !strings.Contains(out, "esc to interrupt") {
		t.Errorf("строка ожидания не подсказывает про esc:\n%s", out)
	}
}

// TestLiveRegionMultiLineInput - многострочный ввод переносится внутри
// рамки, каждая строка - ровно одна строка терминала.
func TestLiveRegionMultiLineInput(t *testing.T) {
	const width = 40
	u, buf := chromeUI(t, width)
	u.LiveStart(`Try "/help"`)
	buf.Reset()
	u.LiveInput("первая строка\nвторая, заметно длиннее, чем влезает в бокс", len("первая строка"))
	out := StripANSI(buf.String())
	if !strings.Contains(out, "первая строка") || !strings.Contains(out, "вторая") {
		t.Errorf("многострочный ввод потерян:\n%s", out)
	}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := cellWidth(l); w > width {
			t.Errorf("строка шириной %d вместо %d: %q", w, width, l)
		}
	}
}

// TestLiveRegionASCIIFallback - без юникода живая область рисуется
// ASCII-рамкой.
func TestLiveRegionASCIIFallback(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{Theme: "ember", Unicode: false, Color: false, Width: 44, Out: buf})
	u.LiveStart(`Try "/help"`)
	out := buf.String()
	if !isASCIIString(out) {
		t.Errorf("ASCII-режим нарисовал юникод:\n%s", out)
	}
	if !strings.Contains(out, "+") {
		t.Errorf("в ASCII-режиме нет рамки:\n%s", out)
	}
}
