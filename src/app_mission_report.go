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
		Mission:  a.mission,
		Tracker:  a.missionTr,
		State:    st,
		HasState: hasState,
		Records:  recs,
		TailOK:   tailOK,
	})

	path := MissionReportPath(a.workDir)
	if err := os.WriteFile(path, []byte(rep), 0o644); err != nil {
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
