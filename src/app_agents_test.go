package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
	"gcli/providers"
	"gcli/subagents"
	"gcli/tools"
	"gcli/ui"
)

// agentsApp — приложение, готовое к командам /agents.
//
// Отдельный GCLI_HOME и отдельный workDir обязательны: команды /agents пишут
// config.json, а listAgents/customAgents читают .gcli/agents из рабочего
// каталога. Без изоляции тесты писали бы в ~/.gcli настоящего пользователя и
// падали бы от его агентов.
func agentsApp(t *testing.T) (*app, *strings.Builder) {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("GCLI_HOME", home)

	buf := &strings.Builder{}
	store := core.NewStore()
	prov := &providers.Provider{
		ID:    "zai",
		Label: "Z.ai",
		NoKey: true,
		Models: []string{
			"glm-4.6",
			"glm-4.6-air",
			"glm-4.5-air",
		},
	}
	a := &app{
		repo:    &core.Repo{Store: store, Cfg: core.DefaultConfig()},
		store:   store,
		ui:      testUI(buf),
		workDir: work,
		prov:    prov,
		model:   "glm-4.6",
		tools:   tools.New(tools.Env{WorkDir: work}),
		memory:  tools.NewMemory(work, store),
		pool:    subagents.NewPool(nil, subagents.PoolOptions{MaxParallel: 3, MaxDepth: 1, Enabled: true, WorkDir: work}),
		sess:    &core.Session{ID: "s1", AgentMode: true},
	}
	a.tools.RegisterSubagentTools()
	return a, buf
}

func testUI(buf *strings.Builder) *ui.UI {
	return ui.New(ui.Options{
		Theme: "ember", Unicode: true, Color: true, Grade: ui.ColorRGB,
		Width: 80, Out: buf,
	})
}

// out — текст вывода команды без ANSI-последовательностей.
func out(buf *strings.Builder) string { return ui.StripANSI(buf.String()) }

// savedConfig — то, что команда реально записала в config.json.
func savedConfig(t *testing.T, a *app) core.Config {
	t.Helper()
	data, err := os.ReadFile(a.store.ConfigPath())
	if err != nil {
		t.Fatalf("config.json не записан: %v", err)
	}
	var cfg core.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config.json не разбирается: %v", err)
	}
	return cfg
}

// TestAgentsRouteOffByDefaultAndExplains — /agents route без аргумента
// показывает состояние и говорит, что именно маршрутизация сделает. Иначе
// переключатель остаётся абстракцией: человек не знает, зачем его включать.
func TestAgentsRouteOffByDefaultAndExplains(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("route")

	got := out(buf)
	for _, want := range []string{"выключена", "/agents route on|off", "70%", "90%"} {
		if !strings.Contains(got, want) {
			t.Errorf("в выводе /agents route нет %q:\n%s", want, got)
		}
	}
}

// TestAgentsRouteOnPersists — включение маршрутизации уходит в config.json,
// иначе настройка живёт только до конца сессии.
func TestAgentsRouteOnPersists(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("route on")

	if !a.repo.Cfg.SubRoute {
		t.Error("cfg.SubRoute не выставлен")
	}
	if !savedConfig(t, a).SubRoute {
		t.Error("sub_route не записан в config.json")
	}
	got := out(buf)
	if !strings.Contains(got, "маршрутизация включена") {
		t.Errorf("нет подтверждения включения:\n%s", got)
	}
	if !strings.Contains(got, "бюджет") {
		t.Errorf("без бюджета надо подсказать, что понижения по нему не будет:\n%s", got)
	}

	// Выключение — тоже на диск, и состояние сразу перечитывается.
	buf.Reset()
	a.cmdAgents("route off")
	if a.repo.Cfg.SubRoute {
		t.Error("cfg.SubRoute не сброшен")
	}
	if savedConfig(t, a).SubRoute {
		t.Error("sub_route=false не записан в config.json")
	}
	if s := out(buf); !strings.Contains(s, "выключена") {
		t.Errorf("нет подтверждения выключения:\n%s", s)
	}
}

// TestAgentsRouteAliases — кириллические синонимы и 1/0 ведут себя как on/off.
func TestAgentsRouteAliases(t *testing.T) {
	for _, on := range []string{"on", "вкл", "1", "true", "да"} {
		a, _ := agentsApp(t)
		a.cmdAgents("route " + on)
		if !a.repo.Cfg.SubRoute {
			t.Errorf("«/agents route %s» должен включать маршрутизацию", on)
		}
	}
	for _, off := range []string{"off", "выкл", "0", "false", "нет"} {
		a, _ := agentsApp(t)
		a.repo.Cfg.SubRoute = true
		a.cmdAgents("route " + off)
		if a.repo.Cfg.SubRoute {
			t.Errorf("«/agents route %s» должен выключать маршрутизацию", off)
		}
	}
}

// TestAgentsRouteRejectsGarbage — мусорный аргумент не должен молча
// включать или выключать маршрутизацию.
func TestAgentsRouteRejectsGarbage(t *testing.T) {
	a, buf := agentsApp(t)
	a.repo.Cfg.SubRoute = true
	a.cmdAgents("route может")

	if !a.repo.Cfg.SubRoute {
		t.Error("мусорный аргумент не должен менять состояние")
	}
	if s := out(buf); !strings.Contains(s, "on | off") {
		t.Errorf("не показаны допустимые значения:\n%s", s)
	}
}

// TestAgentsBudgetOffWhenUnset — без бюджета команда честно говорит, что
// понижения по нему не будет, и подсказывает формат.
func TestAgentsBudgetOffWhenUnset(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("budget")

	got := out(buf)
	if !strings.Contains(got, "не задан") || !strings.Contains(got, "/agents budget 500000") {
		t.Errorf("не сказано, что бюджет не задан:\n%s", got)
	}
}

// TestAgentsBudgetReportsSpend — с бюджетом команда показывает расход и
// процент, а при 70%+ честно предупреждает, что понижаются все роли.
func TestAgentsBudgetReportsSpend(t *testing.T) {
	cases := []struct {
		name  string
		spent int
		limit int
		want  string
	}{
		{"спокойно", 200_000, 500_000, "40%"},
		{"жмёт", 750_000, 1_000_000, "жмёт: понижаются все роли"},
		{"предел", 960_000, 1_000_000, "на пределе: понижаются все роли"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, buf := agentsApp(t)
			a.repo.Cfg.SubBudget = c.limit
			a.sess.Usage.PromptTokens = c.spent
			a.cmdAgents("budget")

			got := out(buf)
			if !strings.Contains(got, c.want) {
				t.Errorf("в выводе нет %q:\n%s", c.want, got)
			}
			if c.spent*100 >= c.limit*70 && !strings.Contains(got, "понижаются все роли") {
				t.Errorf("при %d/%d токенах надо предупредить о понижении всех ролей:\n%s", c.spent, c.limit, got)
			}
		})
	}
}

// TestAgentsBudgetSetAndOffPersists — установка и снятие бюджета пишутся в
// конфиг; снятие важно не меньше: иначе пользователь не может вернуть
// маршрутизацию к «по роли».
func TestAgentsBudgetSetAndOffPersists(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("budget 500000")

	if a.repo.Cfg.SubBudget != 500_000 {
		t.Errorf("cfg.SubBudget = %d, ожидалось 500000", a.repo.Cfg.SubBudget)
	}
	if got := savedConfig(t, a).SubBudget; got != 500_000 {
		t.Errorf("sub_budget в config.json = %d, ожидалось 500000", got)
	}
	if s := out(buf); !strings.Contains(s, "70%") {
		t.Errorf("не сказано, на каких порогах сработает понижение:\n%s", s)
	}

	buf.Reset()
	a.cmdAgents("budget off")
	if a.repo.Cfg.SubBudget != 0 {
		t.Errorf("cfg.SubBudget = %d, ожидалось 0", a.repo.Cfg.SubBudget)
	}
	if got := savedConfig(t, a).SubBudget; got != 0 {
		t.Errorf("sub_budget в config.json = %d, ожидалось 0", got)
	}
}

// TestAgentsBudgetHintsRoutingOff — бюджет без маршрутизации бесполезен;
// об этом надо сказать сразу, а не после первого же понижения.
func TestAgentsBudgetHintsRoutingOff(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("budget 500000")

	if s := out(buf); !strings.Contains(s, "/agents route on") {
		t.Errorf("не напомнено включить маршрутизацию:\n%s", s)
	}
}

// TestAgentsBudgetRejectsBadValues — границы и мусор: значение не должно
// молча превращаться в нулевой бюджет или в потолок на 1 токен.
//
// «0» в списке мусора нет: в конфиге ноль означает «бюджета нет», и команда
// честно трактует его как off, а не как ошибку ввода.
func TestAgentsBudgetRejectsBadValues(t *testing.T) {
	for _, bad := range []string{"-5", "abc", "1000", "99999999"} {
		a, buf := agentsApp(t)
		a.repo.Cfg.SubBudget = 123_456
		a.cmdAgents("budget " + bad)

		if a.repo.Cfg.SubBudget != 123_456 {
			t.Errorf("«budget %s» изменил бюджет на %d", bad, a.repo.Cfg.SubBudget)
		}
		if s := out(buf); !strings.Contains(s, "50000") {
			t.Errorf("«budget %s»: не показаны допустимые границы:\n%s", bad, s)
		}
	}
}

// TestCmdAgentsRoutingDispatchAliases — /agents маршрут и /agents бюджет
// ведут в те же обработчики, что и английские слова.
func TestCmdAgentsRoutingDispatchAliases(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("маршрут on")
	if !a.repo.Cfg.SubRoute {
		t.Error("«/agents маршрут on» не включил маршрутизацию")
	}

	a.cmdAgents("бюджет 750000")
	if a.repo.Cfg.SubBudget != 750_000 {
		t.Errorf("«/agents бюджет 750000»: cfg.SubBudget = %d", a.repo.Cfg.SubBudget)
	}
	if s := out(buf); s == "" {
		t.Error("ожидался вывод по кириллическим синонимам")
	}
}

// TestCmdAgentsParDepthValidation — параллельность и глубина: границы и
// пересборка пула. Пул пересобирается, потому что в старом остались старые
// лимиты, и /agents par N внешне срабатывал бы, а по факту нет.
func TestCmdAgentsParDepthValidation(t *testing.T) {
	t.Run("параллельность", func(t *testing.T) {
		a, _ := agentsApp(t)
		old := a.pool
		a.cmdAgents("par 5")
		if a.repo.Cfg.SubMaxPar != 5 {
			t.Errorf("SubMaxPar = %d, ожидалось 5", a.repo.Cfg.SubMaxPar)
		}
		if a.pool == old {
			t.Error("пул не пересобран — новый лимит не применится")
		}
		if got := savedConfig(t, a).SubMaxPar; got != 5 {
			t.Errorf("sub_max_par в config.json = %d", got)
		}

		a.cmdAgents("par 99")
		if a.repo.Cfg.SubMaxPar != 5 {
			t.Errorf("«par 99» изменил лимит на %d", a.repo.Cfg.SubMaxPar)
		}
		a.cmdAgents("par abc")
		if a.repo.Cfg.SubMaxPar != 5 {
			t.Errorf("«par abc» изменил лимит на %d", a.repo.Cfg.SubMaxPar)
		}
	})

	t.Run("глубина", func(t *testing.T) {
		a, _ := agentsApp(t)
		old := a.pool
		a.cmdAgents("depth 2")
		if a.repo.Cfg.SubMaxDepth != 2 {
			t.Errorf("SubMaxDepth = %d, ожидалось 2", a.repo.Cfg.SubMaxDepth)
		}
		if a.pool == old {
			t.Error("пул не пересобран")
		}
		a.cmdAgents("depth 9")
		if a.repo.Cfg.SubMaxDepth != 2 {
			t.Errorf("«depth 9» изменил глубину на %d", a.repo.Cfg.SubMaxDepth)
		}
	})
}

// TestCmdAgentsModelSameResets — «/agents model same» возвращает модель
// главного агента, иначе «назад» невозможно без правки конфига вручную.
func TestCmdAgentsModelSameResets(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("model glm-4.6-air")
	if a.repo.Cfg.SubModel != "glm-4.6-air" {
		t.Fatalf("SubModel = %q, ожидалось glm-4.6-air", a.repo.Cfg.SubModel)
	}

	buf.Reset()
	a.cmdAgents("model same")
	if a.repo.Cfg.SubModel != "" {
		t.Errorf("SubModel = %q, ожидалась пустая строка", a.repo.Cfg.SubModel)
	}
	if got := savedConfig(t, a).SubModel; got != "" {
		t.Errorf("sub_model в config.json = %q, ожидалась пустая строка", got)
	}
	if s := out(buf); !strings.Contains(s, "главного") {
		t.Errorf("не сказано, что субагенты поедут на модели главного агента:\n%s", s)
	}
}

// TestCmdAgentsTimeoutValidation — таймаут ограничен 1..60 минут.
func TestCmdAgentsTimeoutValidation(t *testing.T) {
	a, _ := agentsApp(t)
	a.cmdAgents("timeout 20")
	if a.repo.Cfg.SubTimeoutMin != 20 {
		t.Errorf("SubTimeoutMin = %d, ожидалось 20", a.repo.Cfg.SubTimeoutMin)
	}
	if got := savedConfig(t, a).SubTimeoutMin; got != 20 {
		t.Errorf("sub_timeout в config.json = %d", got)
	}

	a.cmdAgents("timeout 61")
	if a.repo.Cfg.SubTimeoutMin != 20 {
		t.Errorf("«timeout 61» изменил таймаут на %d", a.repo.Cfg.SubTimeoutMin)
	}
}

// TestCmdAgentsEmptyAndUnknownListRun — пустой вызов и незнакомая подкоманда
// не падают: обе ведут к списку, который ничего не ломает.
func TestCmdAgentsEmptyAndUnknownListRun(t *testing.T) {
	for _, rest := range []string{"", "что-то-неизвестное"} {
		a, buf := agentsApp(t)
		a.cmdAgents(rest)
		if s := out(buf); !strings.Contains(s, "/agents run explorer") {
			t.Errorf("«/agents %s» не показал подсказку запуска:\n%s", rest, s)
		}
	}
}

// TestCmdAgentsOnOffTogglesPoolAndTools — включение и выключение должно
// менять пул и сохраняться в конфиге.
//
// Инструмент spawn_agent при выключении остаётся в реестре намеренно: у
// субагента может быть собственный субагент, и вызывающий его родитель
// заинтересуется, что делегирование выключено, а не что такого инструмента
// нет вообще. Проверяем, что выключенный пул даёт внятную ошибку.
func TestCmdAgentsOnOffTogglesPoolAndTools(t *testing.T) {
	a, _ := agentsApp(t)
	a.cmdAgents("off")
	if a.pool.Enabled() {
		t.Error("пул остался включённым")
	}
	if got := savedConfig(t, a); got.Subagents {
		t.Error("subagents=false не записан")
	}
	if _, err := a.spawnAgent(nil, tools.SpawnArgs{Type: "explorer", Task: "что-то"}); err == nil {
		t.Error("выключенные субагенты должны отвечать ошибкой, а не работать")
	}

	a.cmdAgents("on")
	if !a.pool.Enabled() {
		t.Error("пул не включился")
	}
	if !a.tools.Has("spawn_agent") {
		t.Error("spawn_agent должен быть в реестре при включённых субагентах")
	}
	if got := savedConfig(t, a); !got.Subagents {
		t.Error("subagents=true не записан")
	}
}

// TestCmdAgentsRunWithoutArgsExplainsFormat — /agents run без задачи
// показывает формат и список типов, не начиная запуск.
func TestCmdAgentsRunWithoutArgsExplainsFormat(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("run")

	got := out(buf)
	if !strings.Contains(got, "/agents run <тип|имя-своего> <задача>") {
		t.Errorf("не показан формат команды:\n%s", got)
	}
	if !strings.Contains(got, "explorer") {
		t.Errorf("не показан список типов:\n%s", got)
	}
}

// TestCmdAgentsRunRejectsUnknownType — неизвестный тип должен остановиться
// с предупреждением, а не запустить субагента наугад.
func TestCmdAgentsRunRejectsUnknownType(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("run субагент-неизвестный что-то сделать")

	got := out(buf)
	if !strings.Contains(got, "субагент") {
		t.Errorf("нет предупреждения о неизвестном типе:\n%s", got)
	}
	if len(a.pool.All()) != 0 {
		t.Error("запусков быть не должно — тип не распознан")
	}
}

// TestAgentsConfigFileStaysValidJSON — команды /agents пишут конфиг
// целиком; проверяем, что после серии команд он остаётся разбираемым
// и содержит ожидаемые ключи.
func TestAgentsConfigFileStaysValidJSON(t *testing.T) {
	a, _ := agentsApp(t)
	for _, cmd := range []string{
		"on", "par 4", "depth 2", "model glm-4.6-air",
		"route on", "budget 600000", "timeout 15",
	} {
		a.cmdAgents(cmd)
	}
	data, err := os.ReadFile(filepath.Clean(a.store.ConfigPath()))
	if err != nil {
		t.Fatalf("config.json не записан: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("config.json после серии команд не разбирается: %v\n%s", err, data)
	}
	for key, want := range map[string]any{
		"sub_route":       true,
		"sub_budget":      float64(600_000),
		"sub_max_par":     float64(4),
		"sub_max_depth":   float64(2),
		"sub_timeout_min": float64(15),
		"sub_model":       "glm-4.6-air",
		"subagents":       true,
	} {
		if raw[key] != want {
			t.Errorf("config.json[%q] = %v, ожидалось %v", key, raw[key], want)
		}
	}
}

// TestCmdAgentsCancelWithoutRuns — /agents cancel без запусков должен
// честно сказать «0», а не «отменено субагентов: 0» с другой интонацией.
func TestCmdAgentsCancelWithoutRuns(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("cancel")

	if s := out(buf); !strings.Contains(s, "0") {
		t.Errorf("отмена без запусков должна показать 0:\n%s", s)
	}
}

// TestCmdAgentsNewCreatesTemplate — /agents new создаёт заготовку и второй
// раз не затирает её (иначе правленный агент исчезнет по недосмотру).
func TestCmdAgentsNewCreatesTemplate(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("new api-migrator")

	p := filepath.Join(a.workDir, ".gcli", "agents", "api-migrator.md")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("шаблон не создан: %v", err)
	}

	// Имя с пробелом и кириллицей — не имя агента: такой файл потом не
	// резолвится по имени, и субагент не запустится.
	buf.Reset()
	a.cmdAgents("new плохое имя")
	if _, err := os.Stat(filepath.Join(a.workDir, ".gcli", "agents", "плохое имя.md")); err == nil {
		t.Error("файл с недопустимым именем создан")
	}
	if s := out(buf); !strings.Contains(s, "латиница") {
		t.Errorf("не сказано, какие имена допустимы:\n%s", s)
	}

	if err := os.WriteFile(p, []byte("правленый агент"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	a.cmdAgents("new api-migrator")
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "правленый агент" {
		t.Errorf("существующий агент перезаписан: %v %q", err, data)
	}
	if s := out(buf); !strings.Contains(s, "уже существует") {
		t.Errorf("не сказано, что агент уже есть:\n%s", s)
	}
}

// TestCmdAgentsTypesListsRoles — /agents types показывает роли и их режим
// работы; read-only роли обязаны отличаться, иначе агент берёт на себя
// лишние права незаметно для человека.
func TestCmdAgentsTypesListsRoles(t *testing.T) {
	a, buf := agentsApp(t)
	a.cmdAgents("types")

	got := out(buf)
	for _, want := range []string{"explorer", "только чтение", "чтение и запись", "/agents new"} {
		if !strings.Contains(got, want) {
			t.Errorf("в /agents types нет %q:\n%s", want, got)
		}
	}
}

// TestCmdAgentsStatusShowsSettings — /agents status показывает настройки
// маршрутизации и бюджета: иначе человек вспоминает о них только через
// счёт. Проверяем подстроку, а не таблицу целиком.
func TestCmdAgentsStatusShowsSettings(t *testing.T) {
	a, buf := agentsApp(t)
	a.repo.Cfg.SubRoute = true
	a.repo.Cfg.SubBudget = 500_000
	a.cmdAgents("status")

	got := out(buf)
	for _, want := range []string{"/agents route on|off", "/agents budget N", "параллельно"} {
		if !strings.Contains(got, want) {
			t.Errorf("в /agents status нет подсказки %q:\n%s", want, got)
		}
	}
}
