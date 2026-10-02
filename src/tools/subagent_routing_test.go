package tools

import (
	"context"
	"strings"
	"testing"
)

// downgradedResult — ответ субагента, у которого маршрутизация понизила модель.
func downgradedResult(name, typ string) SpawnResult {
	return SpawnResult{
		Name:     name,
		Type:     typ,
		Model:    "claude-3-5-haiku",
		ModelWhy: "роль «" + typ + "» простая, модель claude-opus-4 дороже",
		Summary:  "карта проекта",
		Full:     "карта проекта",
	}
}

// TestSpawnAgentReportsDowngradedModel — модель, пониженная маршрутизацией,
// показывается в отчёте.
//
// Без этой строки главный агент примет дешёвый отчёт за равноценный и
// построит на нём план так, будто рабочая сила не менялась: по карте кода
// и по правке кода разница в цене оправдана, по размышлению — нет.
func TestSpawnAgentReportsDowngradedModel(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		return downgradedResult(a.Name, "explorer"), nil
	})
	res, err := r.hSpawnAgent(context.Background(), map[string]any{
		"type": "explorer", "task": "составь карту кода",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "claude-3-5-haiku") {
		t.Errorf("выбранная модель не названа:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "простая") {
		t.Errorf("причина понижения не названа:\n%s", res.Text)
	}
}

// TestSpawnAgentsBatchReportsDowngradedModel — в пачке понижение заметно сильнее:
// один бюджет делится на шесть субагентов, и три дешёвых отчёта легко принять
// за шесть равноценных.
func TestSpawnAgentsBatchReportsDowngradedModel(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		return downgradedResult(a.Name, "explorer"), nil
	})
	res, err := r.hSpawnAgents(context.Background(), map[string]any{
		"agents": spawnTasks(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "claude-3-5-haiku") {
		t.Errorf("в пачке не названа выбранная модель:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "простая") {
		t.Errorf("в пачке не названа причина понижения:\n%s", res.Text)
	}
}

// TestSpawnAgentSilentWhenModelNotChanged — обычный запуск не засоряется
// пометками: если маршрутизация выключена, модель не трогали, и лишняя
// строка в каждом отчёте только шумит.
func TestSpawnAgentSilentWhenModelNotChanged(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		return SpawnResult{
			Name: a.Name, Type: "explorer", Model: "claude-opus-4",
			Summary: "карта проекта", Full: "карта проекта",
		}, nil
	})
	res, err := r.hSpawnAgent(context.Background(), map[string]any{
		"type": "explorer", "task": "составь карту кода",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "модель:") {
		t.Errorf("без понижения модель всё равно упомянута:\n%s", res.Text)
	}
}
