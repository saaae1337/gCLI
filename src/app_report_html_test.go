package main

// Тесты минимального markdown→HTML рендера для отчёта миссии.

import (
	"strings"
	"testing"
)

func TestMDToHTMLHeadingsAndParagraphs(t *testing.T) {
	md := "# Заголовок\n\nАбзац с **жирным**.\n\n## Раздел"
	out := mdToHTML(md)
	for _, want := range []string{
		"<h1>Заголовок</h1>", "<h2>Раздел</h2>",
		"<strong>жирным</strong>", "<p>Абзац с ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("нет %q:\n%s", want, out)
		}
	}
}

func TestMDToHTMLEscapes(t *testing.T) {
	// Куски чужого кода в отчёте не должны становиться разметкой.
	out := mdToHTML("Параграф <script>alert(1)</script> и `код`")
	if strings.Contains(out, "<script>") {
		t.Fatalf("script не экранирован:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("нет экранирования:\n%s", out)
	}
	if !strings.Contains(out, "<code>код</code>") {
		t.Fatalf("инлайн-код потерян:\n%s", out)
	}
}

func TestMDToHTMLCodeBlocksAndTables(t *testing.T) {
	md := "```\nls -la <дир>\n```\n\n| a | b |\n|---|---|\n| 1 | **2** |\n"
	out := mdToHTML(md)
	if !strings.Contains(out, "ls -la &lt;дир&gt;") {
		t.Fatalf("кодовый блок не экранирован:\n%s", out)
	}
	if !strings.Contains(out, "<th>a</th>") || !strings.Contains(out, "<td>1</td>") {
		t.Fatalf("таблица сломана:\n%s", out)
	}
	if !strings.Contains(out, "<strong>2</strong>") {
		t.Fatalf("инлайн-разметка в ячейке потеряна:\n%s", out)
	}
}

func TestMDToHTMLListItems(t *testing.T) {
	out := mdToHTML("- первый пункт\n- второй пункт\n")
	if strings.Count(out, "• ") != 2 {
		t.Fatalf("пункты списка:\n%s", out)
	}
}
