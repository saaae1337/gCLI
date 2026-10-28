package tui

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Регрессия: команда вида /stats печатает в ленту панель с
// рамками и ANSI — и интерфейс замирает на десятки секунд. Тест гоняет
// Doc.Text + Layout на реальном выводе и на синтетике из правил.
func TestLayoutNoHangOnSpaceRuns(t *testing.T) {
	raw, err := os.ReadFile("/home/z/my-project/repl_stats.txt")
	if err != nil {
		t.Skip("нет файла с реальным выводом")
	}
	text := string(raw)

	d := &Doc{}
	start := time.Now()
	d.Text(text)
	ls := d.Layout(LayoutOpts{Width: 78, Theme: Dark()})
	t.Logf("реальный вывод: %d блоков → %d строк за %v", len(d.blocks), len(ls), time.Since(start))

	// Синтетика: правило из псевдографики и ANSI-строка — типичные куски
	// старых панелей.
	d2 := &Doc{}
	rule := "  " + strings.Repeat("─", 76) + "\n"
	ansi := "\x1b[38;2;201;138;62m  ✦ \x1b[0m\x1b[2mРасход за 30 дней\x1b[0m\n"
	big := strings.Repeat(rule+ansi, 20)
	start = time.Now()
	d2.Text(big)
	ls2 := d2.Layout(LayoutOpts{Width: 78, Theme: Dark()})
	t.Logf("синтетика 20 панелей: %d строк за %v", len(ls2), time.Since(start))

	if time.Since(start) > 5*time.Second {
		t.Fatalf("раскладка катастрофически медленная: %v", time.Since(start))
	}
}
