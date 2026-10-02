package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Permission — решение движка разрешений для одного действия.
type Permission string

const (
	// PermAsk — спросить пользователя. Значение по умолчанию: агент
	// ничего не делает без согласия, пока правило не сказало иначе.
	PermAsk Permission = "ask"
	// PermAllow — выполнить без вопроса.
	PermAllow Permission = "allow"
	// PermDeny — отказать и не спрашивать.
	PermDeny Permission = "deny"
)

// Valid — известен ли режим.
func (p Permission) Valid() bool {
	return p == PermAsk || p == PermAllow || p == PermDeny
}

// UnmarshalJSON — принять строку режима. Число или мусор — ошибка:
// молча превращённый в «ask» deny опаснее, чем отказ читать конфиг.
func (p *Permission) UnmarshalJSON(b []byte) error {
	raw := strings.TrimSpace(string(b))
	if raw == "null" {
		*p = PermAsk
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("правило разрешения: ждал \"allow\"/\"ask\"/\"deny\", получил %s", raw)
	}
	got := Permission(strings.ToLower(strings.TrimSpace(s)))
	if !got.Valid() {
		return fmt.Errorf("правило разрешения: ждал \"allow\"/\"ask\"/\"deny\", получил %q", s)
	}
	*p = got
	return nil
}

// Rule — одно правило разрешений.
//
// Паттерн записывается как у Claude Code: инструмент в скобках и
// аргументы, например "bash(git status *)", "edit(src/**)".
// Пустые скобки означают «инструмент целиком»: "bash".
type Rule struct {
	Tool    string     `json:"tool"`
	Pattern string     `json:"pattern,omitempty"`
	Mode    Permission `json:"mode"`
	// Source — откуда правило пришло (файл и строка). Не в JSON: это
	// диагностика для /permissions, а не часть конфига.
	Source string `json:"-"`
}

// String — правило в виде, в котором его пишут в конфиге.
func (r Rule) String() string {
	if r.Pattern == "" {
		return r.Tool
	}
	return r.Tool + "(" + r.Pattern + ")"
}

// Label — правило с указанием источника, для списка в /permissions.
func (r Rule) Label() string {
	s := r.String() + " → " + string(r.Mode)
	if r.Source != "" {
		s += "  (" + r.Source + ")"
	}
	return s
}

// Match — подходит ли правило под действие.
//
// Tool сравнивается как шаблон, а не как слово: правило "edit*" должно
// ловить и edit_file, и multi_edit, а "*" — всё подряд. Без этого
// пришлось бы писать одинаковое правило на каждое имя инструмента, и
// человек перестал бы этим пользоваться уже на втором-третьем инструменте.
func (r Rule) Match(tool, subject string) bool {
	if !matchToolName(r.Tool, tool) {
		return false
	}
	if r.Pattern == "" || r.Pattern == "*" {
		return true
	}
	return matchGlob(r.Pattern, subject)
}

// matchToolName — совпадение имени инструмента по шаблону.
func matchToolName(pattern, tool string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if !strings.ContainsAny(pattern, "*?") {
		return strings.EqualFold(pattern, tool)
	}
	return globMatch(strings.ToLower(pattern), strings.ToLower(tool))
}

// Rules — упорядоченный список правил.
//
// Порядок значим: последнее совпавшее правило решает, и это единственная
// схема, при которой не нужно угадывать «какое правило сильнее».
// Сначала общие правила, потом точные — так что "bash(git *)" можно
// сузить правилом "bash(git push *)", а наоборот — нельзя.
type Rules struct {
	items []Rule
	// def — режим по умолчанию из блока "mode" конфига.
	//
	// Он намеренно НЕ хранится обычным правилом "*: mode". Правило с
	// именем инструмента перекрывает дефолт, а строка без имени
	// инструмента ("git push*: ask") — нет: иначе такое правило забило бы
	// явный "bash(git push*): deny" из gcli.json. Пустая строка = спросить.
	def Permission
}

// Add — добавить правило в конец списка (оно станет приоритетнее прежних).
func (rs *Rules) Add(r Rule) {
	if strings.TrimSpace(r.Tool) == "" {
		return
	}
	if r.Mode == "" {
		r.Mode = PermAsk
	}
	rs.items = append(rs.items, r)
}

// SetDefault — задать режим по умолчанию из блока "mode" конфига.
//
// Последний заданный дефолт побеждает, в том числе явный "ask": иначе
// слой, который явно вернул всё к вопросу, не смог бы отменить "mode":
// "allow" из нижнего слоя — писать его специально никто не станет.
func (rs *Rules) SetDefault(m Permission) {
	if !m.Valid() {
		return
	}
	rs.def = m
}

// Default — задан ли режим по умолчанию и какой.
//
// Явный "ask" дефолтом не считается: он равносилен «правил нет», и
// вызывающий обязан вести себя в обоих случаях одинаково — спросить
// пользователя обычным порядком, с учётом опасности команды и флагов
// сессии. Иначе "mode": "ask" молча отключал бы и эти проверки.
func (rs Rules) Default() (Permission, bool) {
	if rs.def == PermAsk {
		return PermAsk, false
	}
	return rs.def, rs.def != ""
}

// Len — сколько правил.
func (rs Rules) Len() int { return len(rs.items) }

// Items — все правила по порядку.
func (rs Rules) Items() []Rule { return rs.items }

// Parse — разобрать строки вида "bash(git *)": "allow" в правила.
//
// Такая запись нужна для компактного и читаемого конфига: список
// объектов с tool/pattern/mode — это многословно, и человек перед
// первым же deny обычно пишет его неправильно.
func ParseRules(list []string) (Rules, error) {
	var rs Rules
	for i, line := range list {
		r, err := parseRule(line)
		if err != nil {
			return rs, fmt.Errorf("правило %d (%q): %w", i+1, line, err)
		}
		rs.Add(r)
	}
	return rs, nil
}

// parseRule — одна строка "bash(git *): deny".
func parseRule(line string) (Rule, error) {
	s := strings.TrimSpace(line)
	if s == "" {
		return Rule{}, nil
	}
	// Режим отделён двоеточием. Двоеточие внутри скобок не ищется:
	// разбираем по последнему «»:», после закрывающей скобки.
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return Rule{}, fmt.Errorf("нет режима, ждал \"bash(git *): allow\"")
	}
	mode := Permission(strings.ToLower(strings.TrimSpace(s[idx+1:])))
	if !mode.Valid() {
		return Rule{}, fmt.Errorf("неизвестный режим %q, ждал allow/ask/deny", strings.TrimSpace(s[idx+1:]))
	}
	spec := strings.TrimSpace(s[:idx])
	tool, pattern := spec, ""
	if open := strings.Index(spec, "("); open >= 0 && strings.HasSuffix(spec, ")") {
		tool = strings.TrimSpace(spec[:open])
		pattern = strings.TrimSpace(spec[open+1 : len(spec)-1])
	} else if looksLikePattern(spec) {
		// Без скобок и с пробелом или «*» — человек писал про саму
		// операцию, а не про инструмент: "git push*: ask". Имя инструмента
		// не указано, значит правило ловит любое действие такого вида —
		// bash, job, shell. Ставить сюда имя инструмента нельзя: строка
		// «npm *» не называет инструмент npm, его просто не существует.
		tool, pattern = "*", spec
	}
	if tool == "" {
		return Rule{}, fmt.Errorf("пустое имя инструмента")
	}
	return Rule{Tool: tool, Pattern: pattern, Mode: mode}, nil
}

// looksLikePattern — похоже ли на шаблон действия, а не на имя инструмента.
//
// Имя инструмента — одно слово без wildcard: "bash", "web_fetch". Всё, где
// есть пробел или «*», человек явно писал про содержимое команды.
func looksLikePattern(spec string) bool {
	return spec != "" && spec != "*" &&
		(strings.ContainsAny(spec, "*?") || strings.ContainsAny(spec, " \t"))
}

// Match — решение по одному имени инструмента, без учёта дефолта из
// блока "mode".
//
// Отдельный метод нужен там, где инструменты перебираются по очереди
// (группа bash/job/shell): дефолт нельзя получать на каждом шаге, иначе он
// перекроет правило, найденное для первого же имени группы.
func (rs Rules) Match(tool, subject string) (Permission, Rule, bool) {
	var (
		hit      Rule
		decision = PermAsk
		found    bool
	)
	for _, r := range rs.items {
		if r.Match(tool, subject) {
			decision, hit, found = r.Mode, r, true
		}
	}
	return decision, hit, found
}

// DecideAny — решение для группы взаимозаменяемых инструментов.
//
// Сначала по конкретным именам («bash(git push*): deny» должно ловить и job),
// а правила без имени инструмента («git push*: ask») — только если по именам
// не нашлось ничего. Иначе короткая строка забивала бы длинную и тем более
// перекрывала бы запрет, написанный явно.
func (rs Rules) DecideAny(tools []string, subject string) (Permission, Rule, bool) {
	for _, t := range tools {
		if d, r, ok := rs.Match(t, subject); ok {
			return d, r, true
		}
	}
	if d, r, ok := rs.Match("*", subject); ok {
		return d, r, true
	}
	if d, ok := rs.Default(); ok {
		return d, Rule{}, true
	}
	return PermAsk, Rule{}, false
}

// Decide — что делать с действием инструмента.
//
// Сначала берётся последнее совпавшее правило. Если правил нет вовсе,
// решение отдаётся вызывающему: там живут дефолты и опасность команды.
func (rs Rules) Decide(tool, subject string) (Permission, Rule, bool) {
	if d, r, ok := rs.Match(tool, subject); ok {
		return d, r, true
	}
	if d, ok := rs.Default(); ok {
		return d, Rule{}, true
	}
	return PermAsk, Rule{}, false
}

// Explain — человекочитаемое объяснение решения по списку правил.
//
// Нужно не для украшения: когда агент натыкается на запрет, человек
// должен видеть, какое именно правило его остановило и где оно задано,
// иначе единственный способ разобраться — перебрать все файлы вручную.
func (rs Rules) Explain(tool, subject string) string {
	d, hit, found := rs.Match(tool, subject)
	if found {
		return hit.Label()
	}
	if def, ok := rs.Default(); ok {
		return "mode=" + string(def) + " (правил нет)"
	}
	return "нет правил — " + string(d)
}

// matchGlob — сравнение паттерна с текстом.
//
// Поддерживается ровно то, что реально нужно для разрешений:
//
//   - любой текст, включая пустой
//     ?  один любой символ
//     ** любой текст, включая разделитель пути
//
// Разделители пути нормализуются в оба вида (\\ на Windows), иначе
// правило "edit(src/**)" перестало бы работать на Windows, где пути
// приходят с обратными слэшами.
func matchGlob(pattern, s string) bool {
	s = normGlobPath(s)
	p := normGlobPath(pattern)
	if p == "*" || p == "**" {
		return true
	}
	// Префикс без wildcard — обычное сравнение. Это самый частый случай
	// ("bash" со всем, "webfetch(domain:example.com)").
	if !strings.ContainsAny(p, "*?") {
		return matchPrefixRule(p, s)
	}
	return globMatch(p, s)
}

// matchPrefixRule — шаблон БЕЗ wildcard: только точное совпадение.
//
// Это осознанное жёсткое правило, а не опечатка. Раньше здесь стояла
// проверка префикса по пробелу, и это была дыра: правило "bash(git): allow"
// разрешало не только "git", но и "git push --force". Человек писал одно
// намерение — «разрешить посмотреть статус», — а получал разрешение на всё
// подряд, и запретить точечно уже было нечем.
//
// За расширенный набор пишут шаблон явно: "bash(git *): allow".
// Единственное исключение — завершающий слэш: "edit(src/)" читается как
// «всё внутри каталога», и там префикс действительно то, что нужно.
func matchPrefixRule(p, s string) bool {
	if strings.HasSuffix(p, "/") {
		return strings.HasPrefix(s, p)
	}
	return p == s
}

// globMatch — поиск с * и ?. Жадный бэктрекинг, входы короткие.
func globMatch(pattern, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]):
			pi++
			si++
		case pi < len(pattern) && pattern[pi] == '*':
			// «**» и «*» для нашей цели одинаковы: граница пути уже
			// нормализована, а различать их в правилах разрешений
			// незачем — это путает того, кто правит конфиг.
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// normGlobPath — привести разделители пути к одному виду.
func normGlobPath(s string) string {
	if strings.Contains(s, `\`) && !strings.Contains(s, "/") {
		return strings.ReplaceAll(s, `\`, "/")
	}
	return s
}

// ---------- Хранение правил в конфиге ----------

// PermissionCfg — блок правил в config.json и gcli.json.
type PermissionCfg struct {
	// Rules — компактные строки "bash(git *): allow".
	Rules []string `json:"rules,omitempty"`
	// Files — расширенный вид объектами, когда нужно поле Source.
	Files []Rule `json:"files,omitempty"`
	// Mode — режим по умолчанию для инструментов без правила.
	// Пустая строка = спросить (старое поведение).
	Mode Permission `json:"mode,omitempty"`
}

// AddTo — разобрать строки и добавить в список правил.
//
// Ошибка НЕ проглатывается: битое правило молча выпало бы из списка, и
// человек увидел бы, что deny «не работает», не имея понятия почему.
// Хорошие правила при этом всё равно добавляются — одна сломанная строка
// не должна обнулять остальные. Вызывающий показывает ошибку пользователю.
func (p *PermissionCfg) AddTo(rs *Rules) error {
	if p == nil {
		return nil
	}
	rs.SetDefault(p.Mode)
	var errs []error
	for i, line := range p.Rules {
		r, err := parseRule(line)
		if err != nil {
			errs = append(errs, fmt.Errorf("правило %d (%q): %w", i+1, line, err))
			continue
		}
		rs.Add(r)
	}
	for _, r := range p.Files {
		rs.Add(r)
	}
	return errors.Join(errs...)
}

// RuleSources — собрать правила из слоёв конфигов.
//
// Аргументы идут ОТ НИЗШЕГО приоритета К ВЫСШЕМУ, и последнее совпавшее
// правило выигрывает. Порядок выбран в сторону читаемости: список слоёв
// можно предъявить пользователю («сначала проект, потом ваш конфиг»), и
// он будет в том же порядке, в котором реально применяются правила.
//
// Обратный порядок внутри функции означал бы, что приоритет слоёв
// приходится держать в голове при каждом вызове, и ошибка в одну
// сторону отдавала бы приоритет файлу из репозитория.
func RuleSources(layers ...*PermissionCfg) (Rules, error) {
	var (
		rs   Rules
		errs []error
	)
	for _, l := range layers {
		if err := l.AddTo(&rs); err != nil {
			errs = append(errs, err)
		}
	}
	return rs, errors.Join(errs...)
}

// ToolsForPermission — имя инструмента для правил разрешений.
//
// Правило пишется по имени инструмента, но подтверждение приходит по
// роду операции (write/exec/net). Это отображение нужно в обоих
// направлениях: чтобы правило "edit" ловило и edit_file, и write_file,
// и чтобы подтверждение знало, какое правило к нему относится.
func ToolsForPermission(kind string) []string {
	switch kind {
	case "write":
		return []string{"edit", "edit_file", "write_file", "multi_edit", "apply_patch", "patch"}
	case "exec":
		return []string{"bash", "job", "shell"}
	case "net":
		return []string{"web_fetch", "web_search", "webfetch", "fetch", "http", "api"}
	}
	return []string{kind}
}

// MatchesAnyTool — подходит ли правило под любой инструмент группы.
func (r Rule) MatchesAnyTool(tools []string) bool {
	for _, t := range tools {
		if r.Match(t, "") {
			return true
		}
	}
	return false
}

// GroupForTool — группа операций, в которую входит инструмент.
//
// Нужна там, где решение спрашивается по конкретному имени инструмента
// (например, /permissions test edit_file ...), а правило написано для
// группы ("edit(src/**)"). Без этого отладочная команда врала бы: агент
// по тому же действию решение получает, а проверка — нет.
func GroupForTool(tool string) ([]string, bool) {
	t := strings.ToLower(strings.TrimSpace(tool))
	for _, kind := range []string{"write", "exec", "net"} {
		for _, name := range ToolsForPermission(kind) {
			if strings.EqualFold(name, t) {
				return ToolsForPermission(kind), true
			}
		}
	}
	return nil, false
}

// SortedToolNames — имена инструментов по алфавиту (для вывода).
func SortedToolNames(tools []string) []string {
	out := append([]string(nil), tools...)
	sort.Strings(out)
	return out
}

// JoinTools — имена инструментов одной строкой, для подсказок.
func JoinTools(tools []string) string { return strings.Join(tools, ", ") }

// PatternForPath — привести путь к виду, который понимают правила.
//
// Правило пишется от проекта (edit(src/**)), а подтверждение получает
// абсолютный путь. Без приведения к виду относительного пути правило
// никогда не сработало бы: /home/u/proj/src/a.go не начинается с src/.
func PatternForPath(workDir, path string) string {
	if workDir != "" {
		if rel, err := filepath.Rel(workDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}
