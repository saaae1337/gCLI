package ui

// Как ширина глифа отражается на реальной картинке. Проверяется не
// число, а то, ради чего ширина вообще считается: колонки в таблице.

import (
	"strings"
	"testing"
)

// renderTable — строки таблицы с выравниванием по ширине. Все колонки,
// включая последнюю, добиваются пробелами до своей ширины — так
// строки обязаны стать одной длины.
func renderTable(rows [][]string, widths []int) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			b.WriteString(cell)
			if pad := widths[i] - cellWidth(cell); pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
			if i < len(row)-1 {
				b.WriteString(" │ ")
			}
		}
		out = append(out, b.String())
	}
	return out
}

// Столбец из знаков статуса обязан держать ширину: иначе строки таблицы
// разъезжаются на ячейку каждая.
func TestStatusColumnKeepsTableAligned(t *testing.T) {
	rows := [][]string{
		{"✔", "прочитать файл", "42 строки"},
		{"✖", "записать файл", "ошибка доступа"},
		{"▲", "запустить сборку", "долго"},
		{"◆", "вызвать субагента", "готов"},
	}
	out := renderTable(rows, []int{1, 20, 14})

	want := cellWidth(out[0])
	for i, line := range out {
		if got := cellWidth(line); got != want {
			t.Errorf("строка %d: ширина %d вместо %d — столбец поехал\n  %q",
				i, got, want, line)
		}
	}
}

// Список из знаков статуса обязан занимать одинаковое место, иначе по
// нему не выровнять столбец.
func TestStatusMarkersUniform(t *testing.T) {
	for _, m := range []string{"✔", "✖", "▲", "◆", "●", "○"} {
		if w := cellWidth(m); w != 1 {
			t.Errorf("маркер %q занимает %d ячеек, а должен одну — иначе "+
				"выровнять по нему столбец нельзя", m, w)
		}
	}
}

// Обрезка по ширине не должна разрывать глиф и не должна выходить за
// лимит: иначе строка переползает на следующую.
func TestTruncateKeepsGlyphIntact(t *testing.T) {
	line := "✔ задача выполнена полностью"
	got := truncateCellsEll(line, 12)
	if got == "" {
		t.Fatal("обрезка съела всё")
	}
	if w := cellWidth(got); w > 12 {
		t.Errorf("после обрезки ширина %d при лимите 12: %q", w, got)
	}
	if strings.ContainsRune(got, '�') {
		t.Errorf("глиф разорван обрезкой: %q", got)
	}
}
