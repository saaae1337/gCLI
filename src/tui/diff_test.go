package tui

import (
	"strings"
	"testing"
)

// Регрессии диффа в модалке подтверждения (diff.go).

// TestLineDiffBigFileSeparateLines — при файле больше maxN строк превью
// отдаёт первые строки КАЖДАЯ отдельным op. Раньше восемь строк склеивались
// в один Text через "\n", а Screen.Text выбрасывает перевод строки: в модалке
// вместо «п первых строк» рисовалась одна слипшаяся нечитаемая строка.
func TestLineDiffBigFileSeparateLines(t *testing.T) {
	var ob, nb strings.Builder
	for i := 0; i < 600; i++ {
		ob.WriteString("старая строка\n")
		nb.WriteString("новая строка\n")
	}
	ops := lineDiff(ob.String(), nb.String())
	for _, op := range ops {
		if strings.Contains(op.Text, "\n") {
			t.Fatalf("op содержит перевод строки — в модалке строки слипнутся: %q", op.Text)
		}
	}
	if len(ops) < 3 {
		t.Fatalf("ожидали заголовок и несколько строк превью, получили %d ops", len(ops))
	}
}

// TestLineDiffTrailingNewlineDifference — разница конечного перевода строки
// не должна выглядеть как «изменений нет»: splitLines не видит хвостовой
// "\n", а файлы при этом разные, и правку снимающую/ставящую перевод,
// пользователь подтверждал бы вслепую.
func TestLineDiffTrailingNewlineDifference(t *testing.T) {
	// Перевод удалён.
	ops := diffWindow(lineDiff("a\nb\n", "a\nb"), 10)
	if !containsOp(ops, '-', "конечный перевод строки") {
		t.Fatalf("удаление конечного перевода не видно в диффе: %+v", ops)
	}
	// Перевод добавлен.
	ops = diffWindow(lineDiff("a\nb", "a\nb\n"), 10)
	if !containsOp(ops, '+', "конечный перевод строки") {
		t.Fatalf("добавление конечного перевода не видно в диффе: %+v", ops)
	}
	// Оба с переводом — по-прежнему «изменений нет».
	ops = diffWindow(lineDiff("a\nb\n", "a\nb\n"), 10)
	if len(ops) != 1 || ops[0].Kind != ' ' || ops[0].Text != "изменений нет" {
		t.Fatalf("одинаковые файлы должны давать «изменений нет»: %+v", ops)
	}
}

func containsOp(ops []diffOp, kind byte, substr string) bool {
	for _, op := range ops {
		if op.Kind == kind && strings.Contains(op.Text, substr) {
			return true
		}
	}
	return false
}
