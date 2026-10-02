package agent

// Регрессионные тесты багфикс-прохода 6.2.1: остановка миссии на итерации
// с вызовами инструментов, передача инструмента хосту из паники, учёт
// токенов в тихих вызовах, фантомные повторы в LoopDetector и сохранение
// размышлений при оборванном потоке.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
)

// testReadTool — read_file со схемой: пустой Schema — невалидный
// json.RawMessage в теле запроса к провайдеру.
func testReadTool(r *tools.Registry) {
	r.Register(&tools.Tool{
		Def: tools.ToolDef{
			Name:        "read_file",
			Description: "тестовый",
			Schema:      `{"type":"object","properties":{}}`,
		},
		Handler: func(ctx context.Context, m map[string]any) (tools.Result, error) {
			return tools.Result{Text: "файл", Summary: "файл"}, nil
		},
	})
}

// usageStep — ход с usage на корне chunk-а (так его и присылает OpenAI).
func usageStep(prompt, completion int) string {
	chunk := fmt.Sprintf(`{"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d}}`, prompt, completion)
	return "data: " + chunk + "\n\n"
}

// TestMissionStopOnToolCallClosesCalls — прогон, остановленный ровно на
// итерации с вызовами инструментов, обязан закрыть каждый вызов ответом.
// Раньше в истории оставалось assistant(tool_calls) без tool-сообщений:
// финальный отчёт умирал с 400-й ошибкой провайдера, и прогон заканчивался
// молча, без результата.
func TestMissionStopOnToolCallClosesCalls(t *testing.T) {
	dir := t.TempDir()
	sess := &usageSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	testReadTool(reg)

	sp := &scriptProvider{steps: [][]string{
		toolStep("read_file", `{"path":"a.go"}`), // ход с вызовом — на нём и остановимся
		textStep("ИТОГОВЫЙ ОТЧЁТ: работа остановлена"),
	}}

	a := New(Deps{
		Registry:  reg,
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Model:     "test-model",
	}, dir)

	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime, MaxToolCalls: 1}.Apply(), 0, 0)
	if !a.StartMission(core.Mission{Mode: core.MissionLongTime, MaxToolCalls: 1}.Apply(),
		MissionDeps{Tracker: tr, Spent: func() int { return 0 }}) {
		t.Fatal("миссия не запустилась")
	}

	if err := a.Run(context.Background(), "задача"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Найти assistant-сообщение с вызовом и убедиться, что сразу за ним
	// идёт tool-ответ с тем же tool_call_id.
	var sawCalls, sawClose bool
	for i, m := range sess.msgs {
		if m.Role == core.RoleAssistant && len(m.ToolCalls) > 0 {
			sawCalls = true
			if i+1 < len(sess.msgs) &&
				sess.msgs[i+1].Role == core.RoleTool &&
				sess.msgs[i+1].ToolCallID == m.ToolCalls[0].ID &&
				strings.Contains(sess.msgs[i+1].Content, "не выполнен") {
				sawClose = true
			}
		}
	}
	if !sawCalls {
		t.Fatal("в истории нет assistant-сообщения с вызовом инструмента")
	}
	if !sawClose {
		t.Error("вызов инструмента не закрыт синтетическим ответом — история отравлена")
	}
}

// TestMissionUserStopOnToolCallClosesCalls — то же при остановке человеком
// (/mission stop): флаг missionStop проверяется на той же итерации.
func TestMissionUserStopOnToolCallClosesCalls(t *testing.T) {
	dir := t.TempDir()
	sess := &usageSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	testReadTool(reg)

	sp := &scriptProvider{steps: [][]string{
		toolStep("read_file", `{"path":"a.go"}`),
		textStep("ОТЧЁТ: остановлено пользователем"),
	}}

	a := New(Deps{
		Registry:  reg,
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Model:     "test-model",
	}, dir)

	tr := core.NewTracker(core.Mission{Mode: core.MissionLongTime}.Apply(), 0, 0)
	a.StartMission(core.Mission{Mode: core.MissionLongTime}.Apply(),
		MissionDeps{Tracker: tr, Spent: func() int { return 0 }})
	// Человек нажал «стоп» до хода: флаг проверяется на первой же итерации
	// после ответа модели с вызовами.
	a.StopMission()

	if err := a.Run(context.Background(), "задача"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for i, m := range sess.msgs {
		if m.Role == core.RoleAssistant && len(m.ToolCalls) > 0 {
			if i+1 >= len(sess.msgs) || sess.msgs[i+1].Role != core.RoleTool {
				t.Fatal("остановка пользователем оставила вызов без ответа")
			}
			return
		}
	}
	t.Fatal("не увидели ход с вызовами инструментов")
}

// TestExecOnePanicPassesToolToHost — паника инструмента не должна передавать
// хосту nil вместо инструмента: обратный вызов разыменовывает категорию,
// и вторая паника (уже внутри recover) убивала бы процесс в параллельной ветке.
func TestExecOnePanicPassesToolToHost(t *testing.T) {
	dir := t.TempDir()
	sess := &fakeSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	reg.Register(&tools.Tool{
		Def:      tools.ToolDef{Name: "boom", Description: "тестовый"},
		Category: "test-cat",
		Handler: func(ctx context.Context, m map[string]any) (tools.Result, error) {
			panic("бум")
		},
	})

	a := New(Deps{Registry: reg, Session: sess}, dir)

	gotName := ""
	var gotTool *tools.Tool
	a.d.OnToolDone = func(tc core.ToolCall, tool *tools.Tool, res tools.Result, err error, elapsed time.Duration) {
		gotName = tc.Name
		gotTool = tool
	}

	res := a.execOne(context.Background(), core.ToolCall{ID: "c1", Name: "boom"})
	if !strings.Contains(res.text, "бум") {
		t.Errorf("паника не превращена в ошибку инструмента: %q", res.text)
	}
	if gotName != "boom" {
		t.Error("OnToolDone не вызван из паники")
	}
	if gotTool == nil {
		t.Fatal("хост получил nil вместо инструмента — его колбэк упал бы вторым recover-ом")
	}
	if gotTool.Category != "test-cat" {
		t.Errorf("категория инструмента потеряна: %q", gotTool.Category)
	}
}

// TestQuietUsageStillCounted — тихий вызов (compact, отчёты субагентов)
// обязан попадать в учёт: раньше quiet глушил и печать, и счётчик.
func TestQuietUsageStillCounted(t *testing.T) {
	dir := t.TempDir()
	sess := &usageSession{}
	sp := &scriptProvider{steps: [][]string{{
		usageStep(100, 20),
		"data: " + jsonChunk(map[string]any{"content": "выжимка"}) + "\n\n",
		"data: " + jsonChunk(map[string]any{"finish": "stop"}) + "\n\n",
		"data: [DONE]\n\n",
	}}}

	a := New(Deps{
		Registry:  tools.New(tools.Env{WorkDir: dir}),
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Model:     "test-model",
	}, dir)

	msg, err := a.callModel(context.Background(), core.ChatRequest{Model: "test-model"}, true)
	if err != nil {
		t.Fatalf("callModel: %v", err)
	}
	if msg.Content != "выжимка" {
		t.Fatalf("ответ потерян: %q", msg.Content)
	}
	if sess.usage.PromptTokens != 100 || sess.usage.CompletionTokens != 20 {
		t.Errorf("тихий вызов не учтён: %+v", sess.usage)
	}
}

// TestLoopDetectorDuplicateInOneBatch — два одинаковых вызова в одной
// итерации раньше оставляли фантомный счётчик навсегда: после вытеснения
// из окна модель получала ложное «повторяешь третий раз» на втором повторе.
func TestLoopDetectorDuplicateInOneBatch(t *testing.T) {
	d := NewLoopDetector()

	// Дубль в одной итерации.
	same := []core.ToolCall{tc("read_file", `{"path":"a.go"}`), tc("read_file", `{"path":"a.go"}`)}
	d.Record(same, nil)

	// Вытесняем окно разными вызовами.
	for i := 0; i < loopLookback; i++ {
		d.Record([]core.ToolCall{tc("read_file", `{"path":"f`+string(rune('a'+i))+`.go"}`)}, nil)
	}
	// Два честных повтора в окне — это ещё не петля (порог — три).
	for i := 0; i < 2; i++ {
		if w := d.Record([]core.ToolCall{tc("read_file", `{"path":"a.go"}`)}, nil); w != "" {
			t.Fatalf("фантомный счётчик дал ложную петлю на %d-м повторе: %s", i+1, w)
		}
	}
}

// TestPartialStreamKeepsReasoning — поток, оборвавшийся после текста,
// обязан сохранить уже показанные человеку размышления: раньше ранний
// возврат шёл мимо строки, где Reasoning попадает в сообщение, и Ctrl+O
// после такого ответа показывал пустоту.
func TestPartialStreamKeepsReasoning(t *testing.T) {
	dir := t.TempDir()
	sess := &fakeSession{}
	// Шаг без [DONE]: сервер отдаёт reasoning + текст и роняет соединение.
	sp := &scriptProvider{steps: [][]string{{
		"data: " + jsonChunk(map[string]any{"reasoning": "думаю о задаче"}) + "\n\n",
		"data: " + jsonChunk(map[string]any{"content": "частичный ответ"}) + "\n\n",
	}}}

	a := New(Deps{
		Registry:  tools.New(tools.Env{WorkDir: dir}),
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Model:     "test-model",
	}, dir)

	msg, err := a.callModel(context.Background(), core.ChatRequest{Model: "test-model"}, false)
	if err != nil {
		t.Fatalf("частичный поток должен возвращаться как успех: %v", err)
	}
	if msg.Reasoning == "" {
		t.Error("размышления потеряны при оборванном потоке")
	}
	if !strings.Contains(msg.Content, "частичный") {
		t.Errorf("текст потерян: %q", msg.Content)
	}
}
