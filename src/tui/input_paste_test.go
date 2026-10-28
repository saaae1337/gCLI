package tui

import (
	"io"
	"testing"
	"time"
)

// Разрыв скобочной вставки между кусками чтения. Вставка длиннее одного
// чтения приходит кусками; пауза между ними — не повод рубить её на Esc:
// раньше форс-таймер (35 мс) выдавал Esc посреди вставки, а её остаток
// печатался в поле ввода посимвольно, включая хвост [201~. Хуже всего,
// что Esc во время хода прерывал текущий запрос.

// collectEvents — прочитать события Reader с общим дедлайном.
func collectEvents(t *testing.T, r *Reader, want int) []Event {
	t.Helper()
	var evs []Event
	deadline := time.After(5 * time.Second)
	for len(evs) < want {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatalf("поток закрылся, события: %#v", evs)
			}
			evs = append(evs, ev)
		case <-deadline:
			t.Fatalf("не дождались событий, есть: %#v", evs)
		}
	}
	return evs
}

// TestReaderSplitPasteSlowChunks — куски вставки с паузой 60 мс (между
// 35 мс форс-таймером старого кода и 250 мс ожиданием вставки).
func TestReaderSplitPasteSlowChunks(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewReader(pr)
	defer r.Close()
	go func() {
		pw.Write([]byte("\x1b[200~привал"))
		time.Sleep(60 * time.Millisecond)
		pw.Write([]byte(" из похода\x1b[201~"))
		pw.Close()
	}()
	evs := collectEvents(t, r, 1)
	if !evs[0].Paste {
		t.Fatalf("ждали одну вставку, получили %#v", evs[0])
	}
	if evs[0].Text != "привал из похода" {
		t.Fatalf("текст вставки = %q", evs[0].Text)
	}
}

// TestReaderPasteStallKeepsPasteOpen — пауза больше ожидания вставки:
// накопленное отдаётся как вставка (не Esc), хвост с закрывающим
// маркером приходит следом второй вставкой.
func TestReaderPasteStallKeepsPasteOpen(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewReader(pr)
	defer r.Close()
	go func() {
		pw.Write([]byte("\x1b[200~часть-1"))
		time.Sleep(400 * time.Millisecond)
		pw.Write([]byte("часть-2\x1b[201~"))
		pw.Close()
	}()
	evs := collectEvents(t, r, 2)
	for i, ev := range evs {
		if !ev.Paste {
			t.Fatalf("событие %d: ждали вставку, получили %#v", i, ev)
		}
	}
	if evs[0].Text != "часть-1" || evs[1].Text != "часть-2" {
		t.Fatalf("тексты: %q, %q", evs[0].Text, evs[1].Text)
	}
}

// TestReaderPasteComplete — вставка целиком в одном чтении: один Paste,
// текст после закрывающего маркера разбирается как обычно.
func TestReaderPasteComplete(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewReader(pr)
	defer r.Close()
	go func() {
		pw.Write([]byte("\x1b[200~раз\r\nдва\x1b[201~ок"))
		pw.Close()
	}()
	evs := collectEvents(t, r, 3)
	if !evs[0].Paste || evs[0].Text != "раз\nдва" {
		t.Fatalf("вставка: %#v", evs[0])
	}
	if evs[1].Key != KeyRune || evs[1].R != 'о' {
		t.Fatalf("после вставки ждали «о», получили %#v", evs[1])
	}
	if evs[2].Key != KeyRune || evs[2].R != 'к' {
		t.Fatalf("потом «к», получили %#v", evs[2])
	}
}
