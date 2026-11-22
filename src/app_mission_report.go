package main

// ---------- /mission report: отчёт по прогону из журнала ----------
//
// Зачем. Журнал прогона — это машиночитаемый список записей, и человеком
// он читается только под дулом пистолета. После ночных прогонов,
// обрывов и возобновлений вопрос всегда один: «что произошло и на чём
// кончились деньги/время». Отчёт отвечает на него в markdown, который
// можно и прочитать, и в задачу/PR приложить.
//
// Границы решения. Отчёт строится ТОЛЬКО из того, что уже на диске:
// journal, mission_state.json и живой трекер. Он не переигрывает события
// и не догадывается, почему модель сделала тот или иной шаг — за этим
// человек идёт в сессию и в историю диалога.

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gcli/core"
)

// kindLabel — как назвать событие журнала в отчёте.
func kindLabel(kind string) string {
	switch kind {
	case core.JournalStart:
		return "старт"
	case core.JournalCheckpoint:
		return "чекпоинт"
	case core.JournalStop:
		return "остановка"
	case core.JournalClose:
		return "сеанс закрыт"
	case core.JournalResume:
		return "продолжение"
	case "":
		return "—"
	}
	return kind
}

// missionReportData — всё, из чего строится отчёт.
//
// Собрано в структуру намеренно: построитель отчёта не должен лезть ни
// на диск, ни в UI, ни в агент. На входе данные, на выходе текст —
// тогда его можно проверять тестом, не запуская агент и не выдумывая
// окружение.
type missionReportData struct {
	// Mission — задание прогона (файл или флаги).
	Mission core.Mission
	// Tracker — живой трекер текущего прогона; nil, когда прогона нет.
	Tracker *core.Tracker
	// State — состояние с диска (mission_state.json).
	State core.MissionState
	// HasState — было ли состояние на диске.
	HasState bool
	// Records — записи журнала в порядке появления.
	Records []core.JournalRecord
	// TailOK — false, если журнал оборван (битый хвост).
	TailOK bool
}

// MissionReportPath — файл отчёта прогона.
func MissionReportPath(workDir string) string {
	return filepath.Join(workDir, ".gcli", "mission_report.md")
}

// buildMissionReport — собрать markdown-отчёт по прогону.
//
// Приоритет источников счётчиков: живой трекер → последняя запись журнала
// → состояние с диска. Трекер самый свежий, журнал — самый честный по
// хронологии, состояние — то, что переживёт перезапуск.
func buildMissionReport(d missionReportData) string {
	var b strings.Builder
	b.WriteString("# Отчёт по автономному прогону\n\n")

	// ---- Рамка: цель и режим. ----
	//
	// Падение приоритета: задание → состояние на диске → журнал. Журнал —
	// последний источник ровно по его замыслу: в каждой записи дублируются
	// objective и mode, чтобы рамка прогона восстанавливалась даже тогда,
	// когда mission.json к этому моменту переписан руками.
	last := core.JournalRecord{}
	if len(d.Records) > 0 {
		last = d.Records[len(d.Records)-1]
	}
	if obj := core.OneLine(d.Mission.Objective); obj != "" {
		b.WriteString("- Цель: " + obj + "\n")
	} else if obj := core.OneLine(d.State.Objective); obj != "" {
		b.WriteString("- Цель: " + obj + "\n")
	} else if obj := core.OneLine(last.Objective); obj != "" {
		b.WriteString("- Цель: " + obj + "\n")
	}
	mode := string(d.Mission.Mode)
	if mode == "" {
		mode = d.State.Mode
	}
	if mode == "" {
		mode = last.Mode
	}
	if mode == "" {
		mode = string(core.MissionNormal)
	}
	b.WriteString("- Режим: " + mode + "\n")

	// ---- Статус. ----
	status := "прогон не начинался"
	switch {
	case d.Tracker != nil && !d.Tracker.Done():
		status = "идёт"
	case d.Tracker != nil && d.Tracker.Done():
		status = "остановлен: " + core.StopReasonLabel(d.Tracker.Stopped())
	case d.HasState && d.State.StopReason != "":
		status = "остановлен: " + core.StopReasonLabel(d.State.StopReason)
	case d.HasState && d.State.Summary != "":
		status = "не окончен: " + core.OneLine(d.State.Summary)
	case last.Reason != "":
		status = "остановлен: " + core.StopReasonLabel(last.Reason)
	case last.Kind == core.JournalClose:
		status = "не окончен: сеанс закрыт, прогон не завершён"
	case len(d.Records) > 0:
		status = "не окончен"
	}
	b.WriteString("- Статус: " + status + "\n")
	if !d.TailOK {
		// Оборванный журнал — не украшение, а оговорка о честности:
		// хроника ниже неполная, и это надо сказать в самом верху.
		b.WriteString("- ВНИМАНИЕ: журнал оборван, часть записей потеряна\n")
	}

	// ---- Итоговые счётчики. ----
	iters, toolCalls, tokens := 0, 0, 0
	var cost float64
	var elapsed time.Duration
	haveCounters := false
	if d.Tracker != nil {
		iters, toolCalls, tokens = d.Tracker.Iters(), d.Tracker.ToolCalls(), d.Tracker.Spent()
		cost = d.Tracker.Cost("", "")
		elapsed = d.Tracker.Elapsed()
		haveCounters = true
	} else if last.Iters > 0 || last.ToolCalls > 0 || last.Tokens > 0 {
		iters, toolCalls, tokens = last.Iters, last.ToolCalls, last.Tokens
		cost = last.SpentUSD
		haveCounters = true
	} else if d.HasState {
		iters, toolCalls, tokens = d.State.Iters, d.State.ToolCalls, d.State.Tokens
		haveCounters = iters > 0 || toolCalls > 0 || tokens > 0
	}

	b.WriteString("\n## Итог\n\n")
	if !haveCounters {
		b.WriteString("Счётчиков нет: прогон не успел ни сделать шаг, ни записаться.\n\n")
	} else {
		b.WriteString("| Метрика | Значение |\n|---|---|\n")
		if elapsed > 0 {
			b.WriteString("| Время | " + core.FormatDur(elapsed) + " |\n")
		}
		b.WriteString("| Итерации | " + strconv.Itoa(iters) + " |\n")
		b.WriteString("| Вызовы инструментов | " + strconv.Itoa(toolCalls) + " |\n")
		b.WriteString("| Токены | " + core.CompactNum(tokens) + " |\n")
		if cost > 0 {
			b.WriteString("| Стоимость | $" + strconv.FormatFloat(cost, 'f', 2, 64) + " |\n")
		}
		if d.Tracker != nil {
			if cp := d.Tracker.Checkpoints(); cp > 0 {
				b.WriteString("| Сохранений состояния | " + strconv.Itoa(cp) + " |\n")
			}
			if cont := d.Tracker.Continues(); cont > 0 {
				b.WriteString("| Продолжений после «готово» | " + strconv.Itoa(cont) + " |\n")
			}
		}
		b.WriteString("\n")
	}

	// ---- Хроника. ----
	if len(d.Records) == 0 {
		b.WriteString("## Хроника\n\nЖурнал пуст: прогон не начинался.\n")
	} else {
		b.WriteString("## Хроника\n\n")
		b.WriteString("| Время | Событие | Итерации | Токены | Что произошло |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, rec := range d.Records {
			b.WriteString("| " + journalTime(rec.TS) +
				" | " + kindLabel(rec.Kind) +
				" | " + strconv.Itoa(rec.Iters) +
				" | " + core.CompactNum(rec.Tokens) +
				" | " + core.Truncate(core.OneLine(rec.Summary), 140) + " |\n")
		}
		b.WriteString("\n")
		if len(last.Tools) > 0 {
			b.WriteString("Последние инструменты перед остановкой: " +
				strings.Join(last.Tools, ", ") + "\n")
		}
	}
	return b.String()
}

// journalTime — время записи для хроники: день и часы.
//
// Прогон живёт днями, поэтому только «15:04» мало: ночной прогон
// начался вчера — и без даты не понять, какая из остановок последняя.
func journalTime(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("02.01 15:04:05")
	}
	return ts
}

// missionReport — команда /mission report: построить и показать отчёт.
func (a *app) missionReport() {
	recs, tailOK, err := core.ReadJournal(core.JournalPath(a.workDir))
	if err != nil {
		// Журнала может не быть — это не повод отказывать в отчёте:
		// состояние и задание всё равно расскажут, что произошло.
		recs, tailOK = nil, true
	}
	st, hasState, _ := core.LoadMissionState(core.MissionStatePath(a.workDir))

	rep := buildMissionReport(missionReportData{
		Mission:  a.missionJob(),
		Tracker:  a.currentMissionTr(),
		State:    st,
		HasState: hasState,
		Records:  recs,
		TailOK:   tailOK,
	})

	// WriteAtomic, а не os.WriteFile: каталог .gcli появляется только если
	// человек что-то уже запускал. Отчёт, запрошенный первым же действием в
	// свежем проекте, обязан создаться — иначе /mission report отказывал с
	// «не найден путь» ровно тогда, когда он нужнее всего.
	path := MissionReportPath(a.workDir)
	if err := core.WriteAtomic(path, []byte(rep), 0o644); err != nil {
		a.ui.Err("отчёт не записан: " + err.Error())
		return
	}
	a.ui.Println("")
	a.ui.Printf("  Отчёт по прогону: %s\n", path)
	a.ui.Printf("  Записей в журнале: %d\n", len(recs))
	if head := reportHeadline(rep); head != "" {
		a.ui.Println("  " + head)
	}
	a.ui.Println("")
}

// reportHeadline — строка «статус» из отчёта, чтобы в консоли сразу
// было видно, чем кончился прогон, без открытия файла.
func reportHeadline(rep string) string {
	for _, ln := range strings.Split(rep, "\n") {
		if s := strings.TrimPrefix(ln, "- Статус: "); s != ln {
			return s
		}
	}
	return ""
}

// ---------- Из app_report_html.go (HTML-отчёт прогона) ----------

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
