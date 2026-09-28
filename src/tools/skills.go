package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gcli/core"
)

// Skill — подключаемый навык агента (markdown с front matter).
type Skill struct {
	Name  string
	Desc  string
	Body  string
	Path  string
	Scope string // "проект" | "глобальный"
}

// SkillsDirs — каталоги навыков (проектные имеют приоритет).
func (r *Registry) SkillsDirs() [][2]string {
	return [][2]string{
		{filepath.Join(r.workDir, ".gcli", "skills"), "проект"},
		{filepath.Join(core.Home(), "skills"), "глобальный"},
	}
}

// reSkillName — валидное имя навыка.
var reSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,30}$`)

// ValidSkillName — проверка имени навыка.
func ValidSkillName(s string) bool { return reSkillName.MatchString(s) }

const skillTemplate = `---
name: %s
description: <краткое описание — когда агенту применять этот навык>
---

# %s

Инструкции для агента. Пиши конкретно: шаги, правила, примеры команд,
чего избегать. Навык загружается инструментом load_skill по запросу модели.
`

// ParseSkill — разобрать markdown-файл навыка.
func ParseSkill(p, scope string) (Skill, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return Skill{}, err
	}
	s := Skill{Path: p, Scope: scope}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.HasPrefix(text, "---") {
		if end := strings.Index(text[3:], "\n---"); end >= 0 {
			fm := text[3 : 3+end]
			s.Body = strings.TrimSpace(text[3+end+4:])
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if v, ok := strings.CutPrefix(line, "name:"); ok {
					s.Name = strings.TrimSpace(strings.Trim(v, `"'`))
				}
				if v, ok := strings.CutPrefix(line, "description:"); ok {
					s.Desc = strings.TrimSpace(strings.Trim(v, `"'`))
				}
			}
		}
	}
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filepath.Base(p), ".md")
	}
	if s.Body == "" {
		s.Body = strings.TrimSpace(text)
	}
	if s.Desc == "" {
		// Первая непустая строка без заголовка — описание.
		for _, l := range strings.Split(s.Body, "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				s.Desc = core.Truncate(core.OneLine(l), 90)
				break
			}
		}
	}
	return s, nil
}

// LoadSkills — все навыки: проектные (приоритет), глобальные и встроенные.
// Пользовательский навык с тем же именем перекрывает встроенный: можно
// подменить поведение builtin-навыка, не дожидаясь обновления gcli.
func (r *Registry) LoadSkills() []Skill {
	var out []Skill
	seen := map[string]bool{}
	for _, d := range r.SkillsDirs() {
		ents, err := os.ReadDir(d[0])
		if err != nil {
			continue
		}
		var files []string
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				files = append(files, filepath.Join(d[0], e.Name()))
			}
		}
		sort.Strings(files)
		for _, f := range files {
			s, err := ParseSkill(f, d[1])
			if err != nil || s.Name == "" || seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			out = append(out, s)
		}
	}
	out = append(out, loadBuiltinSkills(seen)...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SkillOff — навык выключен пользователем.
func (r *Registry) SkillOff(name string) bool {
	for _, n := range r.skillsOff {
		if n == name {
			return true
		}
	}
	return false
}

// SetSkillsOff — задать список выключенных навыков.
func (r *Registry) SetSkillsOff(names []string) { r.skillsOff = names }

// SkillsOffList — список выключенных навыков.
func (r *Registry) SkillsOffList() []string { return r.skillsOff }

// maxSkillDesc — предел длины описания навыка в системном промпте.
//
// Раньше стоял 110, и описание обрезалось ровно на полуслове «Применять,
// когда…» — то есть модель не видела ни одного триггера, ради которых
// навык и грузится. Описание целиком идёт в контекст всегда, поэтому и
// должно быть коротким, а не обрезанным: режет его автор навыка.
// 320 — с запасом на нормальный текст в две строки.
const maxSkillDesc = 320

// SkillsPromptBlock — блок со списком навыков для системного промпта.
func (r *Registry) SkillsPromptBlock() string {
	skills := r.LoadSkills()
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Навыки (skills)\n")
	b.WriteString("Подключаемые навыки. Если задача попадает под описание навыка — сначала вызови инструмент load_skill с его name и следуй инструкциям.\n")
	n := 0
	for _, s := range skills {
		if r.SkillOff(s.Name) {
			continue
		}
		fmt.Fprintf(&b, "- %s — %s\n", s.Name, skillDesc(s.Desc))
		n++
		if n >= 20 {
			break
		}
	}
	if n == 0 {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}

// skillDesc — описание навыка для промпта.
//
// Многострочные описания склеиваются в одну строку: иначе список навыков
// расползается по нескольку строк на навык и промпт разрастается.
// Обрезка — только аварийная, на случай описания в тысячу символов.
func skillDesc(d string) string {
	d = core.Truncate(core.OneLine(d), maxSkillDesc)
	if len([]rune(d)) >= maxSkillDesc {
		d = strings.TrimRight(d, " ,.;:") + " …"
	}
	return d
}

const schemaLoadSkill = `{"type":"object","properties":{"name":{"type":"string","description":"Имя навыка из списка skills в системном промпте"}},"required":["name"]}`

// RegisterSkills — зарегистрировать инструмент load_skill.
func (r *Registry) RegisterSkills() {
	r.register("load_skill", "Загрузить навык (skill) из библиотеки пользователя: полный текст инструкций по имени. Список доступных навыков — в системном промпте и в /skills.",
		schemaLoadSkill, "read", false, r.hLoadSkill)
}

// hLoadSkill — отдать модели полный текст навыка.
func (r *Registry) hLoadSkill(_ context.Context, m map[string]any) (Result, error) {
	name := ArgStr(m, "name")
	if name == "" {
		return Result{}, fmt.Errorf("укажи name навыка")
	}
	for _, s := range r.LoadSkills() {
		if s.Name == name {
			if r.SkillOff(name) {
				return Result{}, fmt.Errorf("навык «%s» выключен — включи: /skill on %s", name, name)
			}
			return Result{
				Text:    fmt.Sprintf("Навык: %s (%s, %s)\n\n%s", s.Name, s.Scope, s.Path, s.Body),
				Summary: "навык " + s.Name,
			}, nil
		}
	}
	names := r.SkillNames()
	hint := ""
	if len(names) > 0 {
		hint = " доступные: " + strings.Join(names, ", ")
	}
	return Result{}, fmt.Errorf("навык «%s» не найден%s", name, hint)
}

// SkillNames — имена включённых навыков.
func (r *Registry) SkillNames() []string {
	var out []string
	for _, s := range r.LoadSkills() {
		if !r.SkillOff(s.Name) {
			out = append(out, s.Name)
		}
	}
	return out
}

// SkillTemplate — шаблон нового навыка.
func SkillTemplate(name string) string { return fmt.Sprintf(skillTemplate, name, name) }
