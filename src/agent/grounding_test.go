package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gcli/providers"
	"gcli/subagents"
	"gcli/tools"
)

// ---------- Проверка отчёта субагента по доказательствам ----------

// scriptProvider — провайдер, отдающий заранее заданные ответы по одному на
// каждый запрос. Это позволяет проверить весь путь RunSubagent без сети:
// модель сначала читает файл, потом отдаёт отчёт со ссылками — и мы видим,
// что подтверждённые и выдуманные ссылки обрабатываются по-разному.
type scriptProvider struct {
	mu      sync.Mutex
	steps   [][]string // один слайс = набор SSE-событий на один запрос
	calls   int
	history []string // тело последнего запроса (для проверки промпта добивки)
}

func (s *scriptProvider) next(t *testing.T) []string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls >= len(s.steps) {
		t.Fatalf("лишний запрос к провайдеру: %d (шагов было %d)", s.calls+1, len(s.steps))
	}
	step := s.steps[s.calls]
	s.calls++
	return step
}

func (s *scriptProvider) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// provider — обернуть скрипт в локальный OpenAI-совместимый сервер.
func (s *scriptProvider) provider(t *testing.T) *providers.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body := readAllBody(r); body != "" {
			s.mu.Lock()
			s.history = append(s.history, body)
			s.mu.Unlock()
		}
		var events []string
		for i, e := range s.next(t) {
			if strings.HasPrefix(e, "@") {
				// Директива: ждать следующего запроса нельзя — это конец шага.
				events = append(events, "data: [DONE]\n\n")
				_ = i
				break
			}
			events = append(events, e)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, e := range events {
			_, _ = fmt.Fprint(w, e)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return &providers.Provider{
		ID: "test", Label: "test", Kind: providers.ProtoOpenAI,
		BaseURL: srv.URL + "/v1", NoKey: true,
	}
}

// textStep — ответ без вызовов инструментов.
func textStep(text string) []string {
	return []string{
		"data: " + jsonChunk(map[string]any{"content": text}) + "\n\n",
		"data: " + jsonChunk(map[string]any{"finish": "stop"}) + "\n\n",
		"data: [DONE]\n\n",
	}
}

// toolStep — ход с вызовом инструмента.
//
// Ключ именно tool_calls: это так устроен формат дельты OpenAI, и другое имя
// молча дало бы «ход без вызовов» — тест прошёл бы, ничего не проверяя.
func toolStep(name, args string) []string {
	call := map[string]any{
		"index": 0, "id": "c1", "type": "function",
		"function": map[string]any{"name": name, "arguments": args},
	}
	return []string{
		"data: " + jsonChunk(map[string]any{"tool_calls": []any{call}}) + "\n\n",
		"data: " + jsonChunk(map[string]any{"finish": "tool_calls"}) + "\n\n",
		"data: [DONE]\n\n",
	}
}

func jsonChunk(v map[string]any) string {
	choice := map[string]any{"index": 0, "delta": v}
	if f, ok := v["finish"]; ok {
		delete(v, "finish")
		choice["delta"] = v
		choice["finish_reason"] = f
	}
	b, _ := json.Marshal(map[string]any{"choices": []any{choice}})
	return string(b)
}

func readAllBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

// setupSubagent — окружение для одного запуска субагента: каталог с файлами
// на 40 строк, провайдер по сценарию и реестр инструментов.
func setupSubagent(t *testing.T, steps [][]string) (dir string, p *providers.Provider, sp *scriptProvider) {
	t.Helper()
	dir = t.TempDir()
	home := t.TempDir()
	old := os.Getenv("GCLI_HOME")
	if err := os.Setenv("GCLI_HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Setenv("GCLI_HOME", old) })

	var sb strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&sb, "строка %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "pool.go"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	// Файл, которого в проекте нет: ссылка на него в отчёте — фантом.
	sp = &scriptProvider{steps: steps}
	p = sp.provider(t)
	return dir, p, sp
}

func runSubagent(t *testing.T, dir string, p *providers.Provider) subagents.Outcome {
	t.Helper()
	reg := func(_ bool, _, _ []string) *tools.Registry {
		return tools.New(tools.Env{WorkDir: dir}).Base()
	}
	return RunSubagent(context.Background(), SubagentDeps{
		Provider:  p,
		Providers: providers.NewClient(),
		Registry:  reg,
		Model:     "test-model",
		WorkDir:   dir,
		MaxIters:  4,
	}, subagents.Spec{Type: subagents.TypeExplorer, Task: "что в pool.go?"})
}

// TestGroundingCatchesPhantomFile — отчёт ссылается на файл, которого нет на
// диске. Раньше такой текст уходил главному агенту как факт.
func TestGroundingCatchesPhantomFile(t *testing.T) {
	report := "## Итог\n- pool.go:10 — верно\n- ghost/нет-такого.go:5 — выдумано"
	dir, p, _ := setupSubagent(t, [][]string{
		toolStep("read_file", `{"path":"pool.go"}`),
		textStep(report),
		// Добивка не помогла: модель повторяет тот же текст.
		textStep(report),
	})
	out := runSubagent(t, dir, p)

	if !strings.Contains(out.Full, "ghost/нет-такого.go") {
		t.Fatalf("фантомный файл пропал из отчёта:\n%s", out.Full)
	}
	if !strings.Contains(out.Full, "Проверка отчёта") {
		t.Errorf("в отчёт не добавлен блок проверки:\n%s", out.Full)
	}
}

// TestGroundingTrustworthyReportHasNoBlock — если субагент говорит только о том,
// что реально прочитал, отчёт принимается без ворчаний проверки.
func TestGroundingTrustworthyReportHasNoBlock(t *testing.T) {
	dir, p, _ := setupSubagent(t, [][]string{
		toolStep("read_file", `{"path":"pool.go"}`),
		textStep("## Итог\n- pool.go:10 содержит «строка 10»"),
	})
	out := runSubagent(t, dir, p)

	if strings.Contains(out.Full, "Проверка отчёта") {
		t.Errorf("подтверждённый отчёт не должен помечаться как непроверенный:\n%s", out.Full)
	}
}

// TestGroundingRepairsUnverifiedReport — главный сценарий: субагент сослался на
// строку, которую не открывал. Должна произойти адресная добивка: модель
// получает список проблемных мест и может их открыть.
func TestGroundingRepairsUnverifiedReport(t *testing.T) {
	dir, p, sp := setupSubagent(t, [][]string{
		// Первое чтение — только начало файла: дальше субагент не смотрел.
		toolStep("read_file", `{"path":"pool.go","offset":1,"limit":10}`),
		// Первая попытка отчёта: часть ссылок выдумана.
		textStep("## Итог\n- pool.go:5 — видел\n- pool.go:39 — не открывал"),
		// Добивка: субагент открывает спорное место.
		toolStep("read_file", `{"path":"pool.go","offset":35,"limit":10}`),
		// Переписанный отчёт — только проверенные утверждения.
		textStep("## Итог\n- pool.go:5 — видел\n- pool.go:39 — проверил, строка есть"),
	})
	out := runSubagent(t, dir, p)

	if sp.count() < 4 {
		t.Fatalf("добивка не запустилась: запросов к провайдеру %d", sp.count())
	}
	// В промпте добивки должны быть перечислены конкретные проблемные места.
	joined := strings.Join(sp.history, "\n")
	if !strings.Contains(joined, "pool.go:39") {
		t.Errorf("в добивке не указан проблемный адрес:\n%s", joined)
	}
	if !strings.Contains(out.Full, "проверил, строка есть") {
		t.Errorf("исправленный отчёт не принят:\n%s", out.Full)
	}
}

// TestRepairSkippedWhenNothingRead — если субагент не читал ничего, добивка не
// запускается: это уже не починка, а полноценное исследование с нуля.
func TestRepairSkippedWhenNothingRead(t *testing.T) {
	dir, p, sp := setupSubagent(t, [][]string{
		textStep("## Итог\n- где-то в проекте есть pool.go:10 — вероятно так"),
	})
	out := runSubagent(t, dir, p)

	if sp.count() != 1 {
		t.Errorf("добивка запущена без единого прочитанного файла: запросов %d", sp.count())
	}
	if !strings.Contains(out.Full, "Проверка отчёта") {
		t.Errorf("непроверенный отчёт без пометки ушёл наверх:\n%s", out.Full)
	}
}

// TestRepairKeepsOriginalOnFailure — если добивка не удалась, исходный отчёт
// сохраняется и лишь помечается. Бросать полезный (хоть и непроверенный)
// результат хуже, чем показать его с оговоркой.
func TestRepairKeepsOriginalOnFailure(t *testing.T) {
	dir, p, _ := setupSubagent(t, [][]string{
		toolStep("read_file", `{"path":"pool.go","offset":1,"limit":10}`),
		textStep("## Итог\n- pool.go:5 — видел\n- pool.go:38 — выдумано"),
		// Добивка сорвалась: вместо отчёта модель бросила фразу на полуслове.
		textStep("Сейчас всё посмотрю и исправлю"),
	})
	out := runSubagent(t, dir, p)

	if !strings.Contains(out.Full, "pool.go:38") {
		t.Errorf("исходный отчёт потерян после неудачной добивки:\n%s", out.Full)
	}
	if !strings.Contains(out.Full, "Проверка отчёта") {
		t.Errorf("непроверенное утверждение не помечено:\n%s", out.Full)
	}
}
