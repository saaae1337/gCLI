package main

// Тесты роутера slash-команд и команд, печатающих состояние.
//
// handleCommand — точка входа для всего, что человек набирает руками, и до
// этого не был покрыт ни одним тестом. Ровно здесь ошибка стоит дороже всего:
// команда, которую роутер не узнал, не падает — она молча ничего не делает,
// и человек решает, что «gcli сломался», вместо того чтобы понять, что
// опечатался. Поэтому проверяем и маршрутизацию, и то, что команда реально
// изменила: конфиг, сессию, модель.
//
// Файл продолжает app_commands_test.go: там чистые функции разбора ввода,
// здесь — решения роутера и команды-команды.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gcli/core"
	"gcli/providers"
	"gcli/subagents"
	"gcli/tools"
)

// cmdApp — приложение, готовое к слэш-командам.
//
// Изоляция обязательна: команды пишут config.json и сессии в GCLI_HOME, а
// /memory и /snapshots читают рабочий каталог. Без отдельных каталогов тесты
// трогали бы настройки пользователя и зависели бы от его файлов.
func cmdApp(t *testing.T) (*app, *strings.Builder) {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	t.Setenv("GCLI_SANDBOX", "")

	buf := &strings.Builder{}
	store := core.NewStore()
	store.Ensure()
	cfg := core.DefaultConfig()
	a := &app{
		repo:    &core.Repo{Store: store, Cfg: cfg},
		store:   store,
		ui:      testUI(buf),
		workDir: work,
		prov:    &providers.Provider{ID: "zai", Label: "Z.ai", NoKey: true, Models: []string{"glm-4.6", "glm-4.6-air"}},
		model:   "glm-4.6",
		tools:   tools.New(tools.Env{WorkDir: work}),
		memory:  tools.NewMemory(work, store),
		pool:    subagents.NewPool(nil, subagents.PoolOptions{MaxParallel: 3, MaxDepth: 1, Enabled: true, WorkDir: work}),
		sess:    &core.Session{ID: "s1", AgentMode: true, Provider: "zai", Model: "glm-4.6"},
	}
	a.registry = providers.Build(cfg)
	a.rules = a.setupRules()
	return a, buf
}

// testStdin — stdinReader с одной готовой строкой.
//
// newStdin() читает настоящий os.Stdin, поэтому в тестах он вешает прогон:
// команда ждала бы ввода до конца. Собираем структуру руками — поля
// приватные, но тест живёт в том же пакете.
func testStdin(line string) *stdinReader {
	s := &stdinReader{lines: make(chan string, 1), keys: make(chan byte, 1)}
	s.lines <- line
	return s
}

// TestHandleCommandQuit — все три имени выхода и все дают false.
//
// Роутер возвращает false только здесь; если /quit «разъедется» с /exit,
// REPL продолжит читать ввод после команды выхода — человек увидит
// приложение, из которого уже не выйти.
func TestHandleCommandQuit(t *testing.T) {
	a, _ := cmdApp(t)
	for _, line := range []string{"/quit", "/exit", "/q", "/QUIT"} {
		if a.handleCommand(line) {
			t.Errorf("%q должен завершать сеанс, а не продолжать его", line)
		}
	}
}

// TestHandleCommandKnownRoutes — каждая команда доходит до своей обработки, а
// не падает в «неизвестная».
//
// Команда, ушедшая в default, не сообщает ни об ошибке, ни о том, что её
// имя неверно, — человек теряет фичу молча.
func TestHandleCommandKnownRoutes(t *testing.T) {
	a, _ := cmdApp(t)
	for _, line := range []string{
		"/help", "/h", "/?", "/status", "/usage", "/tools", "/sessions",
		"/memory", "/yolo", "/autopilot status", "/sandbox status",
	} {
		buf := &strings.Builder{}
		a.ui = testUI(buf)
		if !a.handleCommand(line) {
			t.Errorf("%q прервал сеанс, а не выполнился", line)
		}
		got := out(buf)
		if strings.Contains(got, "неизвестная команда") {
			t.Errorf("%q попала в «неизвестная команда»", line)
		}
		if got == "" {
			t.Errorf("%q ничего не напечатала", line)
		}
	}
}

// TestHandleCommandUnknownWarns — неизвестная команда говорит об этом и
// показывает, где искать список.
func TestHandleCommandUnknownWarns(t *testing.T) {
	a, buf := cmdApp(t)
	if !a.handleCommand("/nosuchcommand") {
		t.Error("неизвестная команда не должна завершать сеанс")
	}
	got := out(buf)
	if !strings.Contains(got, "неизвестная команда") || !strings.Contains(got, "/help") {
		t.Errorf("нужно сказать про неизвестную команду и подсказать /help: %q", got)
	}
}

// TestHandleCommandCustomFallback — кастомная команда из .gcli/commands
// опознаётся роутером.
//
// Пустой файл выбран намеренно: runCustomCommand на пустом промпте выдаёт
// предупреждение и НЕ запускает ход, поэтому тест остаётся проверкой
// маршрутизации, а не сетевого вызова к провайдеру.
func TestHandleCommandCustomFallback(t *testing.T) {
	a, buf := cmdApp(t)
	dir := filepath.Join(a.workDir, ".gcli", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "commit.md"), []byte("---\ndesc: коммит\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.handleCommand("/commit") {
		t.Error("кастомная команда не должна завершать сеанс")
	}
	got := out(buf)
	if strings.Contains(got, "неизвестная команда") {
		t.Errorf("кастомная команда попала в «неизвестная»: %q", got)
	}
	if !strings.Contains(got, "пуста") {
		t.Errorf("пустой промпт должен быть назван пустым: %q", got)
	}
}

// TestAutopilotOnOffAll — режимы автопилота и то, что опасные команды всё
// равно спрашивают.
//
// Проверяем не только переключение, но и предупреждение: режим all снимает
// вопросы со всего, включая разрушительное, и человек должен видеть риск
// прямо в выводе, а не вспоминать его потом, когда rm -rf уже отработал.
func TestAutopilotOnOffAll(t *testing.T) {
	a, buf := cmdApp(t)

	a.cmdAutopilot("on")
	if !a.sess.Perms.Autopilot || a.sess.Perms.AutopilotAll {
		t.Error("/autopilot on ждали Autopilot=true, AutopilotAll=false")
	}
	if !strings.Contains(out(buf), "опасные") {
		t.Errorf("on должен предупреждать про опасные команды: %q", out(buf))
	}

	buf.Reset()
	a.cmdAutopilot("all")
	if !a.sess.Perms.AutopilotAll {
		t.Error("/autopilot all должен включать AutopilotAll")
	}
	if !strings.Contains(strings.ToLower(out(buf)), "всё") {
		t.Errorf("all должен прямо сказать, что одобряет всё: %q", out(buf))
	}

	// off обязан погасить и all: иначе после «выключил автопилот» всё
	// продолжает одобряться без вопроса.
	a.cmdAutopilot("off")
	if a.sess.Perms.Autopilot || a.sess.Perms.AutopilotAll {
		t.Errorf("/autopilot off оставил автопилот включённым: %+v", a.sess.Perms)
	}
	if cfg := savedConfig(t, a); cfg.Autopilot || cfg.AutopilotAll {
		t.Errorf("off должен уйти и в конфиг: %+v", cfg)
	}
}

// TestAutopilotToggleNeverTurnsOnAll — переключатель без аргумента не имеет
// права включать all.
func TestAutopilotToggleNeverTurnsOnAll(t *testing.T) {
	a, _ := cmdApp(t)
	for i := 0; i < 4; i++ {
		a.cmdAutopilot("")
		if a.sess.Perms.AutopilotAll {
			t.Fatal("переключение без аргумента включило режим all — рискованный режим должен требовать явного ввода")
		}
	}
	// Четыре переключения из off: on → off → on → off.
	if a.sess.Perms.Autopilot {
		t.Error("после чётного числа переключений из off ждали off, а получили on")
	}
}

// TestAutopilotStatusNamesMode — /autopilot status называет текущий режим и
// говорит, как его сменить.
func TestAutopilotStatusNamesMode(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdAutopilot("status")
	if got := out(buf); !strings.Contains(got, "off") || !strings.Contains(got, "/autopilot on") {
		t.Errorf("статус должен называть режим и давать подсказку: %q", got)
	}
}

// TestAutopilotRejectsGarbage — негодный аргумент не меняет режим молча.
func TestAutopilotRejectsGarbage(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdAutopilot("возможно")
	got := out(buf)
	if !strings.Contains(got, "формат") {
		t.Errorf("нужен отказ с подсказкой формата: %q", got)
	}
	if a.sess.Perms.Autopilot || a.sess.Perms.AutopilotAll {
		t.Error("негодный аргумент не должен включать автопилот")
	}
}

// TestYoloToggle — YOLO включает и выключает подтверждения.
func TestYoloToggle(t *testing.T) {
	a, _ := cmdApp(t)
	a.cmdYolo()
	if !a.sess.Perms.BashAll || !a.sess.Perms.FileWrite || !a.sess.Perms.WebFetch {
		t.Errorf("YOLO должен снять подтверждения везде: %+v", a.sess.Perms)
	}
	a.cmdYolo()
	if a.sess.Perms.BashAll {
		t.Error("повторный YOLO обязан вернуть подтверждения")
	}
}

// TestAgentModePersists — /agent переключает режим и записывает его в конфиг.
func TestAgentModePersists(t *testing.T) {
	a, _ := cmdApp(t)
	a.cmdAgent("off")
	if a.sess.AgentMode || savedConfig(t, a).Agent {
		t.Error("/agent off должен выключить агентный режим и в сессии, и в конфиге")
	}
	// Без аргумента — переключение, а не включение «на всякий случай».
	a.cmdAgent("")
	if !a.sess.AgentMode {
		t.Error("пустой аргумент должен переключать режим, а не отключать его")
	}
}

// TestThinkStates — /think on|off|auto пишет состояние в конфиг, мусор не
// меняет ничего.
func TestThinkStates(t *testing.T) {
	a, _ := cmdApp(t)
	for _, want := range []string{"on", "off", "auto"} {
		a.cmdThink(want)
		if got := a.repo.Cfg.Think; got != want {
			t.Errorf("/think %s дал %q", want, got)
		}
		if got := savedConfig(t, a).Think; got != want {
			t.Errorf("/think %s не дошёл до конфига: %q", want, got)
		}
	}
	// Русские синонимы и пустой аргумент обязаны работать: /help их рекламирует.
	a.cmdThink("вкл")
	if a.repo.Cfg.Think != "on" {
		t.Errorf("«вкл» не понято: %q", a.repo.Cfg.Think)
	}
	buf := &strings.Builder{}
	a.ui = testUI(buf)
	a.cmdThink("возможно")
	if a.repo.Cfg.Think != "on" {
		t.Error("негодный аргумент изменил /think")
	}
	if !strings.Contains(out(buf), "формат") {
		t.Errorf("нужен отказ с форматом: %q", out(buf))
	}
}

// TestProviderAddKeyModelRemove — полный цикл своего провайдера.
func TestProviderAddKeyModelRemove(t *testing.T) {
	a, _ := cmdApp(t)

	a.cmdProvider("add myapi https://api.myservice.ru/v1/")
	pc := a.repo.Cfg.Providers["myapi"]
	if pc == nil {
		t.Fatal("/provider add не создал провайдера")
	}
	if pc.BaseURL != "https://api.myservice.ru/v1" {
		t.Errorf("кончающийся слеш не срезан: %q", pc.BaseURL)
	}
	if !pc.Custom {
		t.Error("свой провайдер должен быть помечен Custom, иначе его нельзя удалить")
	}
	if a.registry.Find("myapi") == nil {
		t.Error("после add провайдер должен появиться в реестре")
	}

	a.cmdProvider("key myapi sk-secret-value-1234")
	if got := a.repo.Cfg.Providers["myapi"].APIKey; got != "sk-secret-value-1234" {
		t.Errorf("ключ не сохранён: %q", got)
	}

	a.cmdProvider("model myapi my-model")
	if got := a.repo.Cfg.Providers["myapi"].Model; got != "my-model" {
		t.Errorf("модель провайдера не сохранена: %q", got)
	}

	a.cmdProvider("rm myapi")
	if _, ok := a.repo.Cfg.Providers["myapi"]; ok {
		t.Error("/provider rm не удалил провайдера")
	}
}

// TestProviderRejectsBadInput — мусорный провайдер не попадает в конфиг.
func TestProviderRejectsBadInput(t *testing.T) {
	cases := []struct{ line, want string }{
		{"add", "формат"},
		// id проверяется раньше base_url, поэтому здесь id короче двух
		// символов — намеренно, чтобы дойти до проверки http.
		{"add ab b", "http"},
		{"add xy ftp://h/v1", "http"},
		{"add 12345 https://h/v1", "id"},
		{"add good https://h/v1 grpc", "протокол"},
		{"key myapi", "формат"},
		{"key myapi \"\"", "пустой"},
		{"rm openai", "пресеты"},
		{"rm nosuch", "не найден"},
	}
	for _, c := range cases {
		a, buf := cmdApp(t)
		a.cmdProvider(c.line)
		if got := out(buf); !strings.Contains(strings.ToLower(got), strings.ToLower(c.want)) {
			t.Errorf("/provider %s ждали сообщение про %q, получили: %q", c.line, c.want, got)
		}
		if len(a.repo.Cfg.Providers) != 0 {
			t.Errorf("/provider %s всё же что-то записал: %+v", c.line, a.repo.Cfg.Providers)
		}
	}
}

// TestSwitchProvider — при смене провайдера модель, которую этот провайдер
// знает, сохраняется; незнакомая заменяется дефолтной.
func TestSwitchProvider(t *testing.T) {
	a, _ := cmdApp(t)
	p := a.registry.Find("openai")
	if p == nil {
		t.Skip("пресета openai нет в реестре")
	}
	a.model = p.Models[0]
	a.switchProvider(p)
	if a.model != p.Models[0] {
		t.Errorf("модель, доступная у нового провайдера, должна сохраниться: %q", a.model)
	}
	if a.prov.ID != p.ID || a.repo.Cfg.Provider != p.ID || a.sess.Provider != p.ID {
		t.Errorf("провайдер не переключился во всех трёх местах: app=%q cfg=%q sess=%q",
			a.prov.ID, a.repo.Cfg.Provider, a.sess.Provider)
	}

	a.model = "модель-которой-нет"
	a.switchProvider(p)
	if a.model != p.DefaultModel {
		t.Errorf("незнакомая модель должна падать на дефолтную: %q", a.model)
	}
}

// TestSetModelPersists — смена модели доходит до конфига и сессии.
func TestSetModelPersists(t *testing.T) {
	a, _ := cmdApp(t)
	a.setModel("glm-4.6-air")
	if a.model != "glm-4.6-air" || a.repo.Cfg.Model != "glm-4.6-air" || a.sess.Model != "glm-4.6-air" {
		t.Errorf("модель не разошлась по трём местам: %q/%q/%q", a.model, a.repo.Cfg.Model, a.sess.Model)
	}
	if savedConfig(t, a).Model != "glm-4.6-air" {
		t.Error("модель не записана в config.json")
	}
}

// TestCmdModelFindsProviderByModel — /model <имя> сам находит провайдера по
// модели: не только переключает модель, но и уводит на того, кто её умеет.
func TestCmdModelFindsProviderByModel(t *testing.T) {
	a, _ := cmdApp(t)
	p := a.registry.Find("ollama")
	if p == nil || len(p.Models) == 0 {
		t.Skip("пресета ollama нет или у него нет моделей")
	}
	want := p.Models[0]
	a.cmdModel(want)
	if a.model != want {
		t.Errorf("модель не установлена: %q", a.model)
	}
	if a.prov.ID != p.ID {
		t.Errorf("провайдер должен переключиться на владельца модели: %q вместо %q", a.prov.ID, p.ID)
	}
}

// TestClearStartsNewSession — /clear закрывает старую сессию и открывает новую,
// старая остаётся в списке.
func TestClearStartsNewSession(t *testing.T) {
	a, _ := cmdApp(t)
	a.sess.Messages = append(a.sess.Messages, core.Message{Role: core.RoleUser, Content: "привет"})
	old := a.sess.ID
	a.cmdClear()
	if a.sess.ID == old {
		t.Fatal("/clear оставил ту же сессию")
	}
	if len(a.sess.Messages) != 0 {
		t.Errorf("новая сессия должна быть пустой: %d сообщений", len(a.sess.Messages))
	}
	found := false
	for _, s := range a.repo.ListSessions() {
		if s.ID == old {
			found = true
		}
	}
	if !found {
		t.Error("старая сессия должна сохраниться — иначе /clear крадёт историю")
	}
}

// TestSessionsAndResume — список показывает текущую, а /resume <N> грузит
// выбранную вместе с её провайдером и моделью.
func TestSessionsAndResume(t *testing.T) {
	a, buf := cmdApp(t)
	// Текущая сессия обязана лежать на диске: /sessions читает каталог, а не
	// память, и незаписанная сессия в списке просто отсутствует.
	if err := a.repo.SaveSession(a.sess); err != nil {
		t.Fatal(err)
	}
	other := a.repo.NewSession("openai", "gpt-4o", a.workDir)
	other.Messages = append(other.Messages, core.Message{Role: core.RoleUser, Content: "прошлая работа"})
	other.Title = "прошлая работа"
	if err := a.repo.SaveSession(other); err != nil {
		t.Fatal(err)
	}

	a.cmdSessions()
	if got := out(buf); !strings.Contains(got, "текущая") {
		t.Errorf("текущая сессия должна быть помечена: %q", got)
	}

	// Номер берём из того же списка, каким пользуется команда.
	ss := a.repo.ListSessions()
	idx := 0
	for i, s := range ss {
		if s.ID == other.ID {
			idx = i + 1
		}
	}
	if idx == 0 {
		t.Fatal("в списке нет второй сессии")
	}
	buf.Reset()
	a.cmdResume(strconv.Itoa(idx))
	if a.sess.ID != other.ID {
		t.Errorf("загрузилась не та сессия: %q вместо %q", a.sess.ID, other.ID)
	}
	if len(a.sess.Messages) == 0 {
		t.Error("сообщения прошлой сессии потеряны при resume")
	}
	if a.sess.Model != "gpt-4o" {
		t.Errorf("модель из сессии не восстановлена: %q", a.sess.Model)
	}
	if a.sess.Perms.BashExact == nil {
		t.Error("карта точных разрешений должна быть не nil, иначе первый же ход падает на записи в nil-карту")
	}
}

// TestResumeRejectsBadNumber — ответ «не номер» отвергается, а не грузится
// «случайная» сессия.
func TestResumeRejectsBadNumber(t *testing.T) {
	a, buf := cmdApp(t)
	if err := a.repo.SaveSession(a.repo.NewSession("zai", "glm-4.6", a.workDir)); err != nil {
		t.Fatal(err)
	}
	before := a.sess.ID
	// Номер вне диапазона уводит команду в вопрос через stdin.
	a.stdin = testStdin("не номер")
	a.cmdResume("999")
	if !strings.Contains(out(buf), "неверный номер") {
		t.Errorf("мусорный ответ должен быть отвергнут: %q", out(buf))
	}
	if a.sess.ID != before {
		t.Error("неверный номер сменил сессию")
	}
}

// TestMemoryEmptySaysHowToCreate — при пустой памяти /memory подсказывает /init.
func TestMemoryEmptySaysHowToCreate(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdMemory()
	if got := out(buf); !strings.Contains(got, "/init") {
		t.Errorf("нужно сказать, как создать память: %q", got)
	}
}

// TestMemoryListsProjectFile — файл памяти показан относительно проекта.
func TestMemoryListsProjectFile(t *testing.T) {
	a, buf := cmdApp(t)
	if err := os.WriteFile(filepath.Join(a.workDir, "GCLI.md"), []byte("# проект\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cmdMemory()
	got := out(buf)
	if !strings.Contains(got, "GCLI.md") {
		t.Errorf("файл памяти не показан: %q", got)
	}
	if strings.Contains(got, a.workDir) {
		t.Errorf("путь должен быть относительным проекта, а не абсолютным: %q", got)
	}
}

// TestUndoWithoutCheckpoints — без чекпоинтов /undo говорит об этом, а не
// молчит и не падает.
func TestUndoWithoutCheckpoints(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdUndo()
	if got := out(buf); !strings.Contains(got, "нечего отменять") {
		t.Errorf("нужно сказать, что отменять нечего: %q", got)
	}
}

// TestUndoRemovesCreatedFile — откат удаляет файл, созданный агентом.
func TestUndoRemovesCreatedFile(t *testing.T) {
	a, buf := cmdApp(t)
	p := filepath.Join(a.workDir, "новый.txt")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sess.Checkpoints = append(a.sess.Checkpoints, core.CheckpointMeta{Path: p, Existed: false})
	a.cmdUndo()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("созданный агентом файл должен исчезнуть после undo: %v", err)
	}
	if got := out(buf); !strings.Contains(got, "удалён") {
		t.Errorf("нужно сказать, что файл удалён: %q", got)
	}
}

// TestUndoRestoresChangedFile — откат возвращает прежнее содержимое файла.
func TestUndoRestoresChangedFile(t *testing.T) {
	a, _ := cmdApp(t)
	p := filepath.Join(a.workDir, "конфиг.txt")
	backup := filepath.Join(a.workDir, ".gcli-undo-backup")
	if err := os.WriteFile(backup, []byte("старое"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("новое"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.sess.Checkpoints = append(a.sess.Checkpoints, core.CheckpointMeta{Path: p, Existed: true, Backup: backup})
	a.cmdUndo()
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "старое" {
		t.Errorf("undo не вернул прежнее содержимое: %q, %v", data, err)
	}
}

// TestRevertRejectsBadInput — /revert без номера и с несуществующим номером
// отвечают подсказкой и не трогают проект.
func TestRevertRejectsBadInput(t *testing.T) {
	a, buf := cmdApp(t)
	if !core.SnapshotsAvailable() {
		t.Skip("git недоступен")
	}
	a.cmdRevert("не число")
	if !strings.Contains(out(buf), "/snapshots") {
		t.Errorf("нужно сказать, где взять номер: %q", out(buf))
	}
	buf.Reset()
	a.cmdRevert("9999")
	if got := out(buf); !strings.Contains(got, "/snapshots") {
		t.Errorf("несуществующий номер должен быть назван: %q", got)
	}
}

// TestCopyEmptyHistory — /copy без ответов говорит об этом, а не шлёт пустую
// последовательность OSC52 в терминал.
func TestCopyEmptyHistory(t *testing.T) {
	a, buf := cmdApp(t)
	a.cmdCopy()
	got := out(buf)
	if !strings.Contains(got, "нечего копировать") {
		t.Errorf("нужно сказать, что копировать нечего: %q", got)
	}
	if strings.Contains(got, "]52;") {
		t.Error("OSC52 отправлен при пустой истории")
	}
}

// TestCopyUsesLastAssistantAnswer — копируется последний содержательный ответ,
// а не последнее сообщение вообще.
func TestCopyUsesLastAssistantAnswer(t *testing.T) {
	a, buf := cmdApp(t)
	a.sess.Messages = append(a.sess.Messages,
		core.Message{Role: core.RoleAssistant, Content: "старый ответ"},
		core.Message{Role: core.RoleUser, Content: "а покажи новое"},
		core.Message{Role: core.RoleAssistant, Content: "  "}, // пустой — пропускаем
		core.Message{Role: core.RoleAssistant, Content: "новый ответ"},
	)
	a.cmdCopy()
	got := out(buf)
	if !strings.Contains(got, "OSC52") {
		t.Errorf("ожидался OSC52: %q", got)
	}
	if strings.Contains(got, "старый ответ") {
		t.Error("скопирован не тот ответ — берётся последний assistant с текстом")
	}
}
