package main

// HTML-отчёт миссии: self-contained страница для «показать кому-то».
//
// Зачем: mission_report.md — отличный артефакт для git и текстового
// редактора, но для человека со стороны (руководитель, заказчик, просто
// «глянь, что агeнт делал ночью») нужен файл, который открывается двойным
// кликом. Отчёт — одна страница со стилями внутри: кладётся в gist,
// Pages, attach к письму — без зависимостей и без сети при сборке.
//
// Рендер намеренно минимальный: заголовки, списки, таблицы, инлайн-код,
// кодовые блоки, параграфы. Всё остальное — как параграф: отчёт строится
// самим gCLI, его разметка заранее известна.

import (
	"fmt"
	"html"
	"os"
	"strings"

	"gcli/core"
)

// reportHTMLTemplate — каркас страницы. Стили внутри: файл самодостаточен.
const reportHTMLTemplate = `<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
:root { color-scheme: light dark; }
body { font: 15px/1.6 -apple-system, "Segoe UI", Roboto, sans-serif;
       max-width: 860px; margin: 40px auto; padding: 0 20px; }
h1 { border-bottom: 2px solid #8884; padding-bottom: .3em; }
h2 { border-bottom: 1px solid #6663; padding-bottom: .2em; margin-top: 1.6em; }
code { background: #8881; padding: .1em .35em; border-radius: 4px;
       font-family: ui-monospace, Consolas, monospace; font-size: .92em; }
pre { background: #8881; padding: 12px 14px; border-radius: 8px;
      overflow-x: auto; }
pre code { background: none; padding: 0; }
table { border-collapse: collapse; margin: 1em 0; }
th, td { border: 1px solid #8884; padding: 6px 12px; text-align: left; }
th { background: #8881; }
blockquote { border-left: 4px solid #8884; margin-left: 0;
             padding-left: 14px; color: #888; }
hr { border: none; border-top: 1px solid #6663; }
</style>
</head>
<body>
%s
</body>
</html>
`

// mdToHTML — минимальный markdown → HTML.
//
// Почему не импорт: внешних зависимостей в gcli нет вообще (go.mod пуст),
// и одна страница отчёта не повод их заводить. Поддержано ровно то, что
// строит buildMissionReport: #/##/###, списки -, таблицы |…|, кодовые
// блоки ```, инлайн-код `…`, параграфы. Всё экранируется html.EscapeString
// до разметки: содержимое отчёта может включать куски чужого кода.
func mdToHTML(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out strings.Builder
	inCode := false
	var para []string
	var table []string

	flushPara := func() {
		if len(para) > 0 {
			out.WriteString("<p>")
			out.WriteString(inlineMD(strings.Join(para, " ")))
			out.WriteString("</p>\n")
			para = nil
		}
	}
	flushTable := func() {
		if len(table) == 0 {
			return
		}
		// Первая строка — заголовок, вторая — разделитель |---|.
		out.WriteString("<table>\n<thead><tr>")
		for _, cell := range splitTableRow(table[0]) {
			fmt.Fprintf(&out, "<th>%s</th>", inlineMD(cell))
		}
		out.WriteString("</tr></thead>\n<tbody>\n")
		for _, row := range table[1:] {
			if isTableSeparator(row) {
				continue
			}
			out.WriteString("<tr>")
			for _, cell := range splitTableRow(row) {
				fmt.Fprintf(&out, "<td>%s</td>", inlineMD(cell))
			}
			out.WriteString("</tr>\n")
		}
		out.WriteString("</tbody>\n</table>\n")
		table = nil
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "```"):
			flushPara()
			flushTable()
			if inCode {
				out.WriteString("</code></pre>\n")
				inCode = false
			} else {
				out.WriteString("<pre><code>")
				inCode = true
			}
		case inCode:
			out.WriteString(html.EscapeString(line) + "\n")
		case strings.TrimSpace(line) == "":
			flushPara()
			flushTable()
		case strings.HasPrefix(line, "### "):
			flushPara()
			flushTable()
			fmt.Fprintf(&out, "<h3>%s</h3>\n", inlineMD(line[4:]))
		case strings.HasPrefix(line, "## "):
			flushPara()
			flushTable()
			fmt.Fprintf(&out, "<h2>%s</h2>\n", inlineMD(line[3:]))
		case strings.HasPrefix(line, "# "):
			flushPara()
			flushTable()
			fmt.Fprintf(&out, "<h1>%s</h1>\n", inlineMD(line[2:]))
		case strings.HasPrefix(line, "---") && len(strings.TrimSpace(line)) == 3:
			flushPara()
			flushTable()
			out.WriteString("<hr>\n")
		case strings.HasPrefix(line, "|") && strings.HasSuffix(strings.TrimSpace(line), "|"):
			// Строка таблицы — копим до конца блока.
			flushPara()
			table = append(table, line)
		case strings.HasPrefix(strings.TrimSpace(line), "- "):
			flushPara()
			flushTable()
			// Список: без слияния в <ul> — отчёт строится самим gcli,
			// и там только плоские списки; <li> вне <ul> невалиден,
			// поэтому каждый элемент — строка с маркером.
			fmt.Fprintf(&out, "<p>• %s</p>\n", inlineMD(strings.TrimSpace(line[2:])))
		default:
			flushTable()
			para = append(para, strings.TrimSpace(line))
		}
	}
	flushPara()
	flushTable()
	if inCode {
		out.WriteString("</code></pre>\n")
	}
	return out.String()
}

// inlineMD — инлайн-разметка: инлайн-код и жирный. Всё экранировано.
func inlineMD(s string) string {
	s = html.EscapeString(s)
	// Инлайн-код сначала: внутри него разметку не разворачиваем.
	parts := strings.Split(s, "`")
	for i := 1; i < len(parts); i += 2 {
		parts[i] = "<code>" + parts[i] + "</code>"
	}
	s = strings.Join(parts, "")
	// Жирный **текст**.
	for {
		i := strings.Index(s, "**")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+2:], "**")
		if j < 0 {
			break
		}
		s = s[:i] + "<strong>" + s[i+2:i+2+j] + "</strong>" + s[i+2+j+2:]
	}
	return s
}

// splitTableRow — ячейки строки таблицы: | a | b | → a, b.
func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// isTableSeparator — строка вида |---|---:| (разделитель заголовка).
func isTableSeparator(line string) bool {
	for _, cell := range splitTableRow(line) {
		cell = strings.TrimSuffix(strings.TrimPrefix(cell, ":"), ":")
		if strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return len(splitTableRow(line)) > 0
}

// missionReportHTML — /mission report html: markdown → .html рядом.
func (a *app) missionReportHTML() {
	path := MissionReportPath(a.workDir)
	data, err := os.ReadFile(path)
	if err != nil {
		a.ui.Warn("сначала построй отчёт: /mission report")
		return
	}
	title := "Отчёт миссии — gcli " + core.Version
	page := fmt.Sprintf(reportHTMLTemplate, html.EscapeString(title), mdToHTML(string(data)))
	htmlPath := strings.TrimSuffix(path, ".md") + ".html"
	if err := core.WriteAtomic(htmlPath, []byte(page), 0o644); err != nil {
		a.ui.Err("html не записан: " + err.Error())
		return
	}
	a.ui.Ok("страница отчёта: " + htmlPath)
	a.ui.Println("  " + a.ui.Gray("положи в gist/Pages или отправь файлом — стили внутри, внешних ресурсов нет"))
}
