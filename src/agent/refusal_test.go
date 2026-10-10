package agent

import (
	"errors"
	"strings"
	"testing"
)

// ---------- ход без ответа ----------

// TestTurnProblemExplainsRefusal — провайдер, который не отдал ответ,
// обязан сказать об этом.
//
// OpenAI отвечает finish_reason=content_filter, Anthropic — refusal. В
// обоих случаях тело ответа пустое, а ошибки HTTP нет: запрос успешен,
// просто ответ withheld. Раньше агент принимал это за обычное «модель
// закончила», цикл закрывался, и человек видел одно приглашение
// вводить — как будто агент ничего не делал и ничего не сломал.
//
// Тишина здесь хуже отказа: отказ хотя бы можно исправить.
func TestTurnProblemExplainsRefusal(t *testing.T) {
	cases := []struct {
		name   string
		finish string
		want   string
	}{
		{"content_filter", "content_filter", "content_filter"},
		{"refusal", "refusal", "refusal"},
		{"в верхнем регистре", "CONTENT_FILTER", "content_filter"},
		{"с пробелами", " content_filter ", "content_filter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := turnProblem(c.finish, "", 0, "")
			if err == nil {
				t.Fatalf("finish_reason %q не объяснён: ход закончится тишиной", c.finish)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("в объяснении нет самого finish_reason %q: %v", c.want, err)
			}
			if !errors.Is(err, ErrRefused) {
				t.Errorf("ошибка не помечена ErrRefused: %v", err)
			}
		})
	}
}

// TestTurnProblemSilentTurn — ход без текста, вызовов и размышлений при
// отсутствии объяснения тоже молчать не должен.
func TestTurnProblemSilentTurn(t *testing.T) {
	err := turnProblem("stop", "", 0, "")
	if err == nil {
		t.Fatal("пустой ход без объяснения не замечен")
	}
	if !errors.Is(err, ErrEmptyTurn) {
		t.Errorf("ожидалась метка ErrEmptyTurn, получено: %v", err)
	}
}

// TestTurnProblemQuietWhenThereIsContent — нормальный ход ошибкой не
// заканчивается. Иначе агент ругался бы на каждый второй ответ.
func TestTurnProblemQuietWhenThereIsContent(t *testing.T) {
	cases := []struct {
		name                    string
		finish, text, reasoning string
		tools                   int
	}{
		{"есть-текст", "stop", "вот ответ", "", 0},
		{"есть-вызовы", "tool_calls", "", "", 2},
		{"есть-размышления", "length", "", "размышляю", 0},
		{"обычный-финиш", "", "привет", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := turnProblem(c.finish, c.text, c.tools, c.reasoning); err != nil {
				t.Errorf("нормальный ход объявлен проблемой: %v", err)
			}
		})
	}
}

// TestTurnProblemRefusalWinsOverContent — если провайдер отфильтровал
// ответ, но что-то всё же прислал, отказ важнее: иначе агент покажет
// обрывок и промолчит о причине.
func TestTurnProblemRefusalWinsOverContent(t *testing.T) {
	err := turnProblem("content_filter", "часть текста", 0, "")
	if err == nil {
		t.Fatal("отказ провайдера потерялся из-за пришедшего куска текста")
	}
	if !errors.Is(err, ErrRefused) {
		t.Errorf("ожидалась метка ErrRefused: %v", err)
	}
}
