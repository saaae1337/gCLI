package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gcli/core"
)

// ---------- Разбор аргументов /mission start ----------

func TestParseMissionArgsBasic(t *testing.T) {
	m, err := parseMissionArgs("long-time 2h 500k")
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != core.MissionLongTime {
		t.Errorf("режим %q", m.Mode)
	}
	if m.Deadline != core.Dur(2*time.Hour) {
		t.Errorf("срок %s, хотели 2h", m.Deadline)
	}
	if m.TokenBudget != 500_000 {
		t.Errorf("бюджет %d, хотели 500000", m.TokenBudget)
	}
}

func TestParseMissionArgsObjectiveAndAcceptance(t *testing.T) {
	// Кавычки обязательны: без них «go test ./... зелёные» распалось бы
	// на пять критериев, каждый из которых выполнить нельзя.
	m, err := parseMissionArgs(`extra-long-time «починить тесты» «go test ./... зелёные» «vet молчит»`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != core.MissionExtraLong {
		t.Errorf("режим %q", m.Mode)
	}
	if m.Objective != "починить тесты" {
		t.Errorf("цель %q", m.Objective)
	}
	if len(m.Acceptance) != 2 {
		t.Fatalf("критериев %d, хотели 2: %v", len(m.Acceptance), m.Acceptance)
	}
	if m.Acceptance[0] != "go test ./... зелёные" {
		t.Errorf("первый критерий %q — кавычки не удержали фразу", m.Acceptance[0])
	}
}

func TestParseMissionArgsObjectiveWithoutQuotes(t *testing.T) {
	// Цель без кавычек обязана работать: это самая частая форма,
	// и требовать кавычки ради одного слова неудобно.
	m, err := parseMissionArgs("overnight починить")
	if err != nil {
		t.Fatal(err)
	}
	if m.Objective != "починить" {
		t.Errorf("цель %q", m.Objective)
	}
	if m.Mode != core.MissionOvernight {
		t.Errorf("режим %q", m.Mode)
	}
}

func TestParseMissionArgsMoney(t *testing.T) {
	// Деньги и токены в одной строке: «500k,25».
	m, err := parseMissionArgs("long-time 500k,25")
	if err != nil {
		t.Fatal(err)
	}
	if m.TokenBudget != 500_000 {
		t.Errorf("токены %d", m.TokenBudget)
	}
	if m.CostBudget != 25 {
		t.Errorf("деньги %v", m.CostBudget)
	}
}

func TestParseMissionArgsPlainNumberIsTokens(t *testing.T) {
	// Голое «500000» — токены, а не минуты: в разговоре о работе агента
	// «500000 минут» не означает ничего осмысленного.
	m, err := parseMissionArgs("long-time 500000")
	if err != nil {
		t.Fatal(err)
	}
	if m.TokenBudget != 500_000 {
		t.Errorf("токены %d", m.TokenBudget)
	}
}

func TestSplitMissionArgsQuotes(t *testing.T) {
	got := splitMissionArgs(`start "а б" в`)
	if len(got) != 3 {
		t.Fatalf("поля %d: %+v", len(got), got)
	}
	if got[1].text != "а б" || !got[1].quoted {
		t.Errorf("второе поле %+v", got[1])
	}
	if got[2].text != "в" || got[2].quoted {
		t.Errorf("третье поле %+v", got[2])
	}
}

// ---------- Разбор бюджета флага ----------

func TestParseBudgetVariants(t *testing.T) {
	cases := []struct {
		in     string
		tokens int
		cost   float64
	}{
		{"500000", 500000, 0},
		{"500k", 500000, 0},
		{"1.5M", 1500000, 0},
		{"25", 0, 25},
		{"$25", 0, 25},
		{"25.5", 0, 25.5},
		{"500k,25", 500000, 25},
		{"500000 25", 0, 0}, // два числа без разделителя — не ошибка, но и не разбор
	}
	for _, c := range cases {
		tok, cost, err := parseBudget(c.in)
		if c.tokens == 0 && c.cost == 0 && c.in == "500000 25" {
			_ = err
			continue
		}
		if err != nil {
			t.Errorf("parseBudget(%q): %v", c.in, err)
			continue
		}
		if tok != c.tokens || cost != c.cost {
			t.Errorf("parseBudget(%q) = %d, %v; хотели %d, %v", c.in, tok, cost, c.tokens, c.cost)
		}
	}
}

func TestParseTokenNum(t *testing.T) {
	cases := map[string]int{
		"1000":   1000,
		"500k":   500_000,
		"2K":     2000,
		"1.5M":   1_500_000,
		"0.5k":   500,
		"100000": 100_000,
	}
	for in, want := range cases {
		got, err := parseTokenNum(in)
		if err != nil {
			t.Errorf("parseTokenNum(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseTokenNum(%q) = %d, хотели %d", in, got, want)
		}
	}
	if _, err := parseTokenNum("не число"); err == nil {
		t.Error("мусор должен отвергаться, а не превращаться в ноль токенов")
	}
}

// ---------- Состояние прогона на диске ----------

func TestMissionStateRoundTrip(t *testing.T) {
	work := t.TempDir()
	path := core.MissionStatePath(work)
	st := core.MissionState{
		Objective: "починить тесты",
		Mode:      "long-time",
		Summary:   "чекпоинт: время 1ч из 4ч",
		Status:    "время: 1ч из 4h0m0s",
		Iters:     42,
		ToolCalls: 130,
		Tokens:    500_000,
		Updated:   time.Now().Format(time.RFC3339),
	}
	if err := core.SaveMissionState(path, st); err != nil {
		t.Fatal(err)
	}
	got, ok, err := core.LoadMissionState(path)
	if err != nil || !ok {
		t.Fatalf("чтение состояния: ok=%v err=%v", ok, err)
	}
	if got.Objective != st.Objective || got.Iters != st.Iters || got.Tokens != st.Tokens {
		t.Errorf("состояние исказилось: %+v", got)
	}
}

func TestLoadMissionStateAbsent(t *testing.T) {
	_, ok, err := core.LoadMissionState(filepath.Join(t.TempDir(), "нет.json"))
	if err != nil {
		t.Errorf("отсутствие файла — не ошибка: %v", err)
	}
	if ok {
		t.Error("отсутствующий файл не должен читаться как «состояние есть»")
	}
}

func TestFreshMissionState(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	fresh := core.MissionState{Updated: now.Add(-time.Hour).Format(time.RFC3339)}
	if !core.FreshMissionState(fresh, now, 24*time.Hour) {
		t.Error("состояние час назад — свежее")
	}
	old := core.MissionState{Updated: now.Add(-48 * time.Hour).Format(time.RFC3339)}
	if core.FreshMissionState(old, now, 24*time.Hour) {
		t.Error("состояние двое суток назад — не свежее")
	}
	if core.FreshMissionState(core.MissionState{}, now, 24*time.Hour) {
		t.Error("без метки времени состояние не бывает свежим")
	}
}

// ---------- Флаг -mission поверх файла ----------

func TestSetupMissionFlagsOverrideFile(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(core.MissionPath(work)), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  // Задание из репозитория: человек собрал его один раз.
  "objective": "из файла",
  "mode": "normal",
  "deadline": "8h",
  "token_budget": 100000
}`
	if err := os.WriteFile(core.MissionPath(work), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{workDir: work}
	// Флаг -deadline перебивает файл: это разовое переопределение под
	// конкретный запуск, а не замена задания целиком.
	if err := a.setupMission("", "90m", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if a.mission.Objective != "из файла" {
		t.Errorf("флаги затерли цель из файла: %q", a.mission.Objective)
	}
	if a.mission.Deadline != core.Dur(90*time.Minute) {
		t.Errorf("срок %s, флаг должен был перебить файл", a.mission.Deadline)
	}
	if a.mission.TokenBudget != 100_000 {
		t.Errorf("бюджет %d из файла потерялся", a.mission.TokenBudget)
	}
}

func TestSetupMissionModeFlag(t *testing.T) {
	a := &app{workDir: t.TempDir()}
	if err := a.setupMission("long-time", "", "", "цель", ""); err != nil {
		t.Fatal(err)
	}
	if a.mission.Mode != core.MissionLongTime {
		t.Errorf("режим %q", a.mission.Mode)
	}
	if !a.mission.Long() {
		t.Error("long-time обязан считаться длинным прогоном")
	}
	if a.mission.Objective != "цель" {
		t.Errorf("цель %q", a.mission.Objective)
	}
}

func TestSetupMissionBrokenFileIsError(t *testing.T) {
	// Молчаливый откат к обычному режиму опаснее отказа: человек
	// собрал восьмичасовой прогон, а получил двадцать минут.
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(core.MissionPath(work)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(core.MissionPath(work), []byte(`{"deadline": `), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{workDir: work}
	if err := a.setupMission("", "", "", "", ""); err == nil {
		t.Fatal("битый mission.json должен быть ошибкой запуска")
	}
}
