package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Repo — высокоуровневый доступ к конфигу и сессиям.
type Repo struct {
	Store *Store
	Cfg   Config
}

// Open — загрузить конфиг из хранилища (или создать значения по умолчанию).
func Open() *Repo {
	r := &Repo{Store: NewStore(), Cfg: DefaultConfig()}
	r.Cfg = *r.LoadConfig()
	return r
}

// LoadConfig — прочитать config.json, применив значения по умолчанию.
func (r *Repo) LoadConfig() *Config {
	cfg := DefaultConfig()
	data, err := os.ReadFile(r.Store.ConfigPath())
	if err == nil {
		// Комментарии и висячие запятые допускаются и тут: этот файл
		// человек правит руками чаще всех остальных (ключ провайдера,
		// модель, разрешения), и запрет комментариев превращает его
		// в место, где непонятно, что зачем.
		if HasJSONC(data) {
			data = StripJSONC(data)
		}
		_ = json.Unmarshal(data, &cfg)
	}
	if cfg.Think == "" {
		cfg.Think = "auto"
	}
	if cfg.SubMaxDepth <= 0 {
		cfg.SubMaxDepth = 1
	}
	if cfg.SubMaxPar <= 0 {
		cfg.SubMaxPar = 3
	}
	if cfg.SubMaxTurns <= 0 {
		cfg.SubMaxTurns = 12
	}
	if cfg.SubTimeoutMin <= 0 {
		cfg.SubTimeoutMin = 10
	}
	// Бюджет сессии: ноль — это «потолок не задан», и маршрутизация по нему
	// молча выключена. Отрицательное значение в конфиге — опечатка, а не
	// замысел, поэтому оно тоже схлопывается в ноль.
	if cfg.SubBudget < 0 {
		cfg.SubBudget = 0
	}
	// Лимиты продления хода. Отрицательные значения — опечатка, а не замысел:
	// ноль здесь означает «взять дефолт пакета», и пропустить минус вниз нельзя,
	// иначе продление молча выключится и агент упрётся в лимит без объяснения.
	if cfg.MaxItersAbs < 0 {
		cfg.MaxItersAbs = 0
	}
	if cfg.TurnExtendMax < 0 {
		cfg.TurnExtendMax = 0
	}
	if cfg.TurnExtendStep < 0 {
		cfg.TurnExtendStep = 0
	}
	return &cfg
}

// SaveConfig — сохранить конфиг атомарно с правами 0600 (там лежат ключи).
func (r *Repo) SaveConfig() error {
	data, err := json.MarshalIndent(r.Cfg, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(r.Store.ConfigPath(), data, 0o600)
}

// EnsureProviderCfg — запись конфига провайдера (создаётся при необходимости).
func (r *Repo) EnsureProviderCfg(id string) *ProviderCfg {
	if r.Cfg.Providers == nil {
		r.Cfg.Providers = map[string]*ProviderCfg{}
	}
	pc := r.Cfg.Providers[id]
	if pc == nil {
		pc = &ProviderCfg{}
		r.Cfg.Providers[id] = pc
	}
	return pc
}

// ---------- Сессии ----------

// NewSession — создать новую сессию.
func (r *Repo) NewSession(providerID, model, workDir string) *Session {
	now := time.Now()
	return &Session{
		ID:        now.Format("20060102-150405") + "-" + RandID(4),
		Created:   now,
		Updated:   now,
		Provider:  providerID,
		Model:     model,
		AgentMode: r.Cfg.Agent,
		CWD:       workDir,
		Perms:     Perms{BashExact: map[string]bool{}},
	}
}

// SaveSession — сохранить сессию атомарно.
func (r *Repo) SaveSession(s *Session) error {
	if s == nil {
		return nil
	}
	s.Updated = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(filepath.Join(r.Store.Sessions(), s.ID+".json"), data, 0o600)
}

// LoadSession — загрузить сессию по id.
//
// id валидируется: он приходит и из HTTP (?session=...), а «../../x» без
// проверки превращал LoadSession в оракул по json-файлам машины.
func (r *Repo) LoadSession(id string) (*Session, error) {
	if !ValidSessionID(id) {
		return nil, fmt.Errorf("плохой id сессии: %q", id)
	}
	if !strings.HasSuffix(id, ".json") {
		id += ".json"
	}
	data, err := os.ReadFile(filepath.Join(r.Store.Sessions(), id))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Perms.BashExact == nil {
		s.Perms.BashExact = map[string]bool{}
	}
	return &s, nil
}

// ListSessions — сессии, свежие первыми.
func (r *Repo) ListSessions() []Session {
	ents, err := os.ReadDir(r.Store.Sessions())
	if err != nil {
		return nil
	}
	var out []Session
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if s, err := r.LoadSession(strings.TrimSuffix(e.Name(), ".json")); err == nil {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// SessionInfo — шапка сессии без содержимого переписки.
//
// Спискам (таблица /sessions, дашборд /stats, прунинг) сообщения не нужны:
// нужен id, тема, время, модель и счётчики. Полный Unmarshal каждой сессии
// тянет всю переписку в память — на каталоге с крупными сессиями это
// секунды разбора и десятки мегабайт мусора ради пяти полей.
type SessionInfo struct {
	ID        string
	Title     string
	Created   time.Time
	Updated   time.Time
	Provider  string
	Model     string
	AgentMode bool
	Turns     int
	// Messages — только число сообщений, содержимое не грузится.
	// Для текста сообщений нужна полная загрузка: LoadSession.
	Messages int
	Usage    Usage
	Stats    Stats
	CWD      string
}

// ListSessionInfos — шапки сессий, свежие первыми. Потоковое чтение:
// файл открывается декодером, знакомые ключи разбираются, «messages»
// считается по числу элементов, всё остальное пропускается токен-ходом
// без копирования байт в память.
func (r *Repo) ListSessionInfos() []SessionInfo {
	ents, err := os.ReadDir(r.Store.Sessions())
	if err != nil {
		return nil
	}
	out := make([]SessionInfo, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		f, err := os.Open(filepath.Join(r.Store.Sessions(), e.Name()))
		if err != nil {
			continue
		}
		si, err := scanSessionHeader(json.NewDecoder(f))
		f.Close()
		if err != nil {
			continue
		}
		if si.ID == "" {
			si.ID = strings.TrimSuffix(e.Name(), ".json")
		}
		out = append(out, si)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// scanSessionHeader — разобрать шапку сессии из JSON-потока.
//
// Именно Decoder, а не Unmarshal: Unmarshal требует весь файл в памяти,
// а смысл быстрого списка — не держать переписку. Ключи, отсутствующие
// в SessionInfo, пропускаются без разбора, поэтому длинный хвост
// «messages» стоит лишь подсчётом элементов.
func scanSessionHeader(dec *json.Decoder) (SessionInfo, error) {
	var si SessionInfo
	tok, err := dec.Token()
	if err != nil {
		return si, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return si, fmt.Errorf("сессия: ждут объект, получено %v", tok)
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return si, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return si, fmt.Errorf("сессия: ждут имя поля, получено %v", keyTok)
		}
		switch key {
		case "id":
			err = dec.Decode(&si.ID)
		case "title":
			err = dec.Decode(&si.Title)
		case "created":
			err = dec.Decode(&si.Created)
		case "updated":
			err = dec.Decode(&si.Updated)
		case "provider":
			err = dec.Decode(&si.Provider)
		case "model":
			err = dec.Decode(&si.Model)
		case "agent_mode":
			err = dec.Decode(&si.AgentMode)
		case "turns":
			err = dec.Decode(&si.Turns)
		case "usage":
			err = dec.Decode(&si.Usage)
		case "stats":
			err = dec.Decode(&si.Stats)
		case "cwd":
			err = dec.Decode(&si.CWD)
		case "messages":
			si.Messages, err = countMessages(dec)
		default:
			err = skipJSONValue(dec)
		}
		if err != nil {
			return si, err
		}
	}
	_, err = dec.Token() // закрывающая '}' объекта сессии
	return si, err
}

// countMessages — число сообщений сессии. Хранение допускает оба вида:
// «messages":null» (сессия без сообщений — SaveSession так пишет пустую)
// и массив. Полный Unmarshal null терпел молча, поэтому и здесь null —
// ноль, а не ошибка чтения: иначе пустые сессии пропадали из списка.
func countMessages(dec *json.Decoder) (int, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	if tok == nil {
		return 0, nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return 0, fmt.Errorf("сессия: ждут массив или null в messages, получено %v", tok)
	}
	return countJSONElements(dec)
}

// countJSONArray — число элементов массива без разбора содержимого.
// Элементы считаются по закрытию верхнеуровневого значения: для массива
// объектов это каждая его закрывающая скобка, вернувшая глубину в ноль.
func countJSONArray(dec *json.Decoder) (int, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return 0, fmt.Errorf("сессия: ждут массив, получено %v", tok)
	}
	return countJSONElements(dec)
}

// countJSONElements — элементы уже открытого массива, '[' съеден.
func countJSONElements(dec *json.Decoder) (int, error) {
	n, depth := 0, 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, err
		}
		d, ok := tok.(json.Delim)
		if !ok {
			if depth == 0 {
				n++ // скалярный элемент
			}
			continue
		}
		switch d {
		case '[', '{':
			depth++
		default:
			depth--
			if depth < 0 {
				return n, nil // закрылся сам массив
			}
			if depth == 0 {
				n++ // закрылся элемент-объект или элемент-массив
			}
		}
	}
}

// skipJSONValue — пропустить любое JSON-значение, не копируя байты.
// Token-ход по скобочной глубине: декодер сам читает поток посимвольно,
// память под пропускаемое не выделяется вовсе — в отличие от трюка с
// json.RawMessage, который буферизует всё значение целиком.
func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if _, ok := tok.(json.Delim); !ok {
		return nil // скаляр; тип скобки не важен — глубина 1 покрывает и '{', и '['
	}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if dd, ok := tok.(json.Delim); ok {
			if dd == '{' || dd == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

// DeleteSession — удалить сессию и её чекпоинты.
func (r *Repo) DeleteSession(id string) error {
	if !ValidSessionID(id) {
		return fmt.Errorf("плохой id сессии: %q", id)
	}
	// LoadSession принимает и «id», и «id.json»; удаление обязано
	// понимать те же имена, иначе сессия, найденная по файлу, не удаляется.
	id = strings.TrimSuffix(id, ".json")
	_ = os.RemoveAll(filepath.Join(r.Store.Checkpoints(), id))
	return os.Remove(filepath.Join(r.Store.Sessions(), id+".json"))
}

// ValidSessionID — id сессии из сети: только безопасные для имени файла
// символы, без разделителей и «..».
//
// Экспортирована не для удобства, а потому что id приходит из HTTP
// (/v1/history), и проверять его обязаны все, кто превращает его в путь:
// вторая частная копия в app_session.go уже существовала и могла бы
// разойтись с этой молча — security-функция в двух экземплярах это
// уязвимость с отсрочкой.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > 64 || strings.Contains(id, "..") ||
		strings.ContainsAny(id, `/:\`) || strings.HasPrefix(id, ".") {
		return false
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// PruneSessions — оставить последние keep сессий, удалить остальные.
// Возвращает количество удалённых.
func (r *Repo) PruneSessions(keep int) int {
	if keep <= 0 {
		return 0
	}
	all := r.ListSessionInfos()
	if len(all) <= keep {
		return 0
	}
	n := 0
	for _, s := range all[keep:] {
		if err := r.DeleteSession(s.ID); err == nil {
			n++
		}
	}
	return n
}

// ---------- Чекпоинты ----------

// maxCheckpoints — сколько последних бэкапов хранить в сессии.
const maxCheckpoints = 50

// RecordCheckpoint — сохранить копию файла перед изменением (для /undo).
// Если файл не существовал, запоминается только факт отсутствия,
// чтобы /undo сумел удалить созданный файл.
func (r *Repo) RecordCheckpoint(s *Session, path, label string) {
	if s == nil {
		return
	}
	meta := CheckpointMeta{Path: path, Time: time.Now(), Label: label}
	if info, err := os.Stat(path); err == nil {
		meta.Mode = uint32(info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		dir := filepath.Join(r.Store.Checkpoints(), s.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		bp := filepath.Join(dir, RandID(6)+"-"+filepath.Base(path))
		if err := os.WriteFile(bp, data, 0o644); err != nil {
			return // не смогли сохранить копию — не рискуем
		}
		meta.Backup = bp
		meta.Existed = true
	case os.IsNotExist(err):
		meta.Existed = false
	default:
		return // файл есть, но не читается — пропускаем
	}
	s.Checkpoints = append(s.Checkpoints, meta)
	for len(s.Checkpoints) > maxCheckpoints {
		old := s.Checkpoints[0]
		if old.Backup != "" {
			_ = os.Remove(old.Backup)
		}
		s.Checkpoints = s.Checkpoints[1:]
	}
}

// UndoCheckpoint — откатить последний чекпоинт. ok=false, если откатывать нечего.
func (r *Repo) UndoCheckpoint(s *Session) (CheckpointMeta, bool, error) {
	if s == nil {
		return CheckpointMeta{}, false, nil
	}
	if len(s.Checkpoints) == 0 {
		return CheckpointMeta{}, false, nil
	}
	cp := s.Checkpoints[len(s.Checkpoints)-1]
	if !cp.Existed {
		if err := os.Remove(cp.Path); err != nil && !os.IsNotExist(err) {
			// Не смогли удалить созданный файл — откат не состоялся.
			// Молча идти к более старому чекпоинту нельзя: человек
			// получил бы «отменено», а файл остался бы на месте.
			// Чекпоинт остаётся в сессии: откат можно повторить.
			return cp, false, err
		}
		s.Checkpoints = s.Checkpoints[:len(s.Checkpoints)-1]
		return cp, true, nil
	}
	data, err := os.ReadFile(cp.Backup)
	if err != nil {
		_ = os.Remove(cp.Backup)
		return cp, false, err
	}
	if err := os.MkdirAll(filepath.Dir(cp.Path), 0o755); err != nil {
		return cp, false, err
	}
	mode := os.FileMode(0o644)
	if cp.Mode != 0 {
		mode = os.FileMode(cp.Mode)
	}
	if err := WriteAtomic(cp.Path, data, mode); err != nil {
		return cp, false, err
	}
	s.Checkpoints = s.Checkpoints[:len(s.Checkpoints)-1]
	return cp, true, nil
}
