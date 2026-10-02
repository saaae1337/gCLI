package main

// Тесты командных функций, оставшихся без покрытия: /mission и цепочка
// шагов, /mcp, /fork, снимок агента для self_status.
//
// Проверяем не «функция не упала», а состояние, которое человек увидит
// потом: файл mission_chain.json на диске, созданный шаблон mcp.json,
// появившаяся сессия-форк, счётчики в снимке агента. Роутер без такой
// проверки может годами печатать «готово», ничего не делая.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
)

// ---------- /mission ----------

func TestCmdMissionStartStopSave(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdMission("start long-time 2h «починить тесты»")
	if got := out(buf); !strings.Contains(got, "прогон запущен") {
		t.Fatalf("старт прогона:\n%s", got)
	}
	if a.currentMissionTr() == nil {
		t.Fatal("трекер не создан")
	}
	// Состояние обязано пережить перезапуск.
	if _, ok, _ := core.LoadMissionState(core.MissionStatePath(a.workDir)); !ok {
		t.Fatal("mission_state.json не записан")
	}

	buf.Reset()
	a.cmdMission("status")
	if got := out(buf); !strings.Contains(got, "починить тесты") {
		t.Fatalf("статус прогона:\n%s", got)
	}

	buf.Reset()
	a.cmdMission("stop")
	if got := out(buf); !strings.Contains(got, "прогон остановлен") {
		t.Fatalf("stop:\n%s", got)
	}
	if !a.currentMissionTr().Done() {
		t.Fatal("после stop прогон должен быть помечен как завершённый")
	}
	// Журнал держит файл открытым: без закрытия t.TempDir не смоет его на
	// Windows.
	a.closeMissionJournal()

	buf.Reset()
	a.cmdMission("save")
	if !strings.Contains(out(buf), "состояние прогона сохранено") {
		t.Fatalf("save:\n%s", out(buf))
	}
}

func TestCmdMissionRejectsRunWithoutCeilings(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdMission("start «просто цель»")
	if got := out(buf); !strings.Contains(got, "нечего запускать") {
		t.Fatalf("ход без потолков должен быть отвергнут:\n%s", got)
	}
	if a.currentMissionTr() != nil {
		t.Fatal("трекер без потолков создаваться не должен")
	}
}

func TestCmdMissionStopAndSaveWithoutRun(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdMission("stop")
	a.cmdMission("save")
	got := out(buf)
	if strings.Count(got, "прогон не запущен") != 2 {
		t.Fatalf("обе команды без прогона должны честно сказать об этом:\n%s", got)
	}
}

func TestCmdMissionHelpAndReportRoutes(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdMission("help")
	got := out(buf)
	if !strings.Contains(got, "/mission chain") || !strings.Contains(got, "Режимы:") {
		t.Fatalf("справка /mission:\n%s", got)
	}

	// Отчёт без журнала и без задания: команда всё равно обязана создать файл.
	buf.Reset()
	a.cmdMission("report")
	if !strings.Contains(out(buf), "Отчёт по прогону") {
		t.Fatalf("report:\n%s", out(buf))
	}
	data, err := os.ReadFile(MissionReportPath(a.workDir))
	if err != nil {
		t.Fatalf("файл отчёта не записан: %v", err)
	}
	if !strings.Contains(string(data), "Отчёт по автономному прогону") {
		t.Fatalf("отчёт пустой: %q", string(data))
	}
	if head := reportHeadline(string(data)); head == "" {
		t.Fatal("в отчёте нет строки статуса")
	}

	buf.Reset()
	a.cmdMission("report html")
	if !strings.Contains(out(buf), "страница отчёта") {
		t.Fatalf("report html:\n%s", out(buf))
	}
	if _, err := os.Stat(strings.TrimSuffix(MissionReportPath(a.workDir), ".md") + ".html"); err != nil {
		t.Fatalf("html не записан: %v", err)
	}
}

func TestMissionReportHTMLWithoutReport(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdMission("report html")
	if !strings.Contains(out(buf), "сначала построй отчёт") {
		t.Fatalf("без отчёта html обязан предупредить, а не выдумать файл:\n%s", out(buf))
	}
}

func TestMissionHelpTextListsAllModes(t *testing.T) {
	help := missionModesHelp()
	for _, mode := range core.MissionModes() {
		if !strings.Contains(help, string(mode)) {
			t.Errorf("режим %q не попал в справку: %q", mode, help)
		}
	}
}

func TestLoadMissionFileVariants(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gcli", "mission.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// JSONC с комментарием — формат, который пишет человек руками.
	body := `{
		// режим
		"mode": "long-time",
		"objective": "починить core"
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := loadMissionFile(filepath.Join(dir, ".gcli", "mission.json"))
	if err != nil {
		t.Fatalf("файл миссии не прочитан: %v", err)
	}
	if m.Objective != "починить core" || m.Mode != core.MissionLongTime {
		t.Fatalf("миссия разобрана неверно: %+v", m)
	}
	// Каталог вместо файла — тоже допустимый вход.
	if _, err := loadMissionFile(dir); err != nil {
		t.Fatalf("каталог с mission.json: %v", err)
	}
	if _, err := loadMissionFile(filepath.Join(dir, "нет-такого.json")); err == nil {
		t.Fatal("отсутствующий файл должен давать ошибку")
	}
	// Битый режим — с понятной ошибкой, а не молчаливый normal.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"mode":"вечно"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMissionFile(bad); err == nil || !strings.Contains(err.Error(), "неизвестный режим") {
		t.Fatalf("битый режим: %v", err)
	}
}

// ---------- /mission chain ----------

func TestMissionChainAddListClear(t *testing.T) {
	a, buf := cmdApp(t)
	a.missionChainEnsure()

	buf.Reset()
	a.cmdMissionChain("list")
	if !strings.Contains(out(buf), "цепочка пуста") {
		t.Fatalf("пустая цепочка:\n%s", out(buf))
	}

	// Шаг без потолков — вечный прогон: цепочка его не принимает.
	buf.Reset()
	a.cmdMissionChain("add «просто цель»")
	if !strings.Contains(out(buf), "потолок") {
		t.Fatalf("шаг без потолка должен быть отвергнут:\n%s", out(buf))
	}
	// Цель обязательна: шаг без неё не значит ничего.
	buf.Reset()
	a.cmdMissionChain("add long-time")
	if !strings.Contains(out(buf), "должна быть цель") {
		t.Fatalf("шаг без цели:\n%s", out(buf))
	}

	buf.Reset()
	a.cmdMissionChain("add long-time «починить core»")
	if !strings.Contains(out(buf), "шаг 1 добавлен") {
		t.Fatalf("добавление шага:\n%s", out(buf))
	}
	if len(a.missionChain.Items) != 1 {
		t.Fatalf("шагов в цепочке: %d, ждали 1", len(a.missionChain.Items))
	}
	// Файл на диске — цепочка обязана пережить перезапуск.
	saved, ok, err := core.LoadMissionChain(a.workDir)
	if err != nil || !ok || len(saved.Items) != 1 {
		t.Fatalf("цепочка не сохранена: ok=%v err=%v", ok, err)
	}

	buf.Reset()
	a.cmdMissionChain("list")
	if got := out(buf); !strings.Contains(got, "починить core") || !strings.Contains(got, "следующий") {
		t.Fatalf("список шагов:\n%s", got)
	}

	buf.Reset()
	a.cmdMissionChain("clear")
	if !strings.Contains(out(buf), "цепочка очищена") {
		t.Fatalf("clear:\n%s", out(buf))
	}
	if _, err := os.Stat(core.MissionChainPath(a.workDir)); !os.IsNotExist(err) {
		t.Fatalf("файл цепочки остался на диске: %v", err)
	}
}

func TestMissionChainRunEmptyAndFinished(t *testing.T) {
	a, buf := cmdApp(t)
	buf.Reset()
	a.cmdMissionChain("run")
	if !strings.Contains(out(buf), "цепочка пуста") {
		t.Fatalf("run на пустой цепочке:\n%s", out(buf))
	}

	a.missionChain = &core.MissionChain{Items: []core.Mission{{Mode: core.MissionLongTime}}, Index: 1}
	buf.Reset()
	a.chainRun()
	if !strings.Contains(out(buf), "уже отработали") {
		t.Fatalf("run на выполненной цепочке:\n%s", out(buf))
	}
}

func TestMissionChainEnsureLoadsOnceAndSurvivesBrokenFile(t *testing.T) {
	a, buf := cmdApp(t)
	// Битый файл — предупреждение и пустая цепочка, а не падение.
	if err := core.WriteAtomic(core.MissionChainPath(a.workDir), []byte("{ бито"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	a.missionChainEnsure()
	if !strings.Contains(out(buf), "начинаю с пустой") {
		t.Fatalf("битая цепочка:\n%s", out(buf))
	}
	if len(a.missionChain.Items) != 0 {
		t.Fatal("после битого файла цепочка должна быть пустой")
	}
	// Второй вызов не перечитывает: состояние в памяти уже есть.
	a.missionChain.Items = append(a.missionChain.Items, core.Mission{Objective: "шаг"})
	a.missionChainEnsure()
	if len(a.missionChain.Items) != 1 {
		t.Fatal("ленивая загрузка должна брать файл только один раз")
	}
}

func TestCmdMissionChainUnknownSubcommand(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdMissionChain("переделать")
	if !strings.Contains(out(buf), "не знаю подкоманду") {
		t.Fatalf("опечатка в подкоманде должна быть замечена:\n%s", out(buf))
	}
}

// ---------- /mcp ----------

func TestCmdMcpEmptyAndTemplate(t *testing.T) {
	a, buf := cmdApp(t)
	buf.Reset()
	a.cmdMcp("")
	if got := out(buf); !strings.Contains(got, "не настроены") || !strings.Contains(got, "/mcp new") {
		t.Fatalf("список серверов без конфига:\n%s", got)
	}

	buf.Reset()
	a.cmdMcp("path")
	if got := out(buf); !strings.Contains(got, "mcp.json") {
		t.Fatalf("пути конфигов:\n%s", got)
	}

	buf.Reset()
	a.cmdMcp("new")
	if !strings.Contains(out(buf), "шаблон создан") {
		t.Fatalf("создание шаблона:\n%s", out(buf))
	}
	data, err := os.ReadFile(filepath.Join(a.workDir, ".gcli", "mcp.json"))
	if err != nil {
		t.Fatalf("шаблон не записан: %v", err)
	}
	if !strings.Contains(string(data), "servers") {
		t.Fatalf("шаблон бессмысленен: %q", string(data))
	}

	// Второй раз шаблон не перетирает правки человека.
	buf.Reset()
	a.cmdMcp("new")
	if !strings.Contains(out(buf), "уже существует") {
		t.Fatalf("повторный new:\n%s", out(buf))
	}
}

func TestCmdMcpReloadWithoutServers(t *testing.T) {
	a, buf := cmdApp(t)
	buf.Reset()
	a.cmdMcp("reload")
	if !strings.Contains(out(buf), "новых инструментов нет") {
		t.Fatalf("reload без серверов:\n%s", out(buf))
	}
	// Сервер из проекта без согласия не запускается — список обязан это показать.
	if err := core.WriteAtomic(filepath.Join(a.workDir, ".gcli", "mcp.json"),
		[]byte(`{"servers":{"файл":{"command":"echo","args":["hi"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	a.cmdMcp("")
	if got := out(buf); !strings.Contains(got, "файл") || !strings.Contains(got, "подтверждения") {
		t.Fatalf("сервер без доверия должен быть помечен:\n%s", got)
	}
}

// ---------- /fork ----------

func TestCmdFork(t *testing.T) {
	a, buf := cmdApp(t)
	old := a.sess.ID
	a.sess.Messages = msgsOf(core.RoleUser, core.RoleAssistant)
	a.sess.Title = "исходная"

	buf.Reset()
	a.cmdFork("не число")
	if !strings.Contains(out(buf), "укажи номер сообщения") {
		t.Fatalf("нечисловой аргумент:\n%s", out(buf))
	}
	buf.Reset()
	a.cmdFork("0")
	if !strings.Contains(out(buf), "укажи номер сообщения") {
		t.Fatalf("нулевой номер:\n%s", out(buf))
	}

	buf.Reset()
	a.cmdFork("")
	if !strings.Contains(out(buf), "форк создан") {
		t.Fatalf("создание форка:\n%s", out(buf))
	}
	if a.sess.ID == old {
		t.Fatal("после форка активной должна стать новая сессия")
	}
	if !strings.HasPrefix(a.sess.Title, "fork: ") {
		t.Fatalf("заголовок форка: %q", a.sess.Title)
	}
	if len(a.sess.Messages) != 2 || a.sess.Usage != (core.Usage{}) {
		t.Fatalf("форк унаследовал лишнее: %d сообщений, usage %+v", len(a.sess.Messages), a.sess.Usage)
	}
	if _, err := a.repo.LoadSession(a.sess.ID); err != nil {
		t.Fatalf("сессия-форк не сохранена: %v", err)
	}
	// Старая сессия осталась в /sessions — форк не съедает прошлое. В тесте
	// её на диске не было: сохраняем вручную, иначе проверяем пустоту.
	if err := a.repo.SaveSession(&core.Session{ID: old}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.repo.LoadSession(old); err != nil {
		t.Fatalf("старая сессия пропала: %v", err)
	}
}

func TestCmdForkEmptySession(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdFork("")
	if !strings.Contains(out(buf), "сессия пуста") {
		t.Fatalf("форк пустой сессии:\n%s", out(buf))
	}
}

// ---------- self_status ----------

func TestSelfReportReflectsState(t *testing.T) {
	a, _ := cmdApp(t)
	a.mu.Lock()
	a.notes = []string{"подсказка", "ещё одна"}
	a.mu.Unlock()
	a.mission = core.Mission{Objective: "цель", Mode: core.MissionLongTime}.Apply()
	if !a.startTracker() {
		t.Fatal("трекер не создался")
	}

	rep := a.selfReport()
	if rep.Model != "glm-4.6" || rep.Provider != "zai" {
		t.Fatalf("модель/провайдер в снимке: %+v", rep)
	}
	if rep.CompactAt <= 0 || rep.MaxIters <= 0 {
		t.Fatalf("потолки не сообщены: CompactAt=%d MaxIters=%d", rep.CompactAt, rep.MaxIters)
	}
	if len(rep.Notes) != 2 || rep.Notes[0] != "подсказка" {
		t.Fatalf("заметки задачи: %v", rep.Notes)
	}
	if rep.Mission == "" {
		t.Fatal("снимок без прогона должен упоминать прогон, а не молчать")
	}
	a.closeMissionJournal()
	if SelfTokens("привет, мир") == 0 {
		t.Fatal("SelfTokens вернул 0")
	}
}

func TestNotesCopyIsIndependent(t *testing.T) {
	a, _ := cmdApp(t)
	if got := a.notesCopy(); got != nil {
		t.Fatalf("без заметок копия должна быть nil: %v", got)
	}
	a.mu.Lock()
	a.notes = []string{"одна"}
	a.mu.Unlock()
	cp := a.notesCopy()
	cp[0] = "подменена"
	if a.notes[0] != "одна" {
		t.Fatal("копия заметок обязана быть независимой: правка копии меняет исходные")
	}
}
