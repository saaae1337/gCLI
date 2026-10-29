package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// ---------- клэмп прокрутки при пересборке кэша ----------
//
// refreshCacheLocked клэмпил scroll к длине кэша, а не к «длина минус
// высота ленты»: после усадки кэша (смена ширины при прокрутке вверх)
// off упирался в ноль, а счётчик «↑ N» и бегунок рисовались по
// завышенному scroll — три индикатора противоречили друг другу.

func TestRefreshCacheClampsScrollToViewport(t *testing.T) {
	a := newTestApp(strings.NewReader(""), &bytes.Buffer{})
	a.w, a.h = 60, 24

	// Длинные строки: при узкой ленте каждая занимает несколько строк кэша.
	long := strings.Repeat("слово ", 40)
	for i := 0; i < 40; i++ {
		a.doc.Text(fmt.Sprintf("строка %d: %s\n", i, long))
	}
	a.refreshCacheLocked(60)
	h := a.bodyHeight()
	fat := len(a.cache) - h
	if fat <= 0 {
		t.Fatalf("кэш не длиннее ленты: %d строк, лента %d", len(a.cache), h)
	}
	a.scroll = fat // прокрутка до самого верха

	// Широкая лента: переносы исчезают, кэш резко укорачивается.
	a.refreshCacheLocked(600)
	max := len(a.cache) - h
	if max < 0 {
		max = 0
	}
	if a.scroll > max {
		t.Fatalf("scroll=%d пережил усадку кэша до %d строк (лента %d): максимум %d — счётчик и бегунок лгут",
			a.scroll, len(a.cache), h, max)
	}
}

// TestRefreshCacheZeroesScrollWhenFeedFits — лента короче окна:
// прокрутки нет, и счётчик «↑ N» показываться не должен.
func TestRefreshCacheZeroesScrollWhenFeedFits(t *testing.T) {
	a := newTestApp(strings.NewReader(""), &bytes.Buffer{})
	a.w, a.h = 60, 24
	a.doc.Text("короткая строка\n")
	a.scroll = 5
	a.refreshCacheLocked(60)
	if a.scroll != 0 {
		t.Fatalf("лента помещается целиком, scroll=%d — индикатор без прокрутки", a.scroll)
	}
}
