package ui

import (
	"fmt"
	"strings"
	"time"

	"gcli/core"
)

// ---------- Панели окна ----------
//
// Оформление gcli не опирается на возможности терминала: рамки, полосы и
// метрики рисует сам интерфейс, поэтому окно выглядит одинаково в
// Terminal.app, в iTerm2 и в консоли Windows. Отсылка к macOS - верхняя
// Terminal.app, в iTerm2 и в консоли Windows. Концепт - OpenCode:
//
// Метрики - то, ради чего всё затевалось: сколько контекста занято,
// сколько токенов в секунду уходит, сколько времени занял последний
// запрос и сколько это стоило.

// Metrics - снимок расхода сессии для панелей.
//
// Собирается хостом (там, где живут сессия и провайдер) и передаётся в
// интерфейс готовым: пакет ui не знает ни про провайдеров, ни про сессии.
type Metrics struct {
	// Model и Provider - что работает сейчас.
	Model    string
	Provider string
	// Context - занято токенов в контексте, ContextMax - лимит модели.
	Context    int
	ContextMax int
	// Prompt и Completion - токены за сессию (вход и выход).
	Prompt     int
	Completion int
	// Requests - сколько запросов к модели ушло за сессию.
	Requests int
	// Rate - скорость ВЫВОДА модели в токенах в секунду.
	//
	// Именно вывода: входные токены - это повторённый контекст запроса,
	// и делить их на время значит показывать «2194 tok/s», которых
	// никто не писал. Человек ждёт ответа - его и надо мерить.
	Rate float64
	// Last - время последнего запроса, Session - время сессии.
	Last    time.Duration
	Session time.Duration
	// Cost - расход сессии в долларах, CostKnown - цена известна.
	Cost      float64
	CostKnown bool
	// Autopilot и Plan - режимы для бейджей нижней панели.
	Autopilot bool
	Plan      bool
}

// ContextPct - процент занятого контекста.
//
// Ноль, когда лимит неизвестен: показывать «0%» при неизвестном лимите
// значит врать, а молчание честнее.
func (m Metrics) ContextPct() int {
	if m.ContextMax <= 0 {
		return 0
	}
	pct := m.Context * 100 / m.ContextMax
	if pct > 999 {
		pct = 999
	}
	return pct
}

// ContextRatio - занятая доля контекста (0..1) для полосы.
func (m Metrics) ContextRatio() float64 {
	if m.ContextMax <= 0 {
		return 0
	}
	r := float64(m.Context) / float64(m.ContextMax)
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

// RateText - скорость токенов в секунду.
func (m Metrics) RateText() string {
	if m.Rate <= 0 {
		return ""
	}
	if m.Rate < 10 {
		return fmt.Sprintf("%.1f tok/s", m.Rate)
	}
	return fmt.Sprintf("%d tok/s", int(m.Rate+0.5))
}

// ContextText - занятый контекст с процентом от лимита модели.
func (m Metrics) ContextText() string {
	if m.ContextMax <= 0 {
		return core.Kfmt(m.Context) + " ctx"
	}
	return fmt.Sprintf("%s/%s (%d%%)", core.Kfmt(m.Context), core.Kfmt(m.ContextMax), m.ContextPct())
}

// SetMetrics - обновить снимок расхода.
//
// Живёт в интерфейсе, потому что строка ожидания перерисовывается из
// своей горутины: без снимка она не знала бы, сколько занято контекста.
func (u *UI) SetMetrics(m Metrics) {
	u.mu.Lock()
	u.metrics = m
	sink := u.sink()
	u.mu.Unlock()
	if sink != nil {
		sink.SetMetrics(m)
	}
}

// Metrics - текущий снимок расхода.
func (u *UI) Metrics() Metrics {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.metrics
}

// TitleBar/MetricsBar удалены: в интерфейсе Claude Code шапок и панелей
// метрик нет - расходы живут в строке подсказок под полем ввода (live.go),
// а приветствие рисует баннер приложения.

// fitRight - прижать правый блок к правому краю строки.
//
// Если не влезает - правый блок укорачивается, а не левый: показать
// половину расхода хуже, чем потерять название модели.
func fitRight(left, right string, width int) string {
	if right == "" {
		return left
	}
	l, r := visibleWidth(left), visibleWidth(right)
	if l+r+1 > width {
		room := width - l - 1
		if room < 8 {
			// Совсем не влезает: оставляем правый блок, он важнее.
			return truncate(right, maxInt(width-1, 8))
		}
		right = truncate(right, room)
		r = visibleWidth(right)
	}
	if l+r > width {
		return left
	}
	return left + strings.Repeat(" ", width-l-r) + right
}

// ---------- Скруглённые панели ----------

// panelGlyphs - рамка под текущий терминал: скруглённые углы Claude Code
// «╭ ╮ ╰ ╯» и ASCII-запаска «+» для терминалов без юникода.
func (u *UI) panelGlyphs() (tl, tr, bl, br, h, v string) {
	return u.g.TL, u.g.TR, u.g.BL, u.g.BR, u.g.H, u.g.V
}

// PanelOpts - параметры панели.
type PanelOpts struct {
	// Title - заголовок в верхней границе.
	Title string
	// Footer - подпись под содержимым, внутри рамки.
	Footer string
	// Accent - заголовок акцентным цветом.
	Accent bool
}

// Panel - блок в скруглённой рамке.
//
// Рамка рисуется интерфейсом, а не терминалом: содержимое остаётся
// текстом, который можно выделить, скопировать и прочитать без
// псевдографики.
func (u *UI) Panel(content string, o PanelOpts) {
	if u.quiet {
		return
	}
	tl, tr, bl, br, h, v := u.panelGlyphs()
	indent := "  "
	// Рамка занимает четыре колонки: две на отступ и две на границы.
	inner := u.Width() - 4
	if inner < 12 {
		inner = 12
	}

	title := o.Title
	plain := StripANSI(title)
	head := ""
	if plain != "" {
		if o.Accent {
			head = " " + u.c(u.pal.Accent, plain) + " "
		} else {
			head = " " + u.Head(plain) + " "
		}
	}
	// Верхняя граница: заголовок внутри, остальное - заполнитель.
	fill := inner - visibleWidth(head) - 1
	if fill < 0 {
		fill = 0
	}
	top := tl + h + u.c(u.pal.Muted, head) + u.c(u.pal.Muted, strings.Repeat(h, fill)) + tr
	// Убираем двойную рамку, если заголовок съел всё место.
	if visibleWidth(StripANSI(top)) > inner+2 {
		top = tl + u.c(u.pal.Muted, head) + u.c(u.pal.Muted, strings.Repeat(h, maxInt(inner-visibleWidth(StripANSI(head)), 0))) + tr
	}

	var b strings.Builder
	b.WriteString(indent + top + "\n")
	lines := u.contentLines(content, inner-2)
	for _, l := range lines {
		pad := inner - 2 - visibleWidth(l)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(indent + u.c(u.pal.Muted, v) + " " + l +
			strings.Repeat(" ", pad) + " " + u.c(u.pal.Muted, v) + "\n")
	}
	if o.Footer != "" {
		pad := inner - 2 - visibleWidth(o.Footer)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(indent + u.c(u.pal.Muted, v) + " " + u.c(u.pal.Muted, o.Footer) +
			strings.Repeat(" ", pad) + " " + u.c(u.pal.Muted, v) + "\n")
	}
	b.WriteString(indent + bl + strings.Repeat(h, inner) + br + "\n")
	u.emit(b.String())
}

// KVPanel - список «параметр: значение» внутри панели.
//
// Тот же список, что и KVPairs, но в рамке: метрики и настройки лучше
// читаются как карточка, а не как строки в общем потоке.
func (u *UI) KVPanel(title string, rows [][2]string) {
	if u.quiet {
		return
	}
	w := 0
	for _, r := range rows {
		if n := cellWidth(r[0]); n > w {
			w = n
		}
	}
	if w += 2; w < kvWidth {
		w = kvWidth
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(u.Gray(padRight(r[0], w)) + r[1] + "\n")
	}
	u.Panel(strings.TrimRight(b.String(), "\n"), PanelOpts{Title: title})
}
