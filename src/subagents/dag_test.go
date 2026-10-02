package subagents

import (
	"slices"
	"strings"
	"testing"
)

// ---------- DAG: волны, циклы, адресация ----------

// nodes — три узла с именами, готовых к BuildDAG.
func nodes3() []string { return []string{"plan", "impl", "review"} }

func TestBuildDAGIndependentIsSingleWave(t *testing.T) {
	d, err := BuildDAG(nodes3(), [][]string{nil, nil, nil})
	if err != nil {
		t.Fatal(err)
	}
	if d.WaveCount() != 1 {
		t.Fatalf("волн %d, ожидалась 1 — без зависимостей всё идёт параллельно", d.WaveCount())
	}
	if got := d.Order(); len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Errorf("порядок %v, ожидался [0 1 2]", got)
	}
}

// TestBuildDAGWavesFollowDependencies — зависимый узел обязан попасть в
// более позднюю волну, чем все, от кого он зависит.
func TestBuildDAGWavesFollowDependencies(t *testing.T) {
	d, err := BuildDAG(nodes3(), [][]string{
		nil,      // plan: ни от чего
		{"plan"}, // impl: после плана
		{"impl"}, // review: после реализации
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.WaveCount() != 3 {
		t.Fatalf("волн %d (%v), ожидались 3 по одной", d.WaveCount(), d.Waves)
	}
	for i, want := range [][]int{{0}, {1}, {2}} {
		got := d.Waves[i]
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("волна %d = %v, ожидалась %v", i+1, got, want)
		}
	}
}

// TestBuildDAGDiamond — классический ромб: один источник, два средних узла
// (идут вместе) и один сток.
func TestBuildDAGDiamond(t *testing.T) {
	d, err := BuildDAG([]string{"map", "impl", "tests", "rev"}, [][]string{
		nil, nil, {"map"}, {"map"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.WaveCount() != 2 {
		t.Fatalf("волн %d (%v), ожидались 2", d.WaveCount(), d.Waves)
	}
	if len(d.Waves[0]) != 2 {
		t.Errorf("первая волна %v: ожидались map и impl вместе", d.Waves[0])
	}
	if len(d.Waves[1]) != 2 {
		t.Errorf("вторая волна %v: ожидались tests и rev вместе", d.Waves[1])
	}
}

// TestBuildDAGRejectsCycle — цикл это ошибка модели, а не тихий пропуск.
func TestBuildDAGRejectsCycle(t *testing.T) {
	_, err := BuildDAG([]string{"a", "b", "c"}, [][]string{
		{"b"}, {"c"}, {"a"},
	})
	if err == nil {
		t.Fatal("цикл a→b→c→a должен отклоняться")
	}
	msg := err.Error()
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(msg, want) {
			t.Errorf("в ошибке нет имени «%s»: %s", want, msg)
		}
	}
	if !strings.Contains(msg, "цикл") {
		t.Errorf("ошибка не называет проблему циклом: %s", msg)
	}
}

func TestBuildDAGRejectsSelfDependency(t *testing.T) {
	if _, err := BuildDAG([]string{"solo"}, [][]string{{"solo"}}); err == nil {
		t.Fatal("зависимость от самого себя должна отклоняться")
	}
}

func TestBuildDAGUnknownRef(t *testing.T) {
	_, err := BuildDAG([]string{"a", "b"}, [][]string{nil, {"missing"}})
	if err == nil {
		t.Fatal("ссылка в пустоту должна отклоняться")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("ошибка не повторяет проблемную ссылку: %v", err)
	}
}

// TestBuildDAGPositionalRef — агенты без имени адресуются по номеру «#2».
func TestBuildDAGPositionalRef(t *testing.T) {
	d, err := BuildDAG([]string{"auto#1", "auto#2"}, [][]string{nil, {"#1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.DepsOf(1)) != 1 || d.DepsOf(1)[0] != 0 {
		t.Errorf("зависимости узла 2 = %v, ожидался [0]", d.DepsOf(1))
	}
}

func TestRefIndexForms(t *testing.T) {
	names := []string{"Plan", "impl"}
	cases := []struct {
		ref  string
		want int
		ok   bool
	}{
		{"Plan", 0, true},
		{"plan", 0, true},
		{" impl ", 1, true},
		{"#2", 1, true},
		{"2", 1, true},
		{"#0", 0, false},
		{"#3", 0, false},
		{"nope", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := RefIndex(c.ref, names)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("RefIndex(%q) = %d,%v; ожидалось %d,%v", c.ref, got, ok, c.want, c.ok)
		}
	}
}

// TestBuildDAGDuplicateDeps — повтор зависимости не двоит ребро, иначе
// инцидентность топологической сортировки поедет.
func TestBuildDAGDuplicateDeps(t *testing.T) {
	d, err := BuildDAG([]string{"a", "b"}, [][]string{nil, {"a", "a", "#1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.DepsOf(1)) != 1 {
		t.Errorf("зависимостей %d, ожидалась 1", len(d.DepsOf(1)))
	}
	if d.WaveCount() != 2 {
		t.Errorf("волн %d, ожидались 2", d.WaveCount())
	}
}

// TestBuildDAGRejectsDuplicateNames — два одинаковых имени делают зависимости
// неадресуемыми: «дождись a» значило бы сразу два узла.
func TestBuildDAGRejectsDuplicateNames(t *testing.T) {
	if _, err := BuildDAG([]string{"a", "a"}, [][]string{nil, {"a"}}); err == nil {
		t.Fatal("повтор имени должен отклоняться")
	}
	if _, err := BuildDAG([]string{"a", ""}, [][]string{nil, nil}); err == nil {
		t.Fatal("пустое имя должно отклоняться")
	}
	if _, err := BuildDAG(nil, nil); err == nil {
		t.Fatal("пустая пачка должна отклоняться")
	}
}

// TestBuildDAGSkipsIsolatedNode — узел вне цикла не должен пропасть из плана
// вместе с обнаружением цикла (вызывающий покажет ошибку и не запустит пачку,
// но виновник должен быть назван точно).
func TestBuildDAGNamesCycleNotOthers(t *testing.T) {
	_, err := BuildDAG([]string{"ok1", "x", "y", "ok2"}, [][]string{
		nil, {"y"}, {"x"}, nil,
	})
	if err == nil {
		t.Fatal("цикл должен отклоняться")
	}
	if strings.Contains(err.Error(), "ok1") || strings.Contains(err.Error(), "ok2") {
		t.Errorf("в ошибку попали узлы вне цикла: %v", err)
	}
	if !strings.Contains(err.Error(), "x") || !strings.Contains(err.Error(), "y") {
		t.Errorf("в ошибку не попали вершины цикла: %v", err)
	}
}

// TestDAGNilSafe — нулевый DAG не должен паниковать в UI-коде.
func TestDAGNilSafe(t *testing.T) {
	var d *DAG
	if d.Len() != 0 || d.WaveCount() != 0 || d.Order() != nil || d.DepsOf(0) != nil {
		t.Error("нулевой DAG должен вести себя как пустой")
	}
}

// TestBuildDAGLongChainDeterministic — порядок внутри волны и сам план не должны
// зависеть от того, как сортируются карты: два одинаковых вызова дают один
// и тот же результат (иначе отчёты «случайно» переставлялись бы).
func TestBuildDAGLongChainDeterministic(t *testing.T) {
	names := []string{"n1", "n2", "n3", "n4", "n5"}
	deps := [][]string{nil, nil, {"n1", "n2"}, {"n3"}, {"n3"}}
	first, err := BuildDAG(names, deps)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := BuildDAG(names, deps)
		if err != nil {
			t.Fatal(err)
		}
		for w := range first.Waves {
			if !slices.Equal(again.Waves[w], first.Waves[w]) {
				t.Fatalf("волна %d нестабильна: %v против %v", w, again.Waves[w], first.Waves[w])
			}
		}
	}
	if first.WaveCount() != 3 {
		t.Errorf("волн %d, ожидались 3: %v", first.WaveCount(), first.Waves)
	}
}
