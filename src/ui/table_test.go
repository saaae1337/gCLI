package ui

import (
	"strings"
	"testing"
)

// TestTableAlignsColoredCells — цветные ячейки не сдвигают колонки таблицы:
// padRight считает видимую ширину без ANSI-кодов.
func TestTableAlignsColoredCells(t *testing.T) {
	u, buf := uiAt(Color256, 100)
	rows := [][]string{
		{u.Green("готово"), "первая"},
		{"упало", "вторая"},
	}
	u.Table([]Column{
		{Title: "статус", Width: 8},
		{Title: "задача", Width: 8},
	}, rows, BlockOpts{})

	got := buf.String()
	// Строки данных: видимая позиция второй колонки должна совпадать в обеих
	// строках (заголовок "статус · задача" задаёт базу).
	var positions []int
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(StripANSI(line), "первая") || strings.Contains(StripANSI(line), "вторая") {
			head := strings.SplitN(StripANSI(line), "·", 2)[0]
			positions = append(positions, visibleWidth(head))
		}
	}
	if len(positions) != 2 {
		t.Fatalf("ожидалось 2 строки данных, получено %d:\n%s", len(positions), got)
	}
	if positions[0] != positions[1] {
		t.Errorf("колонки разъехались: %d != %d\n%s", positions[0], positions[1], got)
	}
}
