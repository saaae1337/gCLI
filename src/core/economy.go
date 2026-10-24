package core

import (
	"strconv"
	"strings"
)

// ---------- Экономия запросов и токенов ----------

// EconomyStats — счётчики экономии за сессию.
//
// Зачем они нужны: «агент потратил 300 тысяч токенов» — это догадка.
// С этими числами видно, во что именно ушли деньги: растущая история
// (её можно подрезать), повторные чтения (их можно взять из кеша) или
// постоянные схемы инструментов (их стоит кешировать на стороне
// провайдера).
type EconomyStats struct {
	// Requests — сколько запросов к модели ушло за сессию.
	Requests int `json:"requests"`
	// PrunedTokens — сколько токенов вырезано из запросов подрезкой истории.
	PrunedTokens int `json:"pruned_tokens"`
	// PrunedCount — сколько сообщений свёрнуто подрезкой.
	PrunedCount int `json:"pruned_count"`
	// CacheHits — сколько повторных вызовов взято из кеша.
	CacheHits int `json:"cache_hits"`
	// SavedCalls — сколько одинаковых вызовов в одном ходе не выполнено дважды.
	SavedCalls int `json:"saved_calls"`
	// ToolCalls — сколько раз вызывался каждый инструмент.
	ToolCalls map[string]int `json:"tool_calls,omitempty"`
	// ToolTokens — сколько токенов заняли результаты каждого инструмента.
	ToolTokens map[string]int `json:"tool_tokens,omitempty"`
}

// AddCall — учесть вызов инструмента и объём его результата.
func (e *EconomyStats) AddCall(name string, tokens int) {
	if e == nil || name == "" {
		return
	}
	if e.ToolCalls == nil {
		e.ToolCalls = map[string]int{}
	}
	if e.ToolTokens == nil {
		e.ToolTokens = map[string]int{}
	}
	e.ToolCalls[name]++
	e.ToolTokens[name] += tokens
}

// Merge — сложить счётчики (субагенты пишут в общий счёт).
func (e *EconomyStats) Merge(o EconomyStats) {
	if e == nil {
		return
	}
	e.Requests += o.Requests
	e.PrunedTokens += o.PrunedTokens
	e.PrunedCount += o.PrunedCount
	e.CacheHits += o.CacheHits
	e.SavedCalls += o.SavedCalls
	for k, v := range o.ToolCalls {
		if e.ToolCalls == nil {
			e.ToolCalls = map[string]int{}
		}
		e.ToolCalls[k] += v
	}
	for k, v := range o.ToolTokens {
		if e.ToolTokens == nil {
			e.ToolTokens = map[string]int{}
		}
		e.ToolTokens[k] += v
	}
}

// AvgTokensPerRequest — средний расход на один запрос.
//
// Ноль, пока запросов не было: делить на ноль нельзя, а «бесконечно
// много токенов на запрос» — неправда.
func (e EconomyStats) AvgTokensPerRequest() int {
	if e.Requests <= 0 {
		return 0
	}
	return e.PrunedTokens / e.Requests
}

// TopTool — самый «дорогой» инструмент по объёму результатов.
//
// Возвращает имя и токены; пустое имя — данных нет.
func (e EconomyStats) TopTool() (string, int) {
	best, bestN := "", 0
	for name, n := range e.ToolTokens {
		if n > bestN || (n == bestN && name < best) {
			best, bestN = name, n
		}
	}
	return best, bestN
}

// Digest — компактная свёртка длинного текста.
//
// Начало и конец остаются, середина исчезает: по началу видно, что за
// файл, по концу — чем он закончился, а середина (обычно однообразный
// код или длинный лог) интереса не несёт.
//
// maxTokens — потолок в токенах. Ноль или меньше — без обрезки.
func Digest(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return text
	}
	if ApproxTokens(text) <= maxTokens {
		return text
	}
	// Бюджет по символам считаем от самого текста: сколько символов в нём
	// приходится на токен. Для кириллицы это около 1.4, для латиницы — 4,
	// и одна цифра «3 на токен» давала бы то ли обрезку без толку, то ли
	// вдвое больший результат.
	tok := ApproxTokens(text)
	r := []rune(text)
	limit := int(float64(maxTokens) * float64(len(r)) / float64(tok))
	if limit < 40 {
		limit = 40
	}
	if len(r) <= limit {
		return text
	}
	head := limit / 2
	tail := limit - head
	// По возможности режем по границе строки: так структура вывода
	// остаётся читаемой.
	if i := strings.LastIndex(string(r[:head]), "\n"); i > 0 {
		head = i
	}
	if j := strings.Index(string(r[len(r)-tail:]), "\n"); j > 0 {
		tail = tail - j
	}
	hidden := len(r) - head - tail
	if hidden <= 0 {
		return text
	}
	return string(r[:head]) +
		"\n…[свёрнуто символов: " + strconv.Itoa(hidden) + "]…\n" +
		string(r[len(r)-tail:])
}

// DigestResult — свёртка результата инструмента с подписью.
//
// Подпись важна: без неё модель не понимает, что перед ней свёрнутый
// вывод, и принимает его за полный.
func DigestResult(name, text string, maxTokens int) string {
	if maxTokens <= 0 || ApproxTokens(text) <= maxTokens {
		return text
	}
	lines := len(SplitLines(text))
	head := "[свёрнуто: " + name + " — " + strconv.Itoa(lines) + " строк, показаны начало и конец]\n"
	return head + Digest(text, maxTokens)
}
