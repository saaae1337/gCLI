package main

// Тесты промпта шага цепочки: чистая функция, без диска и UI.

import (
	"strings"
	"testing"

	"gcli/core"
)

func TestChainStepPromptFirstAndMiddle(t *testing.T) {
	c := &core.MissionChain{Items: []core.Mission{
		{Objective: "шаг один", Mode: core.MissionLongTime},
		{Objective: "шаг два", Mode: core.MissionOvernight, Acceptance: []string{"тесты зелёные"}},
	}}

	first := chainStepPrompt(c, c.Items[0])
	if !strings.Contains(first, "шаг 1 из 2") || !strings.Contains(first, "шаг один") {
		t.Fatalf("первый шаг: %q", first)
	}
	// На первом шаге ссылки на предыдущий быть не должно.
	if strings.Contains(first, "Предыдущий шаг") {
		t.Fatalf("у первого шага не должно быть «предыдущего»: %q", first)
	}

	c.Index = 1
	second := chainStepPrompt(c, c.Items[1])
	if !strings.Contains(second, "шаг 2 из 2") {
		t.Fatalf("номер шага: %q", second)
	}
	if !strings.Contains(second, "шаг один") || !strings.Contains(second, "Предыдущий шаг") {
		t.Fatalf("перенос контекста потерян: %q", second)
	}
	if !strings.Contains(second, "тесты зелёные") {
		t.Fatalf("критерии приёмки потеряны: %q", second)
	}
}
