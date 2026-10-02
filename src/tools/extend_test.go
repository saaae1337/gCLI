package tools

import (
	"strings"
	"testing"
)

// ---------- Тесты инструмента extend_turns ----------
//
// Инструмент — тонкая обёртка над решением агента, но именно в обёртке
// живут две ошибки, которые не видно в тестах самого агента: молчаливое
// «продление выдалось» вместо отказа и потерянное число итераций в
// Summary. Поэтому проверяем и текст модели, и сводку.

// newExtendReg — реестр с подключённым обработчиком продления.
func newExtendReg(t *testing.T, fn func(reason string, n int) (int, string, error)) *Registry {
	t.Helper()
	return New(Env{WorkDir: t.TempDir(), Extend: fn})
}

// TestExtendTurnsPassesReasonAndN — обёртка обязана доставить мотив и
// запрошенное число без искажений.
func TestExtendTurnsPassesReasonAndN(t *testing.T) {
	var gotReason string
	var gotN int
	r := newExtendReg(t, func(reason string, n int) (int, string, error) {
		gotReason, gotN = reason, n
		return 30, "Продлено на 30 итераций", nil
	})

	res := mustRun(t, r, "extend_turns", map[string]any{"reason": "осталось дописать тесты", "n": 30})
	const want = "осталось дописать тесты"
	if gotReason != want {
		t.Errorf("обоснование доставлено как %q, ждали %q", gotReason, want)
	}
	if gotN != 30 {
		t.Errorf("n доставлено как %d, ждали 30", gotN)
	}
	if !strings.Contains(res.Text, "Продлено на 30") {
		t.Errorf("текст для модели потерял решение: %s", res.Text)
	}
	if !strings.Contains(res.Summary, "30") {
		t.Errorf("сводка не содержит число итераций: %q", res.Summary)
	}
}

// TestExtendTurnsOmittedNIsZero — без n обработчик получает 0, и решение о
// размере шага принимает агент, а не обёртка.
func TestExtendTurnsOmittedNIsZero(t *testing.T) {
	var gotN = -1
	r := newExtendReg(t, func(reason string, n int) (int, string, error) {
		gotN = n
		return 30, "ок", nil
	})
	mustRun(t, r, "extend_turns", map[string]any{"reason": "осталось дописать тесты"})
	if gotN != 0 {
		t.Errorf("без n обработчик получил %d, ждали 0 — решение о шаге принимает агент", gotN)
	}
}

// TestExtendTurnsNegativeNIsZero — отрицательное n не должно уходить в агент
// «как есть»: там оно означало бы «хочу минус итераций».
func TestExtendTurnsNegativeNIsZero(t *testing.T) {
	var gotN = -1
	r := newExtendReg(t, func(reason string, n int) (int, string, error) {
		gotN = n
		return 30, "ок", nil
	})
	mustRun(t, r, "extend_turns", map[string]any{"reason": "осталось дописать тесты", "n": -5})
	if gotN != 0 {
		t.Errorf("отрицательное n дошло как %d, ждали 0", gotN)
	}
}

// TestExtendTurnsDenialIsNotError — отказ не должен быть ошибкой вызова.
//
// Модель читает Message и решает дальше: сменить подход или сдать отчёт.
// Если вернуть err, вызов tools посчитается сломанным и модель может
// начать искать обходной путь вместо чтения объяснения.
func TestExtendTurnsDenialIsNotError(t *testing.T) {
	r := newExtendReg(t, func(reason string, n int) (int, string, error) {
		return 0, "Лимит продлений исчерпан: 8 из 8 за этот ход", nil
	})
	res, err := run(t, r, "extend_turns", map[string]any{"reason": "осталось дописать тесты"})
	if err != nil {
		t.Fatalf("отказ не должен быть ошибкой вызова: %v", err)
	}
	if !strings.Contains(res.Text, "исчерпан") {
		t.Errorf("текст модели потерял причину отказа: %s", res.Text)
	}
}

// TestExtendTurnsMissingCallback — окружение без агента должно давать понятную
// ошибку, а не паникy на nil-указателе.
func TestExtendTurnsMissingCallback(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	_, err := run(t, r, "extend_turns", map[string]any{"reason": "осталось дописать тесты"})
	if err == nil {
		t.Fatal("без Env.Extend ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "недоступен") {
		t.Errorf("в ошибке нет понятного объяснения: %v", err)
	}
}

// TestExtendTurnsInSubagentExplainsWhy — у субагента своего лимита нет,
// ошибка должна говорить это прямо: иначе модель ищет несуществующий лимит.
func TestExtendTurnsInSubagentExplainsWhy(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir(), Depth: 1})
	_, err := run(t, r, "extend_turns", map[string]any{"reason": "осталось дописать тесты"})
	if err == nil {
		t.Fatal("у субагента ожидалась ошибка")
	}
	for _, want := range []string{"субагента", "spawn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет упоминания %q: %v", want, err)
		}
	}
}
