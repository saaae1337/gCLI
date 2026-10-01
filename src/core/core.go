// Package core — фундамент gcli: типы диалога, конфигурация, сессии,
// чекпоинты, файловые и текстовые утилиты.
//
// Пакет не зависит от UI и сетевого слоя: его можно использовать
// и в тестах, и в будущих подкомандах-утилитах.
package core

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Version — версия приложения (обновляется при сборке через -ldflags).
var Version = "5.5.0"

// ---------- Типы диалога ----------

// Role — роль сообщения в диалоге.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall — вызов инструмента, запрошенный моделью.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"` // JSON-строка аргументов
}

// Image — изображение, приложенное к сообщению (зрение агента).
// Data — base64 без префикса data:, MIME — media_type (image/png и т.п.).
// Path — путь к исходному файлу, если изображение загружено с диска.
type Image struct {
	MIME string `json:"mime"`
	Data string `json:"data"`
	Path string `json:"path,omitempty"`
}

// Message — универсальное сообщение (конвертируется под каждый протокол).
type Message struct {
	Role         Role       `json:"role"`
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string     `json:"tool_call_id,omitempty"`
	Name         string     `json:"name,omitempty"`
	Reasoning    string     `json:"reasoning,omitempty"`     // размышления модели
	ReasoningSig string     `json:"reasoning_sig,omitempty"` // подпись thinking для Anthropic
	// Images — изображения, которые модель должна увидеть (зрение).
	// Пусто у обычных текстовых сообщений.
	Images []Image `json:"images,omitempty"`

	// Sub — имя субагента-автора сообщения (пусто = главный агент).
	// Позволяет нескольким агентам вести диалог в одной сессии.
	Sub string `json:"sub,omitempty"`
	// Hidden — служебное сообщение, не показывать в UI.
	Hidden bool `json:"hidden,omitempty"`
}

// Usage — потребление токенов по данным API.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Add — прибавить другое потребление.
func (u *Usage) Add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
}

// Total — суммарное число токенов.
func (u Usage) Total() int { return u.PromptTokens + u.CompletionTokens }

// Delta — событие потоковой генерации.
type Delta struct {
	Text      string
	Reasoning string // скрытые размышления
	Sig       string // подпись thinking-блока (Anthropic)

	// Частичный вызов инструмента.
	IsTool  bool
	TCIndex int
	TCID    string
	TCName  string
	TCArgs  string

	Usage  *Usage
	Finish string
}

// Todo — задача в плане работ.
type Todo struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

// Статусы задач.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoCompleted  = "completed"
)

// ValidStatus — привести произвольную строку к допустимому статусу.
func ValidStatus(s string) string {
	switch s {
	case TodoPending, TodoInProgress, TodoCompleted:
		return s
	}
	return TodoPending
}

// CheckpointMeta — метаданные резервной копии файла для /undo.
type CheckpointMeta struct {
	Path    string    `json:"path"`
	Backup  string    `json:"backup,omitempty"`
	Existed bool      `json:"existed"`
	Time    time.Time `json:"time"`
	Label   string    `json:"label,omitempty"` // какой инструмент изменил файл
}

// Perms — память разрешений текущей сессии.
type Perms struct {
	BashExact map[string]bool `json:"bash_exact,omitempty"`
	BashAll   bool            `json:"bash_all,omitempty"`
	FileWrite bool            `json:"file_write,omitempty"`
	WebFetch  bool            `json:"web_fetch,omitempty"` // выдавать все http-инструменты без вопроса

	// Autopilot — одобрять безопасные операции без вопроса пользователю.
	Autopilot bool `json:"autopilot,omitempty"`
	// AutopilotAll — усиленный автопилот: одобрять вообще всё,
	// включая потенциально опасные команды (rm -rf, sudo, форматирование).
	AutopilotAll bool `json:"autopilot_all,omitempty"`
}

// Clone — глубокая копия (карта тоже копируется).
func (p Perms) Clone() Perms {
	out := p
	out.BashExact = make(map[string]bool, len(p.BashExact))
	for k, v := range p.BashExact {
		out.BashExact[k] = v
	}
	return out
}

// Empty — разрешений нет.
func (p Perms) Empty() bool {
	return !p.BashAll && !p.FileWrite && !p.WebFetch && len(p.BashExact) == 0
}

// ProviderCfg — запись о провайдере в config.json.
type ProviderCfg struct {
	BaseURL  string   `json:"base_url,omitempty"`
	APIKey   string   `json:"api_key,omitempty"`
	Model    string   `json:"model,omitempty"`
	Protocol string   `json:"protocol,omitempty"` // openai | anthropic
	Models   []string `json:"models,omitempty"`
	Custom   bool     `json:"custom,omitempty"`
}

// Config — настройки ~/.gcli/config.json.
type Config struct {
	Provider  string                  `json:"provider"`
	Model     string                  `json:"model"`
	Agent     bool                    `json:"agent"`
	Think     string                  `json:"think,omitempty"` // on | off | auto
	Providers map[string]*ProviderCfg `json:"providers,omitempty"`
	SkillsOff []string                `json:"skills_off,omitempty"`

	// Интерфейс.
	Unicode    *bool `json:"unicode,omitempty"`    // псевдографика в блоках (nil = авто)
	Compact    bool  `json:"compact,omitempty"`    // компактные блоки без пустых строк
	ShowTime   bool  `json:"show_time,omitempty"`  // показывать время в шапках
	Animations *bool `json:"animations,omitempty"` // анимации появления и спиннер (nil = авто)
	// Mascot — маскот «Искра» (кот gcli): встречает в баннере, сидит
	// у строки ввода, живёт в спиннере и провожает при выходе
	// (nil = включён; GCLI_MASCOT=0 выключает принудительно).
	Mascot *bool `json:"mascot,omitempty"`
	// PlanMode — режим планирования: агент сначала предлагает план и не
	// меняет файлы, пока пользователь не одобрит (/plan).
	PlanMode bool `json:"plan_mode,omitempty"`

	// Поведение агента.
	MaxIters    int `json:"max_iters,omitempty"`    // лимит итераций агентного цикла
	AutoCompact int `json:"auto_compact,omitempty"` // порог авто-сжатия контекста (0 = 80k)
	// MaxItersAbs — абсолютный потолок итераций за ход с учётом продлений
	// (0 = 200). Продление не поднимает потолок: оно лишь распределяет его
	// по требованию модели. Значение никогда не считается меньше max_iters —
	// иначе конфиг тихо урезал бы работающий лимит.
	MaxItersAbs int `json:"max_iters_abs,omitempty"`
	// TurnExtendMax — сколько раз за один ход можно продлить лимит
	// (0 = 8). Ноль и отрицательное не значат «нельзя продлевать», а
	// значат «взять дефолт»: выключить продление целиком можно одним
	// лимитом max_iters, заводить для этого отдельный выключатель — лишнее.
	TurnExtendMax int `json:"turn_extend_max,omitempty"`
	// TurnExtendStep — размер одного продления в итерациях (0 = 30).
	// Модель может попросить меньше, но не больше: иначе «продли на 500»
	// превращает механизм в способ обойти потолок.
	TurnExtendStep int    `json:"turn_extend_step,omitempty"`
	Subagents      bool   `json:"subagents,omitempty"`       // разрешить субагентов
	Autopilot      bool   `json:"autopilot,omitempty"`       // автопилот: одобрять безопасные команды самому (0 = спрашивать)
	AutopilotAll   bool   `json:"autopilot_all,omitempty"`   // автопилот повышенного риска: одобрять всё, включая опасные команды
	SubMaxDepth    int    `json:"sub_max_depth,omitempty"`   // глубина вложенности (1..3)
	SubMaxPar      int    `json:"sub_max_par,omitempty"`     // максимум субагентов одновременно
	SubModel       string `json:"sub_model,omitempty"`       // модель субагентов (пусто = как у главного)
	SubMaxTurns    int    `json:"sub_max_turns,omitempty"`   // лимит ходов одного субагента
	SubTimeoutMin  int    `json:"sub_timeout_min,omitempty"` // потолок времени на субагента, минут (0 = 10)
	SubRetries     int    `json:"sub_retries,omitempty"`     // попытки запуска субагента при сбое (1..3, 0 = 2)
	// SubRoute — маршрутизация моделей субагентов по роли и бюджету.
	// По умолчанию выключена: молча перевести субагента на другую модель
	// нельзя, пользователь узнаёт об этом по счёту, а не по /agents.
	SubRoute bool `json:"sub_route,omitempty"`
	// SubBudget — потолок расхода сессии в токенах, под который умещается
	// маршрутизация (0 = не задан). Это не лимит: превышение не запрещает,
	// но на 70% и 90% маршрутизация начинает понижать модель.
	SubBudget     int  `json:"sub_budget,omitempty"`
	ParallelTools bool `json:"parallel_tools,omitempty"` // выполнять параллельные вызовы инструментов
	// Sandbox — песочница файловой системы: ограничить инструменты
	// рабочим каталогом. По умолчанию выключена, потому что в своём
	// проекте ограничение только мешает; в чужом репозитории её
	// включают руками, и это единственная защита от чтения ~/.gcli
	// с ключами API.
	Sandbox bool `json:"sandbox,omitempty"`
}

// DefaultConfig — конфигурация по умолчанию.
func DefaultConfig() Config {
	return Config{Agent: true, Think: "auto", Subagents: true, SubMaxDepth: 1, SubMaxPar: 3}
}

// ToolDef — описание инструмента для API.
type ToolDef struct {
	Name        string
	Description string
	Schema      string // JSON-схема параметров
}

// ChatRequest — запрос к модели.
type ChatRequest struct {
	Model     string
	System    string
	Messages  []Message
	ToolDefs  []ToolDef
	MaxTokens int
	Temp      float64
}

// Session — персистентная сессия диалога.
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	AgentMode bool      `json:"agent_mode"`
	Messages  []Message `json:"messages"`
	Todos     []Todo    `json:"todos,omitempty"`
	Perms     Perms     `json:"perms"`
	Turns     int       `json:"turns"`
	Usage     Usage     `json:"usage"`
	Stats     Stats     `json:"stats"`
	CWD       string    `json:"cwd"`

	Checkpoints []CheckpointMeta `json:"checkpoints,omitempty"`
	// SubagentRuns — журнал запусков субагентов за сессию.
	SubagentRuns []SubagentRecord `json:"subagent_runs,omitempty"`
}

// SubagentRecord — запись о запуске субагента в журнале сессии.
type SubagentRecord struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Task  string `json:"task"`
	Model string `json:"model"`
	// ModelWhy — почему субагент поехал на этой модели. Заполняется
	// маршрутизацией, когда модель понижена по роли или по бюджету: без
	// объяснения счёт за сессию выглядит как ошибка, а он — результат
	// настройки, которую пользователь сам включил.
	ModelWhy string        `json:"model_why,omitempty"`
	Status   string        `json:"status"` // running | done | error | canceled
	Depth    int           `json:"depth"`
	Turns    int           `json:"turns"`
	Tools    int           `json:"tools"`
	Elapsed  time.Duration `json:"elapsed_ns"`
	Summary  string        `json:"summary,omitempty"`
	Err      string        `json:"error,omitempty"`
}

// AddUsage — прибавить расход токенов к сессии.
func (s *Session) AddUsage(u Usage) { s.Usage.Add(u) }

// ---------- Утилиты ----------

// RandID — случайная hex-строка длиной n символов.
func RandID(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)[:n]
}

// Truncate — обрезать строку до n рун (без разрыва UTF-8).
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

// TruncateOnel — обрезать до n рун без многоточия (для выравнивания).
func TruncateOnel(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// OneLine — свернуть все переводы строк в пробелы.
func OneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.Join(strings.Fields(s), " ")
}

// Pad — дополнить строку пробелами до ширины w рун.
func Pad(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// PadLeft — дополнить слева пробелами до ширины w рун.
func PadLeft(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return strings.Repeat(" ", w-n) + s
}

// Min, Max — вспомогательные.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max — вспомогательные.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Clamp — ограничить v диапазоном [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Kfmt — компактное форматирование числа: 12345 → 12.3k.
func Kfmt(n int) string {
	switch {
	case n < 1000:
		return strconvItoa(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

func strconvItoa(n int) string { return fmt.Sprintf("%d", n) }

// HumanSize — размер файла человекочитаемо.
func HumanSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d Б", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f КБ", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f ГБ", float64(n)/(1024*1024*1024))
	}
}

// HumanDuration — длительность человекочитаемо: 950ms, 4.2s, 1m 12s.
func HumanDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dмс", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fс", d.Seconds())
	default:
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if m >= 60 {
			return fmt.Sprintf("%dч %dм", m/60, m%60)
		}
		return fmt.Sprintf("%dм %dс", m, s)
	}
}

// ApproxTokens — приблизительное число токенов в тексте.
//
// Учитывает, что кириллица и CJK токенизируются примерно в 1 token/rune,
// а латиница — примерно 1 token на 4 символа.
func ApproxTokens(s string) int {
	if s == "" {
		return 0
	}
	var cyr, cjk, other, spaces int
	for _, r := range s {
		switch {
		case r == ' ' || r == '\n' || r == '\t' || r == '\r':
			spaces++
		case r >= 0x0410 && r <= 0x044F, r >= 0x0400 && r <= 0x04FF: // кириллица
			cyr++
		case r >= 0x3000 && r <= 0x9FFF, r >= 0xAC00 && r <= 0xD7AF,
			r >= 0xF900 && r <= 0xFAFF: // CJK
			cjk++
		default:
			other++
		}
	}
	// 1 токен ≈ 4 символа латиницы, 1 токен ≈ 1.4 символа кириллицы,
	// 1 токен ≈ 0.7 символа CJK, пробелы почти бесплатны.
	tok := float64(other)/4 + float64(cyr)/1.4 + float64(cjk)/0.7 + float64(spaces)/8
	if tok < 1 && len(s) > 0 {
		return 1
	}
	return int(tok) + 1
}

// TruncateUTF8 — безопасно обрезать по рунам, оставив head и tail.
// Используется для вывода команд и результатов: не рвёт UTF-8.
func TruncateUTF8(s string, head, tail int) string {
	r := []rune(s)
	if len(r) <= head+tail {
		return s
	}
	return string(r[:head]) + "\n…[обрезано, символов: " +
		strconvItoa(len(r)-head-tail) + "]\n" + string(r[len(r)-tail:])
}

// IsBinary — грубая проверка на двоичный файл.
//
// Логика: смотрим на долю «непечатаемых» и управляющих байтов в первых
// 4 КБ. Нулевых байтов в тексте быть не может, но одиночные 0x00 встречаются
// в некоторых исходниках, поэтому одного нуля недостаточно для вывода «двоичный».
//
// Важно: окно проверки UTF-8 должно заканчиваться на границе символа,
// иначе кириллица на стыке даст «бинарный» вердикт (0xD0 — первый байт кириллицы).
func IsBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	const window = 4096
	n := Min(len(data), window)

	// Доля непечатаемых байтов (CR/LF/таб считаем нормальными).
	odd := 0
	for i := 0; i < n; i++ {
		c := data[i]
		if c == 0x09 || c == 0x0A || c == 0x0D {
			continue
		}
		if c < 0x20 || c == 0x7F {
			odd++
		}
	}
	// Больше 10% непечатаемых — это точно не текст.
	if odd*10 > n {
		return true
	}

	// Проверяем UTF-8 по всему окну, но допускаем «хвост» из неполного символа:
	// отступаем назад, пока не найдём начало корректной последовательности.
	end := n
	for end > 0 && !utf8.Valid(data[:end]) && end > n-4 {
		end--
	}
	return !utf8.Valid(data[:end])
}

// SplitLines — разбить на строки без хвостового пустого элемента.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// FlattenMessages — весь текст сообщений (для оценки контекста).
func FlattenMessages(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString(m.Reasoning)
		b.WriteString(" ")
		for _, tc := range m.ToolCalls {
			b.WriteString(tc.Args)
			b.WriteString(" ")
		}
	}
	return b.String()
}

// EstimateContext — приблизительный размер контекста в токенах.
//
// Учитывает только сообщения. Системный промпт сюда НЕ входит намеренно:
// он не растёт от хода к ходу, а историю сжимать нужно. Для оценки полного
// давления на окно используй FullContext.
func EstimateContext(msgs []Message) int { return ApproxTokens(FlattenMessages(msgs)) }

// FullContext — оценка контекста вместе с системным промптом и схемами
// инструментов.
//
// Смысл в том, что системный промпт здесь — не мелочь: базовые правила,
// блок навыков, память проекта и долговременная память вместе дают
// десятки тысяч токенов. Раньше их не считали, и порог авто-сжатия
// срабатывал поздно реального переполнения окна.
func FullContext(system string, toolDefs []ToolDef, msgs []Message) int {
	total := ApproxTokens(system)
	for _, td := range toolDefs {
		total += ApproxTokens(td.Name) + ApproxTokens(td.Description) + ApproxTokens(td.Schema)
	}
	total += EstimateContext(msgs)
	// Изображения в оценке не участвуют (их вес непредсказуем), но их
	// наличие стоит отметить, иначе агент не увидит, что они съедают окно.
	for _, m := range msgs {
		total += 1600 * len(m.Images)
	}
	return total
}

// Slug — превратить произвольную строку в безопасный slug.
func Slug(s string, maxLen int) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '_' || r == '-' || r == '.' || r == '/' || r == ':':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= maxLen {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "item"
	}
	return out
}

// SortStrings — сортировка строк (часто нужна, чтобы не тянуть sort в каждый файл).
func SortStrings(s []string) { sort.Strings(s) }

// StrikeOut — зачёркнутый текст для выполненных задач.
// Используется U+0336 (combining long stroke overlay), поэтому длина строки
// по рунам меняется, но видимая ширина — нет.
func StrikeOut(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		b.WriteRune('\u0336')
	}
	return b.String()
}
