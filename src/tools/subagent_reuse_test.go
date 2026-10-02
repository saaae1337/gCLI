package tools

import (
	"context"
	"strings"
	"testing"
)

// ---------- Пометка о переиспользовании отчёта ----------
//
// Кеш повторов и дедупликация дают модели отчёт, за которым не стоял новый
// запуск субагента. Если об этом не сказать прямо, главный агент построит
// выводы на тексте, полученном до последних правок в коде, и будет считать,
// что они свежие.

// Модель обязана видеть, что запуск не выполнялся, и от кого отчёт.
func TestSpawnAgentReportsReuse(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		return SpawnResult{
			Name: a.Name, Type: "explorer", Summary: "итог",
			Full: "итог", Reuse: "кеш", ReusedFrom: "explorer-1",
		}, nil
	})
	res, err := r.hSpawnAgent(context.Background(), map[string]any{
		"type": "explorer", "task": "найди кеш", "name": "explorer-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "переиспользован") {
		t.Errorf("в ответе нет пометки о переиспользовании:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "explorer-1") {
		t.Errorf("не указано имя первоисточника:\n%s", res.Text)
	}
}

// Обычный запуск не должен выглядеть как кеш.
func TestSpawnAgentNoReuseNote(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		return SpawnResult{Name: a.Name, Summary: "итог", Full: "итог"}, nil
	})
	res, err := r.hSpawnAgent(context.Background(), map[string]any{
		"type": "explorer", "task": "обычная задача", "name": "explorer-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "переиспользован") {
		t.Errorf("обычный запуск помечен как переиспользованный:\n%s", res.Text)
	}
}

// В пачке дедупликация срабатывает чаще всего (соседние агенты просят одно и
// то же), и молчаливый отчёт из кеша выглядел бы как отдельная полноценная
// работа — с собственными ходами и инструментами, которых не было.
func TestSpawnAgentsBatchMarksReuse(t *testing.T) {
	r, _ := newSpawnReg(t, func(_ context.Context, a SpawnArgs) (SpawnResult, error) {
		res := SpawnResult{Name: a.Name, Summary: "итог " + a.Name, Full: "итог " + a.Name}
		if a.Name == "agent2" {
			res.Reuse = "дождался"
			res.ReusedFrom = "agent1"
		}
		return res, nil
	})
	res, err := r.hSpawnAgents(context.Background(), map[string]any{"agents": spawnTasks(3)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "переиспользован") {
		t.Errorf("в пачке нет пометки о переиспользовании:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "agent1") {
		t.Errorf("в пачке не указан первоисточник:\n%s", res.Text)
	}
}
