package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gcli/core"
)

// Memory — память проекта: файлы GCLI.md, которые подмешиваются в промпт.
type Memory struct {
	workDir string
	store   *core.Store
}

// NewMemory — создать менеджер памяти проекта.
func NewMemory(workDir string, store *core.Store) *Memory {
	return &Memory{workDir: workDir, store: store}
}

// memoryNames — имена файлов памяти (в порядке приоритета).
var memoryNames = []string{"GCLI.md", "gcli.md", "CLAUDE.md", "AGENTS.md"}

// Files — найти файлы памяти: проектные + глобальный.
func (m *Memory) Files() []string {
	var out []string
	for _, name := range memoryNames {
		p := filepath.Join(m.workDir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
			break
		}
	}
	if gp := m.store.GlobalMemory(); gp != "" {
		if st, err := os.Stat(gp); err == nil && !st.IsDir() {
			out = append(out, gp)
		}
	}
	return out
}

// Collect — содержимое памяти для системного промпта.
func (m *Memory) Collect() string {
	files := m.Files()
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	total := 0
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := string(data)
		if len(s) > 24*1024 {
			s = core.TruncateUTF8(s, 24*1024, 0)
		}
		total += len(s)
		if total > 64*1024 {
			break
		}
		label := "проекта"
		if strings.HasPrefix(p, m.store.Root) {
			label = "глобальная"
		}
		fmt.Fprintf(&b, "\n## Память %s (%s)\n\n%s\n", label, core.RelToWD(m.workDir, p), s)
	}
	return strings.TrimSpace(b.String())
}

const memoryTemplate = `# GCLI.md — память проекта для gcli

> Этот файл подмешивается в системный промпт агента в каждой сессии.
> Заполни разделы — агент будет учитывать их при работе с кодом.

## О проекте
- Назначение: <что это за проект>
- Стек: <языки, фреймворки, версии>

## Команды
- Сборка: <команда сборки>
- Тесты: <команда запуска тестов>
- Запуск: <как запустить>

## Структура
- <каталог>: <что в нём>

## Соглашения
- Стиль кода: <правила именования, форматирование>
- Тесты: <как их писать>

## Важно
- Не изменять без спроса: <файлы/модули>
- Особенности: <подводные камни, зависимости>
`

// MemoryTemplate — шаблон GCLI.md.
func MemoryTemplate() string { return memoryTemplate }

// ProjectFile — путь к файловому проектному файлу памяти: существующий
// (по приоритету имён) или GCLI.md, который будет создан.
func (m *Memory) ProjectFile() string {
	for _, name := range memoryNames {
		p := filepath.Join(m.workDir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return filepath.Join(m.workDir, "GCLI.md")
}

// AppendFact — дописать факт «#текст» в память проекта. Файл создаётся
// с шаблоном, если его ещё нет. Возвращает путь и ошибку.
func (m *Memory) AppendFact(text string) (string, error) {
	path := m.ProjectFile()
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, []byte(memoryTemplate), 0o644); err != nil {
			return path, err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return path, err
	}
	defer f.Close()
	// Факт идёт отдельным пунктом списка в конец файла — раздел «Важно»
	// пользователь при желании перенесёт сам.
	_, err = fmt.Fprintf(f, "- %s\n", strings.TrimSpace(text))
	return path, err
}

// reMention — @упоминание файла.
var reMention = regexp.MustCompile(`(?:^|\s)@([^\s@]+)`)

// ApplyMentions — раскрыть @файл в тексте пользователя.
func ApplyMentions(workDir, text string) string {
	ms := reMention.FindAllStringSubmatch(text, -1)
	if len(ms) == 0 {
		return text
	}
	seen := map[string]bool{}
	var blocks []string
	total, files := 0, 0
	for _, mm := range ms {
		if files >= 8 {
			blocks = append(blocks, "[лимит: не более 8 приложенных файлов]")
			break
		}
		ref := strings.TrimRight(mm[1], ".,;:!?)]}\"'")
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		p := core.AbsPath(workDir, ref)
		info, err := os.Stat(p)
		if err != nil {
			blocks = append(blocks, fmt.Sprintf("@%s: файл не найден", ref))
			continue
		}
		if info.IsDir() {
			blocks = append(blocks, fmt.Sprintf("@%s: это каталог — посмотри через list_dir", ref))
			continue
		}
		if info.Size() > 120*1024 {
			blocks = append(blocks, fmt.Sprintf("@%s: файл большой (%s) — прочитай часть через read_file", ref, core.HumanSize(int(info.Size()))))
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			blocks = append(blocks, fmt.Sprintf("@%s: не читается (%v)", ref, err))
			continue
		}
		if core.IsBinary(data) {
			blocks = append(blocks, fmt.Sprintf("@%s: двоичный файл — не приложен", ref))
			continue
		}
		if total+len(data) > 400*1024 {
			blocks = append(blocks, "[достигнут лимит объёма приложений — 400 КБ]")
			break
		}
		total += len(data)
		files++
		blocks = append(blocks, fmt.Sprintf("<file path=\"%s\">\n%s\n</file>",
			core.RelToWD(workDir, p), string(data)))
	}
	if files == 0 && len(blocks) == 0 {
		return text
	}
	return text + "\n\n--- Приложенные файлы (@упоминания) ---\n\n" + strings.Join(blocks, "\n\n")
}
