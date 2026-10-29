package tui

import (
	"io"
	"testing"
	"time"
)

// ---------- двойное Esc ----------
//
// Два Esc в одном чтении раньше уходили в ветку «Alt+руна»: 0x1b —
// валидная руна, оба нажатия съедал один Alt-событие, который обработчики
// молча бросали. Прерывание хода и закрытие модалки по Esc не срабатывали,
// пока нажатия не разносил форс-таймер (35 мс).

// TestParseKeyDoubleEscapeIsEsc — разбор на уровне буфера: первый байт
// отдаётся как Esc, второй остаётся на следующую итерацию.
func TestParseKeyDoubleEscapeIsEsc(t *testing.T) {
	ev, n, need := parseKey([]byte("\x1b\x1b"), false)
	if need || n != 1 || ev.Key != KeyEsc {
		t.Fatalf("parseKey(ESC ESC) = ev=%+v n=%d need=%v; хочу KeyEsc, n=1, need=false", ev, n, need)
	}
}

// TestReaderDoubleEscapeDeliversTwoEsc — сквозной путь через Reader:
// оба нажатия доходят до приложения как KeyEsc.
func TestReaderDoubleEscapeDeliversTwoEsc(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewReader(pr)
	defer r.Close()
	go func() {
		pw.Write([]byte("\x1b\x1b"))
		pw.Close()
	}()
	for i := 0; i < 2; i++ {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatalf("поток закрылся на событии %d", i+1)
			}
			if ev.Key != KeyEsc {
				t.Fatalf("событие %d: ждали KeyEsc, получили %#v", i+1, ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("событие %d не пришло: двойное Esc глотается", i+1)
		}
	}
}

// TestParseKeyEscapeSequencesUnchanged — соседние последовательности не
// задеты: стрелка, Alt+Enter, одиночный Esc по форсу.
func TestParseKeyEscapeSequencesUnchanged(t *testing.T) {
	if ev, n, need := parseKey([]byte("\x1b[A"), false); need || n != 3 || ev.Key != KeyUp {
		t.Fatalf("ESC[A = ev=%+v n=%d need=%v; хочу KeyUp, n=3", ev, n, need)
	}
	if ev, n, need := parseKey([]byte("\x1b\r"), false); need || n != 2 || ev.Key != KeyEnter || !ev.Alt {
		t.Fatalf("ESC CR = ev=%+v n=%d need=%v; хочу Alt+Enter, n=2", ev, n, need)
	}
	if ev, n, need := parseKey([]byte("\x1b"), true); need || n != 1 || ev.Key != KeyEsc {
		t.Fatalf("форс ESC = ev=%+v n=%d need=%v; хочу KeyEsc, n=1", ev, n, need)
	}
}
