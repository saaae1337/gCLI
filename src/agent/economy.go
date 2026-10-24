package agent

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gcli/core"
	"gcli/tools"
)

// ---------- Экономия: подрезка истории, кеш результатов, учёт ----------

// Экономия в агенте устроена вокруг одного наблюдения: модель платит за
// каждый запрос целиком — за системный промпт, за схемы инструментов и за
// всю историю, которая накопилась к этому моменту. Три из этих статей
// можно уменьшать:
//
//   - история растёт от хода к ходу, и старые результаты инструментов
//     давят на контекст (подрезка свёртывает их в начало и конец);
//   - модель иногда зовёт тот же инструмент с теми же аргументами
//     (кеш отдаёт прошлый результат, если файл не менялся);
//   - одинаковые вызовы в одном ответе выполняются по одному разу.
//
// Всё это работает с КОПИЕЙ истории: сессия хранит полную историю, и
// подрезанная копия уходит только в запрос к модели.

// economyOptions — параметры экономии для одного запроса.
type economyOptions struct {
	// KeepResults — сколько последних результатов инструментов оставить
	// целиком. Всё, что старше, сворачивается в начало и конец.
	KeepResults int
	// MaxResultTokens — потолок для свежего результата в запросе. Большой
	// вывод команды в 40 тысяч токенов никому не нужен целиком.
	MaxResultTokens int
	// OldResultTokens — потолок для старого результата. Старое знание нужно
	// лишь для ориентира («файл был такой»), поэтому от него оставляется
	// начало и конец.
	OldResultTokens int
	// Budget — с какого размера истории начинать подрезка. Ниже порога
	// история не трогается: пока контекст небольшой, полнота важнее
	// экономии.
	Budget int
}

// dropOldImages — убрать картинки, кроме последних.
//
// Правило не зависит от давления на контекст: картинка нужна модели
// один раз — когда она на неё смотрит. Дальше она только дорогая:
// снимок экрана стоит полторы-две тысячи токенов, и десяток снимков в
// истории съедал половину окна на каждом запросе.
func dropOldImages(msgs []core.Message) {
	last := -1
	for i := range msgs {
		if len(msgs[i].Images) > 0 {
			last = i
		}
	}
	if last < 0 {
		return
	}
	for i := range msgs {
		if i != last {
			msgs[i].Images = nil
		}
	}
}

// defaultEconomy — настройки по умолчанию.
func defaultEconomy() economyOptions {
	return economyOptions{
		KeepResults:     6,
		MaxResultTokens: 4000,
		OldResultTokens: 200,
		Budget:          16_000,
	}
}

// economyOptions — настройки экономии для текущего агента.
//
// Порог подрезки отсчитывается от порога авто-сжатия: подрезка должна
// начинаться заметно раньше, чем истории понадобится сжатие, иначе
// сжатие будет дорогой заменой обычной подрезки.
func (a *Agent) economyOptions() economyOptions {
	opt := defaultEconomy()
	if a != nil && a.d.AutoCompact > 0 {
		opt.Budget = a.d.AutoCompact / 4
	}
	return opt
}

// pruneMessages — подрезать историю для запроса к модели.
//
// Возвращает копию и оценку сэкономленных токенов. Порядок правил важен:
//
//  1. одинаковые результаты — оставляем один (последний), остальные
//     превращаем в указатель: модель всё равно видит содержимое;
//  2. старые результаты — сворачиваем в начало и конец;
//  3. свежие результаты — режем по потолку, но не сворачиваем;
//  4. старые размышления — выбрасываем: они нужны модели только в том
//     ходе, где родились (у Anthropic thinking-блок обязателен только
//     в последнем сообщении ассистента).
//
// Подрезка по давлению (шаги 1–4) идёт только когда история велика:
// пока контекст небольшой, полнота важнее экономии. А картинки
// (шаг 5) убираются всегда — см. dropOldImages.
func pruneMessages(msgs []core.Message, opt economyOptions) ([]core.Message, int, int) {
	if len(msgs) == 0 {
		return msgs, 0, 0
	}
	before := core.EstimateContext(msgs)
	out := append([]core.Message(nil), msgs...)
	// Картинки — всегда: они дороги и нужны один раз.
	dropOldImages(out)
	if opt.Budget > 0 && before <= opt.Budget {
		return out, 0, 0
	}
	pruned := 0

	// Номера сообщений с результатами инструментов.
	var toolIdx []int
	for i, m := range out {
		if m.Role == core.RoleTool {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) == 0 {
		return out, 0, 0
	}
	// Граница «свежих» результатов.
	freshFrom := len(toolIdx) - opt.KeepResults
	if freshFrom < 0 {
		freshFrom = 0
	}

	// 1. Одинаковые результаты: содержимое оставляем у последнего, у более
	// ранних остаётся указатель «тот же вывод ниже» — так модель читает
	// историю сверху вниз и сразу находит содержимое.
	lastSeen := map[string]int{}
	for _, i := range toolIdx {
		lastSeen[out[i].Name+"\x00"+out[i].Content] = i
	}
	for _, i := range toolIdx {
		m := out[i]
		key := m.Name + "\x00" + m.Content
		if last, ok := lastSeen[key]; ok && last != i {
			out[i].Content = "[повтор результата " + m.Name + " — тот же вывод ниже]"
			pruned++
		}
	}

	// 2-3. Сворачиваем старые результаты, режем свежие.
	for n, i := range toolIdx {
		m := &out[i]
		if m.Content == "" {
			continue
		}
		old := m.Content
		if n < freshFrom {
			m.Content = core.DigestResult(m.Name, m.Content, opt.OldResultTokens)
		} else if opt.MaxResultTokens > 0 && core.ApproxTokens(m.Content) > opt.MaxResultTokens {
			m.Content = core.DigestResult(m.Name, m.Content, opt.MaxResultTokens)
		}
		if m.Content != old {
			pruned++
		}
	}

	// 4. Размышления: оставляем только у последнего сообщения ассистента.
	lastAssistant := -1
	for i := range out {
		if out[i].Role == core.RoleAssistant {
			lastAssistant = i
		}
	}
	for i := range out {
		if out[i].Role != core.RoleAssistant || i == lastAssistant {
			continue
		}
		if out[i].Reasoning != "" || out[i].ReasoningSig != "" {
			out[i].Reasoning = ""
			out[i].ReasoningSig = ""
			pruned++
		}
	}

	// 5. Картинки: в контексте нужны один раз — когда модель на них
	// смотрит. Дальше они только дороги: снимок экрана стоит полторы-две
	// тысячи токенов, и десяток снимков в истории съедал половину окна
	// на каждом запросе. Оставляем картинки только у последнего сообщения,
	// где они есть.
	dropOldImages(out)

	after := core.EstimateContext(out)
	if after >= before {
		return out, 0, 0
	}
	return out, before - after, pruned
}

// ---------- Кеш результатов инструментов только для чтения ----------

// cacheTTL — сколько живёт запись кеша.
//
// Ограничение по времени — страховка на случай, когда отпечаток файла
// не удалось снять (инструмент читает не файл, а каталог или вывод
// команды). Запись без отпечатка живёт недолго.
const cacheTTL = 2 * time.Minute

// cacheLimit — сколько записей держим кеш.
const cacheLimit = 64

// resultCache — кеш результатов инструментов, которые ничего не меняют.
//
// Ключ — имя инструмента и аргументы. Значение — результат и отпечаток
// файлов, на которые он опирался: файл изменился — запись больше не
// годится.
type resultCache struct {
	mu    sync.Mutex
	items map[string]cacheEntry
	order []string
}

type cacheEntry struct {
	text  string
	at    time.Time
	stamp string
}

// newResultCache — пустой кеш.
func newResultCache() *resultCache {
	return &resultCache{items: map[string]cacheEntry{}}
}

// get — запись кеша для вызова, если она ещё годна.
//
// stamp — отпечаток файлов на момент кеширования. Пустой отпечаток
// означает «файлов не трогали»: такая запись годна только по времени.
func (c *resultCache) get(key, stamp string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return "", false
	}
	if time.Since(e.at) > cacheTTL {
		c.drop(key)
		return "", false
	}
	if stamp != "" && e.stamp != "" && e.stamp != stamp {
		// Файл изменился — прошлый результат больше неправда.
		c.drop(key)
		return "", false
	}
	return e.text, true
}

// put — запомнить результат вызова.
func (c *resultCache) put(key, text, stamp string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]cacheEntry{}
	}
	if _, exists := c.items[key]; !exists {
		c.order = append(c.order, key)
	}
	c.items[key] = cacheEntry{text: text, at: time.Now(), stamp: stamp}
	for len(c.order) > cacheLimit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}
}

// drop — убрать запись (под mu).
func (c *resultCache) drop(key string) {
	delete(c.items, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// cacheKey — ключ записи кеша для вызова.
func cacheKey(name, args string) string { return name + "\x00" + args }

// cacheableTool — можно ли кешировать результат этого инструмента.
//
// Только то, что ничего не меняет: запись в файл, запуск команды и
// запрос в сеть кешировать нельзя — там важен момент вызова.
func cacheableTool(t *tools.Tool) bool {
	if t == nil {
		return false
	}
	switch t.Category {
	case "read", "plan":
		return true
	}
	return false
}

// fileStamp — отпечаток файлов, на которые опирается вызов.
//
// Пути берутся из аргументов (path, paths, file, files). Отпечаток —
// время изменения и размер: содержимое поменялось — прежний результат
// недействителен.
func fileStamp(args string) string {
	if args == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	var paths []string
	for _, key := range []string{"path", "paths", "file", "files"} {
		switch v := m[key].(type) {
		case string:
			if v != "" {
				paths = append(paths, v)
			}
		case []any:
			for _, it := range v {
				if s, ok := it.(string); ok && s != "" {
					paths = append(paths, s)
				}
			}
		}
	}
	if len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(p + ":" + fileStampOf(p) + ";")
	}
	return b.String()
}

// fileStampOf — отпечаток файла: время изменения и размер.
func fileStampOf(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "нет"
	}
	return strconv.FormatInt(st.ModTime().UnixNano(), 10) + ":" +
		strconv.FormatInt(st.Size(), 10)
}

// ---------- Одинаковые вызовы в одном ответе ----------

// dedupeCalls — убрать из списка вызовов точные повторы.
//
// Модель иногда зовёт один и тот же инструмент с одними и теми же
// аргументами дважды в одном ответе. Выполнять это дважды — значит
// потратить два вызова и два результата на одну и ту же информацию.
//
// Повтор сворачивается только для инструментов, которые ничего не
// меняют (canRepeat): повторное чтение того же файла бесполезно, а
// повторная запись или команда — нет, и молча пропустить её нельзя.
//
// Возвращает уникальные вызовы, их позиции в исходном списке и позиции
// отброшенных повторов (им всё равно нужен ответ: без tool-сообщения
// провайдер отвергает следующий запрос целиком).
func dedupeCalls(calls []core.ToolCall, canRepeat func(name string) bool) (uniq []core.ToolCall, origin []int, dups []int) {
	if len(calls) == 0 {
		return nil, nil, nil
	}
	if len(calls) == 1 {
		return calls, []int{0}, nil
	}
	seen := map[string]int{}
	uniq = make([]core.ToolCall, 0, len(calls))
	origin = make([]int, 0, len(calls))
	for i, c := range calls {
		k := cacheKey(c.Name, c.Args)
		if _, ok := seen[k]; ok && canRepeat != nil && canRepeat(c.Name) {
			dups = append(dups, i)
			continue
		}
		seen[k] = i
		uniq = append(uniq, c)
		origin = append(origin, i)
	}
	return uniq, origin, dups
}

// ---------- Учёт экономии ----------

// economyRecorder — необязательный интерфейс сессии.
//
// Сессия в тестах и в субагентах не обязана уметь принимать счётчики
// экономии, поэтому интерфейс проверяется на лету, а не входит в
// SessionRef: иначе каждый поддельный сессионный тип в тестах пришлось
// бы дополнять новым методом.
type economyRecorder interface {
	RecordEconomy(e core.EconomyStats)
}

// recordEconomy — отдать счётчики экономии сессии (если она умеет).
func (a *Agent) recordEconomy() {
	if a == nil || a.d.Session == nil {
		return
	}
	r, ok := a.d.Session.(economyRecorder)
	if !ok {
		return
	}
	r.RecordEconomy(a.econ)
}

// RequestEstimate — сколько токенов уходит в запрос к модели и сколько
// на этом сэкономила подрезка истории.
//
// Считается по той же логике, что и реальный запрос, поэтому цифра в
// /context совпадает с тем, что уходит провайдеру.
func RequestEstimate(system string, defs []core.ToolDef, msgs []core.Message) (total, saved int) {
	pruned, savedTokens, _ := pruneMessages(msgs, defaultEconomy())
	total = core.FullContext(system, defs, pruned)
	return total, savedTokens
}
