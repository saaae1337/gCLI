package tui

import (
	"strings"
	"testing"
)

// TestANSI16 — базовые цвета темы на 16-цветном терминале остаются
// различимыми: текст не пропадает, акцент не становится серым.
func TestANSI16(t *testing.T) {
	cases := []struct {
		name string
		c    Color
		want int
	}{
		{"фон", Hex("#0E0D0B"), 0},
		{"панель", Hex("#161411"), 0},
		{"граница", Hex("#37312A"), 8},
		{"приглушённый", Hex("#8A8478"), 7},
		{"текст", Hex("#EEE9E3"), 15},
		{"охра", Hex("#D08A3E"), 11},
		{"индиго", Hex("#8C94F5"), 12},
		{"успех", Hex("#7EBE8A"), 10},
		{"ошибка", Hex("#DB6A6E"), 9},
		{"информация", Hex("#7DC4E8"), 14},
	}
	for _, c := range cases {
		if got := ansi16(c.c); got != c.want {
			t.Errorf("%s: ansi16=%d, ждали %d", c.name, got, c.want)
		}
	}
}

// TestXterm256 — почти серые уходят в серую шкалу, остальные — в куб.
func TestXterm256(t *testing.T) {
	cases := []struct {
		name string
		c    Color
		want int
	}{
		{"фон", Hex("#0E0D0B"), 232},
		{"граница", Hex("#37312A"), 236},
		{"текст", Hex("#EEE9E3"), 254},
		{"охра", Hex("#D08A3E"), 173},
		{"индиго", Hex("#8C94F5"), 105},
	}
	for _, c := range cases {
		if got := xterm256(c.c); got != c.want {
			t.Errorf("%s: xterm256=%d, ждали %d", c.name, got, c.want)
		}
	}
	// Любой цвет обязан попасть в палитру xterm.
	for _, h := range []string{"#000000", "#FFFFFF", "#123456", "#7EBE8A", "#DB6A6E"} {
		if got := xterm256(Hex(h)); got < 16 || got > 255 {
			t.Errorf("%s: индекс %d вне палитры 16..255", h, got)
		}
	}
}

// TestRendererColorGrades — рендерер выбирает последовательность под
// глубину палитры, а не всегда шлёт 24-битный цвет.
func TestRendererColorGrades(t *testing.T) {
	st := Style{FG: Hex("#D08A3E"), BG: Hex("#0E0D0B")}

	var b strings.Builder
	r := NewRenderer(&b, true, 256)
	r.sgr(&b, st)
	got := b.String()
	if !strings.Contains(got, "38;5;173") || !strings.Contains(got, "48;5;232") {
		t.Errorf("256 цветов: %q", got)
	}

	b.Reset()
	r = NewRenderer(&b, true, 16)
	r.sgr(&b, st)
	got = b.String()
	if !strings.Contains(got, ";93") || !strings.Contains(got, ";40") {
		t.Errorf("16 цветов: %q", got)
	}

	b.Reset()
	r = NewRenderer(&b, true, 0)
	r.sgr(&b, st)
	got = b.String()
	if !strings.Contains(got, "38;2;208;138;62") || !strings.Contains(got, "48;2;14;13;11") {
		t.Errorf("truecolor: %q", got)
	}

	b.Reset()
	r = NewRenderer(&b, false, 0)
	r.sgr(&b, st)
	if strings.Contains(b.String(), "38;") || strings.Contains(b.String(), "48;") {
		t.Errorf("без цвета остались коды: %q", b.String())
	}
}
