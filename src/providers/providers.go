// Package providers — ИИ-провайдеры: пресеты, пользовательские endpoint-ы,
// два протокола (OpenAI-совместимый и Anthropic), стриминг с SSE,
// ретраи, учёт reasoning и диагностика ключей.
package providers

import (
	"os"
	"sort"
	"strings"

	"gcli/core"
)

// Protocol — протокол общения с провайдером.
const (
	ProtoOpenAI    = "openai"
	ProtoAnthropic = "anthropic"
)

// Provider — описание ИИ-провайдера.
type Provider struct {
	ID           string
	Label        string
	Kind         string // ProtoOpenAI | ProtoAnthropic
	BaseURL      string
	KeyEnvs      []string
	Models       []string
	DefaultModel string
	Key          string
	ExtraHeaders map[string]string
	NoKey        bool
	Custom       bool
	ContextLimit int // максимальный контекст модели в токенах (0 = неизвестно)
}

// EnvOr — переменная окружения или значение по умолчанию.
func EnvOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Presets — встроенные провайдеры.
func Presets() []*Provider {
	ps := []*Provider{
		{
			ID: "zai", Label: "Z.ai GLM", Kind: ProtoOpenAI,
			BaseURL:      EnvOr("ZAI_BASE_URL", "https://api.z.ai/api/paas/v4"),
			KeyEnvs:      []string{"ZAI_API_KEY", "Z_AI_API_KEY", "ZHIPUAI_API_KEY", "GLM_API_KEY"},
			Models:       []string{"glm-4.6", "glm-4.5", "glm-4.5-air", "glm-4.5-flash"},
			ContextLimit: 200_000,
		},
		{
			ID: "openrouter", Label: "OpenRouter · 300+ моделей", Kind: ProtoOpenAI,
			BaseURL: EnvOr("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
			KeyEnvs: []string{"OPENROUTER_API_KEY"},
			Models: []string{
				"z-ai/glm-4.6", "anthropic/claude-sonnet-4.5", "openai/gpt-4o",
				"google/gemini-2.0-flash-001", "deepseek/deepseek-chat", "qwen/qwen3-coder",
			},
			ContextLimit: 128_000,
			ExtraHeaders: map[string]string{
				"HTTP-Referer": "https://github.com/gcli-terminal",
				"X-Title":      "gcli",
			},
		},
		{
			ID: "openai", Label: "OpenAI", Kind: ProtoOpenAI,
			BaseURL:      EnvOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
			KeyEnvs:      []string{"OPENAI_API_KEY"},
			Models:       []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "o4-mini"},
			ContextLimit: 128_000,
		},
		{
			ID: "anthropic", Label: "Anthropic Claude", Kind: ProtoAnthropic,
			BaseURL:      EnvOr("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
			KeyEnvs:      []string{"ANTHROPIC_API_KEY"},
			Models:       []string{"claude-sonnet-4-5", "claude-opus-4-1", "claude-3-7-sonnet-latest", "claude-3-5-haiku-latest"},
			ContextLimit: 200_000,
		},
		{
			ID: "deepseek", Label: "DeepSeek", Kind: ProtoOpenAI,
			BaseURL:      EnvOr("DEEPSEEK_BASE_URL", "https://api.deepseek.com/v1"),
			KeyEnvs:      []string{"DEEPSEEK_API_KEY"},
			Models:       []string{"deepseek-chat", "deepseek-reasoner"},
			ContextLimit: 128_000,
		},
		{
			ID: "groq", Label: "Groq · быстрый инференс", Kind: ProtoOpenAI,
			BaseURL:      EnvOr("GROQ_BASE_URL", "https://api.groq.com/openai/v1"),
			KeyEnvs:      []string{"GROQ_API_KEY"},
			Models:       []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant", "qwen/qwen3-32b"},
			ContextLimit: 128_000,
		},
		{
			ID: "mistral", Label: "Mistral AI", Kind: ProtoOpenAI,
			BaseURL:      EnvOr("MISTRAL_BASE_URL", "https://api.mistral.ai/v1"),
			KeyEnvs:      []string{"MISTRAL_API_KEY"},
			Models:       []string{"mistral-large-latest", "mistral-small-latest", "codestral-latest"},
			ContextLimit: 128_000,
		},
		{
			ID: "xai", Label: "xAI Grok", Kind: ProtoOpenAI,
			BaseURL:      EnvOr("XAI_BASE_URL", "https://api.x.ai/v1"),
			KeyEnvs:      []string{"XAI_API_KEY", "GROK_API_KEY"},
			Models:       []string{"grok-4", "grok-3", "grok-3-mini"},
			ContextLimit: 128_000,
		},
		{
			ID: "together", Label: "Together AI", Kind: ProtoOpenAI,
			BaseURL:      EnvOr("TOGETHER_BASE_URL", "https://api.together.xyz/v1"),
			KeyEnvs:      []string{"TOGETHER_API_KEY"},
			Models:       []string{"Qwen/Qwen3-Coder", "deepseek-ai/DeepSeek-V3", "meta-llama/Llama-3.3-70B-Instruct-Turbo"},
			ContextLimit: 128_000,
		},
		{
			ID: "ollama", Label: "Ollama (локально)", Kind: ProtoOpenAI, NoKey: true,
			BaseURL: EnvOr("OLLAMA_BASE_URL", "http://localhost:11434/v1"),
			Models:  []string{"qwen3", "llama3.1", "deepseek-r1", "gpt-oss:20b"},
		},
		{
			ID: "lmstudio", Label: "LM Studio (локально)", Kind: ProtoOpenAI, NoKey: true,
			BaseURL: EnvOr("LMSTUDIO_BASE_URL", "http://localhost:1234/v1"),
			Models:  []string{},
		},
	}

	// Модель из окружения имеет приоритет.
	if m := EnvOr("GLM_MODEL", EnvOr("ZAI_MODEL", "")); m != "" {
		ps[0].Models = prependUnique(m, ps[0].Models)
		ps[0].DefaultModel = m
	}
	if m := EnvOr("OPENAI_MODEL", ""); m != "" {
		ps[2].Models = prependUnique(m, ps[2].Models)
		ps[2].DefaultModel = m
	}
	if m := EnvOr("ANTHROPIC_MODEL", ""); m != "" {
		ps[3].Models = prependUnique(m, ps[3].Models)
		ps[3].DefaultModel = m
	}
	for _, p := range ps {
		if p.DefaultModel == "" && len(p.Models) > 0 {
			p.DefaultModel = p.Models[0]
		}
	}
	return ps
}

func prependUnique(m string, list []string) []string {
	if m == "" {
		return list
	}
	for _, x := range list {
		if x == m {
			return list
		}
	}
	return append([]string{m}, list...)
}

// ReProvID — допустимое имя провайдера в конфиге.
var reProvID = func(s string) bool {
	if len(s) < 2 || len(s) > 25 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			// цифра допустима, но не первой: id обязан начинаться с буквы,
			// иначе «1»-префиксные служебные имена конфликтуют с провайдерами
			if i == 0 {
				return false
			}
		case r == '_' || r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ValidID — проверка id провайдера.
func ValidID(id string) bool { return reProvID(id) }

// Registry — набор провайдеров (пресеты + пользовательские).
type Registry struct {
	All    []*Provider
	Config core.Config
}

// Build — собрать реестр из конфига.
func Build(cfg core.Config) *Registry {
	r := &Registry{All: Presets(), Config: cfg}

	ids := make([]string, 0, len(cfg.Providers))
	for id := range cfg.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		pc := cfg.Providers[id]
		if pc == nil {
			continue
		}
		p := r.Find(id)
		if p == nil {
			kind := pc.Protocol
			if kind != ProtoAnthropic {
				kind = ProtoOpenAI
			}
			p = &Provider{ID: id, Label: id + " · свой", Kind: kind, Custom: true}
			r.All = append(r.All, p)
		}
		if pc.BaseURL != "" {
			p.BaseURL = pc.BaseURL
		}
		if pc.APIKey != "" {
			p.Key = pc.APIKey
		}
		if pc.Model != "" {
			p.DefaultModel = pc.Model
			p.Models = prependUnique(pc.Model, p.Models)
		}
		for _, m := range pc.Models {
			p.Models = prependUnique(m, p.Models)
		}
	}
	for _, p := range r.All {
		if p.Key == "" {
			p.Key = p.findKey()
		}
	}
	return r
}

// Find — найти провайдера по id.
func (r *Registry) Find(id string) *Provider {
	for _, p := range r.All {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (p *Provider) findKey() string {
	for _, e := range p.KeyEnvs {
		if v := strings.TrimSpace(os.Getenv(e)); v != "" {
			return v
		}
	}
	return ""
}

// HasKey — есть ли ключ (или он не нужен).
func (p *Provider) HasKey() bool { return p.NoKey || p.Key != "" }

// Ready — готов ли провайдер к работе (есть ключ и база).
func (p *Provider) Ready() bool { return p.HasKey() && p.BaseURL != "" }

// MaskKey — замаскировать ключ для показа.
func MaskKey(k string) string {
	r := []rune(k)
	if len(r) <= 8 {
		return strings.Repeat("•", len(r))
	}
	return string(r[:5]) + "…" + string(r[len(r)-4:])
}

// CleanKey — нормализовать вставленный ключ: убрать пробелы, кавычки, "Bearer ".
func CleanKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'`«»")
	if len(s) > 7 && strings.EqualFold(s[:7], "bearer ") {
		s = strings.TrimSpace(s[7:])
	}
	return strings.TrimSpace(s)
}

// Pick — выбрать провайдера: явный id → из конфига → первый с ключом → первый.
func (r *Registry) Pick(prefID string) *Provider {
	if prefID != "" {
		if p := r.Find(prefID); p != nil {
			return p
		}
	}
	if r.Config.Provider != "" {
		if p := r.Find(r.Config.Provider); p != nil {
			return p
		}
	}
	for _, p := range r.All {
		if p.Key != "" {
			return p
		}
	}
	if len(r.All) > 0 {
		return r.All[0]
	}
	return &Provider{ID: "none", Label: "нет провайдера", Kind: ProtoOpenAI}
}

// ResolveModel — выбрать модель: флаг → конфиг (если есть у провайдера) → дефолт.
func ResolveModel(p *Provider, flagModel, cfgModel string) string {
	if flagModel != "" {
		return flagModel
	}
	if cfgModel != "" {
		for _, m := range p.Models {
			if m == cfgModel {
				return cfgModel
			}
		}
		return p.DefaultModel
	}
	return p.DefaultModel
}

// CtxLimit — лимит контекста модели (с запасом на ответ).
func (p *Provider) CtxLimit() int {
	if p.ContextLimit <= 0 {
		return 128_000
	}
	return p.ContextLimit
}

// CtxSoftLimit — безопасный порог контекста для авто-сжатия (85% от лимита).
func (p *Provider) CtxSoftLimit() int {
	return p.CtxLimit() * 85 / 100
}
