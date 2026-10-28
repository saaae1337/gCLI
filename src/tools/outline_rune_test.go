package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Регрессия: outlineHeuristic резал длинную строку по байту 400 —
// срез попадал внутрь руны, и битая строка шла и в регэксп, и в
// сигнатуру, отправляемую агенту (невалидный UTF-8).

func TestOutlineHeuristicCutRuneSafe(t *testing.T) {
	// «def f(): » (9 байт) + добивка до 399 + кириллица: первая руна
	// начинается ровно на байте 399, старый срез line[:400] забирал
	// её первый байт и оставлял строку битой.
	line := "def f(): " + strings.Repeat("x", 390) + strings.Repeat("ы", 30)
	if len(line) <= 400 {
		t.Fatalf("тестовая строка короче предела: %d", len(line))
	}
	out := outlineHeuristic(".py", []string{line})
	if len(out) != 1 {
		t.Fatalf("объявление не найдено: %d", len(out))
	}
	if !utf8.ValidString(out[0].sig) {
		t.Fatalf("невалидный UTF-8 в сигнатуре: %q", out[0].sig)
	}
	if !strings.HasPrefix(out[0].sig, "def f():") {
		t.Fatalf("сигнатура потеряла начало: %q", out[0].sig)
	}
	if out[0].line != 1 {
		t.Fatalf("номер строки %d, ожидался 1", out[0].line)
	}
}

func TestOutlineHeuristicShortLineUntouched(t *testing.T) {
	out := outlineHeuristic(".py", []string{"def short(): pass"})
	if len(out) != 1 {
		t.Fatalf("объявление не найдено: %d", len(out))
	}
	if out[0].sig != "def short(): pass" {
		t.Fatalf("короткая строка искажена: %q", out[0].sig)
	}
}
