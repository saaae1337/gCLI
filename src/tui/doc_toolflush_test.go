package tui

import (
	"strings"
	"testing"
)

// ---------- недописанный хвост и карточка инструмента ----------
//
// Хвост без перевода строки (d.pending) обязан фиксироваться в свой блок
// ДО карточки инструмента. Раньше ToolStart пропускал фиксацию: Layout
// рисовал хвост в самом конце ленты — после карточки, — а при завершении
// строка дописывалась в блок выше, и текст «прыгал» через карточку.

func tailAndCard(t *testing.T, ls []Line) (tail, card int) {
	t.Helper()
	tail, card = -1, -1
	for i, l := range ls {
		txt := l.Text()
		if card < 0 && strings.Contains(txt, "bash") {
			card = i
		}
		if tail < 0 && strings.Contains(txt, "Проверяю") {
			tail = i
		}
	}
	if card < 0 {
		t.Fatal("карточка инструмента не найдена в раскладке")
	}
	if tail < 0 {
		t.Fatal("хвост «Проверяю» пропал из раскладки")
	}
	return tail, card
}

// TestToolStartFlushesPendingTail — хвост печатается выше карточки и не
// перелетает через неё при завершении строки.
func TestToolStartFlushesPendingTail(t *testing.T) {
	d := &Doc{}
	d.BeginStream()
	d.Text("Начал работу.\n") // зафиксированная строка — блок ответа
	d.Text("Проверяю")        // недописанный хвост

	d.ToolStart(ToolInfo{ID: "t1", Name: "bash", Args: "ls", Kind: "bash"})
	tail, card := tailAndCard(t, d.Layout(LayoutOpts{Width: 60, Theme: Dark()}))
	if tail > card {
		t.Fatalf("хвост печатается после карточки (tail=%d, card=%d)", tail, card)
	}

	// Строка завершается: дописывается в блок ответа, порядок не меняется.
	d.Text(" файл\n")
	tail, card = tailAndCard(t, d.Layout(LayoutOpts{Width: 60, Theme: Dark()}))
	if tail > card {
		t.Fatalf("завершённая строка осталась после карточки (tail=%d, card=%d)", tail, card)
	}
}

// TestToolStartFlushPlainTail — хвост обычного (не потокового) вывода
// тоже фиксируется до карточки, а не уезжает в конец ленты.
func TestToolStartFlushPlainTail(t *testing.T) {
	d := &Doc{}
	d.Text("вывод команды без перевода строки") // armed=false → обычный текст

	d.ToolStart(ToolInfo{ID: "t2", Name: "grep", Args: "x", Kind: "read"})
	ls := d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	tail, card := -1, -1
	for i, l := range ls {
		txt := l.Text()
		if card < 0 && strings.Contains(txt, "grep") {
			card = i
		}
		if tail < 0 && strings.Contains(txt, "вывод команды") {
			tail = i
		}
	}
	if card < 0 {
		t.Fatal("карточка инструмента не найдена в раскладке")
	}
	if tail < 0 {
		t.Fatal("хвост обычного вывода пропал из раскладки")
	}
	if tail > card {
		t.Fatalf("хвост печатается после карточки (tail=%d, card=%d)", tail, card)
	}
}
