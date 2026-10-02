package main

// Self-bench: gcli прогоняет набор задач на самом себе и печатает счёт.
//
// Зачем: улучшения агента — слепые, если не измерять. Bench-задача — это
// промпт + ожидаемый результат (файл существует, в файле есть текст,
// команда проходит). Прогон после каждого изменения цикла/промптов/модели
// превращает «кажется, стало лучше» в «проходит 7 из 8 задач, как и раньше».
//
// Как: каждая задача исполняется агентом в чистом временном каталоге
// (дочерний процесс gcli -p -json -yolo — изоляция, как у MCP-сервера),
// результат проверяется локально, без модели. Определения задач —
// .gcli/bench/*.json, JSONC разрешён: в задачах нужны комментарии.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gcli/core"
	"gcli/ui"
)

type benchContains struct {
	File string `json:"file"`
	Text string `json:"text"`
}

type benchExpect struct {
	FilesExist []string        `json:"files_exist"`
	Contains   []benchContains `json:"contains"`
	CommandOK  string          `json:"command_ok"`
}

type benchTask struct {
	Name       string      `json:"name"`
	Prompt     string      `json:"prompt"`
	Setup      []string    `json:"setup"`
	Expect     benchExpect `json:"expect"`
	TimeoutMin int         `json:"timeout_min"`
}

// benchDir — каталог задач по умолчанию.
func benchDir(workDir string) string { return filepath.Join(workDir, ".gcli", "bench") }

// loadBenchTasks — прочитать задачи из каталога (по алфавиту имён файлов:
// порядок прогона стабильный, сравнение прогонов между собой честное).
func loadBenchTasks(dir string) ([]benchTask, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".jsonc")) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var tasks []benchTask
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var t benchTask
		if err := json.Unmarshal([]byte(core.StripJSONC(data)), &t); err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		if strings.TrimSpace(t.Name) == "" {
			t.Name = strings.TrimSuffix(name, filepath.Ext(name))
		}
		if strings.TrimSpace(t.Prompt) == "" {
			return nil, fmt.Errorf("%s: пустой prompt", name)
		}
		e := t.Expect
		if len(e.FilesExist) == 0 && len(e.Contains) == 0 && strings.TrimSpace(e.CommandOK) == "" {
			return nil, fmt.Errorf("%s: не задан ни один критерий проверки", name)
		}
		if t.TimeoutMin <= 0 {
			t.TimeoutMin = 5
		}
		if t.TimeoutMin > 15 {
			t.TimeoutMin = 15
		}
		tasks = append(tasks, t)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("в %s нет задач (*.json)", dir)
	}
	return tasks, nil
}

// benchEvaluate — проверить ожидания: список упавших критериев.
// Пустой список = задача пройдена. Проверка локальная, модель не зовётся.
func benchEvaluate(wd string, e benchExpect) []string {
	var fails []string
	for _, f := range e.FilesExist {
		if _, err := os.Stat(filepath.Join(wd, f)); err != nil {
			fails = append(fails, "файл не создан: "+f)
		}
	}
	for _, c := range e.Contains {
		data, err := os.ReadFile(filepath.Join(wd, c.File))
		if err != nil {
			fails = append(fails, "файл не читается: "+c.File)
			continue
		}
		if !strings.Contains(string(data), c.Text) {
			fails = append(fails, fmt.Sprintf("в %s нет %q", c.File, core.Truncate(c.Text, 60)))
		}
	}
	if cmd := strings.TrimSpace(e.CommandOK); cmd != "" {
		if _, code, _ := watchRunCommand(wd, cmd, 2*time.Minute); code != 0 {
			fails = append(fails, "команда не прошла: "+cmd)
		}
	}
	return fails
}

// runBench — прогнать все задачи. Возврат: были ли провалы (для exit-кода CI).
func (a *app) runBench(dir string) bool {
	tasks, err := loadBenchTasks(dir)
	if err != nil {
		a.ui.Err("bench: " + err.Error())
		return true
	}
	a.ui.Title("Self-bench — задач: " + strconv.Itoa(len(tasks)))
	a.ui.Info("каждая задача исполняется агентом в чистом временном каталоге (gcli -p -json -yolo)")

	passed := 0
	type row struct {
		n    int
		name string
		ok   bool
		note string
	}
	rows := make([]row, 0, len(tasks))
	for i, t := range tasks {
		wd, err := os.MkdirTemp("", "gcli-bench-")
		if err != nil {
			a.ui.Err("bench: временный каталог: " + err.Error())
			return true
		}
		// Чистый каталог после прогона не нужен: задача уже проверена.
		defer os.RemoveAll(wd)

		note := ""
		for _, cmd := range t.Setup {
			out, code, _ := watchRunCommand(wd, cmd, 2*time.Minute)
			if code != 0 {
				note = "setup упал: " + core.Truncate(core.OneLine(out), 80)
				break
			}
		}
		if note == "" {
			timeout := time.Duration(t.TimeoutMin) * time.Minute
			if _, err := mcpSrvPrompt(wd, t.Prompt, timeout); err != nil {
				note = core.Truncate(core.OneLine(err.Error()), 100)
			} else if fails := benchEvaluate(wd, t.Expect); len(fails) > 0 {
				note = strings.Join(fails, "; ")
			}
		}
		ok := note == ""
		if ok {
			passed++
		}
		rows = append(rows, row{n: i + 1, name: t.Name, ok: ok, note: note})
		if !a.quiet {
			if ok {
				fmt.Printf("[%d/%d] ok   %s\n", i+1, len(tasks), t.Name)
			} else {
				fmt.Printf("[%d/%d] FAIL %s — %s\n", i+1, len(tasks), t.Name, note)
			}
		}
	}

	tableRows := make([][]string, 0, len(rows))
	for _, r := range rows {
		result := "ok"
		if !r.ok {
			result = "FAIL"
		}
		tableRows = append(tableRows, []string{
			strconv.Itoa(r.n), core.Truncate(r.name, 30), result, core.Truncate(r.note, 44),
		})
	}
	a.ui.Table([]ui.Column{
		{Title: "#", Width: 3, Right: true}, {Title: "задача", Width: 32},
		{Title: "результат", Width: 9}, {Title: "детали", Width: 46},
	}, tableRows, ui.BlockOpts{Title: "Self-bench"})

	a.ui.KVPairs([][2]string{
		{"итог", fmt.Sprintf("%d из %d задач", passed, len(tasks))},
	})
	return passed < len(tasks)
}
