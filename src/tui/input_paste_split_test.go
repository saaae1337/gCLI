package tui

import (
	"io"
	"strings"
	"testing"
	"time"
)

// ---------- разрезанный маркер конца вставки ----------
//
// На медленном канале пауза между кусками вставки легко превышает 250 мс.
// Если пауза застаёт pending с хвостом, похожим на начало маркера
// ESC[201~ («\x1b[2»), сброс по таймеру отдавал этот хвост как текст
// вставки, а продолжение «01~» приходило следом второй вставкой-мусором:
// в поле ввода оставались невидимые ESC-байты и видимый хвост маркера.

// TestReaderPasteMarkerSplitByStall — закрывающий маркер разрезан паузой
// между кусками: «\x1b[2» | «01~». Заготовка не уезжает в текст вставки,
// а после склейки маркера обычный текст после вставки разбирается как
// обычный ввод.
func TestReaderPasteMarkerSplitByStall(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewReader(pr)
	defer r.Close()
	go func() {
		// Хвост первого куска — начало закрывающего маркера.
		pw.Write([]byte("\x1b[200~abc\x1b[2"))
		// Пауза больше 250-мс ожидания вставки: сброс по таймеру.
		time.Sleep(450 * time.Millisecond)
		// Вторая половина маркера и текст после вставки.
		pw.Write([]byte("01~ок"))
		pw.Close()
	}()
	deadline := time.After(5 * time.Second)
	var evs []Event
loop:
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				break loop // читатель закончил вход
			}
			evs = append(evs, ev)
		case <-deadline:
			t.Fatalf("не дождались конца потока, есть: %#v", evs)
		}
	}
	if len(evs) == 0 {
		t.Fatal("событий нет вовсе")
	}
	var b strings.Builder
	var lastRune rune
	for i, ev := range evs {
		if ev.Paste {
			if strings.ContainsRune(ev.Text, 0x1b) {
				t.Fatalf("событие %d: ESC-байт в тексте вставки: %q", i, ev.Text)
			}
			b.WriteString(ev.Text)
			continue
		}
		if ev.Key == KeyRune {
			lastRune = ev.R
		}
	}
	if got := b.String(); got != "abc" {
		t.Fatalf("текст вставки = %q; хочу %q без ESC-хвостов", got, "abc")
	}
	// «ок» после закрывающего маркера — обычные руны, не часть вставки.
	if lastRune != 'к' {
		t.Fatalf("после вставки ждали «ок», последняя руна: %q", lastRune)
	}
}

// TestReaderPasteEOFMidMarker — поток оборвался посреди закрывающего
// маркера: заготовка маркера не попадает в текст вставки.
func TestReaderPasteEOFMidMarker(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewReader(pr)
	defer r.Close()
	go func() {
		pw.Write([]byte("\x1b[200~xyz\x1b[20"))
		pw.Close()
	}()
	select {
	case ev, ok := <-r.Events():
		if !ok {
			t.Fatal("поток закрылся без события")
		}
		if !ev.Paste {
			t.Fatalf("ждали вставку, получили %#v", ev)
		}
		if ev.Text != "xyz" {
			t.Fatalf("текст вставки = %q; хочу %q (без заготовки маркера)", ev.Text, "xyz")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("событие не пришло")
	}
}
