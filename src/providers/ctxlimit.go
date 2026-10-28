package providers

import (
	"os"
	"strconv"
	"strings"
)

// ---------- Лимиты контекста по моделям ----------
//
// До 0.9.0 лимит контекста был атрибутом провайдера, а не модели:
// у Z.ai всем моделям писалось 200k, у OpenRouter — 128k. Панель
// показывала «100.000» даже для моделей с окном в миллион токенов,
// и авто-сжатие срабатывало на четверти реального контекста.
//
// Таблица ниже — источник истины по популярным моделям. Совпадение —
// по точному имени или по префиксу (у семейств вроде gemini-2.5-* окно
// одно). Порядок перебора важен: сначала точные, потом префиксы от
// длинных к коротким, иначе «glm-4.5-air» поймает правило «glm-4.5».

type ctxRule struct {
	model string // точное имя или префикс
	limit int
}

var ctxExact = map[string]int{
	// Z.ai GLM
	"glm-4.6":       200_000,
	"glm-4.5":       128_000,
	"glm-4.5-air":   128_000,
	"glm-4.5-flash": 128_000,
	"glm-4.5v":      64_000,
	// Anthropic
	"claude-sonnet-4-5":        200_000,
	"claude-sonnet-4-20250514": 200_000,
	"claude-opus-4-1":          200_000,
	"claude-opus-4":            200_000,
	"claude-3-7-sonnet-latest": 200_000,
	"claude-3-5-haiku-latest":  200_000,
	"claude-3-5-sonnet-latest": 200_000,
	// OpenAI
	"gpt-4o":       128_000,
	"gpt-4o-mini":  128_000,
	"gpt-4.1":      1_000_000,
	"gpt-4.1-mini": 1_000_000,
	"gpt-4.1-nano": 1_000_000,
	"o3":           200_000,
	"o4-mini":      200_000,
	"gpt-5":        400_000,
	"gpt-5-mini":   400_000,
	"codex-mini":   200_000, // приблизительно
	// Google
	"gemini-2.0-flash-001":  1_000_000,
	"gemini-2.0-flash":      1_000_000,
	"gemini-2.5-pro":        1_048_576,
	"gemini-2.5-flash":      1_048_576,
	"gemini-2.5-flash-lite": 1_048_576,
	"gemini-3-pro":          1_048_576,
	// DeepSeek
	"deepseek-chat":     128_000,
	"deepseek-reasoner": 128_000,
	// Qwen
	"qwen3-coder":      256_000,
	"qwen/qwen3-coder": 256_000,
	"qwen3-max":        256_000,
	"qwen3-235b-a22b":  256_000,
	// xAI
	"grok-4":      256_000,
	"grok-4-fast": 2_000_000,
	"grok-3":      131_072,
	"grok-3-mini": 131_072,
	// Moonshot
	"kimi-k2-0905-preview":  256_000,
	"kimi-k2-turbo-preview": 256_000,
	"kimi-k2":               128_000,
	// Meta / другие популярные на роутерах
	"meta-llama/llama-3.3-70b-instruct": 128_000,
	"meta-llama/llama-4-maverick":       1_000_000,
	"meta-llama/llama-4-scout":          328_000,
	"mistral-large-latest":              128_000,
	"mistral-medium-latest":             128_000,
	"codestral-latest":                  256_000,
	"minimax/minimax-m2":                200_000,
	"moonshotai/kimi-k2-thinking":       256_000,
	"z-ai/glm-4.6":                      200_000,
	"anthropic/claude-sonnet-4.5":       200_000,
	"openai/gpt-oss-120b":               131_000,
	"openai/gpt-oss-20b":                131_000,
}

// ctxPrefix — префиксные правила: семейство моделей с общим окном.
var ctxPrefix = []ctxRule{
	{"claude-sonnet-4", 200_000},
	{"claude-opus-4", 200_000},
	{"claude-haiku-4", 200_000},
	{"claude-3", 200_000},
	{"gemini-2", 1_000_000},
	{"gemini-3", 1_048_576},
	{"gpt-4.1", 1_000_000},
	{"glm-4.6", 200_000},
	{"glm-4.5", 128_000},
	{"deepseek", 128_000},
	{"qwen3", 256_000},
	{"qwen/qwen3", 256_000},
	{"grok-4", 256_000},
	{"kimi-k2", 256_000},
	{"llama-3", 128_000},
	{"mistral", 128_000},
	{"o3-", 200_000},
	{"o4-mini", 200_000},
	{"gpt-oss", 131_000},
	{"minimax-m2", 200_000},
}

// ModelCtxLimit — реальное окно контекста модели.
//
// Возвращает false, если модели нет в таблице: тогда вызывающий
// откатывается к лимиту провайдера или к безопасному дефолту.
// Сравнение без регистра и без мусора вокруг: модели приходят и как
// «glm-4.6», и как «z-ai/glm-4.6».
func ModelCtxLimit(model string) (int, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	// Владелец-префикс роутера («z-ai/glm-4.6») не влияет на окно.
	if i := strings.LastIndex(m, "/"); i >= 0 {
		if v, ok := ctxExactLookup(m); ok {
			return v, true
		}
		m = m[i+1:]
	}
	if v, ok := ctxExactLookup(m); ok {
		return v, true
	}
	// Суффиксы дат и снапшотов: «gemini-2.5-pro-06-05» — тот же pro.
	base := m
	for _, sep := range []string{"-20", ":latest", ":free"} {
		if i := strings.Index(base, sep); i > 0 {
			base = base[:i]
		}
	}
	if base != m {
		if v, ok := ctxExactLookup(base); ok {
			return v, true
		}
	}
	for _, r := range ctxPrefix {
		if strings.HasPrefix(m, r.model) || strings.HasPrefix(base, r.model) {
			return r.limit, true
		}
	}
	return 0, false
}

func ctxExactLookup(m string) (int, bool) {
	if v, ok := ctxExact[m]; ok {
		return v, true
	}
	// Ключи таблицы в нижнем регистре без пробелов; модели иногда
	// приходят с заглавными («GLM-4.6»).
	if v, ok := ctxExact[strings.ToLower(strings.TrimSpace(m))]; ok {
		return v, true
	}
	return 0, false
}

// envCtxLimit — ручной перебор лимита через GCLI_CONTEXT_LIMIT.
func envCtxLimit() int {
	if v := strings.TrimSpace(os.Getenv("GCLI_CONTEXT_LIMIT")); v != "" {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "k")); err == nil {
			if n > 0 && n < 100_000_000 {
				if strings.HasSuffix(v, "k") {
					n *= 1000
				}
				return n
			}
		}
	}
	return 0
}

// CtxLimitFor — лимит контекста конкретной модели: таблица моделей,
// затем ручной GCLI_CONTEXT_LIMIT, затем лимит провайдера.
func (p *Provider) CtxLimitFor(model string) int {
	if n := envCtxLimit(); n > 0 {
		return n
	}
	if n, ok := ModelCtxLimit(model); ok {
		return n
	}
	return p.CtxLimit()
}

// CtxSoftLimitFor — порог авто-сжатия для конкретной модели.
func (p *Provider) CtxSoftLimitFor(model string) int {
	return p.CtxLimitFor(model) * 85 / 100
}
