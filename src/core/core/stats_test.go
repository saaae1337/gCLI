package core

import (
	"testing"
	"time"
)

func TestStatsRecordRequest(t *testing.T) {
	var s Stats
	s.RecordRequest(Usage{PromptTokens: 100, CompletionTokens: 50}, 2*time.Second)

	if s.Requests != 1 {
		t.Errorf("запросов: %d, ожидался 1", s.Requests)
	}
	if s.TokensIn != 100 || s.TokensOut != 50 {
		t.Errorf("токены: in=%d out=%d, ожидалось 100/50", s.TokensIn, s.TokensOut)
	}
	if s.TokensTotal() != 150 {
		t.Errorf("всего токенов: %d, ожидалось 150", s.TokensTotal())
	}
	if s.LastDuration != 2*time.Second {
		t.Errorf("длительность: %v, ожидалось 2s", s.LastDuration)
	}
	// 50 токенов за 2 секунды = 25 ток/с.
	if got := s.Speed(); got < 24.9 || got > 25.1 {
		t.Errorf("скорость: %.1f ток/с, ожидалось ~25", got)
	}
	if s.AvgDuration() != 2*time.Second {
		t.Errorf("среднее: %v, ожидалось 2s", s.AvgDuration())
	}
	if s.SuccessRate() != 100 {
		t.Errorf("успешность: %d%%, ожидалось 100", s.SuccessRate())
	}
}

func TestStatsMultipleRequests(t *testing.T) {
	var s Stats
	s.RecordRequest(Usage{PromptTokens: 10, CompletionTokens: 5}, 1*time.Second)
	s.RecordRequest(Usage{PromptTokens: 20, CompletionTokens: 8}, 3*time.Second)
	s.RecordError()

	if s.Requests != 2 {
		t.Errorf("запросов: %d, ожидалось 2", s.Requests)
	}
	if s.TokensIn != 30 || s.TokensOut != 13 {
		t.Errorf("токены: in=%d out=%d, ожидалось 30/13", s.TokensIn, s.TokensOut)
	}
	if s.Errors != 1 {
		t.Errorf("ошибок: %d, ожидалась 1", s.Errors)
	}
	// 1 из 2 запросов провалился.
	if got := s.SuccessRate(); got != 50 {
		t.Errorf("успешность: %d%%, ожидалось 50", got)
	}
	if got := s.AvgDuration(); got != 2*time.Second {
		t.Errorf("среднее: %v, ожидалось 2s", got)
	}
	// Последний запрос — 8 токенов за 3с.
	if got := s.Speed(); got < 2.66 || got > 2.67 {
		t.Errorf("скорость: %.3f ток/с, ожидалось ~2.67", got)
	}
}

func TestStatsEmpty(t *testing.T) {
	var s Stats
	if s.Speed() != 0 {
		t.Errorf("скорость без данных должна быть 0, получено %v", s.Speed())
	}
	if s.AvgDuration() != 0 {
		t.Errorf("среднее без данных должно быть 0, получено %v", s.AvgDuration())
	}
	if s.SuccessRate() != 0 {
		t.Errorf("успешность без данных должна быть 0, получено %d", s.SuccessRate())
	}
}

func TestStatsRecordTool(t *testing.T) {
	var s Stats
	s.RecordTool(false)
	s.RecordTool(false)
	s.RecordTool(true)

	if s.Tools != 3 {
		t.Errorf("инструментов: %d, ожидалось 3", s.Tools)
	}
	if s.ToolsFailed != 1 {
		t.Errorf("ошибок инструментов: %d, ожидалась 1", s.ToolsFailed)
	}
}

func TestPriceCost(t *testing.T) {
	p := Price{In: 3.00, Out: 15.00}
	// 1M входных + 1M выходных = 3 + 15 = 18 долларов.
	if got := p.Cost(Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}); got != 18 {
		t.Errorf("стоимость: %v, ожидалось 18", got)
	}
	// 1000 входных + 2000 выходных = 0.003 + 0.03 = 0.033.
	if got := p.Cost(Usage{PromptTokens: 1000, CompletionTokens: 2000}); got < 0.0329 || got > 0.0331 {
		t.Errorf("стоимость: %v, ожидалось ~0.033", got)
	}
	if !p.Known() {
		t.Error("цена должна считаться известной")
	}
	if (Price{}).Known() {
		t.Error("пустая цена не должна считаться известной")
	}
	if (Price{}).Cost(Usage{PromptTokens: 1000}) != 0 {
		t.Error("без цены стоимость должна быть нулевой")
	}
}

func TestFormatUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0"},
		{0.0004, "$0.0004"},
		{0.05, "$0.050"},
		{0.5, "$0.500"},
		{1.239, "$1.24"},
		{18, "$18.00"},
	}
	for _, c := range cases {
		if got := FormatUSD(c.in); got != c.want {
			t.Errorf("FormatUSD(%v) = %q, ожидалось %q", c.in, got, c.want)
		}
	}
}

func TestModelPrice(t *testing.T) {
	// Точное совпадение.
	if _, ok := ModelPrice("zai", "glm-4.6"); !ok {
		t.Error("glm-4.6 у zai: цена должна быть известна")
	}
	// Совпадение по вхождению: у OpenRouter модель со слешем.
	p, ok := ModelPrice("openrouter", "z-ai/glm-4.6")
	if !ok {
		t.Fatal("z-ai/glm-4.6 у openrouter: цена должна находиться по вхождению")
	}
	if p.In <= 0 {
		t.Errorf("цена входа: %v, ожидалось > 0", p.In)
	}
	// Регистр не важен.
	if _, ok := ModelPrice("OpenAI", "GPT-4O"); !ok {
		t.Error("регистр не должен влиять на поиск цены")
	}
	// Неизвестная модель.
	if _, ok := ModelPrice("zai", "какая-то-выдуманная-модель"); ok {
		t.Error("для неизвестной модели цена не должна выдумываться")
	}
	// Неизвестный провайдер.
	if _, ok := ModelPrice("какой-то-провайдер", "glm-4.6"); ok {
		t.Error("для неизвестного провайдера цена не должна выдумываться")
	}
}

func TestPct(t *testing.T) {
	cases := []struct{ part, total, want int }{
		{0, 0, 0},
		{1, 4, 25},
		{1, 3, 33},
		{5, 5, 100},
		{9, 5, 100}, // больше 100% — обрезаем
		{-1, 5, 0},  // отрицательное — обрезаем
	}
	for _, c := range cases {
		if got := Pct(c.part, c.total); got != c.want {
			t.Errorf("Pct(%d,%d) = %d, ожидалось %d", c.part, c.total, got, c.want)
		}
	}
}

func TestKfmt64(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{15000, "15k"},
		{150000, "150k"},
		{1500000, "1.5M"},
		{-5, "-5"},
	}
	for _, c := range cases {
		if got := Kfmt64(c.in); got != c.want {
			t.Errorf("Kfmt64(%v) = %q, ожидалось %q", c.in, got, c.want)
		}
	}
}
