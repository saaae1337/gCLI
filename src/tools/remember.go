package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gcli/core"
)

// ---------- Долговременная память ----------
//
// task_note живёт в пределах сессии: он попадает в промпт субагентов и в
// /export, но исчезает вместе с сессией. Знание, до которого агент сам
// дошёл в ходе работы (как устроен проект, какая команда собирает, где
// грабли), должно переживать сессию — иначе каждый новый запуск начинается
// с нуля, и агент месяцами rediscover'ит одно и то же.
//
// Формат: ~/.gcli/memory.md, строки вида
//
//	- 2026-09-29 сборка: ./build.sh one windows amd64  # факт
//
// Файл вырос — старые записи сворачиваются в «архив» с пометкой даты,
// свежие остаются. Дубликаты по нормализованному тексту не добавляются.

// maxMemoryLines — сколько записей держим в активной части.
const maxMemoryLines = 60

// memoryBlockStart / memoryBlockEnd — маркеры машинных секций в памяти.
const (
	memoryBlockStart = "<!-- gcli:memory -->"
	memoryBlockEnd   = "<!-- /gcli:memory -->"
)

// reMemoryLine — строка записи памяти.
var reMemoryLine = regexp.MustCompile(`^- (\d{4}-\d{2}-\d{2}) (.+?)(?:\s+#\s*(.*))?$`)

// MemoryFile — путь к файлу долговременной памяти.
func MemoryFile() string {
	return filepath.Join(core.Home(), "memory.md")
}

// ReadMemory — прочитать записи долговременной памяти.
func ReadMemory() []MemoryEntry {
	data, err := os.ReadFile(MemoryFile())
	if err != nil {
		return nil
	}
	return ParseMemory(string(data))
}

// MemoryEntry — одна запись долговременной памяти.
type MemoryEntry struct {
	Date string // 2026-09-29
	Fact string // нормализованный текст факта
	Note string // необязательный комментарий после #
}

// ParseMemory — разобрать содержимое файла памяти.
func ParseMemory(s string) []MemoryEntry {
	var out []MemoryEntry
	for _, line := range core.SplitLines(s) {
		m := reMemoryLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		out = append(out, MemoryEntry{Date: m[1], Fact: strings.TrimSpace(m[2]), Note: strings.TrimSpace(m[3])})
	}
	return out
}

// RenderMemory — собрать содержимое файла памяти из записей.
func RenderMemory(entries []MemoryEntry) string {
	var b strings.Builder
	b.WriteString("# Память Джикли — что я узнал о себе и проектах\n")
	b.WriteString("\nЭтот файл пишет сам агент (инструмент remember). Он подмешивается\n")
	b.WriteString("в системный промпт в следующих сессиях. Правь вручную, если нужно.\n")
	for _, e := range entries {
		if e.Note != "" {
			fmt.Fprintf(&b, "- %s %s  # %s\n", e.Date, e.Fact, e.Note)
		} else {
			fmt.Fprintf(&b, "- %s %s\n", e.Date, e.Fact)
		}
	}
	return b.String()
}

// normFact — нормализовать текст факта для сравнения дубликатов.
func normFact(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, ".;,!—- ")
}

// AddMemory — записать факт в долговременную память.
//
// Возвращает текст ответа для модели: что записано, что было уже записано
// раньше (дубликат не плодится), сколько всего записей.
func AddMemory(fact, note string) (string, error) {
	fact = core.Truncate(core.OneLine(fact), 300)
	if fact == "" {
		return "", fmt.Errorf("пустой факт")
	}
	note = core.Truncate(core.OneLine(note), 120)
	date := time.Now().Format("2006-01-02")

	path := MemoryFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	entries := ReadMemory()
	// Дедупликация: тот же факт с тем же комментарием — уже не пишем.
	dup := ""
	for _, e := range entries {
		if normFact(e.Fact) == normFact(fact) {
			dup = e.Date
			break
		}
	}
	if dup != "" {
		// Обновляем комментарий, если он изменился, и двигаем запись вверх.
		return fmt.Sprintf("Факт уже был записан %s — повторно не добавляю.\n- %s %s", dup, date, fact),
			writeMemory(entries)
	}

	entries = append([]MemoryEntry{{Date: date, Fact: fact, Note: note}}, entries...)
	if err := writeMemory(entries); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Записано в долговременную память (%s): «%s». Всего записей: %d.\n"+
		"Оно будет в контексте и в следующих сессиях.", path, fact, len(entries))
	return msg, nil
}

// writeMemory — записать записи, свернув старые при превышении лимита.
func writeMemory(entries []MemoryEntry) error {
	if len(entries) > maxMemoryLines {
		// Свернутые записи не выбрасываем, а прячем в архив: знание не должно
		// пропадать бесследно, но и в промпт попадать не должно.
		old := entries[maxMemoryLines:]
		entries = entries[:maxMemoryLines]
		var b strings.Builder
		b.WriteString(RenderMemory(entries))
		b.WriteString("\n## Архив (свёрнуто автоматически, в промпт не подмешивается)\n")
		for _, e := range old {
			if e.Note != "" {
				fmt.Fprintf(&b, "- %s %s  # %s\n", e.Date, e.Fact, e.Note)
			} else {
				fmt.Fprintf(&b, "- %s %s\n", e.Date, e.Fact)
			}
		}
		return core.WriteAtomic(MemoryFile(), []byte(b.String()), 0o644)
	}
	return core.WriteAtomic(MemoryFile(), []byte(RenderMemory(entries)), 0o644)
}

// MemoryDigest — краткая выжимка памяти для системного промпта.
//
// Берём только «крепкие» записи (с комментарием — их агент пометил как
// важные) плюс самые свежие, иначе промпт разрастается и вытесняет задачу.
func MemoryDigest(maxItems int) string {
	entries := ReadMemory()
	if len(entries) == 0 {
		return ""
	}
	pinned := 0
	for _, e := range entries {
		if e.Note != "" {
			pinned++
		}
	}
	budget := maxItems / 2
	if budget < 3 {
		budget = 3
	}

	var b strings.Builder
	b.WriteString(memoryBlockStart + "\n")
	b.WriteString("## Что я уже знаю (долговременная память)\n\n")
	b.WriteString("Записано мной в прошлых сессиях. Не проверяй заново, но и не верь слепа,\n")
	b.WriteString("если факт противоречит тому, что видишь в коде.\n\n")

	n := 0
	for _, e := range entries {
		if n >= budget {
			break
		}
		if e.Note == "" {
			continue
		}
		if e.Note != "" {
			fmt.Fprintf(&b, "- %s  # %s\n", e.Fact, e.Note)
		}
		n++
	}
	if n > 0 {
		b.WriteString("\n")
	}
	fresh := 0
	for _, e := range entries {
		if fresh >= budget || fresh+n >= maxItems {
			break
		}
		if e.Note != "" {
			continue
		}
		fmt.Fprintf(&b, "- [%s] %s\n", e.Date, e.Fact)
		fresh++
	}
	b.WriteString("\n" + memoryBlockEnd)
	out := strings.TrimSpace(b.String())
	if n == 0 && fresh == 0 {
		return ""
	}
	return out
}

// ForgetMemory — убрать факты, содержащие подстроку. Пусто = ничего не убрано.
func ForgetMemory(query string) (int, error) {
	q := normFact(query)
	if q == "" {
		return 0, fmt.Errorf("укажи, что именно забыть (подстрока факта)")
	}
	entries := ReadMemory()
	kept := make([]MemoryEntry, 0, len(entries))
	removed := 0
	for _, e := range entries {
		if strings.Contains(normFact(e.Fact), q) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := writeMemory(kept); err != nil {
		return 0, err
	}
	return removed, nil
}

// hRemember — долговременная память: записать, забыть, прочитать.
func (r *Registry) hRemember(_ context.Context, m map[string]any) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(ArgStr(m, "action")))
	if action == "" {
		action = "write"
	}
	switch action {
	case "write", "запомнить", "add":
		msg, err := AddMemory(ArgStr(m, "fact"), ArgStr(m, "note"))
		if err != nil {
			return Result{}, err
		}
		return Result{Text: msg, Summary: "память: записано"}, nil

	case "forget", "забыть", "rm":
		n, err := ForgetMemory(ArgStr(m, "fact"))
		if err != nil {
			return Result{}, err
		}
		if n == 0 {
			return Result{
				Text:    "Ничего не нашлось по этому запросу. Память не тронута.",
				Summary: "память: не найдено",
			}, nil
		}
		return Result{Text: fmt.Sprintf("Забыто записей: %d.", n), Summary: "память: забыто"}, nil

	case "read", "list", "прочитать", "":
		entries := ReadMemory()
		if len(entries) == 0 {
			return Result{
				Text:    "Долговременная память пуста. Записывай то, что дорого обошлось узнать: команды сборки, грабли, устройство проекта.",
				Summary: "память: пусто",
			}, nil
		}
		var b strings.Builder
		for _, e := range entries {
			if e.Note != "" {
				fmt.Fprintf(&b, "- %s %s  # %s\n", e.Date, e.Fact, e.Note)
			} else {
				fmt.Fprintf(&b, "- %s %s\n", e.Date, e.Fact)
			}
		}
		return Result{Text: fmt.Sprintf("Память (%d записей):\n%s", len(entries), b.String()),
			Summary: fmt.Sprintf("память: %d записей", len(entries))}, nil
	}
	return Result{}, fmt.Errorf("неизвестное действие %q: используй write, forget или read", action)
}
