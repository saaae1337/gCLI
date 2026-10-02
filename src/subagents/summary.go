package subagents

import (
	"fmt"
	"strings"

	"gcli/core"
)

// Лимиты сводки отчёта.
const (
	// summaryMaxLines — сколько содержательных строк максимум.
	summaryMaxLines = 18
	// summaryMaxChars — потолок по символам, чтобы отчёт не съедал контекст
	// главного агента (он получит сводку, а полный текст — по запросу).
	summaryMaxChars = 1600
)

// Summarize — сжать полный отчёт субагента до нескольких строк,
// чтобы главный агент получил суть, а не простыню.
//
// Блоки кода вырезаются всегда (даже в коротком отчёте): код главному
// агенту не нужен, а токены стоят дорого.
//
// Важно: сохраняются И начало, И конец отчёта. Раньше оставались только
// первые строки, а выводы (последний раздел) терялись — для главного агента
// именно они и были главными.
func Summarize(full string) string {
	lines := core.SplitLines(full)
	if len(lines) == 0 {
		return ""
	}

	// Убираем блоки кода.
	var kept []string
	inFence := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		kept = append(kept, l)
	}
	lines = kept
	if len(lines) == 0 {
		return core.Truncate(core.OneLine(full), summaryMaxChars)
	}

	// Короткий отчёт отдаём целиком.
	if len(lines) <= 14 {
		return clampChars(strings.TrimSpace(strings.Join(lines, "\n")))
	}

	// Длинный: голова + хвост. Хвост обычно содержит выводы.
	var content []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		content = append(content, l)
	}
	if len(content) <= summaryMaxLines {
		return clampChars(strings.Join(content, "\n"))
	}
	head := summaryMaxLines * 2 / 3
	tail := summaryMaxLines - head
	out := make([]string, 0, summaryMaxLines+1)
	out = append(out, content[:head]...)
	out = append(out, fmt.Sprintf("… [%d строк пропущено] …", len(content)-head-tail))
	out = append(out, content[len(content)-tail:]...)
	return clampChars(strings.Join(out, "\n"))
}

// clampChars — ограничить сводку по длине, сохранив начало и конец.
func clampChars(s string) string {
	r := []rune(s)
	if len(r) <= summaryMaxChars {
		return s
	}
	head := summaryMaxChars / 2
	tail := summaryMaxChars - head
	return string(r[:head]) +
		fmt.Sprintf("\n…[сводка обрезана: пропущено %d символов]…\n", len(r)-head-tail) +
		string(r[len(r)-tail:])
}

// Notes — склеить список заметок в блок для промпта.
func Notes(list []string) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range list {
		b.WriteString("- " + n + "\n")
	}
	return strings.TrimSpace(b.String())
}
