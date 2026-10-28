package tui

import "testing"

// Курсор на границах видимых строк: конец логической строки (перед '\n')
// и пустая логическая строка принадлежат СВОЕЙ строке. Раньше CursorPos
// отдавал их следующей строке: курсор рисовался на строку ниже, а Up
// внутри пустой строки зацикливался — цель совпадала с текущей позицией.

// moveLeft — сдвинуть курсор влево на n рун (SetCursor у Composer нет).
func moveLeft(c *Composer, n int) {
	for i := 0; i < n; i++ {
		c.Left()
	}
}

func TestCursorPosEndOfLogicalLine(t *testing.T) {
	c := NewComposer()
	c.SetText("ab\ncd") // cur = 5
	moveLeft(c, 3)      // cur = 2: перед '\n', конец строки «ab»
	x, line := c.CursorPos(40)
	if line != 0 || x != 2 {
		t.Errorf("CursorPos = (%d, %d), хочу (2, 0): конец строки «ab» — её последняя колонка", x, line)
	}
}

func TestCursorPosBeforeTrailingNewline(t *testing.T) {
	c := NewComposer()
	c.SetText("ab\n") // cur = 3
	moveLeft(c, 1)    // cur = 2: конец «ab», дальше пустая вторая строка
	x, line := c.CursorPos(40)
	if line != 0 || x != 2 {
		t.Errorf("CursorPos = (%d, %d), хочу (2, 0)", x, line)
	}
}

func TestCursorPosInsideEmptyLine(t *testing.T) {
	c := NewComposer()
	c.SetText("a\n\nb") // cur = 4
	moveLeft(c, 2)      // cur = 2: внутри пустой строки
	x, line := c.CursorPos(40)
	if line != 1 || x != 0 {
		t.Errorf("CursorPos = (%d, %d), хочу (0, 1): курсор стоит в пустой строке", x, line)
	}
}

func TestComposerUpLeavesEmptyLine(t *testing.T) {
	c := NewComposer()
	c.SetText("a\n\nb")
	moveLeft(c, 2) // cur = 2: внутри пустой строки
	c.Up(40)
	if c.Cursor() == 2 {
		t.Fatal("Up внутри пустой строки не сдвинул курсор: цель совпала с текущей позицией")
	}
	if c.Cursor() != 0 {
		t.Errorf("после Up курсор = %d, хочу 0 (начало строки «a»)", c.Cursor())
	}
}

func TestComposerDownFromEmptyLine(t *testing.T) {
	c := NewComposer()
	c.SetText("a\n\nb")
	moveLeft(c, 2) // cur = 2: внутри пустой строки
	c.Down(40)
	if c.Cursor() != 3 {
		t.Errorf("после Down курсор = %d, хочу 3 (начало строки «b»)", c.Cursor())
	}
}

// Перенос длинной строки не должен измениться: курсор на стыке переноса
// (следующая видимая строка начинается там же) — начало следующей строки,
// как и раньше.
func TestCursorPosWrapBoundaryUnchanged(t *testing.T) {
	c := NewComposer()
	c.SetText("aaaaaa") // одна логическая строка, переносится по 3
	moveLeft(c, 3)      // cur = 3: стык переноса
	x, line := c.CursorPos(3)
	if line != 1 || x != 0 {
		t.Errorf("CursorPos = (%d, %d), хочу (0, 1): стык переноса — начало второй видимой строки", x, line)
	}
}

func TestCursorPosEndOfText(t *testing.T) {
	c := NewComposer()
	c.SetText("ab\ncd") // cur = 5 — конец текста
	x, line := c.CursorPos(40)
	if line != 1 || x != 2 {
		t.Errorf("CursorPos = (%d, %d), хочу (2, 1)", x, line)
	}
}
