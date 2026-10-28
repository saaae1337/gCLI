package tui

import (
	"strings"
	"testing"
	"time"
)

// Регрессия: панели старых команд печатают в ленту правила из псевдографики,
// ANSI и — главное — паддинг ячеек таблиц из одних пробелов. Пробельный
// прогон длиннее ширины строки возвращал из cutText пустой кусок, и
// renderPlainSegs зацикливался навсегда, держа мьютекс: /stats, /usage,
// /model и любой другой вывод с таблицей замораживал интерфейс. Теперь
// раскладка обязана завершаться за миллисекунды.
func TestLayoutNoHangOnSpaceRuns(t *testing.T) {
	d := &Doc{}

	// Правило из боковой псевдографики — без единого пробела внутри.
	rule := "  " + strings.Repeat("─", 76) + "\n"
	// Строка таблицы с ANSI и хвостовым паддингом из пробелов.
	ansi := "\x1b[38;2;201;138;62m  ✦ \x1b[0m\x1b[2mРасход за 30 дней\x1b[0m" +
		strings.Repeat(" ", 90) + "\n"
	// Чистый пробельный прогон — прямой виновник зависания.
	spaces := strings.Repeat(" ", 120) + "\n"
	big := strings.Repeat(rule+ansi+spaces, 20)

	start := time.Now()
	d.Text(big)
	ls := d.Layout(LayoutOpts{Width: 78, Theme: Dark()})
	elapsed := time.Since(start)
	t.Logf("60 строк панелей: %d строк раскладки за %v", len(ls), elapsed)

	if elapsed > 5*time.Second {
		t.Fatalf("раскладка катастрофически медленная: %v", elapsed)
	}
	if len(ls) == 0 {
		t.Fatal("раскладка пустая")
	}
}
