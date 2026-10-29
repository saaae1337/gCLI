package tui

import "testing"

// Регрессии таба в поле ввода. wcwidth меряет '\t' как 0 колонок, а
// Lines() рисует его четырьмя пробелами: курсор уезжал влево на 4
// колонки за каждый таб, переносы и высота поля считались по заниженной
// ширине, строка с табами вылезала за рамку.

// TestCursorPosCountsTabAsRendered — курсор после таба стоит там же, где
// отрисовался бы глиф: «a\tb» рисуется как «a    b», 'b' — в колонке 5.
func TestCursorPosCountsTabAsRendered(t *testing.T) {
	c := NewComposer()
	c.SetText("a\tb")
	c.Left() // курсор на 'b'
	x, line := c.CursorPos(40)
	if line != 0 {
		t.Fatalf("строка %d, ожидалась 0", line)
	}
	if x != 5 {
		t.Fatalf("курсор в колонке %d, ожидалась 5 (таб рисуется 4 пробелами)", x)
	}
}

// TestLinesWrapTabs — строка из одних табов переносится по ширине:
// 8 табов = 32 колонки отрисовки, при ширине 10 — не меньше трёх строк.
// Раньше таб мерился нулём и строка не переносилась вовсе.
func TestLinesWrapTabs(t *testing.T) {
	c := NewComposer()
	c.SetText("\t\t\t\t\t\t\t\t")
	lines := c.Lines(10)
	if len(lines) < 3 {
		t.Fatalf("8 табов (32 колонки) при ширине 10 должны дать >=3 видимых строк, получено %d", len(lines))
	}
}

// TestComposerUpDownWithTabs — Up/Down по строкам с табами считают
// колонку в колонках отрисовки и остаются в границах целевой строки.
func TestComposerUpDownWithTabs(t *testing.T) {
	c := NewComposer()
	c.SetText("abc\nx\ty") // SetText ставит курсор в конец, за 'y'
	x, line := c.CursorPos(40)
	if line != 1 {
		t.Fatalf("строка %d, ожидалась 1", line)
	}
	// Вторая строка рисуется как «x    y» (6 колонок), курсор за 'y'.
	if x != 6 {
		t.Fatalf("колонка %d, ожидалась 6", x)
	}
	// Up: 6 не помещается в «abc» (3 колонки) — курсор в конец строки 0.
	c.Up(40)
	if cur := c.Cursor(); cur != 3 {
		t.Fatalf("после Up курсор %d, ожидался 3 (конец «abc»)", cur)
	}
	// Down: колонка 3 попадает внутрь таба — курсор на его начале (руна 5).
	c.Down(40)
	if cur := c.Cursor(); cur != 5 {
		t.Fatalf("после Down курсор %d, ожидался 5 (начало таба)", cur)
	}
}
