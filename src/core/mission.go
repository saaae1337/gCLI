package core

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- Миссия: автономный прогон ----------
//
// Миссия отвечает на вопрос, на который обычный ход отвечать не умеет:
// «сколько ещё можно работать». У агентного цикла есть счётчик итераций, но
// он ничего не говорит о времени: двадцать итераций по секунде и двадцать
// итераций по пятнадцати минут выглядят в журнале одинаково, а стоят
// разного. Для многочасовой работы нужен бюджет, который можно назвать
// вслух: «до 4 часов», «не дороже 25 долларов», «не больше 200 тысяч
// токенов».
//
// Миссия — это объект, а не пресет промпта: её можно сохранить в проекте,
// показать человеку и положить в снимок состояния. Именно поэтому она
// живёт в core рядом с Config и Session, а не в agent: агент её читает,
// но не владеет ею.
//
// Режимы отличаются только пресетами. Никакой отдельной логики у
// long-time нет и быть не должно: режим — это пара чисел, а всё
// остальное (дедлайн, потолок итераций, цена) человек задаёт сам.

// MissionMode — режим автономной работы.
type MissionMode string

const (
	// MissionNormal — обычный ход: столько, сколько нужно модели.
	MissionNormal MissionMode = "normal"
	// MissionLongTime — несколько часов работы без участия человека.
	MissionLongTime MissionMode = "long-time"
	// MissionExtraLong — многочасовая работа с большим запасом.
	MissionExtraLong MissionMode = "extra-long-time"
	// MissionOvernight — прогон, рассчитанный на оставление на ночь:
	// короткие интервалы сохранения состояния, чтобы пережить перезапуск.
	MissionOvernight MissionMode = "overnight"
)

// Режимы в порядке возрастания бюджета — так их и показывают в UI.
var missionModes = []MissionMode{
	MissionNormal, MissionLongTime, MissionExtraLong, MissionOvernight,
}

// MissionModes — список режимов для подсказок и проверки ввода.
func MissionModes() []MissionMode { return append([]MissionMode(nil), missionModes...) }

// ValidMissionMode — разобрать имя режима. Пустая строка — normal:
// отсутствие настройки не ошибка, а обычный ход.
func ValidMissionMode(s string) (MissionMode, bool) {
	switch MissionMode(strings.ToLower(strings.TrimSpace(s))) {
	case "", MissionNormal:
		return MissionNormal, true
	case MissionLongTime:
		return MissionLongTime, true
	case MissionExtraLong:
		return MissionExtraLong, true
	case MissionOvernight:
		return MissionOvernight, true
	}
	return MissionNormal, false
}

// ---------- Причины остановки ----------

// Причины, по которым автономный прогон останавливается. Строки, а не
// константы сравнения: они попадают в сообщение модели и в строку
// состояния, и человек должен их прочитать без словаря.
const (
	StopDone      = "criteria_met" // модель объявила цель выполненной
	StopDeadline  = "deadline"     // вышел отведённый срок
	StopTokens    = "tokens"       // исчерпан бюджет токенов
	StopCost      = "cost"         // исчерпан бюджет денег
	StopIters     = "iterations"   // исчерпан потолок итераций
	StopTools     = "tool_calls"   // исчернан потолок вызовов инструментов
	StopStalled   = "stalled"      // нет прогресса
	StopCancelled = "cancelled"    // человек прервал
)

// StopReasonLabel — что сказать человеку про причину остановки.
func StopReasonLabel(reason string) string {
	switch reason {
	case StopDone:
		return "цель выполнена"
	case StopDeadline:
		return "вышел срок"
	case StopTokens:
		return "исчерпан бюджет токенов"
	case StopCost:
		return "исчерпан бюджет денег"
	case StopIters:
		return "исчерпан потолок итераций"
	case StopTools:
		return "исчерпан потолок вызовов инструментов"
	case StopStalled:
		return "нет прогресса"
	case StopCancelled:
		return "прервано человеком"
	}
	return reason
}

// ---------- Dur: длительность в виде "4h" ----------

// Dur — длительность, которая в конфиге записывается по-человечески.
//
// В JSON хранится строкой («4h», «90m», «1h30m»), потому что «14400» или
// «14400000000000» читаются хуже, а машинный формат нужен ровно в одном
// месте — внутри программы.
type Dur time.Duration

// ParseDur — разобрать длительность. Голое число трактуется как минуты:
// «30» в разговоре про работу агента — это полчаса, а не тридцать
// наносекунд, и молча разбирать его как секунды опаснее, чем угадать.
func ParseDur(s string) (Dur, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		// Минус проверяем сами: time.ParseDuration охотно берёт «-5h»,
		// а отрицательный срок в задании — это не «работать в обратную
		// сторону», а опечатка, которая обрывает прогон мгновенно.
		if d < 0 {
			return 0, fmt.Errorf("длительность не может быть отрицательной: %s", s)
		}
		return Dur(d), nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("длительность не может быть отрицательной: %s", s)
		}
		// Умножение на минуту переполняется на больших числах: «1077000000»
		// минут — это больше, чем помещается в Duration, и результат
		// молча становился отрицательным. Прогон с таким сроком стартовал
		// «просроченным» и обрывался сразу. Поэтому переполнение — ошибка,
		// а не повод обрезать срок по кругу.
		if int64(n) > int64(math.MaxInt64)/int64(time.Minute) {
			return 0, fmt.Errorf("срок слишком велик: %s (максимум около %d минут)", s, int64(math.MaxInt64)/int64(time.Minute))
		}
		return Dur(time.Duration(n) * time.Minute), nil
	}
	return 0, fmt.Errorf("не понял длительность %q (нужно 4h, 90m или число минут)", s)
}

// D — значение для арифметики.
func (d Dur) D() time.Duration { return time.Duration(d) }

// Or — взять длительность, если она задана, иначе запасную.
func (d Dur) Or(fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return time.Duration(d)
}

func (d Dur) String() string { return time.Duration(d).String() }

// MarshalJSON — в конфиг строкой.
func (d Dur) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON — из строки, из числа-минут и из null.
func (d *Dur) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*d = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// Число в JSON — это минуты, как и в ParseDur.
		var n int
		if err2 := json.Unmarshal(b, &n); err2 != nil {
			return fmt.Errorf("длительность должна быть строкой вида \"4h\": %w", err)
		}
		s = strconv.Itoa(n)
	}
	v, err := ParseDur(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// ---------- Mission: описание прогона ----------

// Mission — задание на автономный прогон.
type Mission struct {
	// Objective — цель одной строкой. Показывается в строке состояния и
	// идёт в системный промпт: без неё агент в многочасовом прогоне
	// потеряет, ради чего он вообще продолжает.
	Objective string `json:"objective,omitempty"`
	// Mode — режим (пресет). Поля ниже переопределяют пресет.
	Mode MissionMode `json:"mode,omitempty"`
	// Deadline — сколько времени отведено на прогон.
	Deadline Dur `json:"deadline,omitempty"`
	// MaxIters — потолок итераций агентного цикла за прогон.
	MaxIters int `json:"max_iters,omitempty"`
	// TokenBudget — потолок токенов за прогон (0 = без потолка).
	TokenBudget int `json:"token_budget,omitempty"`
	// CostBudget — потолок в долларах (0 = без потолка).
	CostBudget float64 `json:"cost_budget,omitempty"`
	// MaxToolCalls — потолок вызовов инструментов (0 = без потолка).
	MaxToolCalls int `json:"max_tool_calls,omitempty"`
	// CheckpointEvery — как часто сохранять состояние на диск, в
	// итерациях. 0 = дефолт режима.
	CheckpointEvery int `json:"checkpoint_every,omitempty"`
	// Acceptance — критерии приёмки. Агент обязан проверить каждый и
	// сказать, выполнен он или нет. Это единственное, что заменяет
	// «модель сказала готово» на проверяемый факт.
	Acceptance []string `json:"acceptance,omitempty"`
	// VerifyDone — требовать проверку критериев перед остановкой.
	// По умолчанию включено, когда критерии заданы: иначе список
	// критериев — просто текст для чтения, а не условие работы.
	VerifyDone *bool `json:"verify_done,omitempty"`
	// StallLimit — сколько итераций без прогресса считать застой.
	// 0 = по умолчанию; отрицательное значение отключает проверку.
	StallLimit int `json:"stall_limit,omitempty"`
}

// verifyFlag — вкл ли режим проверки критериев.
func (m Mission) verifyFlag() bool {
	if m.VerifyDone != nil {
		return *m.VerifyDone
	}
	return len(m.Acceptance) > 0
}

// Verify — надо ли требовать проверку критериев при попытке остановиться.
func (m Mission) Verify() bool { return m.verifyFlag() }

// ---------- Пресеты режимов ----------

// missionPresets — что задаёт режим. Значения подобраны так, чтобы
// обычный агентный ход (40 итераций) укладывался в long-time с запасом,
// а extra-long-time и overnight давали работу на смену.
var missionPresets = map[MissionMode]Mission{
	MissionNormal: {
		// Потолка итераций нет намеренно: в обычном ходе работают лимиты
		// самого агента (agent.DefaultMaxIters и продления), и второй
		// потолок поверх них только мешал бы объяснять обрыв. Срок тоже
		// не задан: обычный ход не обрывается по времени.
		Deadline:        Dur(0),
		MaxIters:        0,
		CheckpointEvery: 0,
	},
	MissionLongTime: {
		Deadline:        Dur(4 * time.Hour),
		MaxIters:        200,
		CheckpointEvery: 10,
	},
	MissionExtraLong: {
		Deadline:        Dur(12 * time.Hour),
		MaxIters:        1500,
		CheckpointEvery: 15,
	},
	MissionOvernight: {
		Deadline:        Dur(8 * time.Hour),
		MaxIters:        1000,
		CheckpointEvery: 5,
	},
}

// MissionForMode — пресет режима без учёта пользовательских полей.
func MissionForMode(mode MissionMode) Mission {
	if m, ok := missionPresets[mode]; ok {
		return m
	}
	return missionPresets[MissionNormal]
}

// Apply — наложить пресет режима, но не затереть явно заданные поля.
//
// Ключевое правило: режим задаёт умолчания, а человек — исключения.
// Если у прогона стоит «4h», смена режима на extra-long-time не должна
// молча увеличить срок до 12 часов: человек задал срок сам, и агент не
// вправе его менять.
func (m Mission) Apply() Mission {
	mode := m.Mode
	if mode == "" {
		mode = MissionNormal
	}
	p, ok := missionPresets[mode]
	if !ok {
		p = missionPresets[MissionNormal]
	}
	out := m
	out.Mode = mode
	if out.Deadline <= 0 {
		out.Deadline = p.Deadline
	}
	if out.MaxIters <= 0 {
		out.MaxIters = p.MaxIters
	}
	if out.CheckpointEvery <= 0 {
		out.CheckpointEvery = p.CheckpointEvery
	}
	if out.StallLimit == 0 {
		out.StallLimit = 12
	}
	return out
}

// Long — отведено ли на прогон заметное время. Обычный ход миссией не
// считается: в режиме normal дедлайн не должен обрывать работу.
func (m Mission) Long() bool {
	return m.Mode != MissionNormal && m.Mode != "" || m.Deadline > 0
}

// Budgeted — есть ли хоть один потолок помимо времени.
func (m Mission) Budgeted() bool {
	return m.TokenBudget > 0 || m.CostBudget > 0 || m.MaxToolCalls > 0
}

// HasAcceptance — заданы ли критерии приёмки.
func (m Mission) HasAcceptance() bool { return len(m.Acceptance) > 0 }

// Normalize — привести поле Mode к валидному, разобрав строку. Ошибка
// возвращается отдельно, чтобы вызывающий мог сказать человеку, какие
// режимы бывают, а не молча заменить опечатку на normal.
func (m *Mission) Normalize() error {
	mode, ok := ValidMissionMode(string(m.Mode))
	if !ok {
		return fmt.Errorf("неизвестный режим %q (бывает: normal, long-time, extra-long-time, overnight)", string(m.Mode))
	}
	m.Mode = mode
	return nil
}

// Summary — одна строка о прогоне для строки состояния.
func (m Mission) Summary() string {
	var parts []string
	if m.Objective != "" {
		parts = append(parts, Truncate(OneLine(m.Objective), 60))
	}
	if m.Mode != "" && m.Mode != MissionNormal {
		parts = append(parts, string(m.Mode))
	}
	if m.Deadline > 0 {
		parts = append(parts, "до "+m.Deadline.String())
	}
	if m.TokenBudget > 0 {
		parts = append(parts, CompactNum(m.TokenBudget)+" токенов")
	}
	if m.CostBudget > 0 {
		parts = append(parts, FormatUSD(m.CostBudget))
	}
	if len(parts) == 0 {
		return "миссия"
	}
	return strings.Join(parts, " · ")
}

// ---------- Файл миссии ----------

// MissionPath — файл задания в каталоге проекта.
func MissionPath(workDir string) string {
	return filepath.Join(workDir, ".gcli", "mission.json")
}

// MissionTemplate — заготовка mission.json с комментариями.
//
// Пишется теми же JSONC-правилами, что и gcli.json: человек открывает
// файл в первый раз и должен понять его без документации.
const MissionTemplate = `{
  // Задание на автономную работу gcli.
  //
  // Одно поле — mission.json — описывает, сколько агент может работать
  // без новых промптов: по времени, по итерациям, по токенам, по деньгам.
  // Пока файла нет, действует обычный ход: столько, сколько нужно модели.
  //
  // Поле "mode" задаёт пресет, остальные поля переопределяют его:
  //   normal          — обычный ход, без потолка времени
  //   long-time       — 4 часа, 200 итераций
  //   extra-long-time — 12 часов, 1500 итераций
  //   overnight       — 8 часов, состояние сохраняется часто

  // Цель одной строкой: что именно считается выполненным.
  // "objective": "починить падающие тесты в пакете core",

  // Критерии приёмки. Агент не вправе объявить работу законченной, пока
  // не проверит каждый пункт и не перечислит, что именно он проверил.
  "acceptance": [
    "go test ./... проходит без падений",
    "vet не выдаёт новых предупреждений"
  ],

  // Бюджеты. Любой из них можно убрать — тогда ограничения не будет.
  // "deadline": "4h",
  // "token_budget": 500000,
  // "cost_budget": 25,
  // "max_iters": 200,
  // "max_tool_calls": 5000,

  // Как часто сохранять состояние на диск, в итерациях. Меньше — надёжнее
  // переживает перезапуск, но чаще пишет на диск.
  // "checkpoint_every": 10
}
`

// LoadMission — прочитать mission.json из проекта.
//
// Отсутствие файла — не ошибка, а обычный ход: возвращается Mission с
// режимом normal. Испорченный файл — ошибка с указанием строки, потому
// что молчаливый откат к обычному режиму означал бы, что человек
// собрал четырёхчасовой прогон, а получил двадцать минут и не узнал бы.
func LoadMission(workDir string) (Mission, string, error) {
	name := MissionPath(workDir)
	data, err := os.ReadFile(name)
	if err != nil {
		if os.IsNotExist(err) {
			return Mission{Mode: MissionNormal}, "", nil
		}
		return Mission{}, name, err
	}
	if HasJSONC(data) {
		data = StripJSONC(data)
	}
	var m Mission
	if err := json.Unmarshal(data, &m); err != nil {
		return Mission{}, name, fmt.Errorf("mission.json не разбирается: %w", err)
	}
	if err := m.Normalize(); err != nil {
		return Mission{}, name, fmt.Errorf("mission.json: %w", err)
	}
	return m.Apply(), name, nil
}

// SaveMission — записать mission.json с комментариями-шаблоном.
func SaveMission(workDir string, m Mission) (string, error) {
	name := MissionPath(workDir)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return "", err
	}
	if err := m.Normalize(); err != nil {
		return "", err
	}
	if err := WriteAtomic(name, []byte(MissionDoc(m.Apply())), 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// MissionDoc — mission.json с комментариями и заданными значениями.
//
// Собирается целиком, а не склейкой «шаблон + тело». Склейка давала два
// объекта подряд — такой файл gcli читал до конца жизни (комментарии
// снимались, первый объект закрывался), а вот первая же правка человеком
// ломалась на первой строке. Один файл — один объект: иначе mission.json
// остаётся миной для того, кто будет его редактировать.
func MissionDoc(m Mission) string {
	var b strings.Builder
	b.WriteString(`{
  // Задание на автономную работу gcli.
  //
  // Пока файла нет, действует обычный ход: столько, сколько нужно модели.
  // Этот файл отвечает на вопрос, на который обычный ход не умеет:
  // «сколько агент может работать без новых промптов».
  //
  // Поле "mode" задаёт пресет, остальные поля его переопределяют:
  //   normal          — обычный ход, без потолка времени
  //   long-time       — 4 часа, 200 итераций
  //   extra-long-time — 12 часов, 1500 итераций
  //   overnight       — 8 часов, состояние сохраняется часто
  //
  // Критерии приёмки — единственное, что заменяет «модель сказала готово»
  // на проверяемый факт. Пока список не пуст, агент не вправе объявить
  // работу законченной, не проверив каждый пункт инструментами.
  //
  // Бюджеты жёсткие: на исходе работа оборвётся сама. Убирайте лишние поля,
  // а не ставьте огромные числа: точный смысл имеет deadline.
`)
	// Поля собираются списком и склеиваются запятыми в конце: дописывать
	// запятую по ходу означает забыть про запятую в последнем поле, и файл
	// перестаёт быть JSON ровно в том случае, когда в нём больше одного
	// необязательного поля.
	var fields []string
	if m.Objective != "" {
		fields = append(fields, fmt.Sprintf("  // Цель одной строкой: что именно считается выполненным.\n"+
			"  \"objective\": %q", OneLine(m.Objective)))
	}
	fields = append(fields, fmt.Sprintf("  \"mode\": %q", string(m.Mode)))
	if len(m.Acceptance) > 0 {
		var ab strings.Builder
		ab.WriteString("  // Критерии приёмки: каждый проверяется инструментом.\n  \"acceptance\": [\n")
		for i, c := range m.Acceptance {
			comma := ","
			if i == len(m.Acceptance)-1 {
				comma = ""
			}
			fmt.Fprintf(&ab, "    %q%s\n", OneLine(c), comma)
		}
		ab.WriteString("  ]")
		fields = append(fields, ab.String())
	}
	if m.Deadline > 0 {
		fields = append(fields, fmt.Sprintf("  // Бюджеты. Любой можно убрать — тогда ограничения не будет.\n"+
			"  // Срок отсчитывается от старта прогона, а не от запуска gcli.\n"+
			"  \"deadline\": %q", m.Deadline.String()))
	}
	// Остальные бюджеты дописываются только когда заданы: нулевой потолок
	// в файле читается как «задан ноль», а человек хотел «не ограничено».
	if m.MaxIters > 0 {
		fields = append(fields, fmt.Sprintf("  \"max_iters\": %d", m.MaxIters))
	}
	if m.TokenBudget > 0 {
		fields = append(fields, fmt.Sprintf("  \"token_budget\": %d", m.TokenBudget))
	}
	if m.CostBudget > 0 {
		fields = append(fields, fmt.Sprintf("  \"cost_budget\": %s",
			strconv.FormatFloat(m.CostBudget, 'f', -1, 64)))
	}
	if m.MaxToolCalls > 0 {
		fields = append(fields, fmt.Sprintf("  \"max_tool_calls\": %d", m.MaxToolCalls))
	}
	if m.CheckpointEvery > 0 {
		fields = append(fields, fmt.Sprintf("  // Как часто сохранять состояние, в итерациях.\n"+
			"  \"checkpoint_every\": %d", m.CheckpointEvery))
	}
	if m.StallLimit != 0 {
		fields = append(fields, fmt.Sprintf("  // Сколько итераций без продвижения считать застоем.\n"+
			"  \"stall_limit\": %d", m.StallLimit))
	}
	if m.VerifyDone != nil {
		fields = append(fields, fmt.Sprintf("  // Требовать проверки критериев перед остановкой.\n"+
			"  \"verify_done\": %t", *m.VerifyDone))
	}
	b.WriteString(strings.Join(fields, ",\n"))
	b.WriteString("\n}\n")
	return b.String()
}

// ---------- Tracker: живое состояние прогона ----------

// Tracker — состояние автономного прогона во времени.
//
// Отдельный тип от Mission по той же причине, что журнал ходов отдельно от
// промпта: Mission — это то, что человек написал (и что переживает
// перезапуск), Tracker — то, что наросло за этот прогон (и что переживать
// незачем). Держать одно в другом значило бы писать на диск изменчивые
// счётчики.
//
// now подменяется в тестах: проверять дедлайн через настоящие часы
// значит ждать четыре часа.
type Tracker struct {
	// mu — сериализует доступ к счётчикам. Записывают их две стороны:
	// агентный цикл (Tick/ChargeTokens/Resume) и человек через HTTP
	// (Stop из /v1/mission/stop, чтение Iters/Spent в /v1/status и при
	// сохранении состояния). Без мьютекса это гонка на остановке: строка
	// stopped — два машинных слова, порванная запись читается мусором.
	mu sync.Mutex

	m     Mission
	now   func() time.Time
	start time.Time

	iters     int
	toolCalls int
	tokens    int
	// spentBefore — расход сессии до старта миссии. Миссия считает только
	// свой расход: иначе прогон на 500k токенов не запустится в сессии,
	// где до него уже набежало 300k.
	spentBefore int
	// costBefore — та же поправка для денег.
	costBefore float64
	// providerID и model — чтобы стоимость считалась по-настоящему.
	// Без них checkBudgets звал Cost с пустыми строками, цена была
	// неизвестна, и бюджет денег не срабатывал никогда: молчаливое
	// обещание «встанет на $25», которое не срабатывает.
	providerID string
	model      string

	// lastProgress — итерация последнего реального продвижения.
	lastProgress int
	// verifyAsked — сколько раз уже требовали проверку критериев.
	verifyAsked int
	// stallWarned — сколько раз предупреждали о застое (чтобы не
	// повторять одно и то же сообщение каждую итерацию).
	stallWarned int
	// continued — сколько раз прогон просил модель продолжить.
	continued int
	// stopped — причина остановки, если она случилась.
	stopped string
	// checkpoints — сколько раз сохраняли состояние.
	checkpoints int
	// resumeShown — приглашение к продолжению уже вставлено в историю.
	//
	// Именно в трекере, а не в движке: хост создаёт агента (и движок) заново
	// на каждый ход, а история сеанса живёт одна. Флаг в движке обнулялся бы
	// вместе с ним, и десять одинаковых «продолжаем с прошлого места»
	// сыпались бы в историю по разу на ход.
	resumeShown bool
}

// NewTracker — начать прогон по заданию. spentBefore и costBefore —
// накопленное до старта, чтобы бюджет миссии считался от её старта.
func NewTracker(m Mission, spentBefore int, costBefore float64) *Tracker {
	return NewTrackerAt(m, spentBefore, costBefore, time.Now)
}

// NewTrackerAt — то же, но с явными часами (для тестов).
func NewTrackerAt(m Mission, spentBefore int, costBefore float64, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	m = m.Apply()
	return &Tracker{
		m: m, now: now, start: now(),
		spentBefore: spentBefore, costBefore: costBefore,
		lastProgress: 0,
	}
}

// SetClock — заменить источник времени.
//
// Нужен тестам, где время идёт сама (часы прогона тикают, а четыре часа
// ждать нельзя), и хостам, которые хотят мерить прогон по своим часам
// с монотонным счётом. Счётчик обнуляется не здесь: смена часов не должна
// задним числом обнулять уже набранные итерации.
func (t *Tracker) SetClock(now func() time.Time) {
	if t == nil || now == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.now = now
}

// WithModel — задать провайдера и модель для расчёта денег.
//
// Без этого бюджет в долларах не может сработать: цена ищется по паре
// «вендор + модель», а пустые строки не находят ничего. Вызывается
// сразу при старте прогона, а не внутри проверки бюджета.
func (t *Tracker) WithModel(providerID, model string) *Tracker {
	if t == nil {
		return t
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.providerID = providerID
	t.model = model
	return t
}

// Mission — задание, по которому идёт прогон.
func (t *Tracker) Mission() Mission { return t.m }

// Started — когда начался прогон.
func (t *Tracker) Started() time.Time { return t.start }

// Elapsed — сколько времени прошло.
func (t *Tracker) Elapsed() time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.elapsed()
}

func (t *Tracker) elapsed() time.Duration { return t.now().Sub(t.start) }

// Left — сколько осталось до дедлайна. Отрицательное значение означает,
// что срок вышел: показывать его надо честно, вместе с «просрочено».
func (t *Tracker) Left() time.Duration {
	if t == nil || t.m.Deadline <= 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.left()
}

func (t *Tracker) left() time.Duration {
	if t.m.Deadline <= 0 {
		return 0
	}
	return t.m.Deadline.D() - t.elapsed()
}

// Overdue — на сколько просрочен прогон.
func (t *Tracker) Overdue() time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Строгое «< 0»: на самой границе срока просрочки ещё нет, есть
	// факт достижения дедлайна, который показывает Status.
	if l := t.left(); l < 0 {
		return -l
	}
	return 0
}

// Iters — выполнено итераций за прогон.
func (t *Tracker) Iters() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.iters
}

// ToolCalls — выполнено вызовов инструментов за прогон.
func (t *Tracker) ToolCalls() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.toolCalls
}

// Spent — расход токенов за прогон (без того, что было до старта).
func (t *Tracker) Spent() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tokens
}

// Checkpoints — сколько раз сохраняли состояние.
func (t *Tracker) Checkpoints() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.checkpoints
}

// Stopped — причина остановки (пусто, если ещё идёт).
func (t *Tracker) Stopped() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

// Stop — записать причину остановки.
func (t *Tracker) Stop(reason string) string {
	if t == nil {
		return reason
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stop(reason)
}

func (t *Tracker) stop(reason string) string {
	if t.stopped == "" {
		t.stopped = reason
	}
	return t.stopped
}

// Done — остановлен ли прогон.
func (t *Tracker) Done() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped != ""
}

func (t *Tracker) done() bool { return t.stopped != "" }

// VerifyAsked — сколько раз требовали проверку критериев.
func (t *Tracker) VerifyAsked() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.verifyAsked
}

// NeedVerify — пора ли требовать проверку критериев перед остановкой.
//
// Задача с критериями, которая «готова» без единой проверки, — это обычно
// не готовая задача, а задача, которую модель посчитала выполненной по
// памяти. Требование проверки ограничено несколькими попытками: иначе
// агент, который честно не может выполнить критерий, будет требовать
// проверки до бесконечности и сожжёт весь бюджет.
func (t *Tracker) NeedVerify() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped != "" {
		return false
	}
	// Поле напрямую, а не через t.m.Verify(): при nil-трекере вызов
	// метода на t.m упал бы с разыменованием nil.
	if !t.m.Verify() {
		return false
	}
	return t.verifyAsked < maxVerifyAsks
}

// AskVerify — зафиксировать, что проверка потребована.
func (t *Tracker) AskVerify() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.verifyAsked++
	return t.verifyAsked
}

// maxVerifyAsks — сколько раз можно требовать проверку критериев.
const maxVerifyAsks = 3

// Tick — отметить выполненную итерацию и проверить потолки.
//
// Вызывается из агентного цикла после каждой итерации. Возвращает
// непустую причину, когда прогон пора останавливать.
//
// Порядок проверок не случаен: сперва то, что человек оплачивает
// деньгами (время и токены), потом количество работы. Застой
// проверяется последним и только при явно заданном StallLimit: иначе
// «нет прогресса» обрывает честную долгую задачу, где две недели идёт
// один и тот же сложный кусок.
func (t *Tracker) Tick(iters, toolCalls, sessionTokens int) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped != "" {
		return t.stopped
	}
	t.iters = iters
	if toolCalls > 0 {
		t.toolCalls += toolCalls
	}
	if sessionTokens > t.spentBefore {
		t.tokens = sessionTokens - t.spentBefore
	}
	if reason := t.checkBudgets(); reason != "" {
		return t.stop(reason)
	}
	return ""
}

// ChargeTokens — обновить расход токенов без сдвига итерации.
func (t *Tracker) ChargeTokens(sessionTokens int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if sessionTokens <= t.spentBefore {
		return
	}
	t.tokens = sessionTokens - t.spentBefore
}

// Resume — принять счётчики прошлого прогона при продолжении.
//
// Без этого продолжение начиналось бы с полного бюджета: человек
// собрал прогон на 500k токенов, прогонал половину, упал, а после
// перезапуска получил ещё 500k сверху — вдвое дороже, чем собирался.
// Поэтому вызвавший обязан передать счётчики ДО первого Tick: иначе
// первый же тик прибавит к ним расход нового запуска и счётчик
// завысится.
//
// Время намеренно НЕ переносится: срок «4 часа» считается от старта
// процесса, а не от начала работы. Иначе после обрыва прогон получил бы
// полный срок сверху и мог бы работать бесконечно, а человек так и не
// узнал бы, что восемь из двенадцати часов уже потрачены.
func (t *Tracker) Resume(iters, toolCalls, tokens int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if iters > t.iters {
		t.iters = iters
	}
	if toolCalls > t.toolCalls {
		t.toolCalls = toolCalls
	}
	if tokens > t.tokens {
		t.tokens = tokens
	}
}

// checkBudgets — потолки, каждый на своём месте. Вызывается только из
// Tick под mu.
func (t *Tracker) checkBudgets() string {
	if t.m.Deadline > 0 && t.now().Sub(t.start) >= t.m.Deadline.D() {
		return StopDeadline
	}
	if t.m.MaxIters > 0 && t.iters >= t.m.MaxIters {
		return StopIters
	}
	if t.m.MaxToolCalls > 0 && t.toolCalls >= t.m.MaxToolCalls {
		return StopTools
	}
	if t.m.TokenBudget > 0 && t.tokens >= t.m.TokenBudget {
		return StopTokens
	}
	if t.m.CostBudget > 0 {
		if c := t.cost(); c > 0 && c >= t.m.CostBudget {
			return StopCost
		}
	}
	return ""
}

// Cost — стоимость прогона в долларах.
//
// Цена берётся у провайдера и модели, заданных при старте (WithModel):
// ввод их аргументами каждый раз означал бы, что цена забывается и
// бюджет денег перестаёт работать. Пустые строки означают «цену не
// знаем», и тогда стоимость ноль — показывать выдуманную сумму хуже,
// чем не показывать никакой.
func (t *Tracker) Cost(providerID, model string) float64 {
	if t == nil || t.m.CostBudget <= 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if providerID == "" && model == "" {
		providerID, model = t.providerID, t.model
	}
	price, ok := ModelPrice(providerID, model)
	if !ok {
		return 0
	}
	return price.Cost(Usage{
		PromptTokens:     t.tokens / 2,
		CompletionTokens: t.tokens - t.tokens/2,
	})
}

// cost — стоимость по вендору и модели, заданным при старте (внутренняя:
// вызывается под mu, например из checkBudgets).
func (t *Tracker) cost() float64 {
	if t.m.CostBudget <= 0 {
		return 0
	}
	price, ok := ModelPrice(t.providerID, t.model)
	if !ok {
		return 0
	}
	return price.Cost(Usage{
		PromptTokens:     t.tokens / 2,
		CompletionTokens: t.tokens - t.tokens/2,
	})
}

// Progress — отметить продвижение: работа продолжается, застоя нет.
func (t *Tracker) Progress() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastProgress = t.iters
}

// Stalled — стоит ли работа в застое.
func (t *Tracker) Stalled() bool {
	if t == nil || t.m.StallLimit <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stalled()
}

func (t *Tracker) stalled() bool {
	if t.m.StallLimit <= 0 || t.stopped != "" {
		return false
	}
	return t.iters-t.lastProgress >= t.m.StallLimit
}

// stallHardFactor — во сколько раз застой должен перерасти лимит, чтобы
// прогон остановился.
//
// Предупреждение модели даётся сразу, а остановка — только втрое позже.
// Иначе одна честная тяжёлая задача (часы работы над одним куском кода,
// где каждый инструмент повторяет предыдущий) убивала бы прогон на
// середине — и вместо пользы давала человеку обрыв в худший момент.
const stallHardFactor = 3

// StallHard — застой перерос в повод остановить прогон.
func (t *Tracker) StallHard() bool {
	if t == nil || t.m.StallLimit <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stallHard()
}

func (t *Tracker) stallHard() bool {
	if t.m.StallLimit <= 0 || t.stopped != "" {
		return false
	}
	return t.iters-t.lastProgress >= t.m.StallLimit*stallHardFactor
}

// StallWarned — сколько раз предупреждали о застое (нужно, чтобы не
// повторять одно и то же сообщение каждую итерацию).
func (t *Tracker) StallWarned() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stallWarned
}

// WarnStall — зафиксировать предупреждение о застое.
func (t *Tracker) WarnStall() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stallWarned++
	return t.stallWarned
}

// ---------- Продолжение работы ----------

// maxContinues — сколько раз прогон может сам сказать модели «продолжай».
//
// Не бесконечность намеренно: модель, которая без единого вызова
// инструмента рапорт��ет о готовности, не станет готова и от третьего
// «продолжай». Потолок превращает трату бюджета в предсказуемую сумму.
const maxContinues = 5

// CanContinue — стоит ли попросить модель продолжить вместо остановки.
//
// Условия намеренно строгие: работа идёт, застоя нет, есть куда расти, и
// прогоны без критериев приёмки вообще не трогаются — там «модель
// закончила» означает ровно то, что написано.
func (t *Tracker) CanContinue() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped != "" || t.continued >= maxContinues {
		return false
	}
	if !t.m.Long() && !t.m.HasAcceptance() {
		return false
	}
	return !t.stallHard()
}

// Continued — отметить, что попросили продолжить, и вернуть номер попытки.
func (t *Tracker) Continued() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.continued++
	return t.continued
}

// Continues — сколько раз прогон просил модель продолжить.
func (t *Tracker) Continues() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.continued
}

// CheckpointDue — пора ли сохранять состояние на диск.
func (t *Tracker) CheckpointDue() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.m.CheckpointEvery
	if n <= 0 {
		return false
	}
	return t.iters > 0 && t.iters%n == 0
}

// CountCheckpoint — зафиксировать сохранение.
func (t *Tracker) CountCheckpoint() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checkpoints++
}

// ---------- Приглашение к продолжению ----------

// MarkResumeShown — отметить, что приглашение к продолжению уже вставлено
// в историю, и вернуть номер вставки (0 — ещё не было).
//
// Отдельный счётчик, а не булев флаг, потому что по нему видно и
// повторное вставление: номер больше единицы означал бы, что история
// засорялась одинаковыми сообщениями, — и это уже видно в тестах и в
// /mission status, а не только по косвенным признакам.
func (t *Tracker) MarkResumeShown() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resumeShown = true
	return 1
}

// ResumeShown — вставляли ли уже приглашение к продолжению.
//
// Состояние долговременное: переживает и ход, и перезапуск процесса,
// потому что лежит рядом со счётчиками прогона.
func (t *Tracker) ResumeShown() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resumeShown
}

// SelfLine — строка о прогоне для self_status.
//
// Отдельный метод, а не переиспользование Status: Status — это короткая
// сводка «сколько прошло из сколького» для /mission status, а здесь нужно
// ещё и то, чего модель иначе не знает: к ней уже обращались с
// требованием продолжить, критерии уже сколько раз просили проверить, и
// прогон был ли подхвачен после обрыва. Без этого модель на длинной
// дистанции не отличает «меня много раз просили продолжить» от
// «меня никто не трогал» и продолжает одно и то же по кругу.
func (t *Tracker) SelfLine() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var b strings.Builder
	if st := t.status(); st != "" {
		b.WriteString(st)
	}
	parts := func(s string) {
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(s)
	}
	fmt.Fprintf(&b, "вызовы инструментов: %d", t.toolCalls)
	if t.continued > 0 {
		parts(fmt.Sprintf("просили продолжить %d %s", t.continued,
			pluralRun(t.continued, "раз", "раза", "раз")))
	}
	if t.verifyAsked > 0 {
		parts(fmt.Sprintf("просили проверить критерии %d %s", t.verifyAsked,
			pluralRun(t.verifyAsked, "раз", "раза", "раз")))
	}
	if t.checkpoints > 0 {
		parts(fmt.Sprintf("сохранений: %d", t.checkpoints))
	}
	if t.resumeShown {
		parts("прогон продолжен после обрыва")
	}
	if t.stopped != "" {
		parts("остановлен: " + StopReasonLabel(t.stopped))
	}
	return b.String()
}

// pluralRun — согласовать существительное с числом по-русски.
//
// Копия правила из tools (там оно живёт для отчёта self_status), а не
// общий помощник: правило на два слова и тянет за собой лишнюю
// зависимость пакетов, которые друг о другу ничего не знают. Если
// понадобится третье место — тогда и выносить.
func pluralRun(n int, one, few, many string) string {
	if n%100 >= 11 && n%100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// Status — строка «сколько прошло из сколького» для интерфейса.
func (t *Tracker) Status() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status()
}

func (t *Tracker) status() string {
	var b strings.Builder
	if t.m.Deadline > 0 {
		left := t.left()
		if left < 0 {
			fmt.Fprintf(&b, "время: просрочено на %s", FormatDur(-left))
		} else {
			fmt.Fprintf(&b, "время: %s из %s", FormatDur(t.elapsed()), t.m.Deadline.String())
		}
	}
	if t.m.MaxIters > 0 {
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		fmt.Fprintf(&b, "итерации: %d из %d", t.iters, t.m.MaxIters)
	}
	if t.m.TokenBudget > 0 {
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		fmt.Fprintf(&b, "токены: %s из %s",
			CompactNum(t.tokens), CompactNum(t.m.TokenBudget))
	}
	if b.Len() == 0 {
		return "миссия: без потолков"
	}
	return b.String()
}

// CompactNum — компактное число: 1500000 → «1.5M».
func CompactNum(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fK", float64(n)/1e3)
	case n >= 1000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return strconv.Itoa(n)
}

// FormatDur — длительность по-человечески: 2ч 30м, 45м, 12с.
//
// Человеку не нужны миллисекунды точности: «2ч 30м» читается сразу, а
// «2h30m0s» приходится разбирать. Секунды остаются, потому что короткие
// паузы между шагами важны.
func FormatDur(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%dс", int(d.Seconds()))
	case d < time.Hour:
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s == 0 {
			return fmt.Sprintf("%dм", m)
		}
		return fmt.Sprintf("%dм %dс", m, s)
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dч", h)
		}
		return fmt.Sprintf("%dч %dм", h, m)
	}
}

// ---------- Состояние прогона на диске ----------

// MissionState — что известно о прогоне после перезапуска.
//
// Отдельный файл от mission.json по той же причине, что Tracker от
// Mission: mission.json человек пишет и правит руками, а mission_state
// перезаписывает машина каждые несколько итераций. Смешай их — и человек
// обнаружит в своём задании счётчики, которые он не писал, а правило
// «удаляй лишние поля» перестанет работать.
type MissionState struct {
	// Objective — цель, ради которой прогон идёт.
	Objective string `json:"objective,omitempty"`
	// Mode — режим прог��на.
	Mode string `json:"mode,omitempty"`
	// Summary — последняя строка состояния: что делалось и где остановились.
	Summary string `json:"summary,omitempty"`
	// Status — живой статус (время, итерации, токены).
	Status string `json:"status,omitempty"`
	// StopReason — причина остановки, если прогон закрылся.
	StopReason string `json:"stop_reason,omitempty"`
	// Iters, ToolCalls, Tokens — счётчики на момент записи.
	// Нужны, чтобы после перезапуска видеть, сколько работы уже оплачено,
	// а не начинать с нуля и не понимать, почему бюджет кончился сразу.
	Iters     int `json:"iters,omitempty"`
	ToolCalls int `json:"tool_calls,omitempty"`
	Tokens    int `json:"tokens,omitempty"`
	// Updated — когда записано (RFC3339). Пустой файл без времени
	// нельзя отличить от свежего.
	Updated string `json:"updated,omitempty"`
}

// MissionStatePath — файл состояния прогона.
func MissionStatePath(workDir string) string {
	return filepath.Join(workDir, ".gcli", "mission_state.json")
}

// SaveMissionState — записать состояние прогона атомарно.
func SaveMissionState(path string, st MissionState) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(path, append(data, '\n'), 0o644)
}

// LoadMissionState — прочитать состояние прогона.
//
// Отсутствие файла — не ошибка: прогон мог и не начинаться. Возвращается
// ok=false, чтобы вызывающий не путал «состояния нет» с «состояние пустое».
func LoadMissionState(path string) (MissionState, bool, error) {
	if path == "" {
		return MissionState{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MissionState{}, false, nil
		}
		return MissionState{}, false, err
	}
	if HasJSONC(data) {
		data = StripJSONC(data)
	}
	var st MissionState
	if err := json.Unmarshal(data, &st); err != nil {
		return MissionState{}, false, fmt.Errorf("mission_state.json не разбирается: %w", err)
	}
	return st, true, nil
}

// FreshMissionState — состояние, записанное недавно.
//
// «Недавно» — намеренно широкое окно (сутки). Узкое окно выглядело бы
// аккуратнее, но обрывало бы прогон, который человек оставил на ночь:
// утром он получил бы «состояние слишком старое» вместо «продолжаем».
// Старое состояние безопасно и полезно: по нему видно, на чём работа встала.
func FreshMissionState(st MissionState, now time.Time, within time.Duration) bool {
	if st.Updated == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, st.Updated)
	if err != nil {
		return false
	}
	return now.Sub(t) >= -time.Hour && now.Sub(t) <= within
}

// ---------- Блок промпта ----------

// PromptBlock — кусок системного промпта про миссию.
//
// Без него модель в многочасовом прогоне не знает ни срока, ни того, что
// критерии надо проверять, и работает вслепую: заканчивает, когда
// перестаёт видеть смысл, а не когда сделано.
func (m Mission) PromptBlock() string {
	if !m.Long() && !m.HasAcceptance() {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Автономный прогон\n")

	if m.Objective != "" {
		fmt.Fprintf(&b, "\nЦель: %s\n", m.Objective)
	}
	if m.HasAcceptance() {
		b.WriteString("\nРабота закончена, только когда выполнены критерии приёмки:\n")
		for _, c := range m.Acceptance {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString("\nПеред тем как объявить работу законченной, ПРОВЕРЬ каждый пункт " +
			"инструментами и перечисли в ответе, каким именно вызовом проверил каждый. " +
			"«Должно работать» — не проверка. Если пункт выполнить нельзя, " +
			"скажи это прямо и назови причину.\n")
	}

	var limits []string
	if m.Deadline > 0 {
		limits = append(limits, "срок "+m.Deadline.String())
	}
	if m.MaxIters > 0 {
		limits = append(limits, "потолок итераций "+strconv.Itoa(m.MaxIters))
	}
	if m.TokenBudget > 0 {
		limits = append(limits, "бюджет "+CompactNum(m.TokenBudget)+" токенов")
	}
	if m.CostBudget > 0 {
		limits = append(limits, "бюджет "+FormatUSD(m.CostBudget))
	}
	if m.MaxToolCalls > 0 {
		limits = append(limits, "потолок "+strconv.Itoa(m.MaxToolCalls)+" вызовов инструментов")
	}
	if len(limits) > 0 {
		sort.Strings(limits)
		fmt.Fprintf(&b, "\nПотолки этого прогона: %s.\n", strings.Join(limits, ", "))
		b.WriteString("Они жёсткие: на исходе работа оборвётся сама, и незавершённое " +
			"останется незавершённым. Планируй порядок шагов так, чтобы к концу " +
			"срока оставалась готовая, проверяемая часть, а не начало.\n")
	}

	b.WriteString("\nРаботай сам, без уточняющих вопросов: человек отошёл. " +
		"Выбирай самый надёжный следующий шаг, проверяй результат и продолжай.\n")
	return b.String()
}
