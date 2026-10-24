package core

// Тесты цепочки миссий: файл, прогресс, следующий шаг.

import (
	"testing"
)

func TestMissionChainLifecycle(t *testing.T) {
	dir := t.TempDir()
	// Пусто: не ошибка, а «цепочки нет».
	if _, ok, err := LoadMissionChain(dir); err != nil || ok {
		t.Fatalf("без файла: ok=%v err=%v", ok, err)
	}

	c := &MissionChain{Items: []Mission{
		{Objective: "шаг первый", Mode: MissionLongTime},
		{Objective: "шаг второй", Mode: MissionOvernight},
	}}
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := LoadMissionChain(dir)
	if err != nil || !ok {
		t.Fatalf("загрузка: ok=%v err=%v", ok, err)
	}
	if len(loaded.Items) != 2 || loaded.Index != 0 {
		t.Fatalf("содержимое: %+v", loaded)
	}

	n, ok := loaded.Next()
	if !ok || n.Objective != "шаг первый" {
		t.Fatalf("первый шаг: %+v ok=%v", n, ok)
	}
	loaded.Index++
	if n, ok := loaded.Next(); !ok || n.Objective != "шаг второй" {
		t.Fatalf("второй шаг: %+v", n)
	}
	loaded.Index++
	if _, ok := loaded.Next(); ok || !loaded.Done() {
		t.Fatal("после всех шагов Done() должен быть true")
	}
}

func TestMissionChainNegativeIndexClamped(t *testing.T) {
	dir := t.TempDir()
	c := &MissionChain{Index: -5, Items: []Mission{{Objective: "x"}}}
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadMissionChain(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Index != 0 {
		t.Fatalf("отрицательный Index должен зажиматься в 0: %d", loaded.Index)
	}
}

// TestMissionChainHighIndexClamped — Index больше числа шагов появляется
// после ручной правки файла; сводка не должна говорить «шаг 5 из 3».
func TestMissionChainHighIndexClamped(t *testing.T) {
	dir := t.TempDir()
	c := &MissionChain{Index: 5, Items: []Mission{{Objective: "x"}}}
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadMissionChain(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Index != len(loaded.Items) {
		t.Fatalf("Index должен зажиматься до %d: %d", len(loaded.Items), loaded.Index)
	}
	if got := loaded.Summary(); got != "шаг 1 из 1" {
		t.Errorf("сводка = %q, хотели шаг 1 из 1", got)
	}
}
