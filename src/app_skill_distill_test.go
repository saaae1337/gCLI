package main

// Тесты автонавыков: имя, частота инструментов, тело навыка.

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"gcli/core"
)

var reValidSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,30}$`)

func TestMissionSkillNameValid(t *testing.T) {
	n := missionSkillName(time.Date(2026, 10, 2, 15, 45, 0, 0, time.UTC))
	if !reValidSkillName.MatchString(n) {
		t.Fatalf("имя невалидно: %q", n)
	}
	if !strings.HasPrefix(n, "auto-") {
		t.Fatalf("имя должно начинаться с auto-: %q", n)
	}
}

func TestTopTools(t *testing.T) {
	counts := map[string]int{
		"bash": 5, "read_file": 9, "edit_file": 3, "grep": 9,
	}
	got := topTools(counts, 3)
	if len(got) != 3 {
		t.Fatalf("длина: %v", got)
	}
	// При равенстве — алфавитный порядок: детерминизм для тестов и диффов.
	if got[0] != "grep" || got[1] != "read_file" || got[2] != "bash" {
		t.Fatalf("порядок: %v", got)
	}
	if len(topTools(nil, 5)) != 0 {
		t.Fatal("пустая карта — пустой список")
	}
}

func TestToolCounts(t *testing.T) {
	recs := []core.JournalRecord{
		{Kind: "checkpoint", Tools: []string{"bash", "bash", "edit_file"}},
		{Kind: "stop", Tools: []string{"bash"}},
	}
	c := toolCounts(recs)
	if c["bash"] != 3 || c["edit_file"] != 1 {
		t.Fatalf("частоты: %v", c)
	}
}

func TestBuildMissionSkillBody(t *testing.T) {
	d := missionReportData{
		Mission:  core.Mission{Objective: "починить все падающие тесты в core", Mode: core.MissionLongTime},
		State:    core.MissionState{Iters: 42, ToolCalls: 60, Tokens: 150000},
		HasState: true,
		Records: []core.JournalRecord{
			{Kind: "start", Objective: "починить все падающие тесты в core"},
			{Kind: "checkpoint", Summary: "нашёл три падающих теста в core/repo.go"},
			{Kind: "checkpoint", Summary: "исправил мьютекс, тесты зелёные"},
			{Kind: "stop", Reason: core.StopDone, Tools: []string{"bash", "edit_file"}},
		},
	}
	name, body := buildMissionSkill(d, time.Date(2026, 10, 2, 15, 45, 0, 0, time.UTC))
	if !strings.HasPrefix(body, "---\n") {
		t.Fatal("нет frontmatter")
	}
	for _, want := range []string{
		"name: " + name,
		"починить все падающие тесты в core",
		"итераций 42", "Ход работы", "исправил мьютекс", "Инструменты",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("в теле навыка нет %q:\n%s", want, body)
		}
	}
	// Чекпоинты идут от поздних к ранним.
	late := strings.Index(body, "исправил мьютекс")
	early := strings.Index(body, "нашёл три падающих")
	if late < 0 || early < 0 || late > early {
		t.Fatalf("порядок чекпоинтов неверен (поздние раньше): late=%d early=%d", late, early)
	}
}
