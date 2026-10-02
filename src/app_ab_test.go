package main

// Тесты разбора аргументов /ab. Чистая функция: сеть не зовётся.

import "testing"

func TestParseAbArgs(t *testing.T) {
	model, prompt, err := parseAbArgs("glm-4.5-air расскажи, чем хорош Go")
	if err != nil {
		t.Fatal(err)
	}
	if model != "glm-4.5-air" || prompt != "расскажи, чем хорош Go" {
		t.Fatalf("model=%q prompt=%q", model, prompt)
	}
	if _, _, err := parseAbArgs(""); err == nil {
		t.Fatal("пустые аргументы должны быть ошибкой")
	}
	if _, _, err := parseAbArgs("glm-4.5-air"); err == nil {
		t.Fatal("модель без промпта должна быть ошибкой")
	}
	// Многословный промпт после модели.
	m, p, err := parseAbArgs("  gpt-4o   два   слова  ")
	if err != nil || m != "gpt-4o" || p != "два   слова" {
		t.Fatalf("m=%q p=%q err=%v", m, p, err)
	}
}
