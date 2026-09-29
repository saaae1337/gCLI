package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gcli/core"
)

// ---------- Детектор зацикливания ----------
//
// Симптом, который виден в чужом коде и в своей работе: модель попадает
// в петлю — повторяет один и тот же вызов с теми же аргументами, снова
// получает тот же результат и продолжает. Формально итерации тратятся,
// лимит цикла расходуется, а прорыва нет. Хуже всего, что это выглядит
// как «агент работает», и человек в это верит.
//
// Детектор не обрывает работу (у модели бывают законные повторы, например
// сборка до успеха), а сообщает ей: что именно повторяется, сколько раз и
// что делать вместо этого. Решение остаётся за моделью — но уже с фактами.

const (
	// loopWarnAfter — сколько одинаковых вызовов подряд считать петлёй.
	loopWarnAfter = 3
	// loopLookback — сколько последних итераций держим в окне.
	loopLookback = 8
	// loopSameErrAfter — столько раз подряд один и тот же текст ошибки.
	loopSameErrAfter = 3
)

// callKey — ключ вызова инструмента: имя + нормализованные аргументы.
//
// Аргументы нормализуются по смыслу, а не по тексту. Раньше здесь стоял
// sha256 от core.OneLine, и это давало ложный отрицательный результат:
// {"path":"a.go"} и { "path": "a.go" } — тот же самый вызов, но разные
// строки, поэтому модель могла повторять один и тот же grep десяток раз
// и не получать предупреждения. Теперь JSON разбирается, ключи полей
// сортируются, и ключ строится по значениям.
func callKey(name, args string) string {
	h := sha256.Sum256([]byte(name + "\x00" + normalizeArgs(args)))
	return name + ":" + hex.EncodeToString(h[:6])
}

// normalizeArgs — привести аргументы к каноническому виду.
func normalizeArgs(args string) string {
	s := strings.TrimSpace(args)
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		if b, err := json.Marshal(canonical(v)); err == nil {
			return string(b)
		}
	}
	// Не JSON — сравниваем как текст, схлопывая пробелы.
	return core.OneLine(s)
}

// canonical — рекурсивно привести к упорядоченному виду (ключи объектов
// сортируются: в JSON их порядок не имеет значения).
func canonical(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = canonical(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = canonical(e)
		}
		return out
	}
	return v
}

// errKey — ключ ошибки инструмента (только неприятности, не весь вывод).
//
// Успешный вывод в ключ не входит: одинаковый вывод bash может быть
// ожидаемым (сборка дважды), а вот одинаковая ошибка три раза подряд —
// почти всегда тупик.
func errKey(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	if !strings.HasPrefix(t, "ошибка") && !strings.HasPrefix(t, "предупреждение") {
		return ""
	}
	if len([]rune(t)) > 200 {
		t = string([]rune(t)[:200])
	}
	return t
}

// LoopDetector — отслеживает повторы вызовов и ошибок в агентном цикле.
//
// Потокобезопасен: execTools выполняет инструменты параллельно, и карта
// пишется из нескольких горутин одновременно.
type LoopDetector struct {
	mu sync.Mutex
	// counts — сколько раз за последние loopLookback итераций встречался вызов.
	counts map[string]int
	// order — порядок появления вызовов, чтобы вытеснять старые.
	order []string
	// errCounts — счётчик подряд идущих одинаковых ошибок.
	errCounts map[string]int
	// warned — о чём уже предупреждали (чтобы не повторять одно и то же).
	warned map[string]bool
	// dropped — сколько вызовов вытеснено из окна.
	dropped int
}

// NewLoopDetector — создать детектор.
func NewLoopDetector() *LoopDetector {
	return &LoopDetector{
		counts:    map[string]int{},
		errCounts: map[string]int{},
		warned:    map[string]bool{},
	}
}

// Record — учесть результат итерации. Возвращает предупреждение, если
// обнаружена петля (пустая строка — петли нет).
//
// results передаются в том же виде, в каком они попадут в контекст:
// модель видит ровно то, о чём её предупреждают.
func (d *LoopDetector) Record(calls []core.ToolCall, results []toolResult) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, tc := range calls {
		k := callKey(tc.Name, tc.Args)
		if d.counts[k] == 0 {
			d.order = append(d.order, k)
		}
		d.counts[k]++
	}
	// Окно: вытесняем вызовы, выпавшие за пределы памяти.
	for len(d.order) > loopLookback {
		oldest := d.order[0]
		d.order = d.order[1:]
		d.counts[oldest]--
		if d.counts[oldest] <= 0 {
			delete(d.counts, oldest)
		}
		d.dropped++
	}

	// Самая частая ошибка в итерации — считаем её повтор, если такие ошибки
	// шли подряд несколько итераций.
	if err := dominantError(results); err != "" {
		d.errCounts[err]++
	} else {
		d.errCounts = map[string]int{}
	}

	return d.diagnose(calls)
}

// diagnose — сформулировать предупреждение.
func (d *LoopDetector) diagnose(calls []core.ToolCall) string {
	// 1. Повтор одного и того же вызова.
	for _, tc := range calls {
		k := callKey(tc.Name, tc.Args)
		if d.counts[k] >= loopWarnAfter && !d.warned[k] {
			d.warned[k] = true
			args := core.Truncate(core.OneLine(tc.Args), 200)
			return fmt.Sprintf(
				"[Система] Стоп: ты повторяешь один и тот же вызов уже %d %s за последние ходы — "+
					"%s %s\n"+
					"Повторный вызов с теми же аргументами вернёт тот же результат и только съест итерации. "+
					"Выбери одно из трёх: изменить аргументы, сменить инструмент на другой способ, "+
					"либо признать, что этого пути нет, и перейти к следующему пункту плана.",
				d.counts[k], plural(d.counts[k], "раз", "раза", "раз"), tc.Name, args)
		}
	}

	// 2. Одна и та же ошибка несколько итераций подряд.
	for e, n := range d.errCounts {
		if n >= loopSameErrAfter && !d.warned["err:"+e] {
			d.warned["err:"+e] = true
			return "[Система] Стоп: последние " + fmt.Sprint(n) + " хода подряд упираются в одну и ту же ошибку:\n" +
				core.Truncate(e, 400) + "\n" +
				"Повторять то же самое бессмысленно. Разберись с причиной: прочитай исходник, измени подход, " +
				"спроси пользователя или зафиксируй факт через task_note и двигайся дальше."
		}
	}
	return ""
}

// plural — согласовать существительное с числом.
func plural(n int, one, few, many string) string {
	if n%100 >= 11 && n%100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// dominantError — самая частая ошибка среди результатов итерации.
func dominantError(results []toolResult) string {
	if len(results) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, r := range results {
		if k := errKey(r.text); k != "" {
			counts[k]++
		}
	}
	best, bestN := "", 0
	for k, n := range counts {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best
}

// Stats — диагностика петли для отчёта о работе агента.
func (d *LoopDetector) Stats() (warned, dropped int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.warned), d.dropped
}
