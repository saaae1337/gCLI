package subagents

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------- DAG-планирование пачки субагентов ----------
//
// Симптом, который это закрывает. Модель просит пачку: «составь план», «сделай
// по плану», «напиши тесты», «сделай ревью» — и ждёт шесть параллельных
// субагентов. На деле план не нужен тому, кто пишет тесты по ещё не
// существующему коду, а ревью бессмысленно до правок. Модель это понимает, но
// инструмент даёт ей ровно один способ: последовательные вызовы spawn_agent
// по одному, с ожиданием каждого — или один вызов, где половина пачки делает
// бессмысленную работу.
//
// Зависимость DependsOn решает это честно: она объявляется в пачке, план
// разворачивается в ВОЛНЫ, всё независимое внутри волны идёт параллельно,
// зависимое стартует после. Ничего не сериализуется «на всякий случай».
//
// Чего DAG НЕ делает: не исполняет себя. Здесь только порядок и проверки —
// запуск остаётся у вызывающего кода (инструмента spawn_agents). Иначе
// планировщик стал бы вторым агентным циклом внутри первого.

// DAG — развёрнутый план запуска пачки.
type DAG struct {
	// Names — итоговое имя узла по индексу в массиве agents.
	Names []string
	// Deps — индексы зависимостей каждого узла (те же, что и Names).
	Deps [][]int
	// Waves — индексы узлов по волнам. Внутри волны всё независимо и идёт
	// параллельно; следующая волна стартует после завершения предыдущей.
	Waves [][]int
}

// Len — узлов в плане.
func (d *DAG) Len() int {
	if d == nil {
		return 0
	}
	return len(d.Names)
}

// WaveCount — сколько волн (1 — всё независимо, как раньше).
func (d *DAG) WaveCount() int {
	if d == nil {
		return 0
	}
	return len(d.Waves)
}

// Order — индексы узлов в порядке запуска (плоский, по волнам).
func (d *DAG) Order() []int {
	if d == nil {
		return nil
	}
	out := make([]int, 0, len(d.Names))
	for _, w := range d.Waves {
		out = append(out, w...)
	}
	return out
}

// DepsOf — индексы зависимостей узла.
func (d *DAG) DepsOf(i int) []int {
	if d == nil || i < 0 || i >= len(d.Deps) {
		return nil
	}
	return d.Deps[i]
}

// RefIndex — разобрать ссылку на узел пачки.
//
// Принимаются две формы:
//
//   - имя субагента (точное или без учёта регистра);
//   - позиционная ссылка «#3» / «3» — третий элемент массива agents.
//
// Позиционная нужна для агентов без имени: их нельзя назвать в задании
// заранее, а завязать зависимость на них иногда необходимо. Имя приоритетнее
// номера: путать «#2» с «двойкой» в имени нельзя.
func RefIndex(ref string, names []string) (int, bool) {
	s := strings.TrimSpace(ref)
	if s == "" {
		return 0, false
	}
	for i, n := range names {
		if n != "" && strings.EqualFold(n, s) {
			return i, true
		}
	}
	digits := strings.TrimPrefix(s, "#")
	if digits == "" {
		return 0, false
	}
	if i, err := strconv.Atoi(digits); err == nil {
		if i >= 1 && i <= len(names) {
			return i - 1, true
		}
	}
	return 0, false
}

// BuildDAG — развернуть пачку в волны.
//
// names — имена узлов: они должны быть непустыми и различными, иначе
// зависимость «дождись a» станет неадресуемой (вызывающий обязан подставить
// автоимена ДО вызова).
// deps — для каждого узла список ссылок (имена или «#N»).
//
// Ошибка возвращается, а не «пропускается молча», потому что оба случая —
// ошибка модели, а не состояние проекта:
//
//   - цикл (a→b→a): запустить нельзя в принципе;
//   - ссылка в никуда (#9 или несуществующее имя): почти всегда опечатка.
//
// Молча выкинуть такой узел — значит отдать модели отчёт «4 готовы», где один
// субагент не запускался, и она построит выводы на неполных данных.
func BuildDAG(names []string, deps [][]string) (*DAG, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("пустая пачка: нечего планировать")
	}
	seen := make(map[string]int, len(names))
	for i, n := range names {
		if strings.TrimSpace(n) == "" {
			return nil, fmt.Errorf("узел %d: пустое имя — зависимости не на что адресовать", i+1)
		}
		key := strings.ToLower(n)
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("имена «%s» повторяются (узлы %d и %d) — переименуй один из них",
				n, prev+1, i+1)
		}
		seen[key] = i
	}

	resolved := make([][]int, len(names))
	for i := range names {
		seenDep := map[int]bool{}
		for _, ref := range deps[i] {
			j, ok := RefIndex(ref, names)
			if !ok {
				return nil, fmt.Errorf("%s: зависимость «%s» не найдена; доступно: %s (или #1..#%d)",
					names[i], strings.TrimSpace(ref), strings.Join(names, ", "), len(names))
			}
			if j == i {
				return nil, fmt.Errorf("%s: зависит от самого себя", names[i])
			}
			if seenDep[j] {
				continue // повтор зависимости — не новая связь
			}
			seenDep[j] = true
			resolved[i] = append(resolved[i], j)
		}
	}

	waves, err := topoWaves(names, resolved)
	if err != nil {
		return nil, err
	}
	return &DAG{Names: names, Deps: resolved, Waves: waves}, nil
}

// topoWaves — алгоритм Кана: слой за слоем, каждый слой — независимые узлы.
//
// Слои, а не просто топологический порядок, — потому что нужна параллельность:
// порядок по одному превратил бы пачку в последовательные вызовы, то есть
// ровно то, от чего мы уходим.
func topoWaves(names []string, deps [][]int) ([][]int, error) {
	n := len(names)
	// dependents[i] — кто ждёт узел i (обратные рёбра).
	dependents := make([][]int, n)
	indeg := make([]int, n)
	for i := 0; i < n; i++ {
		indeg[i] = len(deps[i])
		for _, j := range deps[i] {
			dependents[j] = append(dependents[j], i)
		}
	}
	var waves [][]int
	done := make([]bool, n)
	placed := 0
	for placed < n {
		var layer []int
		// Слой собирается проходом по узлам в порядке массива, поэтому
		// результат детерминирован и совпадает с порядком в agents: отчёты
		// остаются сопоставимыми по номеру.
		for i := 0; i < n; i++ {
			if !done[i] && indeg[i] == 0 {
				layer = append(layer, i)
			}
		}
		if len(layer) == 0 {
			return nil, fmt.Errorf("зависимости образуют цикл: %s — разорви его (задача не может ждать сама себя)",
				strings.Join(cyclePath(names, deps, done), " → "))
		}
		for _, i := range layer {
			done[i] = true
			placed++
		}
		waves = append(waves, layer)
		// Входящие степени уменьшаются только у ещё не размещённых: иначе узлы,
		// поставленные в текущий слой, «развязывали» бы сами себя.
		for _, i := range layer {
			for _, k := range dependents[i] {
				if !done[k] {
					indeg[k]--
				}
			}
		}
	}
	return waves, nil
}

// cyclePath — показать один конкретный цикл, а не «цикл есть».
//
// Список из шести имён бесполезен: модель не поймёт, что именно убрать. Цикл
// показывается как цепочка «a → b → a», где последнее имя повторяет первое, —
// её достаточно, чтобы вычеркнуть одно ребро.
func cyclePath(names []string, deps [][]int, done []bool) []string {
	const (
		white = 0 // не посещён
		gray  = 1 // в текущем стеке
		black = 2 // обработан
	)
	n := len(names)
	color := make([]int, n)
	var stack []int

	// Поиск в глубину с тремя цветами: серый узел в стеке означает ребро в
	// предка, то есть ровно цикл. Рекурсия ограничена числом узлов пачки
	// (максимум единицы от spawn_agents), переполнения стека не будет.
	var visit func(i int) (path []string, found bool)
	visit = func(i int) ([]string, bool) {
		color[i] = gray
		stack = append(stack, i)
		for _, j := range deps[i] {
			switch color[j] {
			case white:
				if p, ok := visit(j); ok {
					return p, true
				}
			case gray:
				// Ребро в текущий стек: цикл от j до вершины стека и обратно.
				start := 0
				for k, v := range stack {
					if v == j {
						start = k
						break
					}
				}
				out := make([]string, 0, len(stack)-start+1)
				for k := start; k < len(stack); k++ {
					out = append(out, names[stack[k]])
				}
				return append(out, names[j]), true
			}
		}
		stack = stack[:len(stack)-1]
		color[i] = black
		return nil, false
	}

	for i := 0; i < n; i++ {
		if done[i] || color[i] != white {
			continue
		}
		if p, ok := visit(i); ok {
			return p
		}
	}
	// Не нашли (не должно случиться): называем хотя бы виновников, иначе в
	// сообщении будет пусто и модель не поймёт, что исправлять.
	var out []string
	for i := 0; i < n; i++ {
		if !done[i] {
			out = append(out, names[i])
		}
	}
	if len(out) == 0 {
		return []string{"?"}
	}
	if len(out) == 1 {
		out = append(out, out[0])
	}
	return out
}
