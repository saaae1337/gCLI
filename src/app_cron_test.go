package main

import (
	"strings"
	"testing"
	"time"
)

// TestCronNextInterval — интервал между итерациями: 15–20 минут.
func TestCronNextInterval(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := cronNextInterval()
		if d < 15*time.Minute || d > 20*time.Minute {
			t.Fatalf("интервал %v вне диапазона 15–20 мин", d)
		}
	}
}

// TestCronUniversalPrompt — промпт не пустой и содержит ключевые разделы.
func TestCronUniversalPrompt(t *testing.T) {
	p := cronUniversalPrompt()
	if p == "" {
		t.Fatal("пустой промпт")
	}
	for _, want := range []string{"баги", "уязвимости", "проект", "отчёт"} {
		if !strings.Contains(p, want) {
			t.Errorf("промпт не содержит раздел %q", want)
		}
	}
}
