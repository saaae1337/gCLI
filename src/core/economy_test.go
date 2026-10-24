package core

// Тесты экономии: свёртка длинных текстов и счётчики расхода.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDigestKeepsHeadAndTail — свёртка оставляет начало и конец: по началу
// видно, что за файл, по концу — чем он закончился.
func TestDigestKeepsHeadAndTail(t *testing.T) {
	text := strings.Repeat("строка вывода\n", 500)
	got := Digest(text, 100)
	if len(got) >= len(text) {
		t.Errorf("свёртка ничего не убрала: %d символов", len(got))
	}
	if !strings.Contains(got, "строка вывода") {
		t.Error("начало потеряно")
	}
	if !strings.Contains(got, "свёрнуто") {
		t.Errorf("нет отметки о свёртке: %q", got[:60])
	}
	if ApproxTokens(got) > 150 {
		t.Errorf("после свёртки %d токенов, ожидалось меньше 150", ApproxTokens(got))
	}
}

// TestDigestCutsLongSingleLine — одна длинная строка (минифицированный
// JSON, лог без переводов) тоже должна сокращаться: раньше свёртка
// работала только по строкам и такую строку не трогала.
func TestDigestCutsLongSingleLine(t *testing.T) {
	text := strings.Repeat("x", 20000)
	got := Digest(text, 100)
	if len(got) >= len(text) {
		t.Error("длинная одиночная строка не сокращена")
	}
	if !strings.Contains(got, "свёрнуто") {
		t.Errorf("нет отметки о свёртке: %q", got[:60])
	}
}

// TestDigestKeepsShortText — короткий текст не трогается.
func TestDigestKeepsShortText(t *testing.T) {
	text := "короткий вывод"
	if got := Digest(text, 100); got != text {
		t.Errorf("короткий текст изменён: %q", got)
	}
}

// TestDigestResultMarksSource — свёрнутый результат инструмента должен
// называть инструмент: иначе модель принимает свёртку за полный вывод.
func TestDigestResultMarksSource(t *testing.T) {
	text := strings.Repeat("вывод команды\n", 500)
	got := DigestResult("bash", text, 100)
	if !strings.Contains(got, "bash") {
		t.Errorf("в свёртке нет имени инструмента: %q", got[:80])
	}
	if !strings.Contains(got, "строк") {
		t.Errorf("в свёртке нет числа строк: %q", got[:80])
	}
}

// TestEconomyStatsMerge — счётчики складываются: субагенты пишут в общий
// счёт сессии.
func TestEconomyStatsMerge(t *testing.T) {
	var e EconomyStats
	e.AddCall("read_file", 100)
	e.AddCall("read_file", 50)
	e.AddCall("bash", 10)
	if e.ToolCalls["read_file"] != 2 || e.ToolTokens["read_file"] != 150 {
		t.Errorf("учёт вызовов неверный: %+v", e.ToolCalls)
	}
	other := EconomyStats{Requests: 2, PrunedTokens: 500, CacheHits: 1}
	e.Merge(other)
	if e.Requests != 2 || e.PrunedTokens != 500 || e.CacheHits != 1 {
		t.Errorf("слияние счётчиков неверное: %+v", e)
	}
	if e.ToolCalls["read_file"] != 2 {
		t.Errorf("счётчики инструментов потерялись при слиянии: %+v", e.ToolCalls)
	}
}

// TestEconomyTopTool — самый дорогой инструмент виден сразу.
func TestEconomyTopTool(t *testing.T) {
	var e EconomyStats
	e.AddCall("read_file", 100)
	e.AddCall("bash", 900)
	name, tok := e.TopTool()
	if name != "bash" || tok != 900 {
		t.Errorf("дорогой инструмент определён неверно: %q %d", name, tok)
	}
}

// TestEconomyAvgTokensPerRequest — средний расход на запрос считается
// только когда запросы были.
func TestEconomyAvgTokensPerRequest(t *testing.T) {
	var e EconomyStats
	if got := e.AvgTokensPerRequest(); got != 0 {
		t.Errorf("без запросов среднее = %d, ожидалось 0", got)
	}
	e.Requests = 4
	e.PrunedTokens = 400
	if got := e.AvgTokensPerRequest(); got != 100 {
		t.Errorf("среднее = %d, ожидалось 100", got)
	}
}

// TestSessionKeepsImagesByPath — в файле сессии картинка лежит путём, а не
// данными: снимок экрана весит мегабайты, и десяток таких в истории
// раздувал сессию и съедал контекст на каждом запросе.
func TestSessionKeepsImagesByPath(t *testing.T) {
	s := &Session{ID: "s1"}
	s.Messages = append(s.Messages, Message{Role: RoleUser, Content: "картинка", Images: []Image{
		{MIME: "image/png", Data: "iVBORw0KGgoAAAANSUhEUg", Path: "shot.png"},
	}})
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "iVBORw0KGgo") {
		t.Errorf("данные картинки попали в файл сессии: %s", data)
	}
	if !strings.Contains(string(data), "shot.png") {
		t.Errorf("путь к картинке потерялся: %s", data)
	}
}
