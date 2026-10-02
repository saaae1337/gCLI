package subagents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// ---------- Кеш и дедупликация субагентов ----------
//
// Модель регулярно поручает одну и ту же работу дважды: «посмотри, где реализована
// авторизация» в одном ходе и «найди, где реализована авторизация» в следующем.
// Второй субагент — это минуты, целый контекст и токены на карту кода, которую
// пять минут назад уже держали в руках. Именно этот случай и закрывает кеш.
//
// Ограничения не технические, а смысловые, и каждое проверяется на входе:
//
//   - Только роли «прочитать и описать» (explorer, reviewer, planner,
//     researcher). Роли с правкой записи не кешируются никогда: их отчёт — это
//     изменение в дереве, и выдать его повторно через минуту значит соврать о
//     состоянии репозитория, который с тех пор могли изменить.
//   - Только роли, у которых физически нет инструментов записи. Это проверяется
//     по набору инструментов, а не по флажку: у docs есть write_file и bash, и
//     формально её отчёт тоже «про код», но повторять его — значит повторно
//     утверждать, что документация уже написана.
//   - Только чистые успешные отчёты. Отчёт с замечаниями (проблемы заземления,
//     недостающие секции) не кешируется: он сформирован конкретным набором
//     доказательств, и повторная выдача скрыла бы, что модель туда полезла
//     снова и то же самое не подтвердила.
//
// Чего кеш НЕ делает: не гасит две одинаковые задачи, уже летящие параллельно.
// Для этого отдельный слой — single-flight (см. Pool.cacheBegin).

// cacheTTL — сколько живёт попадание в кеш.
//
// Двадцать минут — примерно длина одной сессионной работы. Кеш ценен внутри
// сессии, когда модель возвращается к вопросу, и бесполезен на следующей, где
// код уже мог измениться. Кеш живёт только в памяти процесса: записывать
// отчёты субагентов на диск значило бы оставлять следы работы модели на
// машине без чьего-либо согласия.
const cacheTTL = 20 * time.Minute

// cacheMax — сколько попаданий держим.
//
// Восемь с запасом покрывает типовой повтор внутри сессии, а не превращает
// пул отчётов в архив, о котором надо помнить при освобождении памяти.
const cacheMax = 8

// cacheableTypes — роли, чей отчёт имеет смысл переиспользовать.
//
// Намеренно не по флажку ReadOnly у типа, а списком: правило «роль читает —
// значит можно кешировать» перестаёт работать, как только роль получает лишний
// инструмент, и такие случаи легче запретить явно.
var cacheableTypes = map[Type]bool{
	TypeExplorer:   true,
	TypeReviewer:   true,
	TypePlanner:    true,
	TypeResearcher: true,
}

// codeWriteTools — инструменты, меняющие проект или исполняющие чужой код.
//
// Ключевое отличие от tools.isWriteTool: web_search и web_fetch сюда НЕ входят.
// Они обращаются наружу и ничего не меняют на диске, а отсекать их было бы
// глупостью — у explorer'а и planner'а есть web_fetch, и кеш для самых частых
// ролей не включился бы никогда. Цена такого решения осознанная: ответ по
// внешнему источнику может устареть, но за двадцать минут это маловероятно,
// а вот повторная оплата минутного исследования — вполне.
//
// bash и screenshot в списке, хотя формально это «чтение»: и то и другое
// исполняется и способно менять мир (сборка создаёт файлы, скриншот запускает
// браузер с расширениями).
var codeWriteTools = map[string]bool{
	"write_file": true, "edit_file": true, "multi_edit": true,
	"bash": true, "multi_bash": true, "job": true, "dry_run": true, "verify": true,
	"screenshot": true, "read_image": true,
	"mcp": true, "ext": true,
}

// Cacheable — можно ли переиспользовать отчёт этой спецификации.
//
// Проверка идёт от типа к инструментам, потому что безопасность здесь важнее
// выгоды: лишний запрет стоит одной перезапущенной работы, а лишнее разрешение —
// правды, сказанной про уже изменившийся код.
func Cacheable(spec Spec) bool {
	if !cacheableTypes[spec.Type] {
		return false
	}
	// Пользовательский агент: у него свой промпт и свой набор инструментов, и
	// никакой связи с типом — доверять тут нечем.
	if spec.Prompt != "" {
		return false
	}
	// Явный белый список важнее типа: он и есть фактические возможности.
	if len(spec.ToolsAllow) > 0 {
		for _, t := range spec.ToolsAllow {
			if codeWriteTools[t] {
				return false
			}
		}
		return true
	}
	// ToolsAllow пуст — набор берётся по типу (см. ToolsFor). Проверяем здесь,
	// а не доверяем: правило «у роли нет инструментов записи» должно быть видно
	// в коде кеша, а не в другом файле.
	//
	// Смотрим только на allow и НИКОГДА на deny: deny — это то, чего агент не
	// может. Требуя, чтобы и в deny не было пишущих инструментов, мы запретили
	// бы кеш вообще: запрет на spawn_agent и ask_user есть у всех ролей подряд,
	// и проверка не срабатывала бы ни для кого.
	allow, _ := ToolsFor(spec.Type)
	for _, t := range allow {
		if codeWriteTools[t] {
			return false
		}
	}
	return true
}

// normTask — привести текст задачи к каноническому виду для ключа.
//
// Только пробелы и регистр: «Найди, где X» и «найди где x» — это один и тот же
// вопрос от одной и той же модели, и не совпадение ключа означало бы лишний
// запуск на ровном месте. Пунктуация сохраняется: «найди баг» и «найди баги»
// различаются весом, и склеивать их — значит отвечать не на тот вопрос.
func normTask(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// CacheKey — ключ переиспользования отчёта.
//
// Summary и Notes в ключ НЕ входят намеренно: это состояние сессии, которое
// меняется каждый ход, и с ним в ключе кеш не попадал бы никогда — модель,
// вернувшаяся к вопросу через три хода, снова пошла бы работать. Задача
// формулируется заново, и если формулировка совпала — это тот же вопрос.
func CacheKey(spec Spec) string {
	h := sha256.New()
	put := func(label, v string) {
		h.Write([]byte(label))
		h.Write([]byte{0})
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	put("v", "1")
	put("type", string(spec.Type))
	put("task", normTask(spec.Task))
	put("model", strings.TrimSpace(spec.Model))
	put("tools", strings.Join(spec.ToolsAllow, ","))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// CacheableOutcome — годится ли результат, чтобы лечь в кеш.
//
// Считается на выходе, а не на входе, потому что половина условий известна
// только после работы: отчёт мог оказаться пустым, оборванным или не
// подтверждённым журналом инструментов (см. Outcome.Audit).
func CacheableOutcome(spec Spec, out Outcome, err error) bool {
	if err != nil {
		return false
	}
	if strings.TrimSpace(out.Full) == "" {
		return false
	}
	if AssessReport(out.Full) != ReportOK {
		return false
	}
	// Контракт секций: для ролей с контрактом отчёт без секций — не тот ответ,
	// который стоит переиспользовать (см. contract.go).
	if HasContract(spec.Type) && !CheckSections(out.Full, spec.Type).OK() {
		return false
	}
	// Заземление: отчёт с неподтверждёнными ссылками кешировать нельзя — при
	// повторе модель получила бы «проверенные» факты, которые проверены не были.
	if out.Audit != nil && len(out.Audit.Problems()) > 0 {
		return false
	}
	return true
}

// resultCache — отчёты, которые можно отдать повторно без запуска.
type resultCache struct {
	mu    sync.Mutex
	items map[string]cacheEntry
	order []string // ключи от старых к новым
	hits  int
	miss  int
}

type cacheEntry struct {
	out     Outcome
	at      time.Time
	srcName string // имя субагента-первоисточника
	runID   string
}

func newResultCache() *resultCache {
	return &resultCache{items: map[string]cacheEntry{}}
}

// get — попадание или промах.
//
// Промах считается и для просроченной записи: иначе в статистике «кеш работает
// молча», и непонятно, почему после двадцати минут он перестал помогать.
func (c *resultCache) get(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		c.miss++
		return cacheEntry{}, false
	}
	if time.Since(e.at) > cacheTTL {
		c.miss++
		return cacheEntry{}, false
	}
	c.hits++
	return e, true
}

// put — положить отчёт в кеш, вытеснив самый старый при нехватке места.
func (c *resultCache) put(key string, out Outcome, srcName, runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; !exists {
		c.order = append(c.order, key)
	}
	c.items[key] = cacheEntry{out: out, at: time.Now(), srcName: srcName, runID: runID}
	for len(c.order) > cacheMax {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}
}

// forget — убрать конкретный ключ (используется, если лидер упал до записи).
func (c *resultCache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok {
		return
	}
	delete(c.items, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// stats — попадания, промахи и размер (для тестов и сводки).
func (c *resultCache) stats() (hits, miss, size int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.miss, len(c.items)
}

// Reuse — откуда взят отчёт, если работа не выполнялась.
type Reuse string

const (
	// ReuseNone — обычный запуск, отчёт получен свежим. Значение по умолчанию,
	// чтобы «переиспользовано» никогда нельзя было спутать с обычным путём.
	ReuseNone Reuse = ""
	// ReuseCache — отчёт взят из кеша повторов (см. resultCache).
	ReuseCache Reuse = "кеш"
	// ReuseFlight — дождались уже идущей точно такой же работы.
	ReuseFlight Reuse = "дождался"
)

// poolCache — состояние кеша пула: готовые отчёты и идущие работы.
//
// Два хранилища разведены намеренно. Готовые отчёты живут до истечения TTL, а
// flights — только пока идёт работа; держать их в одной таблице значило бы либо
// вытеснять идущую работу как устаревшую, либо вечно держать в памяти то, что
// завершилось минуту назад.
type poolCache struct {
	mu      sync.Mutex
	flights map[string]*flight
	cache   *resultCache
}

func newPoolCache() *poolCache {
	return &poolCache{
		flights: map[string]*flight{},
		cache:   newResultCache(),
	}
}

// flight — один идущий запуск, к которому могут присоединиться ожидающие.
//
// Пока работа не закончилась, её результат ещё не в кеше, но повторять её нельзя:
// две одинаковые задачи в одной пачке (spawn_agents с близкими формулировками)
// — это ровно тот случай, ради которого дедупликация и делается.
type flight struct {
	done    chan struct{}
	out     Outcome
	err     error
	srcName string // имя субагента, выполнившего работу
}

// begin — встать ведущим или присоединиться к идущей работе.
//
// Возвращает (flight, true) для ожидающего и (nil, false) для ведущего, который
// обязан выполнить работу и вызвать finish — даже если она провалилась. Иначе
// ожидающие висели бы до конца сессии, не получив ни ответа, ни причины.
func (p *poolCache) begin(key string) (*flight, bool) {
	if key == "" {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if f, ok := p.flights[key]; ok {
		return f, true
	}
	f := &flight{done: make(chan struct{})}
	p.flights[key] = f
	return nil, false
}

// finish — опубликовать результат ведущего и разбудить ожидающих.
//
// Вызывается ровно один раз на каждый успешный begin. Ключ может отсутствовать
// в flights (если вытеснили, чего быть не должно) — тогда результат всё равно
// уходит в кеш, но будить некого.
func (p *poolCache) finish(key string, out Outcome, err error, cacheable bool, srcName, runID string) {
	if key == "" {
		return
	}
	p.mu.Lock()
	f, ok := p.flights[key]
	if ok {
		f.out, f.err = out, err
		f.srcName = srcName
		delete(p.flights, key)
		p.mu.Unlock()
		close(f.done)
	} else {
		p.mu.Unlock()
	}
	if cacheable {
		p.cache.put(key, out, srcName, runID)
	}
}

// wait — дождаться чужой работы.
//
// Отмена ожидающего ничего не говорит лидеру: его работа всё равно нужна кому-то
// ещё, и прерывать её из-за ухода одного слушателя неправильно.
func waitFlight(ctx context.Context, f *flight) (Outcome, error) {
	select {
	case <-f.done:
		return f.out, f.err
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
}
