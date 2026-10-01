package providers

import (
	"errors"
	"strings"
	"testing"
)

// Ошибка «Provider returned an empty response» приходит от провайдера
// дословно и без диагностики: голое «API: …» пользователю ничего не
// объясняет. Проверяем, что обёртка добавляет и метку, и подсказку.

// Пустой ответ помечается меткой: по ней агент решает повторить ход с
// другим бюджетом токенов.
func TestApiErrorEmptyResponseIsMarked(t *testing.T) {
	err := apiError("Provider returned an empty response")
	if !errors.Is(err, ErrEmptyResponse) {
		t.Errorf("пустой ответ должен помечаться ErrEmptyResponse: %v", err)
	}
}

// Подсказка объясняет причину и говорит, что делать, но не выдаёт текст
// размышлений модели: тот приватен и в ошибке не нужен.
func TestApiErrorEmptyResponseExplains(t *testing.T) {
	err := apiError("Provider returned an empty response").Error()
	if !strings.Contains(err, "/think off") {
		t.Errorf("в ошибке нет подсказки про /think off: %s", err)
	}
	if !strings.Contains(err, "max_tokens") {
		t.Errorf("в ошибке нет упоминания max_tokens — без него причина непонятна: %s", err)
	}
	if !strings.Contains(err, "Provider returned an empty response") {
		t.Errorf("исходное сообщение провайдера должно сохраняться: %s", err)
	}
}

// Остальные ошибки остаются как есть: приклеивать к ним подсказку про
// размышления бессмысленно — она увеличит шум у настоящих поломок.
func TestApiErrorOtherStaysPlain(t *testing.T) {
	err := apiError("rate limit exceeded")
	if errors.Is(err, ErrEmptyResponse) {
		t.Errorf("ошибка лимита не должна считаться пустым ответом: %v", err)
	}
	if got := err.Error(); got != "API: rate limit exceeded" {
		t.Errorf("сообщение изменилось: %q", got)
	}
}
