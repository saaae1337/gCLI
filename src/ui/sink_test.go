package ui

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// captureSink — приёмник для проверки: запоминает события.
type captureSink struct {
	mu      sync.Mutex
	text    strings.Builder
	began   int
	ended   int
	cleared int
}

func (c *captureSink) Text(s string) {
	c.mu.Lock()
	c.text.WriteString(s)
	c.mu.Unlock()
}
func (c *captureSink) BeginStream() { c.mu.Lock(); c.began++; c.mu.Unlock() }
func (c *captureSink) EndStream()   { c.mu.Lock(); c.ended++; c.mu.Unlock() }
func (c *captureSink) SetMeta(string) {
}
func (c *captureSink) BeginThink() {
}
func (c *captureSink) ThinkText(string) {
}
func (c *captureSink) EndThink() {
}
func (c *captureSink) ToolStart(ToolCall) {
}
func (c *captureSink) ToolEnd(ToolCall) {
}
func (c *captureSink) SetMetrics(Metrics) {
}
func (c *captureSink) Clear() { c.mu.Lock(); c.cleared++; c.mu.Unlock() }

func (c *captureSink) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text.String()
}

// lockedBuf — потокобезопасный писатель для теста гонки.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *lockedBuf) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Len()
}

// TestSinkReceivesOutput — при подключённом приёмнике байты в терминал не
// идут, а структурные события доходят.
func TestSinkReceivesOutput(t *testing.T) {
	var out lockedBuf
	u := New(Options{Width: 80, Out: &out})
	s := &captureSink{}
	u.SetSink(s)

	u.Println("привет")
	if out.Len() != 0 {
		t.Fatalf("при подключённом приёмнике в терминал ушло %q", out.String())
	}
	if got := s.String(); !strings.Contains(got, "привет") {
		t.Fatalf("приёмник получил %q", got)
	}

	st := u.NewStream()
	st.Write("ответ\n")
	st.End()
	if s.began != 1 || s.ended != 1 {
		t.Fatalf("границы ответа: начало %d, конец %d", s.began, s.ended)
	}

	u.ClearScreen()
	if s.cleared != 1 {
		t.Fatalf("Clear вызван %d раз", s.cleared)
	}

	// Отключение возвращает обычный вывод.
	u.SetSink(nil)
	u.Println("снова в терминал")
	if !strings.Contains(out.String(), "снова в терминал") {
		t.Fatalf("после отключения приёмника вывод не вернулся в терминал: %q", out.String())
	}
}

// TestSinkConcurrentSwitch — подключение приёмника не гоняется с печатью.
func TestSinkConcurrentSwitch(t *testing.T) {
	out := &lockedBuf{}
	u := New(Options{Width: 80, Out: out})
	s := &captureSink{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); u.Println("строка") }()
		go func() { defer wg.Done(); u.SetSink(s) }()
		go func() { defer wg.Done(); u.SetSink(nil) }()
	}
	wg.Wait()
}
