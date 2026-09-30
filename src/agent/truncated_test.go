package agent

import (
	"errors"
	"fmt"
	"testing"

	"gcli/providers"
)

// Тесты на починку «Provider returned an empty response»: модель с thinking
// тратит весь max_tokens на размышления и не отдаёт ни слова ответа.
// Провайдер в этом случае присылает пустой content и finish_reason=length.

// finish reason «оборвалось по длине» распознаётся у обоих протоколов.
func TestIsTruncatedFinish(t *testing.T) {
	truncated := []string{"length", "max_tokens", "LENGTH", " max_tokens "}
	for _, f := range truncated {
		if !isTruncatedFinish(f) {
			t.Errorf("finish_reason %q должен считаться обрезанием по длине", f)
		}
	}
	// Остальные причины — не повод увеличивать бюджет и повторять ход.
	normal := []string{"", "stop", "tool_calls", "end_turn", "content_filter", "ошибка"}
	for _, f := range normal {
		if isTruncatedFinish(f) {
			t.Errorf("finish_reason %q не должен считаться обрезанием", f)
		}
	}
}

// Бюджет растёт, но не бесконечно: иначе модель с неограниченным thinking
// утащит ход в вечность.
func TestGrowTokens(t *testing.T) {
	if got := growTokens(defaultMaxTokens, 1); got != thinkMaxTokens {
		t.Errorf("первый повтор: %d, ожидалось %d", got, thinkMaxTokens)
	}
	if got := growTokens(thinkMaxTokens, 2); got != retryMaxTokens {
		t.Errorf("второй повтор: %d, ожидалось %d", got, retryMaxTokens)
	}
	// Потолок жёсткий: сколько ни повторяй, дальше retryMaxTokens не уйти.
	for _, attempt := range []int{1, 2, 3, 10} {
		if got := growTokens(retryMaxTokens, attempt); got > retryMaxTokens {
			t.Errorf("потолок превышен: %d > %d", got, retryMaxTokens)
		}
	}
	// Бюджет никогда не должен уменьшаться.
	for _, in := range []int{0, 1024, defaultMaxTokens, thinkMaxTokens, retryMaxTokens} {
		got := growTokens(in, 1)
		if got < in {
			t.Errorf("growTokens(%d) = %d — бюджет уменьшился", in, got)
		}
		if next := growTokens(got, 2); next < got {
			t.Errorf("второй повтор от %d дал %d — бюджет уменьшился", got, next)
		}
	}
}

// Модели с размышлениями нужен больший потолок сразу: размышления и ответ
// делят один бюджет, и на 8k ответа не остаётся.
func TestMaxTokensDependsOnThink(t *testing.T) {
	off := New(Deps{Registry: nil, Think: "off"}, ".")
	if got := off.maxTokens(); got != defaultMaxTokens {
		t.Errorf("без размышлений потолок %d, ожидалось %d", got, defaultMaxTokens)
	}
	for _, think := range []string{"on", "auto"} {
		a := New(Deps{Registry: nil, Think: think}, ".")
		if got := a.maxTokens(); got != thinkMaxTokens {
			t.Errorf("think=%s: потолок %d, ожидалось %d", think, got, thinkMaxTokens)
		}
	}
	// Потолок должен быть реальным, а не декоративным: восемь тысяч на
	// размышления — ровно тот бюджет, из-за которого был этот баг.
	if thinkMaxTokens <= defaultMaxTokens*2 {
		t.Errorf("потолок %d слишком близок к базовому %d", thinkMaxTokens, defaultMaxTokens)
	}
}

// Агент повторяет ход с увеличенным бюджетом именно по этой метке, поэтому
// ошибка, обёрнутая вокруг неё, обязана опознаваться через errors.Is.
func TestEmptyResponseSentinelIsDetectable(t *testing.T) {
	err := fmt.Errorf("%w (провайдер вернул пустой ответ): %w",
		providers.ErrEmptyResponse, errors.New("API: Provider returned an empty response"))
	if !errors.Is(err, providers.ErrEmptyResponse) {
		t.Error("пустой ответ должен опознаваться по метке")
	}
	// Прочие ошибки повтором не лечатся: увеличение бюджета им не поможет.
	other := fmt.Errorf("API: %w", errors.New("rate limit exceeded"))
	if errors.Is(other, providers.ErrEmptyResponse) {
		t.Error("ошибка лимита запросов не должна считаться пустым ответом")
	}
}
