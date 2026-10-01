package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gcli/core"
	"gcli/tools"
)

// permsApp — приложение с правилами из заданных строк.
//
// GCLI_HOME обязателен: cmdPerms init пишет gcli.json в рабочий каталог,
// а чтение глобального конфига — из хранилища. Без изоляции тесты
// трогали бы настоящие настройки пользователя.
func permsApp(t *testing.T, ruleLines ...string) (*app, *strings.Builder) {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	t.Setenv("GCLI_SANDBOX", "")

	if len(ruleLines) > 0 {
		writeProjectRules(t, work, ruleLines)
	}
	buf := &strings.Builder{}
	store := core.NewStore()
	store.Ensure()
	a := &app{
		repo:    &core.Repo{Store: store, Cfg: core.DefaultConfig()},
		store:   store,
		ui:      testUI(buf),
		workDir: work,
		sess:    &core.Session{ID: "s1", AgentMode: true},
		// Реестр нужен: /permissions показывает доверенный код проекта и
		// сканирует его. Без него команда падала бы на nil, и тест
		// проверял бы не правила, а отсутствие реестра.
		tools:  tools.New(tools.Env{WorkDir: work}),
		memory: tools.NewMemory(work, store),
	}
	a.tools.RegisterExtTools()
	a.rules = a.setupRules()
	return a, buf
}

// writeProjectRules — положить gcli.json с правилами в проект.
func writeProjectRules(t *testing.T, work string, lines []string) {
	t.Helper()
	var body strings.Builder
	body.WriteString("{\n  \"permissions\": {\n    \"rules\": [\n")
	for _, l := range lines {
		body.WriteString("      " + quoteJSON(l) + ",\n")
	}
	body.WriteString("    ]\n  }\n}\n")
	if err := os.WriteFile(filepath.Join(work, "gcli.json"), []byte(body.String()), 0o600); err != nil {
		t.Fatalf("запись gcli.json: %v", err)
	}
}

// quoteJSON — обернуть в кавычки с экранированием обратного слэша.
func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

// execReq — запрос подтверждения на запуск команды.
func execReq(cmd string) tools.ConfirmReq {
	return tools.ConfirmReq{Kind: tools.ConfirmExec, Detail: cmd}
}

// writeReq — запрос подтверждения на запись файла.
func writeReq(path string) tools.ConfirmReq {
	return tools.ConfirmReq{Kind: tools.ConfirmWrite, Path: path, New: "x"}
}

// ---------- Загрузка правил ----------

// TestSetupRulesReadsProjectFile — правила из gcli.json применяются.
func TestSetupRulesReadsProjectFile(t *testing.T) {
	a, _ := permsApp(t, "bash(git status*): allow")
	if a.rules.Len() == 0 {
		t.Fatal("правила из gcli.json не загрузились")
	}
	if d, _, ok := a.rules.Decide("bash", "git status --short"); !ok || d != core.PermAllow {
		t.Errorf("правило не сработало: %s %v", d, ok)
	}
}

// TestSetupRulesReportsBrokenFile — битый gcli.json обязан быть виден.
// Молчаливый пропуск выглядел бы так, будто deny просто не сработал.
func TestSetupRulesReportsBrokenFile(t *testing.T) {
	a, _ := permsApp(t)
	bad := `{"permissions": {"rules": ["bash(git status*)" ]}}` // нет режима
	if err := os.WriteFile(filepath.Join(a.workDir, "gcli.json"), []byte(bad), 0o600); err != nil {
		t.Fatalf("запись: %v", err)
	}
	a.rules = a.setupRules()
	notes := strings.Join(a.startupNotes, " ")
	if !strings.Contains(notes, "Правила разрешений") {
		t.Errorf("о битом gcli.json надо сказать, заметки: %v", a.startupNotes)
	}
}

// TestSetupRulesKeepsGlobalWhenProjectBroken — битый проектный файл не
// должен обнулять личные правила.
func TestSetupRulesKeepsGlobalWhenProjectBroken(t *testing.T) {
	a, _ := permsApp(t)
	if err := os.WriteFile(filepath.Join(a.workDir, "gcli.json"), []byte(`{"permissions": [`), 0o600); err != nil {
		t.Fatalf("запись: %v", err)
	}
	// Кладём глобальное правило напрямую в конфиг и перечитываем.
	a.repo.Cfg.Permissions = &core.PermissionCfg{Rules: []string{"bash(go test*): allow"}}
	a.rules = a.setupRules()
	if d, _, ok := a.rules.Decide("bash", "go test ./..."); !ok || d != core.PermAllow {
		t.Errorf("личное правило должно выжить, получили %s %v", d, ok)
	}
}

// TestSetupRulesJSONCComments — комментарии в gcli.json допускаются.
func TestSetupRulesJSONCComments(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	t.Setenv("GCLI_HOME", home)
	src := `{
  // разрешаем сборку
  "permissions": {
    "rules": [
      /* читаемое и проверяемое */
      "bash(go build*): allow",
      "bash(go test*): allow",
    ],
  },
}`
	if err := os.WriteFile(filepath.Join(work, "gcli.json"), []byte(src), 0o600); err != nil {
		t.Fatalf("запись: %v", err)
	}
	cfg, _, err := core.LoadProjectConfig(work)
	if err != nil {
		t.Fatalf("JSONC-конфиг не прочитан: %v", err)
	}
	if cfg == nil || cfg.Permissions == nil || len(cfg.Permissions.Rules) != 2 {
		t.Fatalf("правила не разобраны: %+v", cfg)
	}
}

// ---------- Действие правил ----------

// TestPermDecisionDenyBlocks — deny запрещает действие без вопроса.
func TestPermDecisionDenyBlocks(t *testing.T) {
	a, _ := permsApp(t, "bash(rm *): deny")
	mode, _, ok := a.permDecision(execReq("rm file.txt"))
	if !ok || mode != core.PermDeny {
		t.Fatalf("ожидался deny, получили %s %v", mode, ok)
	}
}

// TestPermDecisionAllowSkipsQuestion — allow снимает вопрос. Проверяем
// косвенно: в quietConfirm-режиме без правила вопрос остаётся, а с
// правилом действие проходит.
func TestPermDecisionAllowSkipsQuestion(t *testing.T) {
	a, _ := permsApp(t, "bash(git status*): allow")
	mode, _, ok := a.permDecision(execReq("git status"))
	if !ok || mode != core.PermAllow {
		t.Errorf("ожидался allow, получили %s %v", mode, ok)
	}
}

// TestPermDecisionNoRuleFallsBack — без правил решение не выносится,
// и всё остаётся как раньше (спросить пользователя).
func TestPermDecisionNoRuleFallsBack(t *testing.T) {
	a, _ := permsApp(t)
	if _, _, ok := a.permDecision(execReq("go test ./...")); ok {
		t.Error("без правил permDecision не должен ничего решать")
	}
}

// TestPermDecisionWriteUsesRelativePath — правило пишут от проекта
// (edit(src/**)), а подтверждение получает абсолютный путь.
func TestPermDecisionWriteUsesRelativePath(t *testing.T) {
	a, _ := permsApp(t, "edit(src/**): allow")
	mode, _, ok := a.permDecision(writeReq(filepath.Join(a.workDir, "src", "main.go")))
	if !ok || mode != core.PermAllow {
		t.Errorf("правило по относительному пути не сработало: %s %v", mode, ok)
	}
}

// TestPermDecisionWriteOutsideProjectNotAllowed — правило edit(src/**)
// не должно разрешать запись вне src.
func TestPermDecisionWriteOutsideProjectNotAllowed(t *testing.T) {
	a, _ := permsApp(t, "edit(src/**): allow")
	mode, _, ok := a.permDecision(writeReq(filepath.Join(a.workDir, "other", "a.go")))
	if ok && mode == core.PermAllow {
		t.Error("правило для src/** не должно разрешать запись в other/")
	}
}

// TestPermDecisionAppliesToGroupTools — правило "bash: deny" должно
// действовать и на job, а не только на bash.
func TestPermDecisionAppliesToGroupTools(t *testing.T) {
	a, _ := permsApp(t, "bash(npm *): deny")
	mode, _, ok := a.permDecision(execReq("npm publish"))
	if !ok || mode != core.PermDeny {
		t.Errorf("правило bash должно ловить и job, получили %s %v", mode, ok)
	}
}

// TestPermDecisionNetUsesGroup — правило на web_fetch работает и для
// сетевых инструментов другой группы.
func TestPermDecisionNetUsesGroup(t *testing.T) {
	a, _ := permsApp(t, "web_fetch: deny")
	req := tools.ConfirmReq{Kind: tools.ConfirmNet, Detail: "https://example.com"}
	mode, _, ok := a.permDecision(req)
	if !ok || mode != core.PermDeny {
		t.Errorf("ожидался deny для сети, получили %s %v", mode, ok)
	}
}

// TestPermDecisionLastRuleWinsInConfirm — два правила на одну команду,
// побеждает последнее.
func TestPermDecisionLastRuleWinsInConfirm(t *testing.T) {
	a, _ := permsApp(t, "bash(rm *): allow", "bash(rm -rf *): deny")
	mode, _, _ := a.permDecision(execReq("rm file.txt"))
	if mode != core.PermAllow {
		t.Errorf("rm без -rf должен остаться allow, получили %s", mode)
	}
	mode, _, _ = a.permDecision(execReq("rm -rf build"))
	if mode != core.PermDeny {
		t.Errorf("rm -rf должен быть deny, получили %s", mode)
	}
}

// ---------- Команда /permissions ----------

// TestPermsInitCreatesFile — /permissions init заводит gcli.json.
func TestPermsInitCreatesFile(t *testing.T) {
	a, buf := permsApp(t)
	a.cmdPerms("init")

	path := filepath.Join(a.workDir, "gcli.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("gcli.json не создан: %v", err)
	}
	// Файл обязан быть читаемым: заготовка с комментариями — это и есть
	// документация, которой у человека больше нет.
	cfg, _, err := core.LoadProjectConfig(a.workDir)
	if err != nil || cfg == nil || cfg.Permissions == nil {
		t.Fatalf("созданный gcli.json не разбирается: %v", err)
	}
	if len(cfg.Permissions.Rules) == 0 {
		t.Error("заготовка должна содержать правила")
	}
	if !strings.Contains(out(buf), "создал gcli.json") {
		t.Errorf("нет подтверждения:\n%s", out(buf))
	}
}

// TestPermsInitKeepsExistingFile — существующие правила не затираются.
func TestPermsInitKeepsExistingFile(t *testing.T) {
	a, buf := permsApp(t, "bash(rm *): deny")
	a.cmdPerms("init")

	data, err := os.ReadFile(filepath.Join(a.workDir, "gcli.json"))
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if !strings.Contains(string(data), "rm *") {
		t.Errorf("правила человека затёрты:\n%s", data)
	}
	if !strings.Contains(out(buf), "уже есть") {
		t.Errorf("надо сказать, что файл уже был:\n%s", out(buf))
	}
}

// TestPermsShowsRulesInOrder — список показывает правила в порядке
// применения: человек должен видеть, какое из двух правил победит.
func TestPermsShowsRulesInOrder(t *testing.T) {
	a, buf := permsApp(t, "bash(go build*): allow", "bash(go test*): allow")
	a.cmdPerms("")

	got := out(buf)
	i1 := strings.Index(got, "go build")
	i2 := strings.Index(got, "go test")
	if i1 < 0 || i2 < 0 {
		t.Fatalf("правила не показаны:\n%s", got)
	}
	if i1 > i2 {
		t.Errorf("правила показаны не в порядке применения:\n%s", got)
	}
	if !strings.Contains(got, "allow") {
		t.Errorf("не показан режим правила:\n%s", got)
	}
}

// TestPermsWithoutRulesSuggestsInit — без правил подсказка init обязана
// быть: иначе человек не узнает, что движок вообще есть.
func TestPermsWithoutRulesSuggestsInit(t *testing.T) {
	a, buf := permsApp(t)
	a.cmdPerms("")
	got := out(buf)
	if !strings.Contains(got, "/permissions init") {
		t.Errorf("нет подсказки про init:\n%s", got)
	}
	if !strings.Contains(got, "0") {
		t.Errorf("счётчик правил должен быть нулём:\n%s", got)
	}
}

// TestPermsWhereListsFiles — /permissions where показывает, где искать
// правила: без этого вопрос «почему моё правило не сработало» нечем
// разрешить.
func TestPermsWhereListsFiles(t *testing.T) {
	a, buf := permsApp(t)
	a.cmdPerms("where")
	got := out(buf)
	if !strings.Contains(got, "gcli.json") || !strings.Contains(got, "config.json") {
		t.Errorf("пути к конфигам не показаны:\n%s", got)
	}
}

// TestPermsBadSubcommandExplains — опечатка в подкоманде не молчит.
func TestPermsBadSubcommandExplains(t *testing.T) {
	a, buf := permsApp(t)
	a.cmdPerms("инт")
	if !strings.Contains(out(buf), "/permissions init") {
		t.Errorf("надо перечислить подкоманды:\n%s", out(buf))
	}
}

// TestPermsResetStillWorks — сброс разрешений не должен сломаться от
// нового блока правил.
func TestPermsResetStillWorks(t *testing.T) {
	a, buf := permsApp(t, "bash(rm *): deny")
	a.sess.Perms.BashExact = map[string]bool{"ls": true}
	a.cmdPerms("reset")
	if len(a.sess.Perms.BashExact) != 0 {
		t.Error("сброс должен очистить разрешённые команды")
	}
	if !strings.Contains(out(buf), "разрешения сброшены") {
		t.Errorf("нет подтверждения сброса:\n%s", out(buf))
	}
}

// ---------- Правило против флагов сессии ----------

// TestDenyBeatsSessionFlags — явный запрет из gcli.json не перебивается
// ничем, что человек натыкал в прошлом разговоре.
//
// Проверяются все четыре пути: «разрешил всё», «разрешил эту команду»,
// автопилот и усиленный автопилот. Любой из них раньше мог пропустить
// удаление файлов, хотя в проекте стояло deny.
func TestDenyBeatsSessionFlags(t *testing.T) {
	cases := []struct {
		name  string
		setup func(a *app)
	}{
		{"BashAll", func(a *app) { a.sess.Perms.BashAll = true }},
		{"BashExact", func(a *app) { a.sess.Perms.BashExact = map[string]bool{"rm file.txt": true} }},
		{"Autopilot", func(a *app) { a.sess.Perms.Autopilot = true }},
		{"AutopilotAll", func(a *app) { a.sess.Perms.AutopilotAll = true; a.sess.Perms.Autopilot = true }},
		{"quiet+AutopilotAll", func(a *app) { a.quiet = true; a.sess.Perms.AutopilotAll = true; a.sess.Perms.Autopilot = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := permsApp(t, "bash(rm *): deny")
			c.setup(a)
			if a.confirm(execReq("rm file.txt")) {
				t.Error("deny должен блокировать действие, но оно разрешено")
			}
		})
	}
}

// TestDenyWinsInQuietMode — в машинном режиме (-p) запрет тоже действует.
// Иначе «-p с gcli.json» был бы удобным способом обойти правила проекта.
func TestDenyWinsInQuietMode(t *testing.T) {
	a, _ := permsApp(t, "bash(rm *): deny")
	a.quiet = true
	if a.confirm(execReq("rm file.txt")) {
		t.Error("quiet-режим не должен обходить deny")
	}
}

// TestDenyNetNotOverriddenByWebFetchFlag — запрет сети не перебит
// согласием «разрешить сетевые запросы» из прошлой сессии.
func TestDenyNetNotOverriddenByWebFetchFlag(t *testing.T) {
	a, _ := permsApp(t, "web_fetch: deny")
	a.sess.Perms.WebFetch = true
	req := tools.ConfirmReq{Kind: tools.ConfirmNet, Detail: "https://example.com"}
	if a.confirm(req) {
		t.Error("deny сети должен блокировать запрос")
	}
}

// TestAllowSkipsQuestionInQuietMode — правило allow должно снимать вопрос
// даже там, где без него действие было бы отклонено молча.
func TestAllowSkipsQuestionInQuietMode(t *testing.T) {
	a, _ := permsApp(t, "bash(go test*): allow")
	a.quiet = true
	if !a.confirm(execReq("go test ./...")) {
		t.Error("allow должен пропустить действие в quiet-режиме")
	}
}

// TestAllowDeniesNothingElse — allow не должен разрешать соседние команды.
func TestAllowDeniesNothingElse(t *testing.T) {
	a, _ := permsApp(t, "bash(go test*): allow")
	a.quiet = true
	if a.confirm(execReq("go mod tidy")) {
		t.Error("allow для go test не должен разрешать go mod tidy")
	}
}

// TestNoRuleStillAsksOldWay — без правил поведение прежнее: в quiet-режиме
// безопасная команда без согласий отклоняется (спросить некого), а с
// согласием — проходит. Правила не должны менять этот путь.
func TestNoRuleStillAsksOldWay(t *testing.T) {
	a, _ := permsApp(t)
	a.quiet = true
	if a.confirm(execReq("go test ./...")) {
		t.Error("без правил и без согласий quiet-режим должен отклонить")
	}
	a.sess.Perms.BashAll = true
	if !a.confirm(execReq("go test ./...")) {
		t.Error("без правил согласие BashAll должно работать как раньше")
	}
}

// ---------- /permissions test ----------

// TestPermsTestExplainsDeny — команда test отвечает на вопрос «почему не
// сработало правило» и называет победившее правило.
func TestPermsTestExplainsDeny(t *testing.T) {
	a, buf := permsApp(t, "bash(git *): allow", "bash(git push*): deny")
	a.cmdPerms(`test bash "git push origin main"`)
	got := out(buf)
	if !strings.Contains(got, "запретить") {
		t.Errorf("ожидался запрет, получили:\n%s", got)
	}
	if !strings.Contains(got, "git push*") {
		t.Errorf("надо показать, какое правило сработало:\n%s", got)
	}
}

// TestPermsTestKeepsCommandCase — команда с большой буквы не должна
// роняться: путь и имя команды приходят как есть.
func TestPermsTestKeepsCommandCase(t *testing.T) {
	a, buf := permsApp(t, "edit(src/**): allow")
	a.cmdPerms("test edit_file src/Main.go")
	got := out(buf)
	if !strings.Contains(got, "разрешить") {
		t.Errorf("ожидался allow для src/Main.go, получили:\n%s", got)
	}
}

// TestPermsTestSaysWhenNoRules — без правил test обязан сказать об этом,
// а не молча показать «ask».
func TestPermsTestSaysWhenNoRules(t *testing.T) {
	a, buf := permsApp(t)
	a.cmdPerms("test bash git status")
	got := out(buf)
	if !strings.Contains(got, "правил нет") {
		t.Errorf("надо сказать, что правил нет:\n%s", got)
	}
}

// TestPermsTestWithoutArgsExplains — без аргументов подсказка с примером.
func TestPermsTestWithoutArgsExplains(t *testing.T) {
	a, buf := permsApp(t, "bash(rm *): deny")
	a.cmdPerms("test")
	if !strings.Contains(out(buf), "нужен инструмент") {
		t.Errorf("надо объяснить формат:\n%s", out(buf))
	}
}

// TestPermsTestByOperationKind — род операции (write) тоже понимается.
func TestPermsTestByOperationKind(t *testing.T) {
	a, buf := permsApp(t, "edit(src/**): allow", "edit(secrets/**): deny")
	a.cmdPerms("test write secrets/token.txt")
	got := out(buf)
	if !strings.Contains(got, "запретить") {
		t.Errorf("ожидался запрет для secrets/token.txt, получили:\n%s", got)
	}
}
