package main

// ---------- Тесты /mission report ----------
//
// Отчёт — лицо прогона после его смерти: по нему человек решает, что
// делать дальше. Поэтому проверяем не «функция не паникует», а то, что
// отчёт честно показывает статус, счётчики и хронику в каждом из
// реальных состояний прогона.

import (
	"strings"
	"testing"
	"time"

	"gcli/core"
)

func TestBuildMissionReportEmpty(t *testing.T) {
	// Ничего не было: отчёт обязан так и сказать, а не выводить нули,
	// притворяясь, что прогон состоялся.
	rep := buildMissionReport(missionReportData{})
	if !strings.Contains(rep, "не начинался") {
		t.Errorf("в пустом отчёте нет честного статуса:\n%s", rep)
	}
	if strings.Contains(rep, "| Итерации |") {
		t.Errorf("пустой отчёт не должен показывать таблицу счётчиков:\n%s", rep)
	}
}

func TestBuildMissionReportRunning(t *testing.T) {
	// Живой прогон: статус «идёт», счётчики — из трекера.
	tr := core.NewTracker(core.Mission{
		Objective: "починить тесты",
		Mode:      core.MissionLongTime,
	}, 0, 0)
	tr.Tick(5, 12, 8000)
	rep := buildMissionReport(missionReportData{Mission: tr.Mission(), Tracker: tr})
	if !strings.Contains(rep, "идёт") {
		t.Errorf("живой прогон показан не как идущий:\n%s", rep)
	}
	if !strings.Contains(rep, "починить тесты") {
		t.Errorf("в отчёте нет цели:\n%s", rep)
	}
	if !strings.Contains(rep, "| Итерации | 5 |") {
		t.Errorf("счётчик итераций не из трекера:\n%s", rep)
	}
	if !strings.Contains(rep, "long-time") {
		t.Errorf("в отчёте нет режима:\n%s", rep)
	}
}

func TestBuildMissionReportStoppedFromJournal(t *testing.T) {
	// Прогон закончился и процесс уже умер: трекера нет, счётчики и
	// причина читаются из последней записи журнала.
	start := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	stop := start.Add(3 * time.Hour)
	recs := []core.JournalRecord{
		{TS: start.Format(time.RFC3339), Kind: core.JournalStart,
			Summary: "прогон запущен", Objective: "рефакторинг", Mode: "overnight"},
		{TS: start.Add(time.Hour).Format(time.RFC3339), Kind: core.JournalCheckpoint,
			Summary: "половина тестов починена", Iters: 40, ToolCalls: 90, Tokens: 150000},
		{TS: stop.Format(time.RFC3339), Kind: core.JournalStop,
			Summary: "бюджет исчерпан", Reason: core.StopCost,
			Iters: 120, ToolCalls: 310, Tokens: 480000, SpentUSD: 24.9,
			Tools: []string{"bash", "edit_file"}},
	}
	rep := buildMissionReport(missionReportData{Records: recs})
	if !strings.Contains(rep, "исчерпан бюджет денег") {
		t.Errorf("причина остановки не из журнала:\n%s", rep)
	}
	if !strings.Contains(rep, "| Итерации | 120 |") {
		t.Errorf("счётчики не взяты из последней записи:\n%s", rep)
	}
	if !strings.Contains(rep, "$24.90") {
		t.Errorf("расход в долларах потерялся:\n%s", rep)
	}
	if !strings.Contains(rep, "01.10 05:00:00") {
		t.Errorf("время остановки без даты теряет ночные прогоны:\n%s", rep)
	}
	if !strings.Contains(rep, "bash, edit_file") {
		t.Errorf("последние инструменты не показаны:\n%s", rep)
	}
	if !strings.Contains(rep, "чекпоинт") {
		t.Errorf("чекпоинта нет в хронике:\n%s", rep)
	}
}

func TestBuildMissionReportTruncatedTail(t *testing.T) {
	// Оборванный журнал — оговорка о честности в первой части отчёта.
	rep := buildMissionReport(missionReportData{
		Records: []core.JournalRecord{
			{TS: "2026-10-01T02:00:00Z", Kind: core.JournalStart, Summary: "старт"},
		},
		TailOK: false,
	})
	if !strings.Contains(rep, "журнал оборван") {
		t.Errorf("обрыв журнала не упомянут:\n%s", rep)
	}
}

func TestBuildMissionReportResumed(t *testing.T) {
	// Возобновление после обрыва — отдельное событие хроники.
	recs := []core.JournalRecord{
		{TS: "2026-10-01T02:00:00Z", Kind: core.JournalStart, Summary: "старт"},
		{TS: "2026-10-01T09:00:00Z", Kind: core.JournalResume, Summary: "продолжен после обрыва"},
	}
	rep := buildMissionReport(missionReportData{Records: recs})
	if !strings.Contains(rep, "продолжение") {
		t.Errorf("resume не подписан в хронике:\n%s", rep)
	}
}

func TestKindLabel(t *testing.T) {
	cases := map[string]string{
		core.JournalStart:      "старт",
		core.JournalCheckpoint: "чекпоинт",
		core.JournalStop:       "остановка",
		core.JournalClose:      "сеанс закрыт",
		core.JournalResume:     "продолжение",
		"":                     "—",
		"чтотостранное":        "чтотостранное",
	}
	for kind, want := range cases {
		if got := kindLabel(kind); got != want {
			t.Errorf("kindLabel(%q) = %q, ждали %q", kind, got, want)
		}
	}
}
