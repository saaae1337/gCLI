package main

// Тесты учёта экономии в сессии: счётчики от агента должны попадать в
// сессию и переживать её сохранение.

import (
	"strings"
	"testing"
	"time"

	"gcli/core"
	"gcli/ui"
)

// TestRecordEconomyMergesCounters — счётчики агента и субагента складываются
// в общий счёт сессии.
func TestRecordEconomyMergesCounters(t *testing.T) {
	a := testApp(t)
	a.RecordEconomy(core.EconomyStats{Requests: 3, PrunedTokens: 100, CacheHits: 1})
	a.RecordEconomy(core.EconomyStats{Requests: 2, PrunedTokens: 50, CacheHits: 2})

	e := a.sess.Economy
	if e.Requests != 5 {
		t.Errorf("запросов %d, ожидалось 5", e.Requests)
	}
	if e.PrunedTokens != 150 {
		t.Errorf("подрезано %d токенов, ожидалось 150", e.PrunedTokens)
	}
	if e.CacheHits != 3 {
		t.Errorf("попаданий в кеш %d, ожидалось 3", e.CacheHits)
	}
}

// TestRecordEconomyKeepsToolCalls — статистика по инструментам не теряется
// при слиянии: по ней видно, кто съедает контекст.
func TestRecordEconomyKeepsToolCalls(t *testing.T) {
	a := testApp(t)
	var e core.EconomyStats
	e.AddCall("read_file", 500)
	a.RecordEconomy(e)
	a.RecordEconomy(e)

	if got := a.sess.Economy.ToolCalls["read_file"]; got != 2 {
		t.Errorf("вызовов read_file %d, ожидалось 2", got)
	}
	if got := a.sess.Economy.ToolTokens["read_file"]; got != 1000 {
		t.Errorf("токенов от read_file %d, ожидалось 1000", got)
	}
}

// TestRateCountsOutputOnly — скорость считается по токенам ВЫВОДА.
//
// Входные токены — это повторённый контекст запроса: делить их на время
// значит показывать «2194 tok/s», которых модель не писала. Человек
// ждёт ответа — его и надо мерить.
func TestRateCountsOutputOnly(t *testing.T) {
	a := testApp(t)
	a.onUsage(core.Usage{PromptTokens: 13000, CompletionTokens: 120}, 2*time.Second)

	if got := a.lastRate; got < 55 || got > 65 {
		t.Errorf("скорость вывода = %.1f, ожидалось около 60 tok/s", got)
	}
}

// testApp — приложение с сессией, репо и UI во временном каталоге для тестов учёта экономии.
func testApp(t *testing.T) *app {
	t.Helper()
	a := &app{}
	a.repo = &core.Repo{Store: &core.Store{Root: t.TempDir()}, Cfg: core.DefaultConfig()}
	buf := &strings.Builder{}
	a.ui = ui.New(ui.Options{
		Theme: "ember", Unicode: true, Color: false, Grade: ui.Color16,
		Animations: false, Width: 80, Out: buf,
	})
	a.sess = &core.Session{}
	return a
}
