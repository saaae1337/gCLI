package main

// Тесты реза истории для /fork. Чистая функция — без диска и UI.

import (
	"testing"

	"gcli/core"
)

func msgsOf(roles ...core.Role) []core.Message {
	out := make([]core.Message, 0, len(roles))
	for _, r := range roles {
		out = append(out, core.Message{Role: r})
	}
	return out
}

func TestSafeForkCutPlain(t *testing.T) {
	// user, assistant — резать можно в любую границу.
	msgs := msgsOf(core.RoleUser, core.RoleAssistant, core.RoleUser, core.RoleAssistant)
	for _, n := range []int{1, 2, 3, 4} {
		if got := safeForkCut(msgs, n); got != n {
			t.Fatalf("n=%d: разрез %d, ждали %d", n, got, n)
		}
	}
}

func TestSafeForkCutNeverInsideToolCall(t *testing.T) {
	// user, assistant(2 вызова), tool, tool, assistant.
	// Границы 2 и 3 — внутри незакрытой пачки вызовов, 4 — хвост из
	// tool-ответов. Все они недопустимы: безопасно только 1 и 5.
	msgs := msgsOf(core.RoleUser, core.RoleAssistant, core.RoleTool, core.RoleTool, core.RoleAssistant)
	msgs[1].ToolCalls = []core.ToolCall{{ID: "a"}, {ID: "b"}}
	for _, n := range []int{2, 3, 4} {
		if got := safeForkCut(msgs, n); got != 1 {
			t.Fatalf("n=%d: разрез %d должен опуститься к 1 (незакрытые вызовы или tool-хвост)", n, got)
		}
	}
	if got := safeForkCut(msgs, 5); got != 5 {
		t.Fatalf("полная история корректна: разрез %d, ждали 5", got)
	}
}

func TestSafeForkCutNoTailOfToolMessages(t *testing.T) {
	// user, assistant(вызов), tool — рез на 3 оставит хвост tool-ответа.
	msgs := msgsOf(core.RoleUser, core.RoleAssistant, core.RoleTool)
	msgs[1].ToolCalls = []core.ToolCall{{ID: "a"}}
	if got := safeForkCut(msgs, 3); got != 1 {
		t.Fatalf("хвост из tool-ответа недопустим: разрез %d, ждали 1", got)
	}
}

func TestSafeForkCutEdgeCases(t *testing.T) {
	msgs := msgsOf(core.RoleUser, core.RoleAssistant)
	if got := safeForkCut(msgs, 0); got != 0 {
		t.Fatalf("n=0 должен дать пустой форк, получили %d", got)
	}
	if got := safeForkCut(msgs, 100); got != 2 {
		t.Fatalf("n больше истории: ждали конец, получили %d", got)
	}
	// Одинокий tool-ответ в начале (повреждённая история) — рез только 0/после.
	broken := msgsOf(core.RoleTool)
	if got := safeForkCut(broken, 1); got != 0 {
		t.Fatalf("история, кончающаяся tool-ответом, не режется: %d", got)
	}
}
