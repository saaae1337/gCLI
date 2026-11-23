package ui

// Тест чистоты REPL-вывода: печатается только ASCII и кириллица.
//
// Пакет tui проверяет свои кадры клетками (ascii_purity_test.go), здесь
// же — потоковый вывод для режима без полноэкранного интерфейса: баннер,
// блоки, таблицы, статусы, глифы. Управляющие ANSI-последовательности
// вырезаются перед проверкой: они не рисуют символы, а красят их.

import (
	"regexp"
	"strings"
	"testing"
)

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;:]*[a-zA-Z]`)

// asciiOrCyrillic — руна допустима в выводе.
func asciiOrCyrillic(r rune) bool {
	if r < 0x7F && r >= 0x20 {
		return true
	}
	if r == '\n' || r == '\t' || r == '\r' {
		return true
	}
	return r >= 0x0400 && r <= 0x04FF
}

func assertOutputASCII(t *testing.T, name, out string) {
	t.Helper()
	clean := ansiSeq.ReplaceAllString(out, "")
	for i, r := range clean {
		if !asciiOrCyrillic(r) {
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

// TestOutputASCIIOnly — весь репертуар REPL-вывода без декоративной
// юникодщины: никаких рамок из псевдографики, стрелок, типографских
// кавычек и тире.
func TestOutputASCIIOnly(t *testing.T) {
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

	// Шапка окна.
	u.TitleBar(TitleBarOpts{
		Title:    "gcli",
		Subtitle: "модель glm-4.6, провайдер zai",
		Right:    "контекст 24%",
	})

	// Блок и таблица.
	u.Block("первая строка\nвторая строка", BlockOpts{Title: "Проверка", Accent: true})
	u.Table(
		[]Column{{Title: "имя", Width: 10}, {Title: "значение", Width: 20}},
		[][]string{{"a", "1"}, {"b", "22"}},
		BlockOpts{Title: "таблица"},
	)

	// Строка статуса.
	u.StatusLine([]StatusItem{
		{Text: "in 18k", Kind: StatusMuted},
		{Text: "out 3.2k", Kind: StatusAccent},
		{Text: "$0.42", Kind: StatusOK},
	})

	// Библиотека не добавляет своих декоративных символов: всё, что
	// попадает в буфер, — ASCII, кириллица и ANSI-раскраска. Текст от
	// модели нормализует лента TUI (cleanControl/sanitizeBlock).
	assertOutputASCII(t, "repl", buf.String())
}
