package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
)

// ---------- Параллельные вызовы инструментов ----------

// markTool — инструмент, который возвращает уникальный текст и картинку.
// Картинка возвращается всегда: именно её теряли при параллельном сборе.
func markTool(r *tools.Registry, name, text string) {
	r.Register(&tools.Tool{
		Def: tools.ToolDef{Name: name, Description: "тестовый инструмент"},
		Handler: func(ctx context.Context, m map[string]any) (tools.Result, error) {
			// Разная задержка: гарантирует, что быстрый вызов придёт раньше
			// медленного, и порядок «как пришли» гарантированно разошёлся бы
			// с порядком вызовов.
			if strings.Contains(name, "slow") {
				time.Sleep(40 * time.Millisecond)
			}
			return tools.Result{
				Text:    text,
				Summary: text,
				Images:  []core.Image{{MIME: "image/png", Data: text}},
			}, nil
		},
	})
}

// TestParallelResultsKeepCallOrder — результаты идут в контекст в порядке
// вызовов модели. Порядок tool_call_id обязателен для провайдера, и по нему
// модель сопоставляет «файл X → результат X».
func TestParallelResultsKeepCallOrder(t *testing.T) {
	dir := t.TempDir()
	sess := &fakeSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	markTool(reg, "fast_one", "ПЕРВЫЙ")
	markTool(reg, "slow_two", "ВТОРОЙ")
	markTool(reg, "fast_three", "ТРЕТИЙ")

	a := New(Deps{Registry: reg, Session: sess, Parallel: true, MaxParallelTools: 3}, dir)

	calls := []core.ToolCall{
		{ID: "call_1", Name: "fast_one"},
		{ID: "call_2", Name: "slow_two"},
		{ID: "call_3", Name: "fast_three"},
	}
	res := a.execTools(context.Background(), calls)

	if len(res) != 3 {
		t.Fatalf("результатов %d, ожидалось 3", len(res))
	}
	want := []string{"ПЕРВЫЙ", "ВТОРОЙ", "ТРЕТИЙ"}
	for i, r := range res {
		if r.tc.ID != calls[i].ID {
			t.Fatalf("позиция %d: tool_call_id %q вместо %q", i, r.tc.ID, calls[i].ID)
		}
		if !strings.Contains(r.text, want[i]) {
			t.Errorf("позиция %d: результат %q вместо %q", i, r.text, want[i])
		}
	}
}

// TestParallelDuplicateIDsKeepResults — результат идёт по позиции вызова, а не
// по map[ID]: два вызова с одинаковым (или пустым) tool_call_id раньше
// схлопывались в одну запись, и в истории появлялись два tool-сообщения с
// одним ID — провайдер отвергает такой запрос целиком.
func TestParallelDuplicateIDsKeepResults(t *testing.T) {
	dir := t.TempDir()
	sess := &fakeSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	markTool(reg, "fast_dup", "ПЕРВЫЙ")
	markTool(reg, "slow_dup", "ВТОРОЙ")

	a := New(Deps{Registry: reg, Session: sess, Parallel: true, MaxParallelTools: 2}, dir)
	calls := []core.ToolCall{
		{ID: "same", Name: "fast_dup"},
		{ID: "same", Name: "slow_dup"},
		{ID: "", Name: "fast_dup"},
	}
	res := a.execTools(context.Background(), calls)

	if len(res) != len(calls) {
		t.Fatalf("результатов %d, ожидалось %d", len(res), len(calls))
	}
	want := []string{"ПЕРВЫЙ", "ВТОРОЙ", "ПЕРВЫЙ"}
	for i, r := range res {
		if r.tc.ID != calls[i].ID {
			t.Errorf("позиция %d: tool_call_id %q вместо %q", i, r.tc.ID, calls[i].ID)
		}
		if !strings.Contains(r.text, want[i]) {
			t.Errorf("позиция %d: результат %q вместо %q", i, r.text, want[i])
		}
	}
}

// TestParallelKeepsImages — картинки из параллельных вызовов не теряются.
// Скриншот в пакете из нескольких вызовов молча исчезал, и модель получала
// «инструмент отработал, а картинки нет».
func TestParallelKeepsImages(t *testing.T) {
	dir := t.TempDir()
	sess := &fakeSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	markTool(reg, "fast_shot", "кадр-1")
	markTool(reg, "slow_shot", "кадр-2")

	a := New(Deps{Registry: reg, Session: sess, Parallel: true, MaxParallelTools: 2}, dir)
	res := a.execTools(context.Background(), []core.ToolCall{
		{ID: "call_1", Name: "fast_shot"},
		{ID: "call_2", Name: "slow_shot"},
	})

	total := 0
	for i, r := range res {
		if len(r.images) == 0 {
			t.Errorf("вызов %d (%s) потерял картинки", i, r.tc.Name)
		}
		total += len(r.images)
	}
	if total != 2 {
		t.Errorf("картинок %d, ожидалось 2", total)
	}
}

// TestSequentialAlsoKeepsImages — то же для последовательных вызовов: там
// результат шёл целиком, и проверка ловит регрессию в обоих путях сразу.
func TestSequentialAlsoKeepsImages(t *testing.T) {
	dir := t.TempDir()
	sess := &fakeSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	markTool(reg, "write_file", "запись")

	a := New(Deps{Registry: reg, Session: sess}, dir)
	res := a.execTools(context.Background(), []core.ToolCall{{ID: "c1", Name: "write_file"}})

	if len(res) != 1 {
		t.Fatalf("результатов %d, ожидался 1", len(res))
	}
	if len(res[0].images) != 1 {
		t.Errorf("картинки потеряны: %d", len(res[0].images))
	}
}

// TestPanickingToolDoesNotKillProcess — паника в инструменте превращается в
// текст ошибки, а не роняет агента вместе с сессией.
func TestPanickingToolDoesNotKillProcess(t *testing.T) {
	dir := t.TempDir()
	sess := &fakeSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	reg.Register(&tools.Tool{
		Def:     tools.ToolDef{Name: "boom", Description: "падает"},
		Handler: func(ctx context.Context, m map[string]any) (tools.Result, error) { panic("бум") },
	})
	a := New(Deps{Registry: reg, Session: sess}, dir)

	res := a.execTools(context.Background(), []core.ToolCall{{ID: "c1", Name: "boom"}})
	if len(res) != 1 {
		t.Fatalf("результатов %d, ожидался 1", len(res))
	}
	if !strings.Contains(res[0].text, "бум") {
		t.Errorf("паника не попала в результат: %q", res[0].text)
	}
}

// ---------- Учёт токенов ----------

// usageSession — сессия, которая помнит расход.
type usageSession struct {
	fakeSession
	usage core.Usage
}

func (s *usageSession) AddUsage(u core.Usage) { s.usage.Add(u) }

// sseProvider — провайдер на локальном сервере, который отдаёт поток с usage
// и сразу обрывает соединение. Именно этот случай раньше терял расход: до
// [DONE] дело не доходило, а счётчик пополнялся только на успешном выходе.
func sseProvider(t *testing.T, body string) *providers.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(body))
		if fl != nil {
			fl.Flush()
		}
		// Соединение закрывается без [DONE] — поток оборван на середине.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return &providers.Provider{
		ID: "local", Label: "local", Kind: providers.ProtoOpenAI,
		BaseURL: srv.URL + "/v1", NoKey: true,
	}
}

// TestUsageCountedOnFailedStream — потраченные токены попадают в счётчик даже
// когда поток оборвался. Раньше счётчик пополнялся только на успешном пути,
// поэтому прерванный по Ctrl+C или по сети запрос был бесплатным в /usage,
// а оценка контекста занижалась и сжатие не срабатывало вовремя.
func TestUsageCountedOnFailedStream(t *testing.T) {
	dir := t.TempDir()
	sess := &usageSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	p := sseProvider(t, "data: {\"choices\":[{\"delta\":{\"content\":\"начало \"}}]}\n\n"+
		"data: {\"choices\":[{\"delta\":{\"content\":\"ответа\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":7}}\n\n")

	a := New(Deps{
		Registry:  reg,
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  p,
		Model:     "test-model",
	}, dir)

	msg, _ := a.callModel(context.Background(), core.ChatRequest{Model: "test-model"}, false)
	if !strings.Contains(msg.Content, "начало") {
		t.Fatalf("текст ответа не собран: %q", msg.Content)
	}
	if sess.usage.PromptTokens != 120 || sess.usage.CompletionTokens != 7 {
		t.Errorf("расход не учтён после обрыва потока: %+v", sess.usage)
	}
}

// TestUsageNotDoubleCountedOnSuccess — на успешном пути расход списывается
// ровно один раз: перенос учёта в defer легко дал бы двойной счёт.
func TestUsageNotDoubleCountedOnSuccess(t *testing.T) {
	dir := t.TempDir()
	sess := &usageSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	p := sseProvider(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ответ\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")

	a := New(Deps{
		Registry:  reg,
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  p,
		Model:     "test-model",
	}, dir)

	if _, err := a.callModel(context.Background(), core.ChatRequest{Model: "test-model"}, false); err != nil {
		t.Fatalf("поток должен был завершиться успешно: %v", err)
	}
	if sess.usage.Total() != 53 {
		t.Errorf("расход посчитан неверно: %+v (ожидалось 53)", sess.usage)
	}
}
