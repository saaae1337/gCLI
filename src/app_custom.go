package main

import (
	"os"
	"path/filepath"
	"strings"

	"gcli/core"
)

// ---------- Кастомные slash-команды ----------
//
// В духе Claude Code: положи Markdown-файл в ./.gcli/commands/ (для
// проекта) или ~/.gcli/commands/ (для себя) — и он станет slash-командой.
//
//      .gcli/commands/commit.md        →  /commit  и  /project:commit
//      ~/.gcli/commands/review.md      →  /review  и  /user:review
//
// Содержимое файла — промпт, который уйдёт модели вместо имени команды.
// Подстановка:
//
//      $ARGUMENTS   — всё, что пользователь написал после команды
//
// Файл может начинаться с frontmatter:
//
//      ---
//      description: закоммитить изменения
//      ---
//
// Описание показывается в /help. Всё остальное считается телом промпта.

// customCommand — одна загруженная команда.
type customCommand struct {
	Name        string // commit, review, ...
	Scope       string // project | user
	Description string
	Body        string
}

// loadCustomCommands — прочитать команды из обоих каталогов.
//
// Проектные перекрывают юзерские с тем же именем: проект — более
// конкретная инструкция, и конфликт разрешается в его пользу.
func (a *app) loadCustomCommands() []customCommand {
	dirs := []struct {
		Dir   string
		Scope string
	}{
		{filepath.Join(a.workDir, ".gcli", "commands"), "project"},
		{filepath.Join(core.Home(), "commands"), "user"},
	}
	seen := map[string]int{}
	out := make([]customCommand, 0, 8)
	for _, d := range dirs {
		entries, err := os.ReadDir(d.Dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				continue
			}
			name := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))), " ", "-"))
			if name == "" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(d.Dir, e.Name()))
			if err != nil {
				continue
			}
			cmd := parseCustomCommand(string(data))
			cmd.Name = name
			cmd.Scope = d.Scope
			// Проектная команда с тем же именем вытесняет юзерскую.
			if i, ok := seen[name]; ok && out[i].Scope == "project" {
				continue
			}
			seen[name] = len(out)
			out = append(out, cmd)
		}
	}
	return out
}

// parseCustomCommand — отделить frontmatter и описание от тела промпта.
func parseCustomCommand(raw string) customCommand {
	body := strings.TrimPrefix(raw, "\ufeff")
	desc := ""
	if strings.HasPrefix(body, "---") {
		if end := strings.Index(body[3:], "\n---"); end >= 0 {
			head := body[3 : 3+end]
			rest := body[3+end+4:]
			rest = strings.TrimPrefix(rest, "\n")
			for _, line := range strings.Split(head, "\n") {
				k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
				if !ok {
					continue
				}
				if strings.EqualFold(strings.TrimSpace(k), "description") {
					desc = strings.TrimSpace(v)
				}
			}
			body = rest
		}
	}
	return customCommand{Description: desc, Body: strings.TrimSpace(body)}
}

// findCustomCommand — найти команду по имени (без слэша), поддерживая
// префиксы области: /project:commit, /user:review.
func (a *app) findCustomCommand(name string) (customCommand, bool) {
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	scope := ""
	switch {
	case strings.HasPrefix(name, "project:"):
		scope, name = "project", strings.TrimPrefix(name, "project:")
	case strings.HasPrefix(name, "user:"):
		scope, name = "user", strings.TrimPrefix(name, "user:")
	}
	for _, c := range a.loadCustomCommands() {
		if c.Name != name {
			continue
		}
		if scope != "" && c.Scope != scope {
			continue
		}
		return c, true
	}
	return customCommand{}, false
}

// runCustomCommand — развернуть промпт команды и выполнить как обычный ход.
func (a *app) runCustomCommand(cmd customCommand, args string) {
	body := strings.ReplaceAll(cmd.Body, "$ARGUMENTS", strings.TrimSpace(args))
	if strings.TrimSpace(body) == "" {
		a.ui.Warn("команда «/" + cmd.Name + "» пуста — добавь промпт в файл")
		return
	}
	if !a.quiet {
		label := "/" + cmd.Name
		if cmd.Description != "" {
			label += " — " + cmd.Description
		}
		a.ui.Println("  " + a.ui.Accent(a.ui.Glyphs().Star) + " " + a.ui.Gray(label))
	}
	if err := a.turn(body); err != nil && !a.quiet {
		a.ui.Err(err.Error())
	}
}

// customHelpRows — строки для /help: какие команды доступны.
func (a *app) customHelpRows() [][2]string {
	cmds := a.loadCustomCommands()
	rows := make([][2]string, 0, len(cmds))
	for _, c := range cmds {
		desc := c.Description
		if desc == "" {
			desc = "кастомная команда (" + c.Scope + ")"
		}
		rows = append(rows, [2]string{"/" + c.Name, desc})
	}
	return rows
}
