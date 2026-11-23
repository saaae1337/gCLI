package ui

// Тест чистоты REPL-вывода: печатается только разрешённый набор рун.
//
// Разрешены: ASCII, кириллица и юникод-глифы Claude Code (⏺ ⎿ ✻ ╭ ╮ ╰ ╯
// и соседи). Всё остальное - экзотические стрелки, эмодзи, типографика -
// запрещено: они рвутся на терминалах без нужных шрифтов. Управляющие
// ANSI-последовательности вырезаются перед проверкой: они не рисуют
// символы, а красят их.

import (
	"regexp"
	"strings"
	"testing"
)

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;:]*[a-zA-Z]`)

// ccAllowed - руна допустима в выводе.
func ccAllowed(r rune) bool {
	if r < 0x7F && r >= 0x20 {
		return true
	}
	if r == '\n' || r == '\t' || r == '\r' {
		return true
	}
	if r >= 0x0400 && r <= 0x04FF {
		return true // кириллица
	}
	// Юникод-глифы набора Claude Code.
	switch r {
	case 0x2500, 0x2502, // ─ │
		0x256D, 0x256E, 0x2570, 0x256F, // ╭ ╮ ╰ ╯
		0x250C, 0x2510, 0x2514, 0x2518, // ┌ ┐ └ ┘
		0x251C, 0x276F, // ├ ❯
		0x2022, 0x2713, 0x00D7, // • ✓ ×
		0x23FA, 0x23BF, // ⏺ ⎿
		0x273B, 0x273D, 0x2736, 0x2733, 0x2722, // ✻ ✽ ✶ ✳ ✢
		0x2026, 0x00B7, 0x2192, // … · →
		0x2588, 0x2591, 0x25CF: // █ ░ ●
		return true
	}
	return false
}

func assertOutputAllowed(t *testing.T, name, out string) {
	t.Helper()
	clean := ansiSeq.ReplaceAllString(out, "")
	for i, r := range clean {
		if !ccAllowed(r) {
			around := clean
			if i > 20 {
				around = clean[i-20:]
			}
			if len(around) > 40 {
				around = around[:40]
			}
			t.Errorf("%s: недопустимая руна %q (U+%04X) рядом с: %q", name, r, r, around)
			return
		}
	}
}

// TestOutputGlyphWhitelist — весь репертуар REPL-вывода укладывается в
// разрешённый набор: статусы, глифы, блоки, таблицы, живая область.
func TestOutputGlyphWhitelist(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme:   "ember",
		Unicode: true,
		Color:   true,
		Width:   80,
		Out:     buf,
	})

	// Статусы и обычный вывод.
	u.Ok("готово")
	u.Err("упало")
	u.Warn("предупреждение")
	u.Info("заметка")
	u.Println(u.Accent("акцент") + " и " + u.Gray("серый") + ", \"кавычки\", - тире, ... многоточие")

	// Глифы — все, включая редкие.
	g := u.Glyphs()
	all := []string{g.TL, g.TR, g.BL, g.BR, g.H, g.V, g.Tee, g.Elbow, g.Pipe, g.Dash,
		g.Arrow, g.Bullet, g.Check, g.Cross, g.Excl, g.Star, g.Dot,
		g.Running, g.Pending, g.BarFull, g.BarEmpty, g.Separator,
		g.Ellipsis, g.Dots, g.DashDot, g.RuleChar, g.Mark, g.Tick, g.Retry}
	u.Println("глифы: " + strings.Join(all, " "))

	// Блок и таблица.
	u.Block("первая строка\nвторая строка", BlockOpts{Title: "Проверка", Accent: true})
	u.Table(
		[]Column{{Title: "имя", Width: 10}, {Title: "значение", Width: 20}},
		[][]string{{"a", "1"}, {"b", "22"}},
		BlockOpts{Title: "таблица"},
	)

	// Вызов инструмента и результат.
	u.RailStart(ToolCall{Name: "read_file", Args: "x.go"})
	u.RailEnd(ToolCall{Name: "read_file", Detail: "481 строка", Status: "ok"})

	// Живая область: бокс ввода, меню, подсказки.
	u.LiveStart(`Try "/help"`)
	u.LiveInput("> пользовательский запрос", 20)
	u.LiveMenu([]MenuItem{{Name: "/help", Desc: "эта справка"}, {Name: "/status", Desc: "состояние"}}, 0)
	u.LiveFooter("? for shortcuts", "glm-4.6 · ctx 12k/128k (9%)")
	u.LiveStop()

	// Библиотека не добавляет своих декоративных символов сверх набора
	// Claude Code: всё, что попало в буфер, — ASCII, кириллица, глифы и
	// ANSI-раскраска.
	assertOutputAllowed(t, "repl", buf.String())
}

// TestASCIIFallbackStaysASCII — без юникода вывод обязан остаться чистым
// ASCII (кириллица разрешена: это текст, а не оформление).
func TestASCIIFallbackStaysASCII(t *testing.T) {
	buf := &strings.Builder{}
	u := New(Options{
		Theme:   "ember",
		Unicode: false,
		Color:   true,
		Width:   80,
		Out:     buf,
	})
	u.Ok("готово")
	u.Err("упало")
	g := u.Glyphs()
	u.Println(strings.Join([]string{g.TL, g.H, g.TR, g.Tick, g.Mark, g.Dot}, " "))
	u.Block("тело", BlockOpts{Title: "Панель"})
	u.Table([]Column{{Title: "имя"}}, [][]string{{"a"}}, BlockOpts{Title: "т"})
	clean := ansiSeq.ReplaceAllString(buf.String(), "")
	for _, r := range clean {
		if r > 127 && !(r >= 0x400 && r <= 0x4FF) {
			t.Errorf("ASCII-режим напечатал не-ASCII руну %q (U+%04X)", r, r)
			return
		}
	}
}
