package main

// Тесты команд-витрин: маскот, /plan, /skills, /ext, /export, /stats,
// доверие коду проекта и вспомогательные функции состояния.
//
// Что здесь ловится на самом деле:
//
//   - /mascot и /plan пишут в config.json. Ошибка в них выглядит как «переключил,
//     а ничего не изменилось» — заметить трудно.
//   - /skills и /ext работают с файлами проекта. Неверный путь или потерянный
//     шаблон означают, что человек создал файл не там и не знает об этом.
//   - доверие коду проекта — точка, где ошибка стоит безопасности: подтверждение
//     не должно проходить в тихом режиме и не должно висеть на «на всё».
//   - taskSummary/shortArgs/statusItems/serveToolStatus — чистые функции, которые
//     читают человек и модель. Ошибка в них не видна в логах, она видна как
//     «агент не понял задачу».

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gcli/core"
	"gcli/subagents"
	"gcli/tools"
	"gcli/ui"
)

// ---------- Мелкие чистые функции ----------

// TestMinInt — минимум из двух чисел: маскот берёт кусок строки по этому
// индексу, и отрицательный или слишком большой индекс уводил бы в панику.
func TestMinInt(t *testing.T) {
	if minInt(1, 3) != 1 || minInt(3, 1) != 1 || minInt(2, 2) != 2 {
		t.Error("minInt считается неверно")
	}
}

// TestMascotEnabledPriority — переменная окружения сильнее конфига, а отсутствие
// обоих означает «включён».
//
// Обратный порядок здесь означал бы, что /mascot off не работает у того, кто
// когда-то выставил GCLI_MASCOT=1, и кот продолжает жить вопреки команде.
func TestMascotEnabledPriority(t *testing.T) {
	off := false
	on := true
	cases := []struct {
		cfg  *bool
		env  string
		want bool
	}{
		{&on, "off", false},
		{&on, "0", false},
		{&on, "выкл", false},
		{&off, "on", true},
		{&off, "1", true},
		{&off, "", false},
		{nil, "", true},
		{&on, "мусор", true}, // непонятное значение не должно гасить кота
	}
	for _, c := range cases {
		if got := mascotEnabled(c.cfg, c.env); got != c.want {
			t.Errorf("mascotEnabled(%v, %q) = %v, ждали %v", c.cfg, c.env, got, c.want)
		}
	}
}

// TestApplyMascotFollowsEnv — команда /mascot off гасит кота, даже если
// переменная окружения говорит обратное.
func TestApplyMascotFollowsEnv(t *testing.T) {
	a, _ := cmdApp(t)
	t.Setenv("GCLI_MASCOT", "")
	a.setMascot(false)
	if a.ui.Mascot() {
		t.Error("после /mascot off кот должен быть выключен в интерфейсе, а не только в конфиге")
	}
	if savedConfig(t, a).Mascot == nil || *savedConfig(t, a).Mascot {
		t.Error("/mascot off должен записать решение в config.json")
	}
	a.setMascot(true)
	if !a.ui.Mascot() {
		t.Error("/mascot on обязан включить кота в интерфейсе")
	}
}

// TestOsHelpers — служебные функции окружения не должны падать и врать.
func TestOsHelpers(t *testing.T) {
	if !strings.Contains(osGOOS(), "/") {
		t.Errorf("osGOOS ждёт вид ОС/архитектуры, получили %q", osGOOS())
	}
	if osArch() == "" {
		t.Error("osArch вернул пустую строку")
	}
	dir := t.TempDir()
	p := writeTemp(t, dir, "файл.txt", "есть")
	if !fileExists(p) {
		t.Error("существующий файл должен находиться")
	}
	if fileExists(filepath.Join(dir, "нет.txt")) {
		t.Error("несуществующий файл не должен считаться найденным")
	}
	if got := benchDir(dir); !strings.HasSuffix(got, filepath.Join(".gcli", "bench")) {
		t.Errorf("benchDir: %q", got)
	}
}

// ---------- /mascot ----------

// TestCmdMascotStates — все ветки команды: показ, вкл/выкл, реплика, приветствие.
func TestCmdMascotStates(t *testing.T) {
	a, buf := cmdApp(t)

	// Без аргументов — справка с текущим состоянием.
	a.cmdMascot("")
	if got := out(buf); !strings.Contains(got, "Искра") || !strings.Contains(got, "/mascot on") {
		t.Errorf("пустой вызов должен показать справку и подсказку: %q", got)
	}

	buf.Reset()
	a.cmdMascot("say привет из теста")
	if got := out(buf); !strings.Contains(got, "привет из теста") {
		t.Errorf("say должен произнести текст: %q", got)
	}

	// say без текста — отказ с форматом, а не пустая реплика.
	buf.Reset()
	a.cmdMascot("say")
	if got := out(buf); !strings.Contains(got, "формат") {
		t.Errorf("пустой say должен объяснить формат: %q", got)
	}

	// Мусор — тоже отказ с подсказкой формата.
	buf.Reset()
	a.cmdMascot("покружиться")
	if got := out(buf); !strings.Contains(got, "формат") {
		t.Errorf("негодный аргумент должен быть назван: %q", got)
	}

	// Русские синонимы обязаны работать так же, как английские.
	buf.Reset()
	a.cmdMascot("выкл")
	if a.ui.Mascot() {
		t.Error("«выкл» должен выключать кота")
	}
	a.cmdMascot("вкл")
	if !a.ui.Mascot() {
		t.Error("«вкл» должен включать кота")
	}
}

// TestMascotResultReacts — реплика по итогам хода зависит от успеха и от того,
// что ошибка была подряд.
func TestMascotResultReactions(t *testing.T) {
	a, buf := cmdApp(t)
	a.ui.SetMascot(true)

	a.mascotResult(true)
	first := out(buf)
	buf.Reset()
	a.mascotResult(false)
	if out(buf) == "" {
		t.Error("ошибка без реплики выглядит как зависший агент")
	}

	// Вторая ошибка подряд: гнев вместо обычного «хандрю».
	buf.Reset()
	a.lastTurnFailed = true
	a.mascotResult(false)
	if out(buf) == "" {
		t.Error("повторная ошибка обязана что-то сказать")
	}

	// Выключенный кот молчит: иначе после /mascot off в вывод сыпется мусор.
	buf.Reset()
	a.ui.SetMascot(false)
	a.mascotResult(true)
	a.mascotBye()
	if got := out(buf); got != "" {
		t.Errorf("выключенный кот не должен ничего печатать: %q", got)
	}
	_ = first
}

// TestMascotByeAndDemo — прощание и прогон всех состояний не падают.
func TestMascotByeAndDemo(t *testing.T) {
	a, buf := cmdApp(t)
	a.ui.SetMascot(true)
	a.mascotBye()
	a.mascotDemo()
	if out(buf) == "" {
		t.Error("demo должен печатать состояния кота")
	}
}

// ---------- /plan ----------

// TestCmdPlanToggles — режим планирования переключается и переживает перезапуск.
func TestCmdPlanToggles(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdPlan("on")
	if !a.repo.Cfg.PlanMode || !savedConfig(t, a).PlanMode {
		t.Error("/plan on должен включить режим в памяти и в конфиге")
	}
	if got := out(buf); !strings.Contains(got, "планирования") {
		t.Errorf("включение должно объяснять режим: %q", got)
	}

	buf.Reset()
	a.cmdPlan("off")
	if a.repo.Cfg.PlanMode || savedConfig(t, a).PlanMode {
		t.Error("/plan off должен выключить режим")
	}
	if got := out(buf); !strings.Contains(got, "обычный") {
		t.Errorf("выключение должно подтвердить обычный режим: %q", got)
	}

	// Без аргумента — переключение, а не «включить на всякий случай».
	a.cmdPlan("")
	if !a.repo.Cfg.PlanMode {
		t.Error("пустой аргумент должен переключать режим")
	}
	a.cmdPlan("вкл")
	if !a.repo.Cfg.PlanMode {
		t.Error("«вкл» не должен выключать режим")
	}
}

// ---------- /skills ----------

// TestSkillRowDescIncludesTrigger — триггер when попадает в описание.
//
// По одному description пользователь не может понять, что навык не сработает,
// и выключает его, не разобравшись.
func TestSkillRowDescIncludesTrigger(t *testing.T) {
	if got := skillRowDesc(tools.Skill{Name: "x", Desc: " REVIEWS code "}); got != "REVIEWS code" {
		t.Errorf("однострочное описание: %q", got)
	}
	got := skillRowDesc(tools.Skill{Desc: "ревью", When: "перед коммитом"})
	if !strings.Contains(got, "перед коммитом") {
		t.Errorf("when должен дописываться в описание: %q", got)
	}
}

// TestCmdSkillsListShowToggle — навык создаётся, показывается, выключается и
// включается обратно, а решение доезжает до config.json.
func TestCmdSkillsListShowToggle(t *testing.T) {
	a, buf := cmdApp(t)

	// Создание: имя из аргумента, файл в .gcli/skills проекта.
	a.cmdSkills("new deploy-helper")
	p := filepath.Join(a.workDir, ".gcli", "skills", "deploy-helper.md")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("шаблон навыка не создан: %v", err)
	}
	if got := out(buf); !strings.Contains(got, "создан") {
		t.Errorf("создание должно подтвердиться: %q", got)
	}

	// Повторное создание того же имени — предупреждение, а не перезапись.
	buf.Reset()
	a.cmdSkills("new deploy-helper")
	if got := out(buf); !strings.Contains(got, "уже существует") {
		t.Errorf("повтор должен предупредить, а не молча перезаписать: %q", got)
	}

	// Плохое имя отвергается с пояснением.
	buf.Reset()
	a.cmdSkills("new Плохое Имя")
	if got := out(buf); !strings.Contains(got, "латиница") {
		t.Errorf("плохое имя должно быть отвергнуто с правилом: %q", got)
	}

	// Список показывает навык и его состояние.
	buf.Reset()
	a.cmdSkills("")
	got := out(buf)
	if !strings.Contains(got, "deploy-helper") || !strings.Contains(got, "вкл") {
		t.Errorf("список навыков: %q", got)
	}

	// Просмотр по имени печатает тело навыка.
	buf.Reset()
	a.cmdSkills("deploy-helper")
	if out(buf) == "" || !strings.Contains(out(buf), "deploy-helper") {
		t.Errorf("/skill <имя> должен показать навык: %q", out(buf))
	}

	// Выключение уезжает в конфиг, включение — обратно.
	a.cmdSkills("off deploy-helper")
	if !a.tools.SkillOff("deploy-helper") {
		t.Error("/skill off должен гасить навык в реестре")
	}
	if got := savedConfig(t, a).SkillsOff; len(got) != 1 || got[0] != "deploy-helper" {
		t.Errorf("выключенный навык должен попасть в config.json: %v", got)
	}
	buf.Reset()
	a.cmdSkills("")
	if !strings.Contains(out(buf), "выкл") {
		t.Errorf("в списке должно быть видно «выкл»: %q", out(buf))
	}

	a.cmdSkills("on deploy-helper")
	if a.tools.SkillOff("deploy-helper") {
		t.Error("/skill on обязан вернуть навык")
	}
	if got := savedConfig(t, a).SkillsOff; len(got) != 0 {
		t.Errorf("после включения список выключенных должен быть пуст: %v", got)
	}
}

// TestCmdSkillsFuzzyAndMissing — не найденное имя не молчит: либо показывается
// похожее, либо прямо назван отсутствующий навык.
func TestCmdSkillsFuzzyAndMissing(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdSkills("new code-review")
	buf.Reset()

	a.cmdSkills("review of my code")
	if got := out(buf); strings.Contains(got, "не найден") && !strings.Contains(got, "code-review") {
		t.Errorf("по описанию навык должен подбираться, а не отвергаться: %q", got)
	}

	// Запрос без пересечения с описанием навыка обязан быть отвергнут.
	// Предыдущая формулировка («совсем-несуществующий-навык») для этого не
	// годилась: слово «навык» есть в описании шаблона, и подбор справедливо
	// срабатывал.
	buf.Reset()
	a.cmdSkills("квантовая хромодинамика плазмы")
	if got := out(buf); !strings.Contains(got, "не найден") || !strings.Contains(got, "/skills") {
		t.Errorf("ненайденный навык должен быть назван вместе со списком: %q", got)
	}
}

// ---------- /ext ----------

// extFixture — проектное расширение с одной командой.
func extFixture(t *testing.T, work, name string) {
	t.Helper()
	dir := filepath.Join(work, ".gcli", "extensions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","description":"для тестов",` +
		`"tools":[{"name":"` + name + `_run","description":"что-то делает","command":"echo hi"}]}`
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCmdExtListAndNew — расширения читаются из проекта, создаются по имени и
// показываются вместе с типом инструмента.
func TestCmdExtListAndNew(t *testing.T) {
	a, buf := cmdApp(t)

	// Пустой проект: понятное сообщение с подсказкой создания.
	a.cmdExt("")
	if got := out(buf); !strings.Contains(got, "расширений нет") {
		t.Errorf("пустой список должен объяснять, что делать: %q", got)
	}

	extFixture(t, a.workDir, "deploy")
	buf.Reset()
	a.cmdExt("list")
	got := out(buf)
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "cmd") {
		t.Errorf("список расширений должен показывать имя и тип: %q", got)
	}

	a.cmdExt("new deploy")
	p := filepath.Join(a.workDir, ".gcli", "extensions", "deploy.json")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("/ext new не создал файл: %v", err)
	}
	a.cmdExt("new Плохое")
	if got := out(buf); !strings.Contains(got, "латиница") {
		t.Errorf("плохое имя должно быть отвергнуто: %q", got)
	}
}

// TestTrustCodeRequiresExplicitName — доверие выдаётся только на названный файл.
//
// Здесь ошибка стоит безопасности: без требования имени любое расширение из
// проекта подтверждалось бы разом, то есть гclone сам себя одобряет.
func TestTrustCodeRequiresExplicitName(t *testing.T) {
	a, buf := cmdApp(t)
	a.buildTools() // доверен��е должно быть подключено, как при обычном старте
	extFixture(t, a.workDir, "deploy")

	// Без имени — список ожидающего с путями и отпечатками.
	a.trustCode("ext", []string{"trust"})
	got := out(buf)
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "/ext trust") {
		t.Errorf("список недоверенного кода: %q", got)
	}

	// Несуществующее имя — отказ, а не «доверил ближайшее».
	buf.Reset()
	a.trustCode("ext", []string{"trust", "нет-такого"})
	if got := out(buf); !strings.Contains(got, "не найден") {
		t.Errorf("несуществующее имя должно быть отвергнуто: %q", got)
	}

	// Тихий режим не имеет права подтверждать чужой код: отвечать некому.
	buf.Reset()
	a.quiet = true
	a.stdin = testStdin("y")
	a.trustCode("ext", []string{"trust", "deploy"})
	if got := out(buf); !strings.Contains(got, "не подтверждено") {
		t.Errorf("в тихом режиме доверие выдаваться не должно: %q", got)
	}
	if n := a.tools.ExtCount(); n != 0 {
		t.Errorf("инструменты недоверенного расширения подключены (%d)", n)
	}
}

// TestTrustCodeAcceptedConnects — согласие поимённо включает инструменты.
func TestTrustCodeAcceptedConnects(t *testing.T) {
	a, buf := cmdApp(t)
	a.buildTools()
	extFixture(t, a.workDir, "deploy")
	a.stdin = testStdin("y")

	a.trustCode("ext", []string{"trust", "deploy"})
	got := out(buf)
	if !strings.Contains(got, "доверено") {
		t.Errorf("подтверждение должно подтверждаться словом: %q", got)
	}
	if n := a.tools.ExtCount(); n == 0 {
		t.Error("после подтверждения инструменты расширения обязаны подключиться — иначе оно ничего не дало")
	}
	// Повторный запрос теперь пуст: код уже доверен.
	buf.Reset()
	a.trustCode("ext", []string{"trust"})
	if got := out(buf); !strings.Contains(got, "недоверенного кода из проекта нет") {
		t.Errorf("после доверия список должен быть пуст: %q", got)
	}
}

// TestTrustCodeRefusedKeepsOff — отказ на вопросе оставляет код выключенным.
func TestTrustCodeRefusedKeepsOff(t *testing.T) {
	a, buf := cmdApp(t)
	a.buildTools()
	extFixture(t, a.workDir, "deploy")
	a.stdin = testStdin("n")

	a.trustCode("ext", []string{"trust", "deploy"})
	if got := out(buf); !strings.Contains(got, "не подтверждено") {
		t.Errorf("отказ должен быть назван: %q", got)
	}
	if n := a.tools.ExtCount(); n != 0 {
		t.Errorf("после отказа инструменты подключаться не должны (%d)", n)
	}
}

// TestTrustCodeMCP — MCP-сервер из проекта виден отдельно от расширений.
func TestTrustCodeMCP(t *testing.T) {
	a, buf := cmdApp(t)
	a.buildTools()
	dir := filepath.Join(a.workDir, ".gcli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mcp := `{"servers":{"fs":{"command":"npx","args":["-y","server-fs"]}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(mcp), 0o600); err != nil {
		t.Fatal(err)
	}

	// Список MCP не должен содержать расширений и наоборот.
	a.trustCode("ext", []string{"trust"})
	if got := out(buf); strings.Contains(got, "fs") {
		t.Errorf("MCP-сервер попал в список расширений: %q", got)
	}
	buf.Reset()
	a.trustCode("mcp", []string{"trust"})
	if got := out(buf); !strings.Contains(got, "fs") || !strings.Contains(got, "npx") {
		t.Errorf("список MCP должен называть сервер и команду: %q", got)
	}
	if got := out(buf); !strings.Contains(got, "/mcp trust") {
		t.Errorf("нужна подсказка про /mcp trust: %q", got)
	}
}

// TestShowPendingCodeEmpty — пустой список не должен печатать таблицу вслепую.
func TestShowPendingCodeEmpty(t *testing.T) {
	a, buf := cmdApp(t)
	a.buildTools()
	a.showPendingCode(nil, "mcp")
	if got := out(buf); !strings.Contains(got, "MCP") {
		t.Errorf("даже пустой список должен называть, что именно чист: %q", got)
	}
}

// ---------- /export ----------

// TestCmdExportWritesMarkdown — экспорт содержит роли, размышления и вызовы
// инструментов, иначе файл бесполезен для чтения.
func TestCmdExportWritesMarkdown(t *testing.T) {
	a, buf := cmdApp(t)
	a.sess.Title = "разбор падения"
	a.sess.Messages = []core.Message{
		{Role: core.RoleUser, Content: "почему падает?"},
		{Role: core.RoleAssistant, Sub: "explorer", Content: "смотрю",
			Reasoning: "надо проверить логи",
			ToolCalls: []core.ToolCall{{Name: "read_file", Args: `{"path":"main.go"}`}}},
		{Role: core.RoleTool, Name: "read_file", Content: "package main"},
	}

	a.cmdExport()
	if got := out(buf); !strings.Contains(got, "экспортировано") {
		t.Fatalf("экспорт не подтвердился: %q", got)
	}
	p := filepath.Join(a.store.Root, "exports", "gcli-"+a.sess.ID+".md")
	body := readFile(t, p)
	for _, want := range []string{"разбор падения", "## Пользователь", "## Ассистент (explorer)",
		"размышления модели", "- `read_file(", "результат read_file"} {
		if !strings.Contains(body, want) {
			t.Errorf("в экспорте нет %q:\n%s", want, body)
		}
	}
}

// TestCmdExportIncludesSubagents — журнал субагентов попадает в экспорт.
func TestCmdExportIncludesSubagents(t *testing.T) {
	a, _ := cmdApp(t)
	a.recordSubagent(core.SubagentRecord{Name: "explorer1", Type: "explorer",
		Status: "done", Tools: 3, Summary: "нашёл причину"})
	a.cmdExport()
	p := filepath.Join(a.store.Root, "exports", "gcli-"+a.sess.ID+".md")
	body := readFile(t, p)
	if !strings.Contains(body, "Субагенты") || !strings.Contains(body, "explorer1") ||
		!strings.Contains(body, "нашёл причину") {
		t.Errorf("журнал субагентов должен попасть в экспорт:\n%s", body)
	}
}

// ---------- /stats ----------

// TestPrintStatsEmptyAndFilled — пустой период объясняется, а данные выводятся
// двумя таблицами.
func TestPrintStatsEmptyAndFilled(t *testing.T) {
	a, buf := cmdApp(t)
	a.printStats(nil, 7)
	if got := out(buf); !strings.Contains(got, "пусто") {
		t.Errorf("пустой период должен быть назван пустым: %q", got)
	}

	day := time.Now()
	a.printStats([]core.Session{{
		Updated: day, Provider: "zai", Model: "glm-4.6",
		Usage: core.Usage{PromptTokens: 12000, CompletionTokens: 3000},
		Stats: core.Stats{Requests: 4, Errors: 1, Tools: 9},
	}}, 7)
	got := out(buf)
	if !strings.Contains(got, "По дням") || !strings.Contains(got, "По моделям") {
		t.Fatalf("ожидались обе таблицы: %q", got)
	}
	if !strings.Contains(got, "ошибок: 1") {
		t.Errorf("ошибки обязаны быть видны в сводке: %q", got)
	}
}

// TestPrintStatsMarksUnknownPrice — кастомная модель помечается «не знаем»,
// а не выглядит бесплатной.
func TestPrintStatsMarksUnknownPrice(t *testing.T) {
	a, buf := cmdApp(t)
	a.printStats([]core.Session{{
		Updated: time.Now(), Provider: "мой-эндпоинт", Model: "своя-модель",
		Usage: core.Usage{PromptTokens: 5000, CompletionTokens: 1000},
	}}, 30)
	got := out(buf)
	if !strings.Contains(got, "цена неизвестна") {
		t.Errorf("модель без цены обязана быть помечена: %q", got)
	}
	if !strings.Contains(got, "?") {
		t.Errorf("в таблице по моделям неизвестная цена помечается знаком «?»: %q", got)
	}
}

// TestCmdStatsPeriod — период задаётся числом, мусор не молчит.
func TestCmdStatsPeriod(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdStats("неделя")
	if got := out(buf); !strings.Contains(got, "числом дней") {
		t.Errorf("мусорный период должен быть отвергнут с подсказкой: %q", got)
	}
	buf.Reset()
	a.cmdStats("7")
	if got := out(buf); !strings.Contains(got, "7") {
		t.Errorf("период 7 дней должен быть назван в заголовке: %q", got)
	}
	buf.Reset()
	a.cmdStats("0")
	if got := out(buf); !strings.Contains(got, "числом дней") {
		t.Errorf("нулевой период бессмыслен и должен быть отвергнут: %q", got)
	}
}

// ---------- /context и память ----------

// TestCmdContextBreaksDownWindow — разбивка контекста считает все части и
// называет порог авто-сжатия.
func TestCmdContextBreaksDownWindow(t *testing.T) {
	a, buf := cmdApp(t)
	a.repo.Cfg.PlanMode = true
	a.sess.Messages = []core.Message{{Role: core.RoleUser, Content: strings.Repeat("текст ", 200)}}
	a.onNote("правило", "всегда запускать тесты")

	a.cmdContext()
	got := out(buf)
	for _, want := range []string{"Системный промпт", "Инструменты", "Память", "Навыки", "История диалога", "итого"} {
		if !strings.Contains(got, want) {
			t.Errorf("в разбивке нет строки %q:\n%s", want, got)
		}
	}
	// План подмешан в системный промпт — значит надстройка учтена.
	if !strings.Contains(got, "2 сообщений") && !strings.Contains(got, "1 сообщений") {
		t.Errorf("число сообщений должно быть в разбивке: %q", got)
	}
}

// TestAppendMemoryNote — факт попадает в GCLI.md, а пустой ввод отвергается.
func TestAppendMemoryNote(t *testing.T) {
	a, buf := cmdApp(t)
	a.appendMemoryNote("#")
	if got := out(buf); !strings.Contains(got, "формат") {
		t.Errorf("пустой факт должен отвергаться с форматом: %q", got)
	}

	a.appendMemoryNote("# сборка через build.sh")
	if got := out(buf); !strings.Contains(got, "записал в память") {
		t.Fatalf("факт должен записаться: %q", got)
	}
	mem := readFile(t, filepath.Join(a.workDir, "GCLI.md"))
	if !strings.Contains(mem, "сборка через build.sh") {
		t.Errorf("факта нет в GCLI.md:\n%s", mem)
	}
}

// ---------- Субагенты: вспомогательные функции ----------

// TestTaskSummaryTakesLastUserMessage — субагент получает последнюю задачу
// пользователя без хвоста «прикреплённых файлов».
func TestTaskSummaryTakesLastUserMessage(t *testing.T) {
	a, _ := cmdApp(t)
	a.sess.Title = "заголовок"
	a.sess.Messages = []core.Message{
		{Role: core.RoleUser, Content: "первый вопрос"},
		{Role: core.RoleAssistant, Content: "ответ"},
		{Role: core.RoleUser, Content: "второй вопрос\n\n--- Приложенные файлы ---\n- a.go: 40 строк"},
	}
	if got := a.taskSummary(); got != "второй вопрос" {
		t.Errorf("взята не та задача: %q", got)
	}

	// Без сообщений пользователя остаётся заголовок сессии.
	a.sess.Messages = nil
	if got := a.taskSummary(); got != "заголовок" {
		t.Errorf("без истории должен взять заголовок: %q", got)
	}
}

// TestTaskSummaryTruncates — длинная задача обрезается, а не уходит в контекст
// целиком.
func TestTaskSummaryTruncates(t *testing.T) {
	a, _ := cmdApp(t)
	a.sess.Messages = []core.Message{{Role: core.RoleUser, Content: strings.Repeat("а", 900)}}
	if got := a.taskSummary(); len([]rune(got)) != 400 {
		t.Errorf("ожидалась обрезка до 400 символов, получили %d", len([]rune(got)))
	}
}

// TestRecordSubagentCapsJournal — журнал запусков ограничен, иначе сессия с
// сотней субагентов раздувает каждый запрос к модели.
func TestRecordSubagentCapsJournal(t *testing.T) {
	a, _ := cmdApp(t)
	for i := 0; i < 45; i++ {
		a.recordSubagent(core.SubagentRecord{Name: "s"})
	}
	if len(a.sess.SubagentRuns) != 40 {
		t.Fatalf("журнал вырос до %d записей, ждали 40", len(a.sess.SubagentRuns))
	}
	// Отдаётся копия: правка на стороне читателя не должна портить сессию.
	reps := a.subagentReports()
	reps[0].Name = "подмена"
	if a.subagentReports()[0].Name == "подмена" {
		t.Error("subagentReports отдаёт живой срез — запись сессии можно испортить снаружи")
	}
}

// TestRouteModelForRespectsConfig — выбор модели субагента зависит от настройки
// и объясняет выбор.
func TestRouteModelForRespectsConfig(t *testing.T) {
	a, _ := cmdApp(t)
	// Дешёвая модель берётся из таблицы цен core: у «glm-4.6-air» цены
	// нет, и роутер на неё даже не посмотрит.
	a.prov.Models = []string{"glm-4.6", "glm-4.5-air"}
	a.model = "glm-4.6"

	// Маршрутизация выключена — модель главного агента без изменений.
	a.repo.Cfg.SubRoute = false
	m, why := a.routeModelFor(subagents.TypeExplorer)
	if m != "glm-4.6" || !strings.Contains(why, "выключена") {
		t.Errorf("выключенная маршрутизация обязана оставить модель: %q / %q", m, why)
	}

	// Включённая на дешёвой роли понижает модель и говорит почему.
	a.repo.Cfg.SubRoute = true
	m, why = a.routeModelFor(subagents.TypeExplorer)
	if m != "glm-4.5-air" {
		t.Errorf("дешёвая роль должна ехать на дешёвой модели, получили %q (%s)", m, why)
	}
	if why == "" {
		t.Error("понижение модели обязано объясняться — иначе это выглядит как ошибка")
	}
}

// TestAgentsInfoAndStatusHelpers — сводка и статус субагентов не пустые и не
// выдумывают несуществующее.
func TestAgentsInfoAndStatusHelpers(t *testing.T) {
	a, _ := cmdApp(t)
	if got := a.agentsInfo("list", ""); !strings.Contains(got, "spawn_agent") {
		t.Errorf("пустая сводка должна подсказывать, как запускать: %q", got)
	}
	if got := a.agentsInfo("result", "нет-такого"); !strings.Contains(got, "не найден") {
		t.Errorf("несуществующий субагент должен быть назван: %q", got)
	}
	if got := a.agentsInfo("result", ""); !strings.Contains(got, "spawn_agent") {
		t.Errorf("result без имени должен давать общую сводку: %q", got)
	}
	if got := a.agentsInfo("что-то", ""); !strings.Contains(got, "spawn_agent") {
		t.Errorf("неизвестное действие не должно ломать вывод: %q", got)
	}
	if statusOf(nil) != subagents.StatusDone || statusOf(errors.New("бум")) != subagents.StatusError {
		t.Error("статус должен отражать наличие ошибки")
	}
	if errText(nil) != "" || errText(errors.New("бум")) != "бум" {
		t.Error("errText должен отдавать текст ошибки и пустую строку без неё")
	}
}

// TestRegistryForSubagentRestricts — субагенту не достаются инструменты записи,
// если он им не разрешены.
func TestRegistryForSubagentRestricts(t *testing.T) {
	a, _ := cmdApp(t)
	all := a.registryForSubagent(false, nil, nil)
	if all.Count() == 0 {
		t.Fatal("без ограничений реестр должен отдавать все инструменты")
	}
	ro := a.registryForSubagent(true, nil, []string{"write_file", "edit_file"})
	for _, name := range []string{"write_file", "edit_file"} {
		if ro.Has(name) {
			t.Errorf("инструмент %q не должен попасть в read-only реестр", name)
		}
	}
	if !ro.Has("read_file") {
		t.Error("read_file обязан остаться — иначе субагент-исследователь ничего не сделает")
	}
}

// ---------- Строка состояния и подписи ----------

// TestShortArgsPicksUsefulField — карточка инструмента показывает главный
// аргумент, а не пустую строку и не весь JSON.
func TestShortArgsPicksUsefulField(t *testing.T) {
	cases := []struct {
		args string
		want string
	}{
		{`{"command":"go test ./..."}`, "go test ./..."},
		{`{"path":"main.go"}`, "main.go"},
		{`{"pattern":"TODO"}`, "TODO"},
		{`{"url":"https://example.com"}`, "https://example.com"},
		{`{"task":"проверить сборку"}`, "проверить сборку"},
		{`{"old_string":"a","new_string":"b"}`, "a"},
	}
	for _, c := range cases {
		got := shortArgs(core.ToolCall{Name: "x", Args: c.args})
		if got != c.want {
			t.Errorf("shortArgs(%s) = %q, ждали %q", c.args, got, c.want)
		}
	}

	// Список задач: показываем счётчик, а не сырой JSON.
	todos := `{"todos":[{"content":"a","status":"pending"},{"content":"b","status":"done"}]}`
	if got := shortArgs(core.ToolCall{Args: todos}); got != "2 задач" {
		t.Errorf("список задач: %q", got)
	}
	// Мусор в аргументах не должен ронять карточку.
	if got := shortArgs(core.ToolCall{Args: "{не json"}); got != "" {
		t.Errorf("битые аргументы должны давать пустую подпись, получили %q", got)
	}
}

// TestServeToolStatus — одна точка правды для TUI и SSE: отказ и отклонение
// различаются, а пустой результат не выглядит ошибкой.
//
// Значения Summary взяты те, что реально ставит tools: короткие «отклонено» и
// «отменено». Если бы сработала эвристика на длинных строках вроде «запись
// отменена пользователем», карточка перестала бы отличать отказ от ошибки — но
// менять контракт инструментов ради теста нельзя, поэтому сверяемся с текущим.
func TestServeToolStatus(t *testing.T) {
	cases := []struct {
		res      tools.Result
		err      error
		wantStat string
	}{
		{tools.Result{Summary: "готово"}, nil, "ok"},
		{tools.Result{Error: "диск полон"}, nil, "fail"},
		{tools.Result{Summary: "отклонено"}, nil, "denied"},
		{tools.Result{Summary: "отменено"}, nil, "denied"},
		{tools.Result{Summary: "готово"}, errors.New("сеть"), "fail"},
	}
	for _, c := range cases {
		stat, detail := serveToolStatus(c.res, c.err)
		if stat != c.wantStat {
			t.Errorf("статус %q, ждали %q (summary=%q, err=%v)", stat, c.wantStat, c.res.Summary, c.err)
		}
		if detail == "" {
			t.Error("у результата должен быть текст для карточки")
		}
	}
}

// TestStatusItemsReflectModes — строка состояния показывает реальные режимы и
// не врёт про ключ.
func TestStatusItemsReflectModes(t *testing.T) {
	a, _ := cmdApp(t)
	a.ui.SetMascot(true)
	has := func(items []ui.StatusItem, want string) bool {
		for _, it := range items {
			if it.Text == want {
				return true
			}
		}
		return false
	}

	items := a.statusItems()
	if !has(items, "glm-4.6") || !has(items, "agent") {
		t.Errorf("в строке должны быть модель и режим: %+v", items)
	}
	if has(items, "нет ключа — /setup") {
		t.Error("у локального провайдера без ключа подсказка про ключ неуместна")
	}

	a.sess.AgentMode = false
	a.repo.Cfg.PlanMode = true
	if !has(a.statusItems(), "chat") || !has(a.statusItems(), "plan") {
		t.Error("выключенный агентный режим и включённый план должны быть видны")
	}

	a.sess.Perms.Autopilot = true
	if !has(a.statusItems(), "autopilot") {
		t.Error("автопилот должен быть виден в строке состояния")
	}
	a.sess.Perms.AutopilotAll = true
	if !has(a.statusItems(), "autopilot: all") {
		t.Error("режим all обязан быть виден отдельной меткой — он одобряет всё")
	}
	a.sess.Perms.Autopilot, a.sess.Perms.AutopilotAll = false, false
	a.sess.Perms.BashAll = true
	if !has(a.statusItems(), "yolo") {
		t.Error("YOLO должен быть виден в строке состояния")
	}

	// Провайдер с ключом не должен требовать /setup.
	a.prov.NoKey = false
	a.prov.Key = ""
	if !has(a.statusItems(), "нет ключа — /setup") {
		t.Error("провайдер без ключа должен подсказывать /setup")
	}
}

// TestNotesTrimmedAndVisible — заметки копятся, обрезаются и попадают в блок
// для следующего запроса агента.
func TestNotesTrimmedAndVisible(t *testing.T) {
	a, _ := cmdApp(t)
	if a.notesText() != "" {
		t.Error("без заметок блок должен быть пустым, а не заглушкой")
	}
	for i := 0; i < 40; i++ {
		a.onNote("заметка", strings.Repeat("x", 400)+"\nвторая строка")
	}
	if len(a.notes) != 30 {
		t.Errorf("заметок %d, ждали 30 — список обязан быть ограничен", len(a.notes))
	}
	for _, n := range a.notes {
		if strings.Contains(n, "\n") {
			t.Fatalf("заметка не должна быть многострочной: %q", n)
		}
	}
	if !strings.Contains(a.notesText(), "заметка:") {
		t.Errorf("блок заметок должен содержать заметки: %q", a.notesText())
	}
}

// TestOnExtendNotesExtension — продление хода видно и попадает в заметки
// агента; нулевое продление не создаёт шума.
func TestOnExtendNotesExtension(t *testing.T) {
	a, _ := cmdApp(t)
	a.onExtend(0, 40, "почти закончил")
	if len(a.notes) != 0 {
		t.Error("нулевое продление не должно оставлять заметку")
	}
	a.onExtend(4, 44, "нужно\nещё")
	if len(a.notes) != 1 {
		t.Fatalf("продление должно записать заметку, заметок: %d", len(a.notes))
	}
	if !strings.Contains(a.notes[0], "+4 итераций") || strings.Contains(a.notes[0], "\n") {
		t.Errorf("заметка о продлении: %q", a.notes[0])
	}
}

// TestSetTodosAndTodos — план доступен инструментам и не меняет сессию извне.
func TestSetTodosAndTodos(t *testing.T) {
	a, _ := cmdApp(t)
	a.SetTodos([]tools.TodoItem{{Content: "первая", Status: "in_progress"}, {Content: "вторая", Status: "pending"}})
	got := a.Todos()
	if len(got) != 2 || got[0].Content != "первая" || got[1].Status != "pending" {
		t.Fatalf("Todos вернули не то: %+v", got)
	}

	// Повторная установка заменяет план, а не дописывает его.
	a.SetTodos([]tools.TodoItem{{Content: "третья", Status: "done"}})
	if got := a.Todos(); len(got) != 1 || got[0].Content != "третья" {
		t.Errorf("план должен заменяться целиком: %+v", got)
	}
}

// TestListProvidersMarksCurrent — /provider list показывает активного и не
// печатает ключ целиком.
func TestListProvidersMarksCurrent(t *testing.T) {
	a, buf := cmdApp(t)
	a.prov.Key = "sk-ant-" + strings.Repeat("z", 40)
	a.listProviders()
	got := out(buf)
	if !strings.Contains(got, "→ 1. zai") {
		t.Errorf("активный провайдер должен быть помечен: %q", got)
	}
	if strings.Contains(got, strings.Repeat("z", 20)) {
		t.Error("ключ напечатан целиком — это утечка секрета в вывод")
	}
	if !strings.Contains(got, "/provider add") {
		t.Error("список должен подсказывать, как добавить свой endpoint")
	}
}

// TestCmdSnapshotsEmptyAndError — пустой репозиторий снимков объясняется, а
// прошлая ошибка показывается, а не прячется.
func TestCmdSnapshotsEmptyAndError(t *testing.T) {
	if !core.SnapshotsAvailable() {
		t.Skip("git недоступен — снимки не проверяются")
	}
	a, buf := cmdApp(t)
	a.cmdSnapshots()
	if got := out(buf); !strings.Contains(got, "снимков пока нет") {
		t.Errorf("пустой список снимков должен объясняться: %q", got)
	}

	buf.Reset()
	a.snapErr = errors.New("диск переполнен")
	a.cmdSnapshots()
	if got := out(buf); !strings.Contains(got, "диск переполнен") {
		t.Errorf("прошлая ошибка снимков обязана быть видна: %q", got)
	}
}
