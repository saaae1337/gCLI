package core

import (
	"strings"
	"sync"
	"testing"
)

// TestCaptureShortOutput — вывод меньше окна приезжает целиком.
func TestCaptureShortOutput(t *testing.T) {
	c := NewCapture(1024)
	if _, err := c.Write([]byte("привет, мир")); err != nil {
		t.Fatal(err)
	}
	if got := c.String(); got != "привет, мир" {
		t.Fatalf("короткий вывод искажён: %q", got)
	}
	if c.Total() != int64(len("привет, мир")) {
		t.Fatalf("Total: %d, ожидалось %d", c.Total(), len("привет, мир"))
	}
}

// TestCaptureHeadAndTail — длинный вывод сжимается до головы и хвоста.
func TestCaptureHeadAndTail(t *testing.T) {
	c := NewCapture(512)
	var full strings.Builder
	full.WriteString(strings.Repeat("A", 500)) // голова
	full.WriteString(strings.Repeat("B", 2000))
	full.WriteString(strings.Repeat("C", 500)) // хвост
	if _, err := c.Write([]byte(full.String())); err != nil {
		t.Fatal(err)
	}
	out := c.String()
	if !strings.Contains(out, strings.Repeat("A", 50)) {
		t.Errorf("в собранном нет начала вывода")
	}
	if !strings.Contains(out, strings.Repeat("C", 50)) {
		t.Errorf("в собранном нет конца вывода")
	}
	if strings.Contains(out, strings.Repeat("B", 100)) {
		t.Errorf("середина не вырезана")
	}
	if !strings.Contains(out, "пропущено") {
		t.Errorf("нет метки о пропущенном: %q", out)
	}
	// Память: окно ограничено, даже если вывод огромен (256 — внутренний
	// минимум NewCapture).
	if len(c.ring) > 512 || len(c.head) > 512 {
		t.Fatalf("буферы выросли за пределы лимита: head=%d ring=%d", len(c.head), len(c.ring))
	}
}

// TestCaptureRuneEdges — многобайтовая руна, разрезанная окном, не даёт
// знак замены в собранном тексте.
func TestCaptureRuneEdges(t *testing.T) {
	c := NewCapture(8)
	// «жж» — 4 байта на символ; окно 8 байт режет ровно посередине.
	if _, err := c.Write([]byte(strings.Repeat("ж", 10))); err != nil {
		t.Fatal(err)
	}
	out := c.String()
	if strings.ContainsRune(out, '\uFFFD') {
		t.Fatalf("знак замены попал в вывод: %q", out)
	}
}

// TestCaptureConcurrent — писатель из двух горутин (Stdout+Stderr) не гоняет.
func TestCaptureConcurrent(t *testing.T) {
	c := NewCapture(4096)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = c.Write([]byte("строка данных для гонки\n"))
			}
		}(i)
	}
	wg.Wait()
	if c.Total() != int64(8*50*len("строка данных для гонки\n")) {
		t.Fatalf("Total не сходится: %d", c.Total())
	}
	if strings.Contains(c.String(), "\uFFFD") {
		t.Fatalf("знак замены в выводе")
	}
}
