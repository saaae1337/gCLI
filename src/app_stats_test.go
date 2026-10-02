package main

// Тесты агрегатора дашборда. Чистая функция: без диска и UI.

import (
	"math"
	"testing"
	"time"

	"gcli/core"
)

func TestStatsAggregateGroups(t *testing.T) {
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	old := day.AddDate(0, 0, -40)
	sessions := []core.Session{
		{Updated: day, Provider: "zai", Model: "glm-4.6",
			Usage: core.Usage{PromptTokens: 1000, CompletionTokens: 500},
			Stats: core.Stats{Requests: 3, Tools: 5}},
		{Updated: day, Provider: "zai", Model: "glm-4.6",
			Usage: core.Usage{PromptTokens: 2000, CompletionTokens: 100},
			Stats: core.Stats{Requests: 2}},
		{Updated: day, Provider: "custom", Model: "my-model",
			Usage: core.Usage{PromptTokens: 777, CompletionTokens: 0},
			Stats: core.Stats{Requests: 1}},
		{Updated: old, Provider: "zai", Model: "glm-4.6",
			Usage: core.Usage{PromptTokens: 999999, CompletionTokens: 0}},
	}
	tot, dayRows, modelRows := statsAggregate(sessions, day.AddDate(0, 0, -29))

	if tot.sessions != 3 {
		t.Fatalf("старая сессия должна быть отфильтрована: %d", tot.sessions)
	}
	if tot.in != 1000+2000+777 || tot.out != 600 {
		t.Fatalf("токены неверны: %+v", tot)
	}
	if tot.requests != 6 {
		t.Fatalf("запросы: %d, ждали 6", tot.requests)
	}
	if len(dayRows) != 1 || dayRows[0].sessions != 3 {
		t.Fatalf("группировка по дням: %+v", dayRows)
	}
	if len(modelRows) != 2 {
		t.Fatalf("модели: %+v", modelRows)
	}
	// Цена известна только для zai/glm-4.6; custom — unknown сессия.
	if tot.unknown != 1 {
		t.Fatalf("unknown: %d, ждали 1", tot.unknown)
	}
	if modelRows[0].model != "glm-4.6" {
		t.Fatalf("сортировка по деньгам: %+v", modelRows)
	}
	if modelRows[0].cost <= 0 {
		t.Fatalf("стоимость известной модели должна быть > 0: %v", modelRows[0].cost)
	}
	if math.Abs(modelRows[1].cost) > 1e-9 {
		t.Fatalf("у модели без цены стоимость должна быть 0, получили %v", modelRows[1].cost)
	}
}
