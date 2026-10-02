package subagents

import (
	"strings"
	"testing"
)

func TestSummarizeKeepsHeadAndTail(t *testing.T) {
	// Длинный отчёт: выводы в конце терялись при старой реализации.
	var b strings.Builder
	b.WriteString("## Найдено\n")
	for i := 0; i < 40; i++ {
		b.WriteString("- пункт промежуточный номер ")
		b.WriteString(strings.Repeat("x", 10))
		b.WriteString("\n")
	}
	b.WriteString("## Вывод\n- главный вывод задачи в самом конце\n")
	full := b.String()

	got := Summarize(full)

	if !strings.Contains(got, "Найдено") {
		t.Errorf("потеряно начало отчёта:\n%s", got)
	}
	// Главное: хвост (выводы) обязан выжить.
	if !strings.Contains(got, "главный вывод задачи в самом конце") {
		t.Errorf("потеряны выводы в конце отчёта:\n%s", got)
	}
	if !strings.Contains(got, "пропущено") {
		t.Errorf("нет пометки о пропуске:\n%s", got)
	}
}

func TestSummarizeRespectsCharLimit(t *testing.T) {
	// Даже короткий по строкам отчёт ограничен по символам.
	long := strings.Repeat("абвгд ", 3000)
	got := Summarize(long)
	if n := len([]rune(got)); n > summaryMaxChars+120 {
		t.Errorf("сводка не ограничена по символам: %d", n)
	}
	if !strings.Contains(got, "обрезана") {
		t.Errorf("нет пометки об обрезке: %q", head(got))
	}
}

// head — укоротить текст для сообщения об ошибке.
func head(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func TestSummarizeShortReport(t *testing.T) {
	got := Summarize("## Найдено\n- файл:строка — что там")
	if got != "## Найдено\n- файл:строка — что там" {
		t.Errorf("короткий отчёт искажён: %q", got)
	}
}

func TestCustomTypeCannotSpawnSubagents(t *testing.T) {
	// custom без ограничений получал spawn_agent с Depth=0 → рекурсия.
	_, deny := ToolsFor(TypeCustom)
	if !contains(deny, "spawn_agent") {
		t.Errorf("тип custom должен запрещать вложенных субагентов: %v", deny)
	}
}

func TestAllTypesDenyNestedSpawn(t *testing.T) {
	for _, tp := range Types {
		_, deny := ToolsFor(tp)
		if !contains(deny, "spawn_agent") {
			t.Errorf("тип %s допускает вложенный spawn_agent", tp)
		}
	}
}

func TestToolsForDenyDoesNotOverrestrictCoder(t *testing.T) {
	// coder должен писать файлы, но не порождать субагентов.
	allow, deny := ToolsFor(TypeCoder)
	if contains(deny, "write_file") || contains(deny, "edit_file") {
		t.Errorf("coder не должен терять инструменты записи: %v", deny)
	}
	if len(allow) != 0 {
		t.Errorf("у coder белый список должен быть пустым (всё, кроме вложенных): %v", allow)
	}
}
