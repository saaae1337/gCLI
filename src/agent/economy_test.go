package agent

// Тесты экономии: подрезка истории, кеш результатов, свёртка повторов и
// учёт счётчиков.

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gcli/core"
	"gcli/providers"
	"gcli/tools"
)

// econSession — сессия, которая принимает счётчики экономии.
type econSession struct {
	fakeSession
	econ core.EconomyStats
}

func (s *econSession) RecordEconomy(e core.EconomyStats) { s.econ.Merge(e) }

// toolMsg — сообщение с результатом инструмента.
func toolMsg(name, content string) core.Message {
	return core.Message{Role: core.RoleTool, Name: name, Content: content, ToolCallID: name + "-1"}
}

// TestPruneKeepsRecentResults — старые результаты сворачиваются, свежие
// остаются целиком: модель должна видеть то, что сделала только что.
func TestPruneKeepsRecentResults(t *testing.T) {
	opt := economyOptions{KeepResults: 2, MaxResultTokens: 4000, OldResultTokens: 200, Budget: 1}
	var msgs []core.Message
	for i := 0; i < 5; i++ {
		msgs = append(msgs, toolMsg("read_file", strings.Repeat("вывод ", 200)+string(rune('a'+i))))
	}
	msgs = append(msgs, toolMsg("read_file", "СВЕЖИЙ"))

	out, saved, pruned := pruneMessages(msgs, opt)
	if saved <= 0 {
		t.Fatal("подрезка обязана экономить токены")
	}
	if pruned == 0 {
		t.Error("подрезка ничего не посчитала")
	}
	last := out[len(out)-1]
	if last.Content != "СВЕЖИЙ" {
		t.Errorf("свежий результат изменён: %q", last.Content)
	}
	// Свежие (последние два) должны остаться целиком.
	if !strings.Contains(out[len(out)-2].Content, "вывод") {
		t.Errorf("второй свежий результат свёрнут: %.60q", out[len(out)-2].Content)
	}
	// Давние — свёрнуты.
	if !strings.Contains(out[0].Content, "свёрнуто") {
		t.Errorf("давний результат не свёрнут: %.60q", out[0].Content)
	}
}

// TestPruneCollapsesDuplicateResults — два одинаковых результата в
// истории стоят одного: второй превращается в указатель.
func TestPruneCollapsesDuplicateResults(t *testing.T) {
	opt := economyOptions{KeepResults: 10, MaxResultTokens: 4000, OldResultTokens: 200, Budget: 1}
	msgs := []core.Message{
		toolMsg("read_file", "одинаковый вывод"),
		toolMsg("read_file", "одинаковый вывод"),
	}
	out, _, _ := pruneMessages(msgs, opt)
	if !strings.Contains(out[0].Content, "повтор результата") {
		t.Errorf("дубликат не свёрнут: %q", out[0].Content)
	}
	if out[1].Content != "одинаковый вывод" {
		t.Errorf("последний результат потерян: %q", out[1].Content)
	}
}

// TestPruneDropsOldReasoning — размышления нужны только там, где родились:
// у последнего сообщения ассистента они остаются, у старых — нет.
func TestPruneDropsOldReasoning(t *testing.T) {
	opt := economyOptions{KeepResults: 1, MaxResultTokens: 4000, Budget: 1}
	msgs := []core.Message{
		{Role: core.RoleAssistant, Content: "первый", Reasoning: "долгие размышления", ReasoningSig: "sig"},
		toolMsg("read_file", "вывод"),
		{Role: core.RoleAssistant, Content: "второй", Reasoning: "свежие размышления", ReasoningSig: "sig2"},
	}
	out, _, _ := pruneMessages(msgs, opt)
	if out[0].Reasoning != "" {
		t.Errorf("старые размышления остались: %q", out[0].Reasoning)
	}
	if out[2].Reasoning != "свежие размышления" {
		t.Errorf("свежие размышления потеряны: %q", out[2].Reasoning)
	}
}

// TestPruneKeepsSmallHistory — пока контекст небольшой, история не трогается:
// полнота важнее экономии.
func TestPruneKeepsSmallHistory(t *testing.T) {
	opt := economyOptions{KeepResults: 2, MaxResultTokens: 4000, Budget: 100_000}
	msgs := []core.Message{toolMsg("read_file", "маленький вывод")}
	out, saved, pruned := pruneMessages(msgs, opt)
	if saved != 0 || pruned != 0 {
		t.Errorf("маленькая история подрезана: saved=%d pruned=%d", saved, pruned)
	}
	if out[0].Content != "маленький вывод" {
		t.Error("содержимое изменено")
	}
}

// TestDedupeCallsOnlyForReadTools — повтор сворачивается только там, где
// повтор ничего не меняет.
func TestDedupeCallsOnlyForReadTools(t *testing.T) {
	read := func(name string) bool { return name == "read_file" }
	never := func(string) bool { return false }
	calls := []core.ToolCall{
		{ID: "1", Name: "read_file", Args: `{"path":"a.go"}`},
		{ID: "2", Name: "read_file", Args: `{"path":"a.go"}`},
		{ID: "3", Name: "write_file", Args: `{"path":"a.go"}`},
		{ID: "4", Name: "write_file", Args: `{"path":"a.go"}`},
	}
	uniq, origin, dups := dedupeCalls(calls, read)
	if len(dups) != 1 || dups[0] != 1 {
		t.Fatalf("повтор чтения не свёрнут: dups=%v", dups)
	}
	if len(uniq) != 3 || len(origin) != 3 {
		t.Fatalf("уникальных вызовов %d, ожидалось 3", len(uniq))
	}
	_, _, dups = dedupeCalls(calls, never)
	if len(dups) != 0 {
		t.Errorf("повтор записи свёрнут: dups=%v", dups)
	}
}

// TestResultCacheInvalidatedByFileChange — прошлый результат годится,
// пока файл не менялся.
func TestResultCacheInvalidatedByFileChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newResultCache()
	args := `{"path":` + strconv.Quote(p) + `}`
	key := cacheKey("read_file", args)
	stamp := fileStamp(args)
	c.put(key, "старое содержимое", stamp)

	if got, ok := c.get(key, stamp); !ok || got != "старое содержимое" {
		t.Fatalf("кеш не отдал результат: %q ok=%v", got, ok)
	}
	// Файл изменился — результат больше недействителен.
	if err := os.WriteFile(p, []byte("package a // изменён\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newStamp := fileStamp(args)
	if _, ok := c.get(key, newStamp); ok {
		t.Error("изменившийся файл не сбросил кеш")
	}
}

// TestRepeatedReadUsesCache — повторное чтение того же файла не должно
// снова вызывать инструмент: работа уже сделана, а файл не менялся.
func TestRepeatedReadUsesCache(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := &fakeSession{}
	reg := tools.New(tools.Env{WorkDir: dir, Session: sess})
	calls := 0
	reg.Register(&tools.Tool{
		Def: tools.ToolDef{
			Name: "read_file", Description: "чтение",
			Schema: `{"type":"object","properties":{}}`,
		},
		Category: "read",
		Handler: func(ctx context.Context, m map[string]any) (tools.Result, error) {
			calls++
			return tools.Result{Text: "содержимое файла"}, nil
		},
	})
	a := New(Deps{Registry: reg, Session: sess}, dir)
	tc := core.ToolCall{ID: "1", Name: "read_file", Args: `{"path":"` + p + `"}`}

	first := a.execOne(context.Background(), tc)
	second := a.execOne(context.Background(), tc)
	if calls != 1 {
		t.Errorf("инструмент вызван %d раз, ожидался 1", calls)
	}
	if first.text != second.text {
		t.Errorf("кеш отдал другой результат: %q / %q", first.text, second.text)
	}
	if a.econ.CacheHits != 1 {
		t.Errorf("попаданий в кеш %d, ожидалось 1", a.econ.CacheHits)
	}
}

// TestBuildRequestSendsPrunedHistory — в запрос уходит подрезанная копия,
// а сессия хранит историю целиком: человек должен видеть всё, что было.
func TestBuildRequestSendsPrunedHistory(t *testing.T) {
	sess := &fakeSession{}
	for i := 0; i < 10; i++ {
		sess.msgs = append(sess.msgs, toolMsg("read_file",
			strings.Repeat("вывод ", 500)+string(rune('a'+i))))
	}
	a := New(Deps{Session: sess, AutoCompact: 4000}, t.TempDir())
	creq := a.BuildRequest()
	if len(creq.Messages) != len(sess.msgs) {
		t.Fatalf("в запрос ушло %d сообщений, ожидалось %d", len(creq.Messages), len(sess.msgs))
	}
	if !strings.Contains(creq.Messages[0].Content, "свёрнуто") {
		t.Errorf("в запрос ушёл неподрезанный результат: %.60q", creq.Messages[0].Content)
	}
	if !strings.Contains(sess.msgs[0].Content, "вывод") {
		t.Error("сессия потеряла результат")
	}
	if a.econ.PrunedTokens <= 0 {
		t.Error("счётчик подрезки не вырос")
	}
}

// TestEconomyReachesSession — счётчики экономики доходят до сессии.
func TestEconomyReachesSession(t *testing.T) {
	sess := &econSession{}
	a := New(Deps{Session: sess}, t.TempDir())
	a.econ.Requests = 3
	a.econ.PrunedTokens = 100
	a.recordEconomy()
	if sess.econ.Requests != 3 || sess.econ.PrunedTokens != 100 {
		t.Errorf("счётчики не дошли до сессии: %+v", sess.econ)
	}
}

// TestEconomyCountersResetBetweenTurns — счётчики за ход не копятся:
// иначе расход сессии считался бы несколько раз.
func TestEconomyCountersResetBetweenTurns(t *testing.T) {
	dir := t.TempDir()
	sess := &econSession{}
	sp := &scriptProvider{steps: [][]string{
		textStep("первый ответ"),
		textStep("второй ответ"),
	}}
	a := New(Deps{
		Session:   sess,
		Providers: providers.NewClient(),
		Provider:  sp.provider(t),
		Model:     "test-model",
	}, dir)

	if err := a.Run(context.Background(), "первый вопрос"); err != nil {
		t.Fatalf("первый ход: %v", err)
	}
	if err := a.Run(context.Background(), "второй вопрос"); err != nil {
		t.Fatalf("второй ход: %v", err)
	}
	// Два хода — два запроса, а не три (1 + 2).
	if sess.econ.Requests != 2 {
		t.Errorf("запросов учтено %d, ожидалось 2", sess.econ.Requests)
	}
}

// TestPruneDropsOldImages — картинка нужна модели один раз: когда она на
// неё смотрит. Дальше она только дорогая (снимок экрана — полторы-две
// тысячи токенов), и десяток снимков в истории съедал половину окна на
// каждом запросе.
func TestPruneDropsOldImages(t *testing.T) {
	opt := economyOptions{KeepResults: 6, MaxResultTokens: 4000, OldResultTokens: 200, Budget: 1_000_000}
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "снимок 1", Images: []core.Image{{MIME: "image/png", Data: "AAA", Path: "a.png"}}},
		{Role: core.RoleUser, Content: "текст", Images: nil},
		{Role: core.RoleUser, Content: "снимок 2", Images: []core.Image{{MIME: "image/png", Data: "BBB", Path: "b.png"}}},
	}
	out, _, _ := pruneMessages(msgs, opt)
	if len(out[0].Images) != 0 {
		t.Errorf("старая картинка осталась в запросе: %+v", out[0].Images)
	}
	if len(out[2].Images) != 1 {
		t.Errorf("последняя картинка потеряна: %+v", out[2].Images)
	}
}

// TestFillImagesRestoresFromFile — данные картинки возвращаются в запрос
// из файла: в сессии лежит только путь.
func TestFillImagesRestoresFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.png")
	raw := []byte("\x89PNG\r\n\x1a\n" + "пиксели")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "картинка", Images: []core.Image{{MIME: "image/png", Path: p}}},
	}
	fillImages(msgs)
	if msgs[0].Images[0].Data == "" {
		t.Fatal("данные картинки не вернулись из файла")
	}
	got, err := base64.StdEncoding.DecodeString(msgs[0].Images[0].Data)
	if err != nil {
		t.Fatalf("данные картинки не base64: %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("в запрос ушла не та картинка: %q", got)
	}
}

// TestRequestEstimateCountsPruning — оценка запроса совпадает с тем, что
// реально уходит провайдеру.
func TestRequestEstimateCountsPruning(t *testing.T) {
	var msgs []core.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, toolMsg("read_file",
			strings.Repeat("вывод ", 500)+string(rune('a'+i))))
	}
	total, saved := RequestEstimate("системный промпт", nil, msgs)
	if total <= 0 {
		t.Fatal("оценка запроса пустая")
	}
	if saved <= 0 {
		t.Error("подрезка не учтена в оценке")
	}
}
