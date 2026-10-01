package subagents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- Тесты кеша повторов и дедупликации ----------

// cacheRunner — счётчик вызовов runner'а: кеш виден по тому, что работа НЕ была
// выполнена, и никакого другого способа это доказать в тесте нет.
type cacheRunner struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
	body  string
}

func (c *cacheRunner) run(_ context.Context, spec Spec) (Outcome, error) {
	c.mu.Lock()
	c.calls++
	d, body := c.delay, c.body
	c.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	if body == "" {
		body = "## Найдено\n- pool.go:39\n\n## Вывод\n- кеш живёт в пуле\n"
	}
	return Outcome{Full: body, Summary: "отчёт", Turns: 1, Tools: 1}, nil
}

func (c *cacheRunner) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newCachePool(r *cacheRunner) *Pool {
	return NewPool(r.run, PoolOptions{MaxParallel: 4, MaxDepth: 2, Enabled: true})
}

func cacheSpec(task string) Spec {
	return Spec{Type: TypeExplorer, Task: task, Depth: 1}
}

func TestCacheKeyStableForSameTask(t *testing.T) {
	a := CacheKey(cacheSpec("  Найти, где кеш  "))
	b := CacheKey(cacheSpec("найти, где кеш"))
	if a != b {
		t.Fatalf("ключ зависит от регистра и пробелов: %s != %s", a, b)
	}
}

func TestCacheKeyDistinguishesRoleModelTask(t *testing.T) {
	base := cacheSpec("посмотри авторизацию")
	cases := []struct {
		name string
		spec Spec
	}{
		{"роль", Spec{Type: TypeReviewer, Task: base.Task, Depth: 1}},
		{"модель", Spec{Type: base.Type, Task: base.Task, Model: "opus", Depth: 1}},
		{"задача", cacheSpec("посмотри авторизацию в другом месте")},
		{"инструменты", Spec{Type: base.Type, Task: base.Task, ToolsAllow: []string{"read_file"}, Depth: 1}},
	}
	for _, c := range cases {
		if CacheKey(c.spec) == CacheKey(base) {
			t.Errorf("%s: ключ совпал — разные работы переиспользуют отчёт", c.name)
		}
	}
}

// Роль с правкой записи не кешируется никогда: её отчёт утверждает, что
// изменение уже сделано, а повтор через минуту — враньё.
func TestCacheableRejectsWriteRoles(t *testing.T) {
	for _, typ := range []Type{TypeCoder, TypeTester, TypeFrontend, TypeDocs, TypeGeneral, TypeCustom} {
		if Cacheable(Spec{Type: typ, Task: "задача", Depth: 1}) {
			t.Errorf("роль %s кешируется — её отчёт описывает изменение", typ)
		}
	}
}

func TestCacheableAcceptsReadRoles(t *testing.T) {
	for _, typ := range []Type{TypeExplorer, TypeReviewer, TypePlanner, TypeResearcher} {
		if !Cacheable(Spec{Type: typ, Task: "задача", Depth: 1}) {
			t.Errorf("роль %s не кешируется — самый частый повтор не закрыт", typ)
		}
	}
}

// Пользовательский агент не кешируется: у него свой промпт и неизвестный набор
// инструментов, доверять тут нечем.
func TestCacheableRejectsCustomAgent(t *testing.T) {
	if Cacheable(Spec{Type: TypeExplorer, Task: "задача", Prompt: "свой промпт", Depth: 1}) {
		t.Fatal("пользовательский агент кешируется")
	}
}

// Явный белый список важнее типа: он и есть фактические возможности агента.
func TestCacheableHonorsToolsAllow(t *testing.T) {
	if !Cacheable(Spec{Type: TypeExplorer, Task: "з", ToolsAllow: []string{"read_file", "web_fetch"}, Depth: 1}) {
		t.Fatal("read_file+web_fetch не считаются пишущими")
	}
	if Cacheable(Spec{Type: TypeExplorer, Task: "з", ToolsAllow: []string{"read_file", "edit_file"}, Depth: 1}) {
		t.Fatal("edit_file в белом списке, а кеш разрешён")
	}
}

func TestSecondCallHitsCache(t *testing.T) {
	r := &cacheRunner{}
	p := newCachePool(r)

	if _, err := p.Spawn(context.Background(), cacheSpec("найди кеш")); err != nil {
		t.Fatalf("первый запуск: %v", err)
	}
	out, err := p.Spawn(context.Background(), cacheSpec("найди кеш"))
	if err != nil {
		t.Fatalf("второй запуск: %v", err)
	}
	if out.Reuse != ReuseCache {
		t.Errorf("второй запуск не из кеша: %q", out.Reuse)
	}
	if out.ReusedFrom == "" {
		t.Error("не указано имя первоисточника")
	}
	if r.count() != 1 {
		t.Errorf("runner вызван %d раз, ожидался 1 — кеш не сработал", r.count())
	}
	if hits, _, _ := p.CacheStats(); hits != 1 {
		t.Errorf("попаданий %d, ожидалось 1", hits)
	}
}

// Отчёт из кеша попадает в журнал: иначе вызов исчез бы из /agents и выглядел бы
// как не отработавший.
func TestCacheHitRecordedInJournal(t *testing.T) {
	r := &cacheRunner{}
	p := newCachePool(r)
	if _, err := p.Spawn(context.Background(), cacheSpec("найди кеш")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Spawn(context.Background(), cacheSpec("найди кеш")); err != nil {
		t.Fatal(err)
	}
	runs := p.All()
	if len(runs) != 2 {
		t.Fatalf("в журнале %d запусков, ожидалось 2", len(runs))
	}
	// All() отдаёт новые первыми — наверху должен быть второй, из кеша.
	if runs[0].Reuse != ReuseCache {
		t.Errorf("последний запуск в журнале: %q, ожидался кеш", runs[0].Reuse)
	}
	if runs[0].Status != StatusDone {
		t.Errorf("статус взятого из кеша: %q, ожидался done", runs[0].Status)
	}
}

func TestDifferentTasksNotCached(t *testing.T) {
	r := &cacheRunner{}
	p := newCachePool(r)
	if _, err := p.Spawn(context.Background(), cacheSpec("первая задача")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Spawn(context.Background(), cacheSpec("вторая задача")); err != nil {
		t.Fatal(err)
	}
	if r.count() != 2 {
		t.Errorf("runner вызван %d раз — разные задачи должны выполняться", r.count())
	}
}

// Грязный отчёт в кеш не ложится: иначе модель получала бы повторно то, что
// слои прочности и заземления уже отвергли.
func TestBadReportNotCached(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"пусто", "   "},
		{"обрывок", "## Найдено\n- pool.go:39\n\n## Вывод\nТеперь ещё посмотрю"},
		{"без секций", "Просто абзац на две строки о том, что всё выглядит нормально и вопросов нет."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &cacheRunner{body: c.body}
			p := newCachePool(r)
			// Ошибка первого запуска здесь допустима (пустой отчёт), и даже
			// обязательна: проверяется только то, что грязный результат не
			// попал в кеш и работа выполнена заново.
			_, _ = p.Spawn(context.Background(), cacheSpec("задача"))
			_, _ = p.Spawn(context.Background(), cacheSpec("задача"))
			if r.count() != 2 {
				t.Errorf("runner вызван %d раз: плохой отчёт закэшировался", r.count())
			}
		})
	}
}

// Отчёт с неподтверждёнными ссылками кешировать нельзя.
func TestUnverifiedReportNotCached(t *testing.T) {
	spec := cacheSpec("задача")
	out := Outcome{
		Full:  "## Найдено\n- pool.go:39\n\n## Вывод\n- кеш живёт в пуле\n",
		Audit: &GroundingReport{Checked: 1, Unsupported: []Claim{{Where: "pool.go:39"}}},
	}
	if CacheableOutcome(spec, out, nil) {
		t.Error("отчёт с неподтверждённой ссылкой кешируется")
	}
	clean := out
	clean.Audit = &GroundingReport{Checked: 1, Supported: 1}
	if !CacheableOutcome(spec, clean, nil) {
		t.Error("чистый отчёт не кешируется")
	}
}

func TestErroredRunNotCached(t *testing.T) {
	spec := cacheSpec("задача")
	out := Outcome{Full: "## Найдено\n- x\n\n## Вывод\n- ок\n"}
	if CacheableOutcome(spec, out, errors.New("упал")) {
		t.Error("упавший запуск кешируется")
	}
}

// Две одинаковые задачи в одной пачке не должны выполняться вдвоём.
func TestParallelDuplicateJoinsFlight(t *testing.T) {
	r := &cacheRunner{delay: 120 * time.Millisecond}
	p := newCachePool(r)

	var wg sync.WaitGroup
	outs := make([]Outcome, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o, err := p.Spawn(context.Background(), cacheSpec("одинаковая задача"))
			if err != nil {
				t.Errorf("запуск %d: %v", i, err)
				return
			}
			outs[i] = o
		}(i)
	}
	wg.Wait()

	if r.count() != 1 {
		t.Errorf("runner вызван %d раз — дедупликация не сработала", r.count())
	}
	joined := 0
	for _, o := range outs {
		if o.Reuse == ReuseFlight {
			joined++
		}
	}
	if joined != 3 {
		t.Errorf("дождались чужих %d, ожидалось 3", joined)
	}
}

// Провал ведущего не должен оставлять ожидающих висеть: они получают ту же
// ошибку, а не пустой «отчёт».
func TestFlightLeaderFailureReleasesWaiters(t *testing.T) {
	boom := errors.New("сеть отвалилась")
	p := NewPool(func(context.Context, Spec) (Outcome, error) {
		time.Sleep(80 * time.Millisecond)
		return Outcome{}, boom
	}, PoolOptions{MaxParallel: 2, MaxDepth: 2, Enabled: true})

	done := make(chan error, 1)
	go func() {
		_, err := p.Spawn(context.Background(), cacheSpec("падающая задача"))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	_, err := p.Spawn(context.Background(), cacheSpec("падающая задача"))
	if !errors.Is(err, boom) {
		t.Errorf("ожидающий получил %v, ожидалась та же ошибка", err)
	}
	if e := <-done; !errors.Is(e, boom) {
		t.Errorf("ведущий вернул %v", e)
	}
}

func TestCacheDisabledNoReuse(t *testing.T) {
	r := &cacheRunner{}
	p := newCachePool(r)
	if _, err := p.Spawn(context.Background(), cacheSpec("задача")); err != nil {
		t.Fatal(err)
	}
	p.SetEnabled(false)
	if _, err := p.Spawn(context.Background(), cacheSpec("задача")); err == nil {
		t.Error("выключенный пул пропустил запуск")
	}
	if r.count() != 1 {
		t.Errorf("runner вызван %d раз при отключённых субагентах", r.count())
	}
}

func TestCacheEvictionKeepsBound(t *testing.T) {
	c := newResultCache()
	for i := 0; i < cacheMax*2; i++ {
		c.put(string(rune('a'+i%26))+string(rune('0'+i/26)), Outcome{Full: "x"}, "n", "id")
	}
	if _, _, size := c.stats(); size > cacheMax {
		t.Errorf("в кеше %d записей, максимум %d", size, cacheMax)
	}
}

func TestCacheExpiredEntryIsMiss(t *testing.T) {
	c := newResultCache()
	key := "k"
	c.put(key, Outcome{Full: "старый отчёт"}, "n", "id")
	c.mu.Lock()
	e := c.items[key]
	e.at = time.Now().Add(-cacheTTL - time.Minute)
	c.items[key] = e
	c.mu.Unlock()
	if _, ok := c.get(key); ok {
		t.Error("просроченная запись выдана как свежая")
	}
}

func TestForgetRemovesKey(t *testing.T) {
	c := newResultCache()
	c.put("k", Outcome{Full: "x"}, "n", "id")
	c.forget("k")
	if _, ok := c.get("k"); ok {
		t.Error("забытая запись всё ещё в кеше")
	}
	if _, _, size := c.stats(); size != 0 {
		t.Errorf("после forget в кеше %d записей", size)
	}
}

func TestSummaryMentionsCacheStats(t *testing.T) {
	r := &cacheRunner{}
	p := newCachePool(r)
	_, _ = p.Spawn(context.Background(), cacheSpec("задача"))
	_, _ = p.Spawn(context.Background(), cacheSpec("задача"))
	s := p.Summary()
	if s == "" {
		t.Fatal("сводка пуста")
	}
	if hits, _, _ := p.CacheStats(); hits == 1 {
		if !strings.Contains(s, "Кеш повторов") {
			t.Errorf("в сводке нет строки о кеше:\n%s", s)
		}
	}
}
