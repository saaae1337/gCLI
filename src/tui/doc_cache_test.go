package tui

import (
	"reflect"
	"strings"
	"testing"
)

// Регрессия 0.9.1: раскладка ленты кэшируется по блокам. Раньше Layout
// перерисовывал всю ленту на каждый изменённый кадр — при busy спиннер
// менял ключ кэша каждые два тика, и длинная сессия перевёрстывалась
// целиком каждые ~220 мс (процессор + мусор O(всей сессии) на кадр).
func TestLayoutBlockCacheReuse(t *testing.T) {
	d := &Doc{}
	d.AddUser("вопрос пользователя")
	d.BeginStream()
	d.StreamText("ответ модели\n") // с \n — блок создаётся лениво по строке
	d.EndStream()

	l1 := d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	if len(l1) == 0 {
		t.Fatal("раскладка пустая")
	}
	if len(d.lay) != 2 {
		t.Fatalf("в кэше %d блоков, ожидалось 2", len(d.lay))
	}

	// Кадр без изменений: строки переиспользуются, а не пересобираются.
	l2 := d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	if len(l2) != len(l1) {
		t.Fatalf("длина раскладки изменилась без изменений: %d → %d", len(l1), len(l2))
	}
	if reflect.ValueOf(l1[0]).Pointer() != reflect.ValueOf(l2[0]).Pointer() {
		t.Fatal("неизменённый блок пересобран заново")
	}

	// Новый блок: старые строки остались теми же срезами.
	d.ToolStart(ToolInfo{ID: "t1", Name: "bash", Args: "ls", Kind: "bash"})
	l3 := d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	if len(d.lay) != 3 {
		t.Fatalf("в кэше %d блоков, ожидалось 3", len(d.lay))
	}
	if reflect.ValueOf(l1[0]).Pointer() != reflect.ValueOf(l3[0]).Pointer() {
		t.Fatal("блок запроса пересобран после добавления карточки инструмента")
	}

	// Закрытие карточки меняет её содержимое (статус), но не трогает соседей.
	userLine := reflect.ValueOf(l3[0]).Pointer()
	d.ToolEnd(ToolInfo{ID: "t1", Name: "bash", Status: "ok", Elapsed: 42})
	l4 := d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	if reflect.ValueOf(l4[0]).Pointer() != userLine {
		t.Fatal("блок запроса пересобран после завершения инструмента")
	}
	idx := -1
	for i, l := range l3 {
		if strings.Contains(l.Text(), "bash") {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(l4) {
		t.Fatalf("карточка инструмента не найдена в раскладке: idx=%d", idx)
	}
	if l3[idx].Text() == l4[idx].Text() {
		t.Fatal("карточка инструмента не изменилась после ToolEnd")
	}

	// Рост открытого блока: обновляется только он. Свёрнутые размышления —
	// одна строка-сводка, но счётчик строк в ней обязан обновиться.
	d.BeginThink()
	l5 := d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	d.ThinkText("размышляю над задачей\n")
	l6 := d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	if len(l6) != len(l5) {
		t.Fatalf("свёрнутый блок вырос: %d → %d", len(l5), len(l6))
	}
	if l5[len(l5)-1].Text() == l6[len(l6)-1].Text() {
		t.Fatal("сводка размышлений не обновилась после ThinkText")
	}

	// Смена ширины — полная перерисовка, кэш сброшен.
	d.Layout(LayoutOpts{Width: 90, Theme: Dark()})
	if len(d.lay) != 4 {
		t.Fatalf("после смены ширины в кэше %d блоков, ожидалось 4 (все заново)", len(d.lay))
	}
}

// TestLayoutCacheAfterClear — Clear обязан сбрасывать кэш: иначе карта
// держала бы строки удалённых блоков (утечка), а при совпадении указателей
// переиспользовала бы устаревшую отрисовку.
func TestLayoutCacheAfterClear(t *testing.T) {
	d := &Doc{}
	d.AddUser("первый вопрос")
	d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	d.Clear()
	if d.lay != nil {
		t.Fatal("Clear не сбросил кэш раскладки")
	}
	d.AddUser("второй вопрос")
	ls := d.Layout(LayoutOpts{Width: 60, Theme: Dark()})
	var joined strings.Builder
	for _, l := range ls {
		joined.WriteString(l.Text())
		joined.WriteByte('\n')
	}
	if !strings.Contains(joined.String(), "второй вопрос") {
		t.Fatalf("после Clear в ленте устаревший текст: %q", joined.String())
	}
}
