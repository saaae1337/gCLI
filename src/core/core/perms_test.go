package core

import (
	"path/filepath"

	"strings"
	"testing"
)

// ---------- JSONC ----------

// TestStripJSONCLineComments — комментарии снимаются, строки сохраняются.
func TestStripJSONCLineComments(t *testing.T) {
	src := []byte("{\n  // это комментарий\n  \"a\": 1, // хвостовой\n  \"b\": 2\n}\n")
	got := string(StripJSONC(src))
	for _, unwanted := range []string{"комментарий", "хвостовой"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("комментарий %q не убран: %s", unwanted, got)
		}
	}
	if !strings.Contains(got, `"a": 1`) || !strings.Contains(got, `"b": 2`) {
		t.Errorf("данные потеряны: %s", got)
	}
}

// TestStripJSONCKeepsStrings — содержимое строк не трогается. Это главная
// ловушка: URL "https://x" и путь "a//b" не должны превратиться в мусор.
func TestStripJSONCKeepsStrings(t *testing.T) {
	src := []byte(`{"url": "https://example.com/a", "cmd": "grep // x", "star": "/* не комментарий */", "esc": "кавычка \" и // слэш"}`)
	got := string(StripJSONC(src))
	for _, want := range []string{"https://example.com/a", "grep // x", "/* не комментарий */", `\" и // слэш`} {
		if !strings.Contains(got, want) {
			t.Errorf("строка %q пострадала: %s", want, got)
		}
	}
}

// TestStripJSONCEscapedQuote — экранированная кавычка не закрывает строку.
func TestStripJSONCEscapedQuote(t *testing.T) {
	src := []byte(`{"a": "он сказал \" // и всё", "b": 2}`)
	got := string(StripJSONC(src))
	if !strings.Contains(got, "всё") || !strings.Contains(got, `"b": 2`) {
		t.Errorf("разбор поехал после экранированной кавычки: %s", got)
	}
}

// TestStripJSONCBlockComments — многострочные комментарии снимаются,
// номера строк внутри них заменяются переводами.
func TestStripJSONCBlockComments(t *testing.T) {
	src := []byte("{\n/* три\nстроки\nкомментария */\n\"a\": 1\n}")
	got := string(StripJSONC(src))
	if strings.Contains(got, "комментария") {
		t.Errorf("блочный комментарий не убран: %s", got)
	}
	// Переводов строк должно остаться столько же, сколько было: замена
	// комментария пустотой иначе сдвинула бы номера строк в ошибке
	// разбора, и человек читал бы «ошибка в строке 12», указывая выше
	// настоящей проблемы.
	if n, want := strings.Count(got, "\n"), strings.Count(string(src), "\n"); n != want {
		t.Errorf("переводы строк должны сохраниться: получили %d, ждали %d: %s", n, want, got)
	}
}

// TestStripJSONCTrailingCommas — висячие запятые снимаются.
func TestStripJSONCTrailingCommas(t *testing.T) {
	for _, c := range []struct{ in, out string }{
		{`{"a": 1,}`, `{"a": 1}`},
		{`[1, 2, ]`, `[1, 2 ]`},
		{`{"a": [1,],}`, `{"a": [1]}`},
		{`{"a": 1}`, `{"a": 1}`},
	} {
		got := string(StripJSONC([]byte(c.in)))
		if got != c.out {
			t.Errorf("StripJSONC(%q) = %q, ждали %q", c.in, got, c.out)
		}
	}
}

// TestStripJSONCCommaInString — запятая перед закрывающей кавычкой это данные.
func TestStripJSONCCommaInString(t *testing.T) {
	src := []byte(`{"a": "x,}", "b": 2}`)
	if got := string(StripJSONC(src)); !strings.Contains(got, `"x,}"`) {
		t.Errorf("запятая внутри строки съедена вместе со строкой: %s", got)
	}
}

// TestHasJSONC — детектор не должен ловить обычный JSON.
func TestHasJSONC(t *testing.T) {
	for _, c := range []struct {
		src  string
		want bool
	}{
		{`{"a":1}`, false},
		{`{"a":"b"}`, false},
		{`{"a": "// нет"}`, false}, // внутри строки — это данные
		{`{"a":1,}`, true},
		{`{"a":1 /* да */}`, true},
	} {
		if got := HasJSONC([]byte(c.src)); got != c.want {
			t.Errorf("HasJSONC(%q) = %v, ждали %v", c.src, got, c.want)
		}
	}
}

// ---------- Разбор правил ----------

func TestParseRule(t *testing.T) {
	for _, c := range []struct {
		in      string
		tool    string
		pattern string
		mode    Permission
	}{
		{"bash: allow", "bash", "", PermAllow},
		{"  bash(git status *): allow  ", "bash", "git status *", PermAllow},
		{"edit(src/**): deny", "edit", "src/**", PermDeny},
		{"web_fetch(domain:example.com): ask", "web_fetch", "domain:example.com", PermAsk},
		{"bash: DENY", "bash", "", PermDeny},
	} {
		r, err := parseRule(c.in)
		if err != nil {
			t.Fatalf("parseRule(%q): %v", c.in, err)
		}
		if r.Tool != c.tool || r.Pattern != c.pattern || r.Mode != c.mode {
			t.Errorf("parseRule(%q) = %+v, ждали {%s %s %s}", c.in, r, c.tool, c.pattern, c.mode)
		}
	}
}

func TestParseRuleErrors(t *testing.T) {
	for _, in := range []string{
		"bash",             // нет режима
		"bash(git): maybe", // неизвестный режим
		": allow",          // пустой инструмент
	} {
		if r, err := parseRule(in); err == nil {
			t.Errorf("parseRule(%q) должен вернуть ошибку, получил %+v", in, r)
		}
	}
}

// TestParseRuleKeepsColonInPattern — двоеточие внутри скобок (например
// web_fetch(domain:x)) не должно ломать разбор режима.
func TestParseRuleKeepsColonInPattern(t *testing.T) {
	r, err := parseRule("web_fetch(domain:example.com): deny")
	if err != nil {
		t.Fatalf("parseRule: %v", err)
	}
	if r.Pattern != "domain:example.com" || r.Mode != PermDeny {
		t.Errorf("получили %+v, ждали pattern=domain:example.com mode=deny", r)
	}
}

func TestParseRulesKeepsOrder(t *testing.T) {
	rs, err := ParseRules([]string{"bash: allow", "bash(rm *): deny"})
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	if rs.Len() != 2 {
		t.Fatalf("ожидалось 2 правила, получили %d", rs.Len())
	}
}

// TestParseRulesReportsIndex — ошибка должна называть номер строки:
// иначе в конфиге из двадцати правил искать её вручную.
func TestParseRulesReportsIndex(t *testing.T) {
	_, err := ParseRules([]string{"bash: allow", "bash: maybe", "read: allow"})
	if err == nil {
		t.Fatal("ждалась ошибка")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("в ошибке должен быть номер правила: %v", err)
	}
}

// ---------- Решение ----------

// TestDecideLastMatchWins — базовое правило системы.
func TestDecideLastMatchWins(t *testing.T) {
	rs, _ := ParseRules([]string{"bash: allow", "bash(git push *): ask", "bash(git push --force *): deny"})
	for _, c := range []struct {
		subj string
		want Permission
	}{
		{"ls", PermAllow},
		{"go test ./...", PermAllow},
		{"git status", PermAllow},
		{"git push origin main", PermAsk},
		{"git push --force origin main", PermDeny},
	} {
		if d, _, _ := rs.Decide("bash", c.subj); d != c.want {
			t.Errorf("Decide(bash, %q) = %s, ждали %s", c.subj, d, c.want)
		}
	}
}

func TestDecideNoRuleIsAsk(t *testing.T) {
	var rs Rules
	d, _, ok := rs.Decide("bash", "rm -rf /")
	if ok {
		t.Error("без правил совпадения быть не должно")
	}
	if d != PermAsk {
		t.Errorf("дефолт без правил = %s, ждали ask", d)
	}
}

func TestDecideEmptyPatternIsWholeTool(t *testing.T) {
	rs, _ := ParseRules([]string{"edit: allow"})
	if d, _, ok := rs.Decide("edit", "src/main.go"); !ok || d != PermAllow {
		t.Errorf("правило без скобок должно ловить любой аргумент: %s %v", d, ok)
	}
}

// ---------- Матчинг шаблонов ----------

func TestMatchGlob(t *testing.T) {
	for _, c := range []struct {
		pat, s string
		want   bool
	}{
		{"*", "что угодно", true},
		{"*", "", true},
		{"go build*", "go build ./...", true},
		{"go build*", "go test ./...", false},
		{"git status*", "git status --short", true},
		{"rm *", "rm file.txt", true},
		{"rm *", "rmdir x", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"src/**", "src/a/b/c.go", true},
		{"*push*", "git push main", true},
		{"*push*", "git pull main", false},
		// Точное имя команды не должно разрешать продолжение.
		{"git", "git push", false},
		{"git", "git", true},
		{"git", "git push", false},
		{"bash", "bash", true},
	} {
		if got := matchGlob(c.pat, c.s); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, ждали %v", c.pat, c.s, got, c.want)
		}
	}
}

// TestMatchGlobWindowsSeparators — правило из gcli.json пишут со слэшем,
// а пути на Windows приходят с обратным. Без нормализации правило
// edit(src/**) не сработало бы ни на одной Windows-машине.
func TestMatchGlobWindowsSeparators(t *testing.T) {
	if !matchGlob("src/**", `src\main\go.go`) {
		t.Error("правило со слэшем должно матчить путь с обратным слэшем")
	}
	if !matchGlob("edit", "edit") {
		t.Error("совпадение имён должно работать")
	}
}

// TestMatchGlobStarCrossesPath — одиночный * тоже пересекает разделители:
// иначе правило "bash(go *)" не разрешило бы "go test ./...".
func TestMatchGlobStarCrossesPath(t *testing.T) {
	if !matchGlob("go *", "go test ./pkg/...") {
		t.Error("одиночный * должен пересекать разделители пути")
	}
}

func TestRuleMatchCaseInsensitiveTool(t *testing.T) {
	r := Rule{Tool: "Bash", Pattern: "ls*", Mode: PermAllow}
	if !r.Match("bash", "ls -la") {
		t.Error("имя инструмента должно сравниваться без учёта регистра")
	}
}

func TestRuleLabelShowsSource(t *testing.T) {
	r := Rule{Tool: "bash", Pattern: "rm *", Mode: PermDeny, Source: "gcli.json:12"}
	l := r.Label()
	if !strings.Contains(l, "bash(rm *)") || !strings.Contains(l, "deny") || !strings.Contains(l, "gcli.json:12") {
		t.Errorf("метка неполная: %q", l)
	}
}

// ---------- Слои ----------

// TestRuleSourcesUserWinsOverProject — правило из ~/.gcli/config.json должно
// перекрывать проектное. Иначе файл из репозитория управляет поведением
// агента у того, кто этот файл не открывал.
func TestRuleSourcesUserWinsOverProject(t *testing.T) {
	user := &PermissionCfg{Rules: []string{"bash: allow"}}
	project := &PermissionCfg{Rules: []string{"bash(rm *): deny"}}
	// Порядок аргументов — от низшего приоритета к высшему.
	rs, err := RuleSources(project, user)
	if err != nil {
		t.Fatalf("разбор правил: %v", err)
	}

	if d, _, _ := rs.Decide("bash", "rm file"); d != PermAllow {
		t.Errorf("личное правило должно перекрывать проектное, получили %s", d)
	}
}

// TestRuleSourcesProjectStillAppliesWhenNoUserRule — без личного правила
// проектное действует: иначе gcli.json был бы мёртвым файлом.
func TestRuleSourcesProjectStillAppliesWhenNoUserRule(t *testing.T) {
	project := &PermissionCfg{Rules: []string{"bash(go test*): allow"}}
	rs, _ := RuleSources(project, nil)
	if d, _, ok := rs.Decide("bash", "go test ./..."); !ok || d != PermAllow {
		t.Errorf("проектное правило должно действовать, получили %s %v", d, ok)
	}
}

// TestLayeredRulesFromPutsUserLast — сквозная проверка склеивания слоёв:
// личный конфиг обязан перекрывать проектный.
func TestLayeredRulesFromPutsUserLast(t *testing.T) {
	global := &Config{Permissions: &PermissionCfg{Rules: []string{"bash(rm *): allow"}}}
	project := &ProjectConfig{Permissions: &PermissionCfg{Rules: []string{"bash(rm *): deny"}}}
	rs, _ := LayeredRulesFrom(global, project)
	if d, _, _ := rs.Decide("bash", "rm -rf build"); d != PermAllow {
		t.Errorf("личный конфиг должен перекрывать проектный, получили %s", d)
	}
}

// TestRuleSourcesProjectWinsOverNothing — без личного правила проектное
// действует.
func TestRuleSourcesProjectWinsOverNothing(t *testing.T) {
	project := &PermissionCfg{Rules: []string{"bash(go test*): allow"}}
	rs, _ := RuleSources(nil, project)
	if d, _, ok := rs.Decide("bash", "go test ./..."); !ok || d != PermAllow {
		t.Errorf("проектное правило должно действовать, получили %s %v", d, ok)
	}
}

// TestPermissionCfgModeIsDefaultRule — блок mode задаёт режим для всех
// инструментов, и частные правила перекрывают его.
func TestPermissionCfgModeIsDefaultRule(t *testing.T) {
	cfg := &PermissionCfg{Mode: PermAllow, Rules: []string{"bash(rm *): deny"}}
	var rules Rules
	if err := cfg.AddTo(&rules); err != nil {
		t.Fatalf("разбор правил: %v", err)
	}
	if d, _, _ := rules.Decide("read_file", "x"); d != PermAllow {
		t.Errorf("mode должен разрешать остальные инструменты, получили %s", d)
	}
	if d, _, _ := rules.Decide("bash", "rm x"); d != PermDeny {
		t.Errorf("частное правило должно перекрывать mode, получили %s", d)
	}
}

// TestPermissionCfgModeNotBeatenByToollessRule — дефолт из блока mode не
// должен перекрываться правилом без имени инструмента.
//
// Регрессия: дефолт раньше хранился обычным правилом "*: mode", и правило
// "git push*: ask" (строка без имени инструмента) перебивало явный
// "bash(git push*): deny" — запрет молча превращался в вопрос.
func TestPermissionCfgModeNotBeatenByToollessRule(t *testing.T) {
	cfg := &PermissionCfg{Mode: PermDeny, Rules: []string{"bash(git status*): allow"}}
	var rules Rules
	if err := cfg.AddTo(&rules); err != nil {
		t.Fatalf("разбор правил: %v", err)
	}
	if d, _, _ := rules.Decide("bash", "git status"); d != PermAllow {
		t.Errorf("правило должно выиграть у дефолта, получили %s", d)
	}
	if d, _, _ := rules.Decide("bash", "rm -rf /"); d != PermDeny {
		t.Errorf("дефолт должен действовать для прочих команд, получили %s", d)
	}
}

// TestDecideAnyPrefersNamedOverToolless — правило с именем инструмента
// проверяется раньше строки без имени: иначе короткая строка забила бы
// явный запрет.
func TestDecideAnyPrefersNamedOverToolless(t *testing.T) {
	rs, err := ParseRules([]string{"git push*: ask", "bash(git push*): deny"})
	if err != nil {
		t.Fatalf("разбор правил: %v", err)
	}
	d, hit, ok := rs.DecideAny(ToolsForPermission("exec"), "git push --force")
	if !ok || d != PermDeny {
		t.Errorf("ожидался deny, получили %s %v (правило %q)", d, ok, hit.String())
	}
}

// TestDecideAnyFallsBackToToollessRule — если по именам группы ничего нет,
// строка без имени инструмента всё равно применяется.
func TestDecideAnyFallsBackToToollessRule(t *testing.T) {
	rs, err := ParseRules([]string{"npm *: deny"})
	if err != nil {
		t.Fatalf("разбор правил: %v", err)
	}
	d, _, ok := rs.DecideAny(ToolsForPermission("exec"), "npm publish")
	if !ok || d != PermDeny {
		t.Errorf("ожидался deny для job по правилу без имени, получили %s %v", d, ok)
	}
}

// TestSetDefaultLaterLayerWins — слой, который явно вернул всё к вопросу,
// перекрывает "mode": "allow" из нижнего слоя.
func TestSetDefaultLaterLayerWins(t *testing.T) {
	rs, err := RuleSources(
		&PermissionCfg{Mode: PermAllow},
		&PermissionCfg{Mode: PermAsk},
	)
	if err != nil {
		t.Fatalf("разбор правил: %v", err)
	}
	if d, _, ok := rs.Decide("read_file", "x"); ok || d != PermAsk {
		t.Errorf("ожидался сброс к вопросу, получили %s %v", d, ok)
	}
}

// TestAddToKeepsGoodRulesAndReportsBroken — битое правило не должно
// молча выпасть: остальные применяются, а ошибка доходит до вызывающего.
func TestAddToKeepsGoodRulesAndReportsBroken(t *testing.T) {
	cfg := &PermissionCfg{Rules: []string{
		"bash(rm *): deny",
		"bash(go test*)", // нет режима
		"bash(go build*): allow",
	}}
	var rules Rules
	err := cfg.AddTo(&rules)
	if err == nil {
		t.Fatal("битое правило должно возвращать ошибку, а не молчать")
	}
	if rules.Len() != 2 {
		t.Errorf("хорошие правила должны сохраниться, осталось %d", rules.Len())
	}
	if d, _, _ := rules.Decide("bash", "rm x"); d != PermDeny {
		t.Errorf("deny должен примениться, получили %s", d)
	}
}

// ---------- Сопоставление инструментов ----------

func TestToolsForPermission(t *testing.T) {
	write := ToolsForPermission("write")
	if !hasStr(write, "edit_file") || !hasStr(write, "multi_edit") {
		t.Errorf("группа записи неполна: %v", write)
	}
	exec := ToolsForPermission("exec")
	if !hasStr(exec, "bash") || !hasStr(exec, "job") {
		t.Errorf("группа выполнения неполна: %v", exec)
	}
	net := ToolsForPermission("net")
	if !hasStr(net, "web_fetch") {
		t.Errorf("группа сети неполна: %v", net)
	}
}

func TestPatternForPathRelativeToProject(t *testing.T) {
	work := t.TempDir()
	got := PatternForPath(work, filepath.Join(work, "src", "main.go"))
	if got != "src/main.go" {
		t.Errorf("получили %q, ждали src/main.go", got)
	}
}

// TestPatternForPathOutsideKeepsAbsolute — путь вне проекта должен остаться
// абсолютным: иначе правило вида "edit(*)" разрешило бы правку чего угодно.
func TestPatternForPathOutsideKeepsAbsolute(t *testing.T) {
	work := t.TempDir()
	got := PatternForPath(work, "/etc/passwd")
	if !strings.HasPrefix(got, "/") {
		t.Errorf("вне проекта должен остаться абсолютный путь, получили %q", got)
	}
}

// ---------- Permission в JSON ----------

func TestPermissionUnmarshal(t *testing.T) {
	for raw, want := range map[string]Permission{
		`"allow"`: PermAllow,
		`"ask"`:   PermAsk,
		`"deny"`:  PermDeny,
		`"DENY"`:  PermDeny,
		`null`:    PermAsk,
	} {
		var p Permission
		if err := p.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatalf("UnmarshalJSON(%q): %v", raw, err)
		}
		if p != want {
			t.Errorf("UnmarshalJSON(%q) = %s, ждали %s", raw, p, want)
		}
	}
}

func TestPermissionUnmarshalRejectsGarbage(t *testing.T) {
	for _, raw := range []string{`"maybe"`, `1`, `[]`, `{}`} {
		var p Permission
		if err := p.UnmarshalJSON([]byte(raw)); err == nil {
			t.Errorf("UnmarshalJSON(%q) должен вернуть ошибку", raw)
		}
	}
}

func hasStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
