package tools

// Тесты проверки HTML-страницы: битые ресурсы, мёртвые якоря, пустой
// title и картинки без alt ломают страницу тихо.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckHTMLPageFindsProblems(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "index.html")
	body := `<!DOCTYPE html><html><head><title>Ок</title></head><body>
<h1 id="anchor">Живой якорь</h1>
<link rel="stylesheet" href="styles.css">
<script src="app.js"></script>
<img src="logo.png">
<a href="#anchor">якорь</a>
<a href="#missing">мёртвый якорь</a>
</body></html>`
	if err := os.WriteFile(page, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := checkHTMLPage(page)
	if len(rep.Problems) == 0 {
		t.Fatal("проблемы не найдены")
	}
	joined := strings.Join(rep.Problems, "\n")
	for _, want := range []string{"styles.css", "app.js", "logo.png", "#missing"} {
		if !strings.Contains(joined, want) {
			t.Errorf("не найдено %q:\n%s", want, joined)
		}
	}
	// Мёртвый якорь — это про страницу, а не про внешний адрес.
	if strings.Contains(joined, "#anchor") {
		t.Errorf("живой якорь сочтён мёртвым:\n%s", joined)
	}
	// Внешние адреса проверять нечем.
	if strings.Contains(joined, "https://") {
		t.Errorf("внешний адрес попал в проблемы:\n%s", joined)
	}
}

func TestCheckHTMLPageCleanPage(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "index.html")
	body := `<!DOCTYPE html><html><head><title>Ок</title>
<meta name="viewport" content="width=device-width"></head>
<body><h1 id="top">Привет</h1><img src="logo.png" alt="Логотип"></body></html>`
	if err := os.WriteFile(page, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Картинка должна существовать, иначе это уже проблема.
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := checkHTMLPage(page)
	if len(rep.Problems) != 0 {
		t.Errorf("чистая страница показала проблемы:\n%s", strings.Join(rep.Problems, "\n"))
	}
	if rep.Checks == 0 {
		t.Error("проверок не насчитано")
	}
}

func TestCheckHTMLPageNoViewport(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "index.html")
	body := `<!DOCTYPE html><html><head><title>Ок</title></head><body>текст</body></html>`
	if err := os.WriteFile(page, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := checkHTMLPage(page)
	if !strings.Contains(strings.Join(rep.Problems, "\n"), "viewport") {
		t.Errorf("нет viewport — а это поломка мобильной вёрстки:\n%v", rep.Problems)
	}
}

func TestFindHTMLPagePicksIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findHTMLPage(dir); filepath.Base(got) != "index.html" {
		t.Errorf("найдена страница %q, ожидался index.html", got)
	}
	// Без страниц — пусто.
	if got := findHTMLPage(t.TempDir()); got != "" {
		t.Errorf("найдена страница %q, хотя её нет", got)
	}
}
