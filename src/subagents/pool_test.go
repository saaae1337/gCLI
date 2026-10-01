package subagents

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseType(t *testing.T) {
	cases := map[string]Type{
		"explorer":   TypeExplorer,
		"Explorer":   TypeExplorer,
		" REVIEWER ": TypeReviewer,
		"кодер":      TypeCoder,
		"code":       TypeCoder,
		"веб":        TypeResearcher,
		"web":        TypeResearcher,
		"custom":     TypeCustom,
		"":           TypeCustom,
	}
	for in, want := range cases {
		got, hint := ParseType(in)
		if got != want {
			t.Errorf("ParseType(%q) = %q, ожидалось %q", in, got, want)
		}
		if hint != "" && got != want {
			t.Logf("подсказка для %q: %s", in, hint)
		}
	}
	// Неизвестное имя может оказаться пользовательским агентом: ошибкой
	// не считается, резолвится в custom.
	if got, hint := ParseType("абв"); got != TypeCustom || hint != "" {
		t.Errorf("неизвестный тип: got=%q hint=%q", got, hint)
	}
}

func TestTypeReadOnly(t *testing.T) {
	ro := []Type{TypeExplorer, TypeReviewer, TypeResearcher}
	rw := []Type{TypeCoder, TypeCustom}
	for _, x := range ro {
		if !x.ReadOnly() {
			t.Errorf("%s должен быть только для чтения", x)
		}
	}
	for _, x := range rw {
		if x.ReadOnly() {
			t.Errorf("%s должен иметь право записи", x)
		}
	}
}

func TestToolsForRestrictsWrites(t *testing.T) {
	allow, deny := ToolsFor(TypeExplorer)
	if !contains(allow, "read_file") {
		t.Error("explorer должен читать файлы")
	}
	if contains(allow, "write_file") {
		t.Error("explorer не должен писать файлы")
	}
	if !contains(deny, "write_file") || !contains(deny, "spawn_agent") {
		t.Errorf("explorer должен запрещать запись и вложенность: %v", deny)
	}
}

func TestPromptsAreDistinct(t *testing.T) {
	ctx := PromptContext{WorkDir: "/tmp", Summary: "задача", Notes: "заметки"}
	prompts := map[Type]string{}
	for _, tp := range Types {
		p := tp.Prompt(ctx)
		if strings.TrimSpace(p) == "" {
			t.Errorf("пустой промпт для %s", tp)
		}
		if !strings.Contains(p, "/tmp") {
			t.Errorf("в промпте %s нет рабочего каталога", tp)
		}
		if !strings.Contains(p, "задача") {
			t.Errorf("в промпте %s нет описания задачи", tp)
		}
		prompts[tp] = p
	}
	// Промпты разных типов не должны совпадать.
	if prompts[TypeExplorer] == prompts[TypeReviewer] {
		t.Error("промпты explorer и reviewer совпадают")
	}
}

func TestSummarize(t *testing.T) {
	short := "## Найдено\n- файл:строка"
	if got := Summarize(short); !strings.Contains(got, "файл") {
		t.Errorf("короткий отчёт искажён: %q", got)
	}

	long := "## Отчёт\n" + strings.Repeat("строка содержания\n", 60) + "хвост"
	got := Summarize(long)
	if len(got) > 3000 {
		t.Errorf("сводка слишком длинная: %d символов", len(got))
	}
	if !strings.Contains(got, "Отчёт") {
		t.Errorf("заголовок потерян: %q", got)
	}

	// Блоки кода вырезаются.
	withCode := "текст\n```go\nfunc main() {}\n```\nещё"
	got2 := Summarize(withCode)
	if strings.Contains(got2, "func main") {
		t.Errorf("код не вырезан: %q", got2)
	}
	if Summarize("") != "" {
		t.Error("пустой отчёт должен давать пустую строку")
	}
}

func TestNotes(t *testing.T) {
	if got := Notes(nil); got != "" {
		t.Errorf("Notes(nil) = %q", got)
	}
	got := Notes([]string{"первая", "вторая"})
	if !strings.Contains(got, "первая") || !strings.Contains(got, "вторая") {
		t.Errorf("Notes = %q", got)
	}
}

// okRunner — заглушка, всегда возвращающая отчёт.
func okRunner(_ context.Context, spec Spec) (Outcome, error) {
	return Outcome{
		Full:    "## Отчёт\nпо задаче: " + spec.Task,
		Summary: "кратко",
		Turns:   3,
		Tools:   5,
	}, nil
}

func TestPoolSpawn(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: true, MaxDepth: 1, MaxParallel: 2})
	out, err := p.Spawn(context.Background(), Spec{Type: TypeExplorer, Task: "найти что-нибудь", Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Full, "найти") {
		t.Errorf("отчёт пуст: %q", out.Full)
	}

	runs := p.All()
	if len(runs) != 1 {
		t.Fatalf("ожидался 1 запуск, получено %d", len(runs))
	}
	r := runs[0]
	if r.Status != StatusDone {
		t.Errorf("статус: %s", r.Status)
	}
	if r.Tools != 5 {
		t.Errorf("инструментов: %d", r.Tools)
	}
	if r.Name == "" {
		t.Error("имя не присвоено")
	}
}

func TestPoolDepthLimit(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: true, MaxDepth: 1})
	// Depth 1 — разрешено (субагент первого уровня).
	if _, err := p.Spawn(context.Background(), Spec{Task: "задача", Depth: 1}); err != nil {
		t.Errorf("Depth=1 должен быть разрешён: %v", err)
	}
	// Depth 2 — запрещено.
	if _, err := p.Spawn(context.Background(), Spec{Task: "задача", Depth: 2}); err == nil {
		t.Error("Depth=2 должен быть запрещён при MaxDepth=1")
	}
}

func TestPoolDisabled(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: false, MaxDepth: 2})
	if p.Enabled() {
		t.Error("пул должен быть выключен")
	}
	if _, err := p.Spawn(context.Background(), Spec{Task: "x", Depth: 1}); err == nil {
		t.Error("выключенный пул не должен запускать субагентов")
	}
}

func TestPoolErrorRecorded(t *testing.T) {
	p := NewPool(func(context.Context, Spec) (Outcome, error) {
		return Outcome{}, fmt.Errorf("модель недоступна")
	}, PoolOptions{Enabled: true, MaxDepth: 1})
	_, err := p.Spawn(context.Background(), Spec{Task: "x", Depth: 1})
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	runs := p.All()
	if len(runs) != 1 || runs[0].Status != StatusError {
		t.Errorf("ошибка не записана: %+v", runs)
	}
	if runs[0].Err == "" {
		t.Error("текст ошибки пуст")
	}
}

func TestPoolEmptyReport(t *testing.T) {
	p := NewPool(func(context.Context, Spec) (Outcome, error) {
		return Outcome{Full: "   "}, nil
	}, PoolOptions{Enabled: true, MaxDepth: 1})
	if _, err := p.Spawn(context.Background(), Spec{Task: "x", Depth: 1}); err == nil {
		t.Error("пустой отчёт должен считаться ошибкой")
	}
}

func TestPoolParallelLimit(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	release := make(chan struct{})

	p := NewPool(func(ctx context.Context, spec Spec) (Outcome, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()

		select {
		case <-release:
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}

		mu.Lock()
		active--
		mu.Unlock()
		return Outcome{Full: "ок"}, nil
	}, PoolOptions{Enabled: true, MaxDepth: 1, MaxParallel: 2})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Spawn(context.Background(), Spec{Task: "x", Depth: 1})
		}()
	}
	time.Sleep(400 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	got := maxActive
	mu.Unlock()
	if got > 2 {
		t.Errorf("превышен лимит параллельности: %d > 2", got)
	}
	if got < 1 {
		t.Error("субагенты не выполнялись параллельно")
	}
}

func TestPoolSummaryAndDetails(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: true, MaxDepth: 1})
	_, _ = p.Spawn(context.Background(), Spec{Type: TypeExplorer, Task: "первая задача", Name: "и1", Depth: 1})
	_, _ = p.Spawn(context.Background(), Spec{Type: TypeReviewer, Task: "вторая задача", Name: "р1", Depth: 1})

	sum := p.Summary()
	if !strings.Contains(sum, "и1") || !strings.Contains(sum, "р1") {
		t.Errorf("сводка не содержит имён:\n%s", sum)
	}

	det := p.Details("и1")
	if !strings.Contains(det, "первая задача") {
		t.Errorf("детали не содержат задачу:\n%s", det)
	}
	if p.Details("нет-такого") == "" {
		t.Error("для несуществующего имени ожидалось сообщение")
	}
}

// TestPoolDetailsExplainsDowngradedModel — выбор модели виден в /agents.
//
// Понижение маршрутизацией без объяснения читается как чужая ошибка,
// а это настройка самого пользователя: без этой строки он не сможет ни
// отключить её, ни понять, почему счёт стал меньше.
func TestPoolDetailsExplainsDowngradedModel(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: true, MaxDepth: 1})
	_, _ = p.Spawn(context.Background(), Spec{
		Type:     TypeExplorer,
		Task:     "составь карту кода",
		Name:     "карта",
		Model:    "claude-3-5-haiku",
		ModelWhy: "роль «explorer» простая, модель claude-opus-4 дороже",
		Depth:    1,
	})
	det := p.Details("карта")
	if !strings.Contains(det, "claude-3-5-haiku") {
		t.Errorf("выбранная модель не показана:\n%s", det)
	}
	if !strings.Contains(det, "простая") {
		t.Errorf("причина выбора не показана:\n%s", det)
	}
}

// TestPoolDetailsSilentWithoutRouting — запуск без маршрутизации не получает
// лишних строк: иначе детали каждого субагента обрастают пустым шумом.
func TestPoolDetailsSilentWithoutRouting(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: true, MaxDepth: 1})
	_, _ = p.Spawn(context.Background(), Spec{
		Type: TypeExplorer, Task: "задача", Name: "и1", Depth: 1,
	})
	if det := p.Details("и1"); strings.Contains(det, "Выбор модели") {
		t.Errorf("без маршрутизации напечатано объяснение:\n%s", det)
	}
}

func TestPoolTable(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: true, MaxDepth: 1})
	_, _ = p.Spawn(context.Background(), Spec{Type: TypeExplorer, Task: "задача", Depth: 1})
	rows := p.Table()
	if len(rows) != 1 {
		t.Fatalf("ожидалась 1 строка, получено %d", len(rows))
	}
	if len(rows[0]) < 6 {
		t.Errorf("недостаточно колонок: %d", len(rows[0]))
	}
}

func TestPoolAutoNames(t *testing.T) {
	p := NewPool(okRunner, PoolOptions{Enabled: true, MaxDepth: 1})
	_, _ = p.Spawn(context.Background(), Spec{Type: TypeExplorer, Task: "a", Depth: 1})
	_, _ = p.Spawn(context.Background(), Spec{Type: TypeExplorer, Task: "b", Depth: 1})
	names := map[string]bool{}
	for _, r := range p.All() {
		names[r.Name] = true
	}
	if len(names) != 2 {
		t.Errorf("имена должны различаться: %v", names)
	}
}

func TestPoolCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	p := NewPool(func(c context.Context, spec Spec) (Outcome, error) {
		close(started)
		<-c.Done()
		return Outcome{}, c.Err()
	}, PoolOptions{Enabled: true, MaxDepth: 1})

	go func() {
		_, _ = p.Spawn(ctx, Spec{Task: "долгая", Depth: 1})
	}()
	<-started
	if p.Running() != 1 {
		t.Errorf("ожидался 1 работающий, получено %d", p.Running())
	}
	if n := p.Cancel(); n != 1 {
		t.Errorf("отменено %d, ожидалось 1", n)
	}
	time.Sleep(200 * time.Millisecond)
	if p.Running() != 0 {
		t.Errorf("после отмены работающих: %d", p.Running())
	}
}

func TestRunStatusText(t *testing.T) {
	cases := map[Status]string{
		StatusRunning:  "выполняется",
		StatusDone:     "готов",
		StatusError:    "ошибка",
		StatusCanceled: "отменён",
	}
	for st, want := range cases {
		r := &Run{Status: st}
		if got := r.StatusText(); got != want {
			t.Errorf("StatusText(%s) = %q, ожидалось %q", st, got, want)
		}
	}
}

func TestRunLabel(t *testing.T) {
	r1 := &Run{Name: "мой", Type: TypeExplorer}
	if r1.Label() != "мой" {
		t.Errorf("Label = %q", r1.Label())
	}
	r2 := &Run{Type: TypeReviewer}
	if r2.Label() != TypeReviewer.Label() {
		t.Errorf("без имени Label = %q", r2.Label())
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
