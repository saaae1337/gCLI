package tui

import (
	"bytes"
	"strings"
	"testing"
)

// Контракты сетки клеток на составных символах. Именно здесь старый
// рунный движок расходился с терминалом: сетка отводила под флаг четыре
// клетки, а терминал рисовал одну картинку шириной две — и лента
// разъезжалась. Теперь кластер занимает столько клеток, сколько рисует
// терминал, и печатается в вывод целиком.

func TestScreenTextFlag(t *testing.T) {
	s := NewScreen(20, 3, Color{})
	end := s.Text(0, 0, "🇷🇺 ok", Style{})
	// Флаг — 2 клетки, пробел 1, «ok» 2: курсор на 5-й колонке.
	if end != 5 {
		t.Errorf("Text вернул %d, хочу 5", end)
	}
	plain := s.Plain()
	if !strings.HasPrefix(plain, "🇷🇺 ok") {
		t.Errorf("Plain = %q, хочу префикс «🇷🇺 ok»", plain)
	}
	// В сетке флаг занимает ровно 2 клетки: третья клетка — пробел,
	// а не хвост второго индикатора.
	if c := s.Cells[2]; c.R != ' ' && c.R != 0 {
		t.Errorf("клетка 2 занята %q — флаг разъехался", c.R)
	}
}

func TestScreenTextZWJFamily(t *testing.T) {
	s := NewScreen(20, 3, Color{})
	fam := "👨‍👩‍👧‍👦"
	end := s.Text(0, 0, fam+"!", Style{})
	// Семья — одна картинка в 2 клетки, «!» — одна.
	if end != 3 {
		t.Errorf("Text вернул %d, хочу 3", end)
	}
	// Вся последовательность хранится в базовой клетке и попадает в вывод.
	c := s.Cells[0]
	if c.R != '👨' || c.Rest == "" {
		t.Fatalf("базовая клетка: R=%q Rest=%q", c.R, c.Rest)
	}
	full := string(c.R) + c.Rest
	if full != fam {
		t.Errorf("клетка хранит %q, хочу %q", full, fam)
	}
	// Клетка 1 — continuation, клеток после кластера больше нет.
	if !s.Cells[1].Cont {
		t.Error("клетка 1 должна быть continuation")
	}
}

func TestScreenTextSkinTone(t *testing.T) {
	s := NewScreen(20, 3, Color{})
	end := s.Text(0, 0, "👍🏽x", Style{})
	if end != 3 {
		t.Errorf("Text вернул %d, хочу 3 (эмодзи 2 + x 1)", end)
	}
	if got := string(s.Cells[0].R) + s.Cells[0].Rest; got != "👍🏽" {
		t.Errorf("клетка хранит %q, хочу «👍🏽»", got)
	}
}

func TestScreenTextKeycap(t *testing.T) {
	s := NewScreen(20, 3, Color{})
	// 1️⃣ рисуется как эмодзи: 2 клетки, хотя цифра узкая.
	end := s.Text(0, 0, "1️⃣", Style{})
	if end != 2 {
		t.Errorf("Text вернул %d, хочу 2", end)
	}
}

func TestRendererDrawEmitsWholeCluster(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, false, 0)
	if err := r.Start(false); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s := NewScreen(20, 1, Color{})
	s.Text(0, 0, "a🇷🇺b", Style{})
	r.Draw(s)
	// В эскейп-выводе флаг обязан прийти целиком: терминал рисует его
	// одной картинкой, потерянная руна осталась бы мусором на экране.
	if !strings.Contains(buf.String(), "a🇷🇺b") {
		t.Errorf("Draw потерял кластер: %q", buf.String())
	}
}

func TestRendererDrawWideAtLineEnd(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, false, 0)
	if err := r.Start(false); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Широкий символ, не помещающийся в последнюю клетку, заменяется
	// пробелом — как и раньше, но теперь и для кластеров.
	s := NewScreen(3, 1, Color{})
	s.Text(2, 0, "日", Style{})
	if c := s.Cells[2]; c.R != ' ' {
		t.Errorf("клетка 2 = %q, хочу пробел", c.R)
	}
}

func TestComposerClusterCursor(t *testing.T) {
	c := NewComposer()
	c.SetText("ab🇷🇺cd")
	// Курсор в конец: колонка = 1+1+2+1+1 = 6.
	if x, _ := c.CursorPos(40); x != 6 {
		t.Errorf("CursorPos = %d, хочу 6", x)
	}
	// Перенос: там, где флаг уже не помещается, он уходит на следующую
	// строку ЦЕЛИКОМ. Старый рунный движок резал по рунам: «ab» + первый
	// индикатор оставались на первой строке, второй — на второй, и в
	// ленте появлялись два битых квадратика вместо флага.
	c2 := NewComposer()
	c2.SetText("ab🇷🇺")
	ranges2 := c2.visualRanges(3)
	if len(ranges2) != 2 {
		t.Fatalf("visualRanges(3) дал %d строки, хочу 2", len(ranges2))
	}
	if got := string(c2.text[ranges2[0][0]:ranges2[0][1]]); got != "ab" {
		t.Errorf("первая строка %q, хочу «ab»", got)
	}
	if got := string(c2.text[ranges2[1][0]:ranges2[1][1]]); got != "🇷🇺" {
		t.Errorf("вторая строка %q, хочу «🇷🇺»", got)
	}
}

func TestWrapPlainClusters(t *testing.T) {
	// Слово с флагом не режется посередине кластера.
	lines := wrapPlain("x 🇷🇺🇷🇺", 5)
	for i, ln := range lines {
		if strings.ContainsRune(ln, 0) {
			t.Errorf("строка %d содержит пустые руны: %q", i, ln)
		}
	}
	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "🇷🇺") {
		t.Errorf("флаг потерялся: %q", joined)
	}
}
