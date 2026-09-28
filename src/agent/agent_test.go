package agent

import (
	"strings"
	"testing"

	"gcli/subagents"
	"gcli/tools"
)

// newTestAgent — агент с пустым реестром (нужен только для canRunParallel).
func newTestAgent() *Agent {
	return New(Deps{Registry: tools.New(tools.Env{})}, ".")
}

func TestSpawnAgentRunsParallel(t *testing.T) {
	a := newTestAgent()
	if !a.canRunParallel("spawn_agent") {
		t.Error("spawn_agent должен выполняться параллельно: это весь смысл делегирования")
	}
	// Порядок важен для плана и заметок.
	for _, name := range []string{"todo_write", "task_note"} {
		if a.canRunParallel(name) {
			t.Errorf("%s не должен выполняться параллельно", name)
		}
	}
	// Читающие инструменты по-прежнему параллельны.
	for _, name := range []string{"read_file", "grep", "glob", "list_dir", "web_search"} {
		if !a.canRunParallel(name) {
			t.Errorf("%s должен выполняться параллельно", name)
		}
	}
}

func TestFinalizePromptAsksForReport(t *testing.T) {
	for _, want := range []string{"ИТОГОВЫЙ ОТЧЁТ", "по-русски", "не выполнена"} {
		if !strings.Contains(finalizePrompt, want) {
			t.Errorf("в финальном промпте нет %q", want)
		}
	}
	if finalizeTurns < 1 {
		t.Error("нужен хотя бы один финальный ход")
	}
}

func TestMainPromptTeachesDelegation(t *testing.T) {
	// Правила, без которых субагенты дублируют работу: границы задачи,
	// формат отчёта, честный отказ от лишней делегации.
	for _, want := range []string{
		"Когда НЕ делегировать",
		"Как формулировать task",
		"формат отчёта",
		"Параллельность",
		"task_note",
	} {
		if !strings.Contains(mainPrompt, want) {
			t.Errorf("в mainPrompt нет блока %q", want)
		}
	}
}

func TestSubagentPromptsRequireReport(t *testing.T) {
	ctx := subagents.PromptContext{WorkDir: "."}
	for _, tp := range subagents.Types {
		p := tp.Prompt(ctx)
		if !strings.Contains(p, "Отчёт") {
			t.Errorf("промпт %s не требует отчёт", tp)
		}
		// Главный агент не видит контекст субагента — это надо сказать явно.
		if !strings.Contains(p, "не видит") {
			t.Errorf("промпт %s не объясняет, что отчёт читает главный агент", tp)
		}
	}
}
