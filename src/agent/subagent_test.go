package agent

import (
	"testing"
)

// Тест на главный дефект: субагент возвращал обрывок рассуждения
// («Let me get the remaining pieces…») вместо отчёта.
func TestIsNarrativeOnly(t *testing.T) {
	// Случаи, которые раньше уходили наверх как «результат».
	narrative := []string{
		"",
		"   ",
		"(пусто)",
		"Let me get the remaining pieces.",
		"Хорошо, теперь посмотрю остальное.",
		"Теперь надо проверить ещё пару файлов",
	}
	for _, s := range narrative {
		if !isNarrativeOnly(s) {
			t.Errorf("обрывок рассуждения не распознан как сбой: %q", s)
		}
	}

	// Нормальные отчёты — со структурой, фактами, ссылками на файлы.
	reports := []string{
		"## Найдено\n- src/tools/subagent.go:27 — регистрация\n- src/subagents/pool.go:169 — запуск",
		"Отчёт: реализовано 3 правки в pool.go и 1 тест.",
		"## Итог\nКод готов, осталось проверить сборку.",
	}
	for _, s := range reports {
		if isNarrativeOnly(s) {
			t.Errorf("нормальный отчёт отброшен как нарратив: %q", s)
		}
	}
}

func TestSplitNotes(t *testing.T) {
	got := splitNotes("- первый\n- * второй\n• третий\n\n- четвёртый")
	want := []string{"первый", "второй", "третий", "четвёртый"}
	if len(got) != len(want) {
		t.Fatalf("получено %d заметок: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("заметка %d = %q, ожидалось %q", i, got[i], want[i])
		}
	}
}

func TestLastAssistantText(t *testing.T) {
	// Берётся последнее содержательное сообщение ассистента, а не инструмента.
	if got := lastAssistantText(nil); got != "" {
		t.Errorf("ожидалась пустая строка, получено %q", got)
	}
}
