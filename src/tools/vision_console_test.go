package tools

// Тесты ошибок консоли страницы: агент должен видеть, что скрипт на
// странице падает, не заводя для этого отдельных инструментов.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConsoleErrorsParsing — из лога браузера берутся только ошибки самой
// страницы: сообщения расширений и обычные console.log — шум.
func TestConsoleErrorsParsing(t *testing.T) {
	log := strings.Join([]string{
		`[123:45:67/011.000:INFO:CONSOLE(1)] "обычное сообщение", source: file:///C:/p.html (1)`,
		`[123:45:67/011.000:INFO:CONSOLE(12)] "Uncaught ReferenceError: x is not defined", source: file:///C:/p.html (12)`,
		`[123:45:67/011.000:ERROR:CONSOLE(20)] "ошибка приложения", source: file:///C:/p.html (20)`,
		`[123:45:67/011.000:INFO:CONSOLE(6150)] "[ProtocolLaunch] handler constructed", source: chrome-extension://abc/background.rollup.js (6150)`,
		`[123:45:67/011.000:ERROR:chrome\browser\task_manager\providers\fallback_task_provider.cc:126] Every renderer should have at least one task`,
	}, "\n")

	got := consoleErrors(log)
	if len(got) != 2 {
		t.Fatalf("ошибок найдено %d, ожидалось 2: %v", len(got), got)
	}
	if !strings.Contains(got[0], "ReferenceError") {
		t.Errorf("первая ошибка не та: %q", got[0])
	}
	if !strings.Contains(got[1], "ошибка приложения") {
		t.Errorf("вторая ошибка не та: %q", got[1])
	}
	for _, g := range got {
		if strings.Contains(g, "ProtocolLaunch") || strings.Contains(g, "Every renderer") {
			t.Errorf("в ошибки попал шум браузера: %q", g)
		}
	}
}

// TestScreenshotReportsConsoleErrors — снимок страницы с падающим скриптом
// приносит текст ошибки: иначе агент увидит красивый экран и не узнает,
// что страница сломана.
func TestScreenshotReportsConsoleErrors(t *testing.T) {
	if _, err := findBrowser(); err != nil {
		t.Skip("браузера нет")
	}
	dir := t.TempDir()
	page := filepath.Join(dir, "p.html")
	body := `<!DOCTYPE html><html><body><h1>hi</h1>
<script>undefinedFunction();</script>
</body></html>`
	if err := os.WriteFile(page, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(Env{WorkDir: dir})
	res, err := r.hScreenshot(context.Background(), map[string]any{
		"target": page, "out": filepath.Join(dir, "shot.png"),
		"width": 800, "height": 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) == 0 {
		t.Fatal("скриншот не приложен к результату")
	}
	if !strings.Contains(res.Text, "ReferenceError") {
		t.Errorf("ошибка консоли не попала в результат:\n%s", res.Text)
	}
}
