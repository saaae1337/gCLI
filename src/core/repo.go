package core

import (
	"encoding/json"
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
func (r *Repo) LoadSession(id string) (*Session, error) {
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

// DeleteSession — удалить сессию и её чекпоинты.
func (r *Repo) DeleteSession(id string) error {
	_ = os.RemoveAll(filepath.Join(r.Store.Checkpoints(), id))
	return os.Remove(filepath.Join(r.Store.Sessions(), id+".json"))
}

// PruneSessions — оставить последние keep сессий, удалить остальные.
// Возвращает количество удалённых.
func (r *Repo) PruneSessions(keep int) int {
	if keep <= 0 {
		return 0
	}
	all := r.ListSessions()
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
	for len(s.Checkpoints) > 0 {
		cp := s.Checkpoints[len(s.Checkpoints)-1]
		s.Checkpoints = s.Checkpoints[:len(s.Checkpoints)-1]
		if !cp.Existed {
			if err := os.Remove(cp.Path); err == nil || os.IsNotExist(err) {
				return cp, true, nil
			}
			continue
		}
		data, err := os.ReadFile(cp.Backup)
		if err != nil {
			_ = os.Remove(cp.Backup)
			return cp, false, err
		}
		if err := os.MkdirAll(filepath.Dir(cp.Path), 0o755); err != nil {
			return cp, false, err
		}
		if err := WriteAtomic(cp.Path, data, 0o644); err != nil {
			return cp, false, err
		}
		return cp, true, nil
	}
	return CheckpointMeta{}, false, nil
}
