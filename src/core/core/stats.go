package core

import (
	"fmt"
	"math"
	"time"
)

// ---------- Статистика сессии ----------

// Stats — счётчики сессии: запросы, токены, инструменты, ошибки
// и тайминги. Живут в Session.Stats, поэтому переживают сохранение
// и загрузку сессии. Их показывают /status и /usage.
type Stats struct {
	// TokensIn / TokensOut — суммарные токены входа и выхода.
	TokensIn  int `json:"tokens_in"`
	TokensOut int `json:"tokens_out"`

	// Requests — сколько раз обращались к модели (включая итерации агента).
	Requests int `json:"requests"`
	// Errors — запросы, завершившиеся ошибкой.
	Errors int `json:"errors"`
	// Tools / ToolsFailed — вызовы инструментов и неуспешные из них.
	Tools       int `json:"tools"`
	ToolsFailed int `json:"tools_failed"`

	// Last* — последний запрос: удобно для «запрос сейчас занял 4.2с».
	LastPrompt     int           `json:"last_prompt"`
	LastCompletion int           `json:"last_completion"`
	LastDuration   time.Duration `json:"last_duration_ns"`
	LastAt         time.Time     `json:"last_at,omitempty"`

	// TotalDuration — суммарное время всех запросов к модели.
	TotalDuration time.Duration `json:"total_duration_ns"`
	// FirstAt — когда был первый запрос (для расчёта длительности сессии).
	FirstAt time.Time `json:"first_at,omitempty"`
}

// RecordRequest — учесть завершённый запрос к модели.
func (s *Stats) RecordRequest(u Usage, d time.Duration) {
	s.Requests++
	s.Add(u)
	s.LastPrompt = u.PromptTokens
	s.LastCompletion = u.CompletionTokens
	s.LastDuration = d
	s.LastAt = time.Now()
	s.TotalDuration += d
	if s.FirstAt.IsZero() {
		s.FirstAt = s.LastAt
	}
}

// Add — прибавить потребление токенов (без увеличения счётчика запросов).
func (s *Stats) Add(u Usage) {
	s.TokensIn += u.PromptTokens
	s.TokensOut += u.CompletionTokens
}

// TokensTotal — все токены сессии.
func (s Stats) TokensTotal() int { return s.TokensIn + s.TokensOut }

// RecordError — учесть неудачный запрос (токены при этом не добавляются).
func (s *Stats) RecordError() { s.Errors++ }

// RecordTool — учесть вызов инструмента.
func (s *Stats) RecordTool(failed bool) {
	s.Tools++
	if failed {
		s.ToolsFailed++
	}
}

// Speed — скорость генерации в токенах/сек по последнему запросу.
func (s Stats) Speed() float64 {
	if s.LastDuration <= 0 || s.LastCompletion <= 0 {
		return 0
	}
	return float64(s.LastCompletion) / s.LastDuration.Seconds()
}

// AvgDuration — среднее время одного запроса.
func (s Stats) AvgDuration() time.Duration {
	if s.Requests <= 0 {
		return 0
	}
	return time.Duration(int64(s.TotalDuration) / int64(s.Requests))
}

// SessionTime — сколько длится работа с моделью в этой сессии.
func (s Stats) SessionTime() time.Duration {
	if s.FirstAt.IsZero() {
		return 0
	}
	return time.Since(s.FirstAt)
}

// SuccessRate — доля успешных запросов в процентах (0 = нет данных).
func (s Stats) SuccessRate() int {
	if s.Requests <= 0 {
		return 0
	}
	return int(math.Round(float64(s.Requests-s.Errors) / float64(s.Requests) * 100))
}

// ---------- Оценка стоимости ----------

// Price — цена в долларах за миллион токенов (ввод/вывод).
type Price struct {
	In  float64
	Out float64
}

// Known — цена задана хотя бы для одной стороны.
func (p Price) Known() bool { return p.In > 0 || p.Out > 0 }

// Cost — стоимость в долларах по заданным ценам.
func (p Price) Cost(u Usage) float64 {
	if !p.Known() {
		return 0
	}
	return float64(u.PromptTokens)/1e6*p.In +
		float64(u.CompletionTokens)/1e6*p.Out
}

// FormatUSD — человекочитаемая цена: $0.0123, $1.20, $12.00.
func FormatUSD(v float64) string {
	switch {
	case v <= 0:
		return "$0"
	case v < 0.01:
		return fmt.Sprintf("$%.4f", v)
	case v < 1:
		return fmt.Sprintf("$%.3f", v)
	default:
		return fmt.Sprintf("$%.2f", v)
	}
}

// ---------- Форматирование метрик ----------

// Kfmt64 — компактное число для дробных величин (например токены/сек):
// 0.0 → "0", 1200 → "1.2k", 1500000 → "1.5M".
func Kfmt64(f float64) string {
	switch {
	case f < 0:
		return "-" + Kfmt64(-f)
	case f < 1000:
		return fmt.Sprintf("%.0f", f)
	case f < 10_000:
		return fmt.Sprintf("%.1fk", f/1000)
	case f < 1_000_000:
		return fmt.Sprintf("%.0fk", f/1000)
	case f < 10_000_000:
		return fmt.Sprintf("%.1fM", f/1e6)
	default:
		return fmt.Sprintf("%.0fM", f/1e6)
	}
}

// Pct — процент part от total, ограниченный диапазоном 0..100.
func Pct(part, total int) int {
	if total <= 0 {
		return 0
	}
	v := part * 100 / total
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
